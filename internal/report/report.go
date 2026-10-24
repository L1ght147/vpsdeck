// Package report defines the payload the agent sends to the hub.
package report

import "time"

// Report is a single snapshot of a server's state.
type Report struct {
	Name         string    `json:"name"`
	Hostname     string    `json:"hostname"`
	AgentVersion string    `json:"agent_version"`
	CollectedAt  time.Time `json:"collected_at"`

	OS     string `json:"os"`
	Kernel string `json:"kernel"`
	Arch   string `json:"arch"`
	Uptime uint64 `json:"uptime"` // seconds

	CPU    CPU    `json:"cpu"`
	Memory Memory `json:"memory"`
	Disks  []Disk `json:"disks"`

	Updates Updates `json:"updates"`
}

type CPU struct {
	Cores   int     `json:"cores"`
	Percent float64 `json:"percent"`
	Load1   float64 `json:"load1"`
	Load5   float64 `json:"load5"`
	Load15  float64 `json:"load15"`
}

type Memory struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Percent   float64 `json:"percent"`
	SwapTotal uint64  `json:"swap_total"`
	SwapUsed  uint64  `json:"swap_used"`
}

type Disk struct {
	Mount         string  `json:"mount"`
	FSType        string  `json:"fstype"`
	Total         uint64  `json:"total"`
	Used          uint64  `json:"used"`
	Free          uint64  `json:"free"`
	Percent       float64 `json:"percent"`
	InodesPercent float64 `json:"inodes_percent"`
}

type Package struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Security bool   `json:"security"`
}

type Updates struct {
	Supported      bool      `json:"supported"`
	Total          int       `json:"total"`
	Security       int       `json:"security"`
	Packages       []Package `json:"packages,omitempty"`
	RebootRequired bool      `json:"reboot_required"`
	CheckedAt      time.Time `json:"checked_at"`
	Error          string    `json:"error,omitempty"`
}

// MaxDiskPercent returns the fullest disk's usage percent.
func (r *Report) MaxDiskPercent() float64 {
	var m float64
	for _, d := range r.Disks {
		if d.Percent > m {
			m = d.Percent
		}
	}
	return m
}
