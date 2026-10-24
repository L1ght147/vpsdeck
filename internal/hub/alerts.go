package hub

import (
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/L1ght147/vpsdeck/internal/i18n"
	"github.com/L1ght147/vpsdeck/internal/report"
)

type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelCrit
)

// hysteresis is how far (in percent points) usage must drop below a threshold
// before the alert clears, so a value hovering at the line does not flap.
const hysteresis = 2.0

const (
	KindOffline = "offline"
	KindOnline  = "online"
	KindDisk    = "disk"
	KindCPU     = "cpu"
	KindMem     = "mem"
)

// Event is a change of alert level worth telling the admins about.
type Event struct {
	ServerID   int64
	ServerName string
	Kind       string
	Level      Level
	Mount      string
	Percent    float64
	Free       uint64
	Duration   time.Duration
}

type Notifier interface {
	Notify(Event)
}

type Alerts struct {
	cfg    *Config
	store  *Store
	notify Notifier
	now    func() time.Time
	// started is when the hub came up; agents get offline_after to report
	// before anyone is declared offline, so a hub restart raises no false alarms.
	started time.Time

	mu        sync.Mutex
	overSince map[string]time.Time // "serverID/kind" → when usage first went over the threshold
}

func NewAlerts(cfg *Config, store *Store, n Notifier) *Alerts {
	return &Alerts{cfg: cfg, store: store, notify: n, now: time.Now, started: time.Now(), overSince: map[string]time.Time{}}
}

// OnReport evaluates a fresh report against the stored alert state.
func (a *Alerts) OnReport(srv Server, r *report.Report) {
	levels, err := a.store.AlertLevels(srv.ID)
	if err != nil {
		slog.Error("load alert state", "err", err)
		return
	}
	now := a.now()
	name := r.Name
	if name == "" {
		name = srv.Name
	}
	var events []Event
	set := func(key string, to Level, ev Event) {
		if levels[key] == to {
			return
		}
		if err := a.store.SetAlertLevel(srv.ID, key, to); err != nil {
			slog.Error("save alert state", "err", err)
			return
		}
		ev.ServerID, ev.ServerName, ev.Level = srv.ID, name, to
		events = append(events, ev)
	}

	if levels[KindOffline] != LevelOK {
		set(KindOffline, LevelOK, Event{Kind: KindOnline})
	}

	th := a.cfg.Thresholds
	seenDisks := map[string]bool{}
	for _, d := range r.Disks {
		key := KindDisk + ":" + d.Mount
		seenDisks[key] = true
		to := thresholdLevel(levels[key], d.Percent, th.DiskWarn, th.DiskCrit)
		set(key, to, Event{Kind: KindDisk, Mount: d.Mount, Percent: d.Percent, Free: d.Free})
	}
	// A disk that is no longer reported (unmounted) clears silently.
	for key := range levels {
		if strings.HasPrefix(key, KindDisk+":") && !seenDisks[key] {
			_ = a.store.SetAlertLevel(srv.ID, key, LevelOK)
		}
	}

	set(KindCPU, a.sustained(srv.ID, KindCPU, levels[KindCPU], r.CPU.Percent, th.CPU, now),
		Event{Kind: KindCPU, Percent: r.CPU.Percent, Duration: th.Sustain})
	set(KindMem, a.sustained(srv.ID, KindMem, levels[KindMem], r.Memory.Percent, th.Mem, now),
		Event{Kind: KindMem, Percent: r.Memory.Percent, Duration: th.Sustain})

	a.emit(srv, events, now)
}

// CheckOffline marks servers that stopped reporting.
func (a *Alerts) CheckOffline() {
	servers, err := a.store.Servers()
	if err != nil {
		slog.Error("list servers", "err", err)
		return
	}
	now := a.now()
	if now.Sub(a.started) < a.cfg.Thresholds.OfflineAfter {
		return
	}
	for _, srv := range servers {
		if srv.LastSeen.IsZero() {
			continue // never reported yet: pending, not offline
		}
		silent := now.Sub(srv.LastSeen)
		if silent <= a.cfg.Thresholds.OfflineAfter {
			continue
		}
		levels, err := a.store.AlertLevels(srv.ID)
		if err != nil || levels[KindOffline] == LevelCrit {
			continue
		}
		if err := a.store.SetAlertLevel(srv.ID, KindOffline, LevelCrit); err != nil {
			continue
		}
		a.emit(srv, []Event{{ServerID: srv.ID, ServerName: srv.Name, Kind: KindOffline, Level: LevelCrit, Duration: silent}}, now)
	}
}

func (a *Alerts) emit(srv Server, events []Event, now time.Time) {
	if a.notify == nil || srv.MutedUntil.After(now) {
		return
	}
	for _, e := range events {
		a.notify.Notify(e)
	}
}

// thresholdLevel picks a level from usage, keeping the current one until usage
// drops hysteresis points below its threshold.
func thresholdLevel(cur Level, pct, warn, crit float64) Level {
	to := LevelOK
	switch {
	case pct >= crit:
		to = LevelCrit
	case pct >= warn:
		to = LevelWarn
	}
	if to < cur {
		limit := warn
		if cur == LevelCrit {
			limit = crit
		}
		if pct > limit-hysteresis {
			return cur
		}
	}
	return to
}

// sustained returns LevelWarn once usage has stayed at or above the threshold for Sustain.
func (a *Alerts) sustained(id int64, kind string, cur Level, pct, threshold float64, now time.Time) Level {
	key := fmt.Sprintf("%d/%s", id, kind)
	a.mu.Lock()
	defer a.mu.Unlock()
	if pct < threshold {
		delete(a.overSince, key)
		if cur != LevelOK && pct > threshold-hysteresis {
			return cur
		}
		return LevelOK
	}
	since, ok := a.overSince[key]
	if !ok {
		a.overSince[key] = now
		since = now
	}
	if cur != LevelOK || now.Sub(since) >= a.cfg.Thresholds.Sustain {
		return LevelWarn
	}
	return LevelOK
}

// FormatEvent renders an event as a Telegram HTML message.
func FormatEvent(lang string, e Event) string {
	name := html.EscapeString(e.ServerName)
	switch e.Kind {
	case KindOffline:
		return i18n.T(lang, "alert.offline", name, i18n.Duration(lang, e.Duration))
	case KindOnline:
		return i18n.T(lang, "alert.online", name)
	case KindDisk:
		mount := html.EscapeString(e.Mount)
		switch e.Level {
		case LevelCrit:
			return i18n.T(lang, "alert.disk.crit", name, mount, e.Percent, humanBytes(e.Free))
		case LevelWarn:
			return i18n.T(lang, "alert.disk.warn", name, mount, e.Percent, humanBytes(e.Free))
		default:
			return i18n.T(lang, "alert.disk.ok", name, mount, e.Percent)
		}
	case KindCPU, KindMem:
		if e.Level == LevelOK {
			return i18n.T(lang, "alert."+e.Kind+".ok", name, e.Percent)
		}
		return i18n.T(lang, "alert."+e.Kind, name, e.Percent, i18n.Duration(lang, e.Duration))
	}
	return name + ": " + e.Kind
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
