// Package hub receives agent reports, raises alerts and runs the Telegram bot.
package hub

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/L1ght147/vpsdeck/internal/i18n"
)

const InstallScriptURL = "https://raw.githubusercontent.com/L1ght147/vpsdeck/main/scripts/install.sh"

type Hub struct {
	cfg     *Config
	store   *Store
	alerts  *Alerts
	bot     *Bot
	version string
	dev     bool
	// fingerprint of the hub's TLS certificate, handed to agents in install commands.
	fingerprint string
	releaseAPI  string

	mu            sync.Mutex
	latestVersion string
}

type Options struct {
	Version string
	// Dev runs without the Telegram bot, for local development.
	Dev bool
}

func New(cfg *Config, store *Store, opts Options) *Hub {
	h := &Hub{cfg: cfg, store: store, version: opts.Version, dev: opts.Dev, releaseAPI: latestReleaseAPI}
	if !cfg.TLSDisable {
		h.fingerprint, _ = CertFingerprint(cfg.TLSCert)
	}
	return h
}

// Lang is the language chosen with /lang, or the configured one.
func (h *Hub) Lang() string {
	if v, err := h.store.Setting("lang"); err == nil && v != "" {
		return i18n.Normalize(v)
	}
	return h.cfg.Lang
}

func (h *Hub) Run(ctx context.Context) error {
	var tlsCfg *tls.Config
	if !h.cfg.TLSDisable {
		cert, err := EnsureCert(h.cfg.TLSCert, h.cfg.TLSKey)
		if err != nil {
			return err
		}
		h.fingerprint = Fingerprint(cert.Certificate[0])
		tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}

	var notifier Notifier
	if !h.dev {
		b, err := NewBot(h)
		if err != nil {
			return fmt.Errorf("telegram bot: %w", err)
		}
		h.bot = b
		notifier = b
	}
	h.alerts = NewAlerts(h.cfg, h.store, notifier)

	srv := &http.Server{
		Addr:              h.cfg.Listen,
		Handler:           h.routes(),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("hub listening", "addr", h.cfg.Listen, "public_url", h.cfg.PublicURL, "tls", tlsCfg != nil, "dev", h.dev)
		var err error
		if tlsCfg != nil {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	if h.bot != nil {
		h.bot.ctx = ctx
		go h.bot.Start(ctx)
	}
	go h.offlineLoop(ctx)
	go h.digestLoop(ctx)
	go h.updateLoop(ctx)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func (h *Hub) offlineLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.alerts.CheckOffline()
		}
	}
}

func (h *Hub) digestLoop(ctx context.Context) {
	for {
		next := nextDigest(time.Now().In(h.cfg.loc), h.cfg.DigestTime)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			if h.bot != nil {
				if msg := h.Digest(h.Lang()); msg != "" {
					h.bot.Broadcast(ctx, msg)
				}
			}
		}
	}
}

func nextDigest(now time.Time, hhmm string) time.Time {
	t, _ := time.Parse("15:04", hhmm)
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Status values shown in the bot and mini app.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
	StatusPending = "pending"
)

func (h *Hub) status(s Server, now time.Time) string {
	switch {
	case s.LastSeen.IsZero():
		return StatusPending
	case now.Sub(s.LastSeen) > h.cfg.Thresholds.OfflineAfter:
		return StatusOffline
	default:
		return StatusOnline
	}
}

// Digest lists servers with security updates or a pending reboot; empty when there are none.
func (h *Hub) Digest(lang string) string {
	servers, err := h.store.Servers()
	if err != nil {
		slog.Error("digest", "err", err)
		return ""
	}
	var lines []string
	for _, s := range servers {
		r := s.Report
		if r == nil || (r.Updates.Security == 0 && !r.Updates.RebootRequired) {
			continue
		}
		line := i18n.T(lang, "digest.line", html.EscapeString(s.Name), r.Updates.Total, r.Updates.Security)
		if r.Updates.RebootRequired {
			line += i18n.T(lang, "digest.reboot")
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return i18n.T(lang, "digest.header") + "\n\n" + strings.Join(lines, "\n")
}

var (
	unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	dashes     = regexp.MustCompile(`-{2,}`)
)

// SanitizeName keeps server names safe to paste into a shell command.
func SanitizeName(s string) string {
	s = strings.Trim(dashes.ReplaceAllString(unsafeName.ReplaceAllString(strings.TrimSpace(s), "-"), "-"), "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// AddServer registers a server and returns the one-line install command for its agent.
func (h *Hub) AddServer(name string) (Server, string, error) {
	srv, tok, err := h.store.AddServer(SanitizeName(name))
	if err != nil {
		return Server{}, "", err
	}
	return srv, h.InstallCommand(tok, srv.Name), nil
}

// InstallCommand is the one-line agent install command for a server token.
func (h *Hub) InstallCommand(token, name string) string {
	cmd := fmt.Sprintf("curl -fsSL %s | sudo bash -s -- agent --url %s --token %s --name %s --lang %s",
		InstallScriptURL, h.cfg.PublicURL, token, name, h.Lang())
	if h.fingerprint != "" {
		cmd += " --fingerprint " + h.fingerprint
	}
	return cmd
}
