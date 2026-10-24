package hub

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/L1ght147/vpsdeck/internal/i18n"
	"github.com/L1ght147/vpsdeck/internal/report"
)

// Telegram rejects messages longer than 4096 characters; keep a margin for markup.
const maxMessageLen = 3800

// bar renders a 10-cell usage bar.
func bar(pct float64) string {
	n := int(pct/10 + 0.5)
	n = max(0, min(10, n))
	return strings.Repeat("▰", n) + strings.Repeat("▱", 10-n)
}

func statusText(lang, st string) string { return i18n.T(lang, "status."+st) }

// hasProblem reports whether a server is offline, waiting, or over a threshold.
func (h *Hub) hasProblem(s Server, now time.Time) bool {
	if h.status(s, now) != StatusOnline {
		return true
	}
	r, th := s.Report, h.cfg.Thresholds
	return r != nil && (r.MaxDiskPercent() >= th.DiskWarn || r.CPU.Percent >= th.CPU || r.Memory.Percent >= th.Mem)
}

func hasUpdates(s Server) bool {
	return s.Report != nil && (s.Report.Updates.Total > 0 || s.Report.Updates.RebootRequired)
}

// MenuStats are the counts shown in the main menu.
type MenuStats struct {
	Total, Online, Offline, Pending, Problems, Updates int
}

func (h *Hub) stats(servers []Server, now time.Time) MenuStats {
	st := MenuStats{Total: len(servers)}
	for _, s := range servers {
		switch h.status(s, now) {
		case StatusOnline:
			st.Online++
		case StatusOffline:
			st.Offline++
		default:
			st.Pending++
		}
		if h.hasProblem(s, now) {
			st.Problems++
		}
		if hasUpdates(s) {
			st.Updates++
		}
	}
	return st
}

// MainMenu is the text above the main menu buttons and the counts for their labels.
func (h *Hub) MainMenu(lang string) (string, MenuStats, error) {
	servers, err := h.store.Servers()
	if err != nil {
		return "", MenuStats{}, err
	}
	now := time.Now()
	st := h.stats(servers, now)
	var b strings.Builder
	b.WriteString(i18n.T(lang, "menu.title") + "\n\n")
	if st.Total == 0 {
		b.WriteString(i18n.T(lang, "menu.empty"))
	} else {
		b.WriteString(i18n.T(lang, "menu.counts", st.Total, st.Online, st.Offline, st.Pending))
	}
	b.WriteString("\n\n" + i18n.T(lang, "menu.as_of", now.In(h.cfg.loc).Format("15:04")))
	return b.String(), st, nil
}

// ProblemServer is one row of the Require attention screen.
type ProblemServer struct {
	ID    int64
	Label string
}

// Problems lists servers that are offline, waiting or over a threshold, with the reasons.
func (h *Hub) Problems(lang string) (string, []ProblemServer, error) {
	servers, err := h.store.Servers()
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	th := h.cfg.Thresholds
	var lines []string
	var rows []ProblemServer
	for _, s := range servers {
		if !h.hasProblem(s, now) {
			continue
		}
		name := html.EscapeString(s.Name)
		var reasons []string
		switch st := h.status(s, now); st {
		case StatusPending:
			lines = append(lines, i18n.T(lang, "line.pending", name))
			reasons = append(reasons, statusText(lang, st))
		case StatusOffline:
			lines = append(lines, i18n.T(lang, "line.offline", name, i18n.Duration(lang, now.Sub(s.LastSeen))))
			reasons = append(reasons, statusText(lang, st))
		}
		if r := s.Report; r != nil && h.status(s, now) == StatusOnline {
			for _, d := range r.Disks {
				if d.Percent >= th.DiskWarn {
					lines = append(lines, i18n.T(lang, "line.disk", name, html.EscapeString(d.Mount), d.Percent))
					reasons = append(reasons, i18n.T(lang, "reason.disk", d.Mount, d.Percent))
				}
			}
			if r.CPU.Percent >= th.CPU {
				lines = append(lines, i18n.T(lang, "line.cpu", name, r.CPU.Percent))
				reasons = append(reasons, i18n.T(lang, "reason.cpu", r.CPU.Percent))
			}
			if r.Memory.Percent >= th.Mem {
				lines = append(lines, i18n.T(lang, "line.mem", name, r.Memory.Percent))
				reasons = append(reasons, i18n.T(lang, "reason.mem", r.Memory.Percent))
			}
		}
		rows = append(rows, ProblemServer{ID: s.ID, Label: s.Name + " · " + strings.Join(reasons, ", ")})
	}

	text := i18n.T(lang, "problems.header") + "\n\n"
	if len(lines) == 0 {
		return text + i18n.T(lang, "problems.none"), nil, nil
	}
	return text + strings.Join(lines, "\n") + "\n\n" + i18n.T(lang, "problems.hint"), rows, nil
}

