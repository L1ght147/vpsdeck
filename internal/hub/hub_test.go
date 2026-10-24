package hub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/L1ght147/vpsdeck/internal/i18n"
	"github.com/L1ght147/vpsdeck/internal/report"
)

func testHub(t *testing.T) (*Hub, *recorder) {
	t.Helper()
	cfg := &Config{BotToken: "123:test", Admins: []int64{42}, PublicURL: "https://203.0.113.5:8443", DBPath: filepath.Join(t.TempDir(), "hub.db")}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	rec := &recorder{}
	h := New(cfg, store, Options{Version: "test"})
	h.alerts = NewAlerts(cfg, store, rec)
	return h, rec
}

type recorder struct{ events []Event }

func (r *recorder) Notify(e Event) { r.events = append(r.events, e) }

func (r *recorder) take() []Event {
	e := r.events
	r.events = nil
	return e
}

func TestIngestAuth(t *testing.T) {
	h, _ := testHub(t)
	srv, tok, err := h.store.AddServer("web-1")
	if err != nil {
		t.Fatal(err)
	}
	handler := h.routes()

	post := func(token string, body []byte) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/report", bytes.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	body, _ := json.Marshal(report.Report{Name: "web-1"})
	if code := post("", body); code != http.StatusUnauthorized {
		t.Errorf("no token: got %d", code)
	}
	if code := post("wrong", body); code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d", code)
	}
	if code := post(tok, []byte("{not json")); code != http.StatusBadRequest {
		t.Errorf("bad body: got %d", code)
	}
	if code := post(tok, body); code != http.StatusNoContent {
		t.Fatalf("valid report: got %d", code)
	}
	got, err := h.store.Server(srv.ID)
	if err != nil || got.Report == nil || got.LastSeen.IsZero() {
		t.Fatalf("report not stored: %+v %v", got, err)
	}
}

func TestAlertsDisk(t *testing.T) {
	h, rec := testHub(t)
	srv, _, _ := h.store.AddServer("db-1")
	rep := func(pct float64) *report.Report {
		return &report.Report{Name: "db-1", Disks: []report.Disk{{Mount: "/", Percent: pct, Free: 1 << 30}}}
	}

	h.alerts.OnReport(srv, rep(50))
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("no alert expected at 50%%, got %+v", ev)
	}
	h.alerts.OnReport(srv, rep(86))
	if ev := rec.take(); len(ev) != 1 || ev[0].Level != LevelWarn {
		t.Fatalf("want one warn, got %+v", ev)
	}
	h.alerts.OnReport(srv, rep(87))
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("same level must not repeat, got %+v", ev)
	}
	h.alerts.OnReport(srv, rep(96))
	if ev := rec.take(); len(ev) != 1 || ev[0].Level != LevelCrit {
		t.Fatalf("want crit, got %+v", ev)
	}
	h.alerts.OnReport(srv, rep(94)) // within hysteresis of crit: stays crit
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("hysteresis: got %+v", ev)
	}
	h.alerts.OnReport(srv, rep(40))
	if ev := rec.take(); len(ev) != 1 || ev[0].Level != LevelOK {
		t.Fatalf("want recovery, got %+v", ev)
	}
}

func TestAlertsCPUSustained(t *testing.T) {
	h, rec := testHub(t)
	srv, _, _ := h.store.AddServer("cpu-1")
	clock := time.Unix(1_700_000_000, 0)
	h.alerts.now = func() time.Time { return clock }
	rep := &report.Report{Name: "cpu-1"}
	rep.CPU.Percent = 99

	h.alerts.OnReport(srv, rep)
	clock = clock.Add(5 * time.Minute)
	h.alerts.OnReport(srv, rep)
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("short spike must not alert, got %+v", ev)
	}
	clock = clock.Add(6 * time.Minute)
	h.alerts.OnReport(srv, rep)
	if ev := rec.take(); len(ev) != 1 || ev[0].Kind != KindCPU || ev[0].Level != LevelWarn {
		t.Fatalf("want sustained cpu alert, got %+v", ev)
	}
	rep.CPU.Percent = 20
	h.alerts.OnReport(srv, rep)
	if ev := rec.take(); len(ev) != 1 || ev[0].Level != LevelOK {
		t.Fatalf("want cpu recovery, got %+v", ev)
	}
}

