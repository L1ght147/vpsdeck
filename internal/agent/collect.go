package agent

import (
	"context"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/L1ght147/vpsdeck/internal/report"
)

// collectSystem fills everything except updates.
func collectSystem(ctx context.Context, r *report.Report) {
	r.Arch = runtime.GOARCH
	if h, err := os.Hostname(); err == nil {
		r.Hostname = h
	}

	if info, err := host.InfoWithContext(ctx); err == nil {
		r.Uptime = info.Uptime
		r.Kernel = info.KernelVersion
		r.OS = strings.TrimSpace(info.Platform + " " + info.PlatformVersion)
	}
	if pretty := osPrettyName(); pretty != "" {
		r.OS = pretty
	}

	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		r.CPU.Cores = n
	}
	// Sampling over one second gives a stable, current figure.
	if p, err := cpu.PercentWithContext(ctx, time.Second, false); err == nil && len(p) > 0 {
		r.CPU.Percent = round1(p[0])
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		r.CPU.Load1, r.CPU.Load5, r.CPU.Load15 = round2(l.Load1), round2(l.Load5), round2(l.Load15)
	}

	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		r.Memory.Total = v.Total
		r.Memory.Used = v.Used
		r.Memory.Percent = round1(v.UsedPercent)
	}
	if s, err := mem.SwapMemoryWithContext(ctx); err == nil {
		r.Memory.SwapTotal = s.Total
		r.Memory.SwapUsed = s.Used
	}

	r.Disks = collectDisks(ctx)
}

func collectDisks(ctx context.Context) []report.Disk {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []report.Disk
	for _, p := range parts {
		if !realFS(p.Fstype, p.Mountpoint) || seen[p.Device] {
			continue
		}
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		seen[p.Device] = true
		out = append(out, report.Disk{
			Mount:         p.Mountpoint,
			FSType:        p.Fstype,
			Total:         u.Total,
			Used:          u.Used,
			Free:          u.Free,
			Percent:       round1(u.UsedPercent),
			InodesPercent: round1(u.InodesUsedPercent),
		})
	}
	return out
}

var skipFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true, "proc": true,
	"sysfs": true, "cgroup": true, "cgroup2": true, "devpts": true, "mqueue": true,
	"debugfs": true, "tracefs": true, "securityfs": true, "pstore": true, "bpf": true,
	"autofs": true, "fusectl": true, "configfs": true, "hugetlbfs": true, "nsfs": true,
	"ramfs": true, "efivarfs": true, "binfmt_misc": true, "fuse.lxcfs": true, "iso9660": true,
	"devfs": true, "nullfs": true,
}

var skipMountPrefixes = []string{"/snap/", "/var/lib/docker/", "/run/", "/proc/", "/sys/", "/dev/", "/System/Volumes/"}

func realFS(fstype, mount string) bool {
	if skipFS[fstype] {
		return false
	}
	for _, p := range skipMountPrefixes {
		if strings.HasPrefix(mount, p) {
			return false
		}
	}
	return true
}

func osPrettyName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return parseOSRelease(string(b))
}

func parseOSRelease(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