// Card is the detailed view of one server.
func (h *Hub) Card(lang string, s Server, now time.Time) string {
	var b strings.Builder
	b.WriteString(i18n.T(lang, "card.title", html.EscapeString(s.Name), statusText(lang, h.status(s, now))) + "\n")

	r := s.Report
	if r == nil {
		b.WriteString(i18n.T(lang, "card.pending"))
		return b.String()
	}
	fmt.Fprintf(&b, "%s · %s · %s\n", html.EscapeString(r.OS), html.EscapeString(r.Kernel), r.Arch)
	b.WriteString(i18n.T(lang, "card.agent", html.EscapeString(r.AgentVersion)) + "\n")
	b.WriteString(i18n.T(lang, "card.uptime", i18n.Duration(lang, time.Duration(r.Uptime)*time.Second),
		i18n.Duration(lang, now.Sub(s.LastSeen))))
	if s.MutedUntil.After(now) {
		b.WriteString("\n" + i18n.T(lang, "card.muted", s.MutedUntil.In(h.cfg.loc).Format("02.01 15:04")))
	}

	b.WriteString("\n\n")
	fmt.Fprintf(&b, "<b>CPU</b>  <code>%s</code> %.0f%%\n", bar(r.CPU.Percent), r.CPU.Percent)
	b.WriteString(i18n.T(lang, "card.load", r.CPU.Cores, r.CPU.Load1, r.CPU.Load5, r.CPU.Load15) + "\n")
	fmt.Fprintf(&b, "<b>RAM</b>  <code>%s</code> %.0f%%\n", bar(r.Memory.Percent), r.Memory.Percent)
	fmt.Fprintf(&b, "%s / %s", humanBytes(r.Memory.Used), humanBytes(r.Memory.Total))
	if r.Memory.SwapTotal > 0 {
		fmt.Fprintf(&b, " · swap %s / %s", humanBytes(r.Memory.SwapUsed), humanBytes(r.Memory.SwapTotal))
	}
	b.WriteString("\n\n" + i18n.T(lang, "card.disks") + "\n")
	for _, d := range r.Disks {
		fmt.Fprintf(&b, "<code>%s</code> %.0f%%  <b>%s</b>\n", bar(d.Percent), d.Percent, html.EscapeString(d.Mount))
		b.WriteString(i18n.T(lang, "card.disk_free", humanBytes(d.Free), humanBytes(d.Total), d.InodesPercent) + "\n")
	}

	b.WriteString("\n" + updatesLine(lang, r.Updates))
	return b.String()
}

func updatesLine(lang string, u report.Updates) string {
	var lines []string
	switch {
	case !u.Supported:
		lines = append(lines, i18n.T(lang, "card.updates_unsupported"))
	case u.Total == 0:
		lines = append(lines, i18n.T(lang, "card.up_to_date"))
	default:
		lines = append(lines, i18n.T(lang, "card.updates", u.Total, u.Security))
	}
	if u.RebootRequired {
		lines = append(lines, i18n.T(lang, "card.reboot"))
	}
	if u.Error != "" {
		lines = append(lines, html.EscapeString(u.Error))
	}
	return strings.Join(lines, "\n")
}

// Packages lists a server's pending updates, security ones first.
func (h *Hub) Packages(lang string, s Server) string {
	var b strings.Builder
	b.WriteString(i18n.T(lang, "card.packages", html.EscapeString(s.Name)) + "\n\n")
	r := s.Report
	if r == nil || !r.Updates.Supported {
		b.WriteString(i18n.T(lang, "card.updates_unsupported"))
		return b.String()
	}
	u := r.Updates
	if u.Total == 0 {
		b.WriteString(i18n.T(lang, "card.up_to_date"))
		return b.String()
	}
	b.WriteString(i18n.T(lang, "card.updates", u.Total, u.Security) + "\n\n")

	pkgs := append([]report.Package(nil), u.Packages...)
	sort.SliceStable(pkgs, func(i, j int) bool {
		if pkgs[i].Security != pkgs[j].Security {
			return pkgs[i].Security
		}
		return pkgs[i].Name < pkgs[j].Name
	})
	sec := i18n.T(lang, "card.security")
	shown := 0
	for _, p := range pkgs {
		line := fmt.Sprintf("<code>%s</code> %s", html.EscapeString(p.Name), html.EscapeString(p.Version))
		if p.Security {
			line += " · <b>" + sec + "</b>"
		}
		line += "\n"
		if b.Len()+len(line) > maxMessageLen {
			break
		}
		b.WriteString(line)
		shown++
	}
	if rest := u.Total - shown; rest > 0 {
		b.WriteString(i18n.T(lang, "card.more_packages", rest) + "\n")
	}
	b.WriteString("\n" + i18n.T(lang, "card.upgrade_hint"))
	return b.String()
}

// Settings shows the language and the alert configuration.
func (h *Hub) Settings(lang string) string {
	th := h.cfg.Thresholds
	latest := h.LatestVersion()
	if latest == "" {
		latest = i18n.T(lang, "settings.unknown")
	}
	return i18n.T(lang, "settings.text", i18n.T(lang, "lang.name"), html.EscapeString(h.version), html.EscapeString(latest),
		th.DiskWarn, th.DiskCrit, th.CPU, th.Mem,
		i18n.Duration(lang, th.Sustain), i18n.Duration(lang, th.OfflineAfter), h.cfg.DigestTime)
}