func TestAlertsOfflineAndMute(t *testing.T) {
	h, rec := testHub(t)
	srv, _, _ := h.store.AddServer("vpn-1")
	clock := time.Now()
	h.alerts.now = func() time.Time { return clock }
	h.alerts.started = clock.Add(-time.Hour)

	h.alerts.CheckOffline()
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("never-seen server must not be offline, got %+v", ev)
	}

	r := &report.Report{Name: "vpn-1"}
	if err := h.store.SaveReport(srv.ID, r, clock); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(4 * time.Minute)
	h.alerts.CheckOffline()
	if ev := rec.take(); len(ev) != 1 || ev[0].Kind != KindOffline {
		t.Fatalf("want offline, got %+v", ev)
	}
	h.alerts.CheckOffline()
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("offline must fire once, got %+v", ev)
	}
	h.alerts.OnReport(srv, r)
	if ev := rec.take(); len(ev) != 1 || ev[0].Kind != KindOnline {
		t.Fatalf("want back online, got %+v", ev)
	}

	// Muted servers keep state but send nothing.
	if err := h.store.Mute(srv.ID, clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(10 * time.Minute)
	h.alerts.CheckOffline()
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("muted server alerted: %+v", ev)
	}
}

func TestNextDigest(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 1, 10, 8, 0, 0, 0, loc)
	if got := nextDigest(now, "09:00"); !got.Equal(time.Date(2026, 1, 10, 9, 0, 0, 0, loc)) {
		t.Errorf("same day: %v", got)
	}
	now = time.Date(2026, 1, 10, 9, 0, 0, 0, loc)
	if got := nextDigest(now, "09:00"); !got.Equal(time.Date(2026, 1, 11, 9, 0, 0, 0, loc)) {
		t.Errorf("next day: %v", got)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"web-1":          "web-1",
		"  my server ":   "my-server",
		"a;rm -rf /":     "a-rm-rf",
		"$(whoami)":      "whoami",
		"vpn.de.example": "vpn.de.example",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstallCommandPinsCertificate(t *testing.T) {
	h, _ := testHub(t)
	h.cfg.PublicURL = "https://203.0.113.5:8443"
	h.fingerprint = "abc123"
	cmd := h.InstallCommand("tok", "web-1")
	for _, want := range []string{"--url https://203.0.113.5:8443", "--token tok", "--name web-1", "--fingerprint abc123"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("install command %q lacks %q", cmd, want)
		}
	}
}

func TestEnsureCertIsStable(t *testing.T) {
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "hub.crt"), filepath.Join(dir, "hub.key")
	c1, err := EnsureCert(crt, key)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := EnsureCert(crt, key)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := CertFingerprint(crt)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(c1.Certificate[0]) != Fingerprint(c2.Certificate[0]) || fp != Fingerprint(c1.Certificate[0]) {
		t.Fatal("certificate changed between loads")
	}
}

