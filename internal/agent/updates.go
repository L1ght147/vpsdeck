package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/L1ght147/vpsdeck/internal/report"
)

const (
	aptCheckPath       = "/usr/lib/update-notifier/apt-check"
	rebootRequiredPath = "/var/run/reboot-required"
	maxPackages        = 200
)

// checkUpdates reads the pending package updates from the local apt cache.
// It never runs `apt update`: Ubuntu and Debian refresh the cache themselves
// (apt-daily.timer), and the agent does not run as root.
func checkUpdates(ctx context.Context) report.Updates {
	u := report.Updates{CheckedAt: time.Now().UTC()}
	_, err := os.Stat(rebootRequiredPath)
	u.RebootRequired = err == nil

	if _, err := exec.LookPath("apt"); err != nil {
		return u
	}
	u.Supported = true

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	out, err := runC(ctx, "apt", "list", "--upgradable")
	if err != nil {
		u.Error = err.Error()
		return u
	}
	pkgs := parseAptList(out)
	u.Total = len(pkgs)
	for _, p := range pkgs {
		if p.Security {
			u.Security++
		}
	}
	if len(pkgs) > maxPackages {
		pkgs = pkgs[:maxPackages]
	}
	u.Packages = pkgs

	// apt-check knows about phased updates, so its counts are more accurate on Ubuntu.
	if _, err := os.Stat(aptCheckPath); err == nil {
		if total, sec, ok := runAptCheck(ctx); ok {
			u.Total, u.Security = total, sec
		}
	}
	return u
}

func runC(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return stdout.String(), nil
}

func runAptCheck(ctx context.Context) (total, security int, ok bool) {
	cmd := exec.CommandContext(ctx, aptCheckPath)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, 0, false
	}
	return parseAptCheck(stderr.String())
}

// parseAptCheck parses apt-check's "<total>;<security>" output.
func parseAptCheck(s string) (total, security int, ok bool) {
	a, b, found := strings.Cut(strings.TrimSpace(s), ";")
	if !found {
		return 0, 0, false
	}
	t, err1 := strconv.Atoi(a)
	sec, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return t, sec, true
}

// parseAptList parses lines like
// "bash/jammy-updates,jammy-security 5.1-6ubuntu1.1 amd64 [upgradable from: 5.1-6ubuntu1]".
func parseAptList(s string) []report.Package {
	var out []report.Package
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Listing") || strings.HasPrefix(line, "WARNING") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, sources, found := strings.Cut(fields[0], "/")
		if !found {
			continue
		}
		out = append(out, report.Package{
			Name:     name,
			Version:  fields[1],
			Security: strings.Contains(sources, "-security"),
		})
	}
	return out
}
