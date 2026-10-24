package hub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultConfigPath = "/etc/vpsdeck/hub.yaml"

type Thresholds struct {
	DiskWarn     float64       `yaml:"disk_warn" json:"disk_warn"`
	DiskCrit     float64       `yaml:"disk_crit" json:"disk_crit"`
	CPU          float64       `yaml:"cpu" json:"cpu"`
	Mem          float64       `yaml:"mem" json:"mem"`
	Sustain      time.Duration `yaml:"sustain" json:"sustain"`
	OfflineAfter time.Duration `yaml:"offline_after" json:"offline_after"`
}

type Config struct {
	Listen     string     `yaml:"listen"`
	PublicURL  string     `yaml:"public_url"` // the address agents use, e.g. https://203.0.113.5:8443
	TLSCert    string     `yaml:"tls_cert"`
	TLSKey     string     `yaml:"tls_key"`
	TLSDisable bool       `yaml:"tls_disable"` // when the hub sits behind your own HTTPS proxy
	BotToken   string     `yaml:"bot_token"`
	Admins     []int64    `yaml:"admins"`
	Lang       string     `yaml:"lang"`
	DBPath     string     `yaml:"db_path"`
	DigestTime string     `yaml:"digest_time"`
	Timezone   string     `yaml:"timezone"`
	Thresholds Thresholds `yaml:"thresholds"`
	// DisableUpdateCheck turns off the daily check for new releases on GitHub.
	DisableUpdateCheck bool `yaml:"disable_update_check"`

	loc *time.Location
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := Config{}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.applyDefaults()
	return &c, c.validate()
}

func (c *Config) applyDefaults() {
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.Listen == "" {
		c.Listen = ":8443"
	}
	if c.Lang != "ru" {
		c.Lang = "en"
	}
	if c.DBPath == "" {
		c.DBPath = "/var/lib/vpsdeck/hub.db"
	}
	if c.TLSCert == "" {
		c.TLSCert = filepath.Join(filepath.Dir(c.DBPath), "hub.crt")
	}
	if c.TLSKey == "" {
		c.TLSKey = filepath.Join(filepath.Dir(c.DBPath), "hub.key")
	}
	if c.DigestTime == "" {
		c.DigestTime = "09:00"
	}
	t := &c.Thresholds
	if t.DiskWarn == 0 {
		t.DiskWarn = 85
	}
	if t.DiskCrit == 0 {
		t.DiskCrit = 95
	}
	if t.CPU == 0 {
		t.CPU = 90
	}
	if t.Mem == 0 {
		t.Mem = 90
	}
	if t.Sustain == 0 {
		t.Sustain = 10 * time.Minute
	}
	if t.OfflineAfter == 0 {
		t.OfflineAfter = 3 * time.Minute
	}
}

func (c *Config) validate() error {
	if c.BotToken == "" {
		return errors.New("bot_token is required")
	}
	if c.PublicURL == "" {
		return errors.New("public_url is required (the address agents connect to)")
	}
	if len(c.Admins) == 0 {
		return errors.New("admins must contain at least one Telegram user ID")
	}
	if _, err := time.Parse("15:04", c.DigestTime); err != nil {
		return fmt.Errorf("digest_time must be HH:MM: %w", err)
	}
	c.loc = time.Local
	if c.Timezone != "" {
		loc, err := time.LoadLocation(c.Timezone)
		if err != nil {
			return fmt.Errorf("timezone: %w", err)
		}
		c.loc = loc
	}
	return nil
}

func (c *Config) IsAdmin(id int64) bool {
	for _, a := range c.Admins {
		if a == id {
			return true
		}
	}
	return false
}

// DevConfig is used by `vpsdeck hub -dev` when no config file is given.
func DevConfig() *Config {
	c := &Config{Listen: "127.0.0.1:8080", PublicURL: "http://127.0.0.1:8080", DBPath: "vpsdeck-dev.db", TLSDisable: true}
	c.applyDefaults()
	c.loc = time.Local
	return c
}
