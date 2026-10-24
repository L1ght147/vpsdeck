// Package agent collects server metrics and pushes them to the hub.
package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/L1ght147/vpsdeck/internal/report"
)

const DefaultConfigPath = "/etc/vpsdeck/agent.yaml"

type Config struct {
	HubURL string `yaml:"hub_url"`
	Token  string `yaml:"token"`
	// Fingerprint pins the hub's self-signed certificate (hex SHA-256 of its DER bytes).
	Fingerprint    string        `yaml:"fingerprint"`
	Name           string        `yaml:"name"`
	Interval       time.Duration `yaml:"interval"`
	UpdateInterval time.Duration `yaml:"update_interval"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.HubURL = strings.TrimRight(c.HubURL, "/")
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.UpdateInterval <= 0 {
		c.UpdateInterval = time.Hour
	}
	return &c, nil
}

func (c *Config) Validate() error {
	if c.HubURL == "" {
		return errors.New("hub_url is required")
	}
	if c.Token == "" {
		return errors.New("token is required")
	}
	return nil
}

type Agent struct {
	cfg     *Config
	version string
	client  *http.Client

	mu      sync.Mutex
	updates report.Updates
}

func New(cfg *Config, version string) *Agent {
	return &Agent{cfg: cfg, version: version, client: newClient(cfg.Fingerprint)}
}

// newClient trusts exactly the certificate with the given fingerprint, or the
// system CAs when no fingerprint is set (hub behind a proxy with a real certificate).
func newClient(fingerprint string) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if want := normalizeFingerprint(fingerprint); want != "" {
		tr.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Chain verification is replaced by the pin below.
			InsecureSkipVerify: true, //nolint:gosec
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return errors.New("hub sent no certificate")
				}
				sum := sha256.Sum256(rawCerts[0])
				if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) != 1 {
					return errors.New("hub certificate fingerprint mismatch")
				}
				return nil
			},
		}
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: tr}
}

func normalizeFingerprint(s string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(s)))
}

// Collect builds a full report, checking updates synchronously.
func (a *Agent) Collect(ctx context.Context) report.Report {
	a.refreshUpdates(ctx)
	return a.snapshot(ctx)
}

func (a *Agent) snapshot(ctx context.Context) report.Report {
	r := report.Report{AgentVersion: a.version, CollectedAt: time.Now().UTC()}
	collectSystem(ctx, &r)
	r.Name = a.cfg.Name
	if r.Name == "" {
		r.Name = r.Hostname
	}
	a.mu.Lock()
	r.Updates = a.updates
	a.mu.Unlock()
	return r
}

func (a *Agent) refreshUpdates(ctx context.Context) {
	u := checkUpdates(ctx)
	a.mu.Lock()
	a.updates = u
	a.mu.Unlock()
}

// Run sends a report every interval until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.cfg.Validate(); err != nil {
		return err
	}
	slog.Info("agent started", "hub", a.cfg.HubURL, "interval", a.cfg.Interval)

	a.refreshUpdates(ctx)
	go func() {
		t := time.NewTicker(a.cfg.UpdateInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.refreshUpdates(ctx)
			}
		}
	}()

	t := time.NewTicker(a.cfg.Interval)
	defer t.Stop()
	failures := 0
	for {
		if err := a.Send(ctx, a.snapshot(ctx)); err != nil {
			failures++
			// Log the first failure and then every 10th, so a long hub outage does not flood the journal.
			if failures == 1 || failures%10 == 0 {
				slog.Warn("report failed", "err", err, "failures", failures)
			}
		} else if failures > 0 {
			slog.Info("hub reachable again", "after_failures", failures)
			failures = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (a *Agent) Send(ctx context.Context, r report.Report) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.HubURL+"/api/v1/report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("User-Agent", "vpsdeck-agent/"+a.version)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("hub returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
