package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/L1ght147/vpsdeck/internal/i18n"
)

const (
	Repo             = "L1ght147/vpsdeck"
	latestReleaseAPI = "https://api.github.com/repos/" + Repo + "/releases/latest"
	releasePageURL   = "https://github.com/" + Repo + "/releases/tag/"

	updateCheckDelay    = time.Minute
	updateCheckInterval = 24 * time.Hour
	settingNotified     = "update_notified"
)

// updateLoop checks GitHub for a newer release once a day. The hub only tells
// the admins about it: updating is done with the installer, never by the bot.
func (h *Hub) updateLoop(ctx context.Context) {
	if h.cfg.DisableUpdateCheck || parseVersion(h.version) == nil {
		return
	}
	wait := updateCheckDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateCheckInterval
		if err := h.checkForUpdate(ctx); err != nil {
			slog.Warn("update check", "err", err)
		}
	}
}

func (h *Hub) checkForUpdate(ctx context.Context) error {
	latest, err := fetchLatestVersion(ctx, h.releaseAPI)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.latestVersion = latest
	h.mu.Unlock()

	if !newerVersion(latest, h.version) {
		return nil
	}
	if done, _ := h.store.Setting(settingNotified); done == latest {
		return nil
	}
	if h.bot != nil {
		h.bot.Broadcast(ctx, h.UpdateNotice(h.Lang(), latest))
	}
	return h.store.SetSetting(settingNotified, latest)
}

// LatestVersion is the newest release seen by the last check, or "".
func (h *Hub) LatestVersion() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.latestVersion
}

// UpdateNotice is the message announcing a new release.
func (h *Hub) UpdateNotice(lang, latest string) string {
	var b strings.Builder
	b.WriteString(i18n.T(lang, "update.title") + "\n\n")
	b.WriteString(i18n.T(lang, "update.versions", html.EscapeString(h.version), html.EscapeString(latest)) + "\n")
	if n := h.outdatedAgents(latest); n > 0 {
		b.WriteString(i18n.T(lang, "update.outdated_agents", n) + "\n")
	}
	b.WriteString("\n" + i18n.T(lang, "update.how", html.EscapeString(fmt.Sprintf("curl -fsSL %s | sudo bash -s -- update", InstallScriptURL))))
	b.WriteString("\n\n" + i18n.T(lang, "update.changes", releasePageURL+latest))
	return b.String()
}

func (h *Hub) outdatedAgents(latest string) int {
	servers, err := h.store.Servers()
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range servers {
		if s.Report != nil && newerVersion(latest, s.Report.AgentVersion) {
			n++
		}
	}
	return n
}

func fetchLatestVersion(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if parseVersion(rel.TagName) == nil {
		return "", fmt.Errorf("github: unexpected tag %q", rel.TagName)
	}
	return rel.TagName, nil
}

// parseVersion parses "v1.2.3" into its numbers; nil for anything else (e.g. "dev").
func parseVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

// newerVersion reports whether a is a later release than b.
func newerVersion(a, b string) bool {
	va, vb := parseVersion(a), parseVersion(b)
	if va == nil || vb == nil {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}