func TestCardAndPackages(t *testing.T) {
	h, _ := testHub(t)
	srv, _, _ := h.store.AddServer("vpn-de-1")
	r := &report.Report{
		Name: "vpn-de-1", OS: "Ubuntu 24.04.1 LTS", Kernel: "6.8.0-45-generic", Arch: "amd64", Uptime: 900000,
		CPU:    report.CPU{Cores: 2, Percent: 12, Load1: 0.31, Load5: 0.2, Load15: 0.1},
		Memory: report.Memory{Total: 4 << 30, Used: 3 << 30, Percent: 75, SwapTotal: 1 << 30, SwapUsed: 1 << 20},
		Disks: []report.Disk{
			{Mount: "/", Total: 40 << 30, Free: 3 << 30, Percent: 92.5, InodesPercent: 11},
			{Mount: "/data", Total: 100 << 30, Free: 80 << 30, Percent: 20, InodesPercent: 1},
		},
		Updates: report.Updates{Supported: true, Total: 300, Security: 2, RebootRequired: true},
	}
	for i := 0; i < 300; i++ {
		r.Updates.Packages = append(r.Updates.Packages, report.Package{Name: "pkg-" + strings.Repeat("x", i%20), Version: "1.2.3-ubuntu0.1", Security: i < 2})
	}
	if err := h.store.SaveReport(srv.ID, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	s, _ := h.store.Server(srv.ID)

	card := h.Card("ru", s, time.Now())
	for _, want := range []string{"vpn-de-1", "в сети", "Ubuntu 24.04.1 LTS", "92%", "/data", "<b>Доступно обновлений:</b> 300", "Требуется перезагрузка"} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
	pk := h.Packages("ru", s)
	if len(pk) > 4096 {
		t.Errorf("packages message too long: %d", len(pk))
	}
	if !strings.Contains(pk, "Не показано") {
		t.Errorf("long package list not truncated:\n%s", pk)
	}
}

func TestNoOfflineAlertsRightAfterHubStart(t *testing.T) {
	h, rec := testHub(t)
	srv, _, _ := h.store.AddServer("db-2")
	start := time.Now()
	clock := start
	h.alerts.now = func() time.Time { return clock }
	h.alerts.started = start
	// The last report is an hour old: the hub was down meanwhile.
	if err := h.store.SaveReport(srv.ID, &report.Report{Name: "db-2"}, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	clock = start.Add(time.Minute)
	h.alerts.CheckOffline()
	if ev := rec.take(); len(ev) != 0 {
		t.Fatalf("alerted during the startup grace period: %+v", ev)
	}
	clock = start.Add(4 * time.Minute)
	h.alerts.CheckOffline()
	if ev := rec.take(); len(ev) != 1 || ev[0].Kind != KindOffline {
		t.Fatalf("want offline after the grace period, got %+v", ev)
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.4.0", "v0.3.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.2.9", "v0.3.0", false},
		{"v0.4.0", "dev", false},
		{"garbage", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckForUpdateNotifiesOnce(t *testing.T) {
	h, _ := testHub(t)
	h.version = "v0.3.0"
	tag := "v0.4.0"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	defer gh.Close()
	h.releaseAPI = gh.URL

	if err := h.checkForUpdate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.LatestVersion() != "v0.4.0" {
		t.Fatalf("latest = %q", h.LatestVersion())
	}
	if v, _ := h.store.Setting(settingNotified); v != "v0.4.0" {
		t.Fatalf("notification not recorded: %q", v)
	}

	tag = "v0.3.0" // same as installed: nothing to announce
	h.version = "v0.3.0"
	_ = h.store.SetSetting(settingNotified, "")
	if err := h.checkForUpdate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.store.Setting(settingNotified); v != "" {
		t.Fatalf("announced a version that is not newer: %q", v)
	}

	notice := h.UpdateNotice("ru", "v0.4.0")
	for _, want := range []string{"v0.3.0", "v0.4.0", "sudo bash -s -- update", "releases/tag/v0.4.0"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice lacks %q:\n%s", want, notice)
		}
	}
}

func TestMainMenuAndProblems(t *testing.T) {
	h, _ := testHub(t)
	ok, _, _ := h.store.AddServer("web-1")
	full, _, _ := h.store.AddServer("db-1")
	h.store.AddServer("new-1") // never reported: waiting
	now := time.Now()
	_ = h.store.SaveReport(ok.ID, &report.Report{Name: "web-1", Disks: []report.Disk{{Mount: "/", Percent: 40}}}, now)
	_ = h.store.SaveReport(full.ID, &report.Report{Name: "db-1", Disks: []report.Disk{{Mount: "/", Percent: 91}},
		Updates: report.Updates{Supported: true, Total: 3, Security: 1}}, now)

	text, st, err := h.MainMenu("ru")
	if err != nil {
		t.Fatal(err)
	}
	if st != (MenuStats{Total: 3, Online: 2, Pending: 1, Problems: 2, Updates: 1}) {
		t.Errorf("stats = %+v", st)
	}
	if !strings.Contains(text, "Серверов: 3") || !strings.Contains(text, "Данные на") {
		t.Errorf("menu text:\n%s", text)
	}

	ptext, rows, err := h.Problems("ru")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Label != "db-1 · диск / 91%" || rows[1].Label != "new-1 · ожидает подключения" {
		t.Errorf("rows = %+v", rows)
	}
	if !strings.Contains(ptext, "раздел / заполнен на 91%") {
		t.Errorf("problems text:\n%s", ptext)
	}
}

// Every literal key passed to i18n.T in this package must exist in both catalogs.
func TestUsedKeysExist(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	// Keys built by concatenation ("alert." + kind) are followed by "+" and skipped.
	re := regexp.MustCompile(`i18n\.T\([a-z]+, "([a-z0-9_.]+)"[,)]`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			for _, lang := range i18n.Langs {
				if !i18n.Keys(lang)[m[1]] {
					t.Errorf("%s: key %q missing in %s.json", f, m[1], lang)
				}
			}
		}
	}
}
