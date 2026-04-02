package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration structure.
type Config struct {
	SDRpp    SDRppConfig     `yaml:"sdrpp"`
	Schedule []ScheduleEntry `yaml:"schedule"`
}

// SDRppConfig holds connection settings for the SDR++ rigctl server.
type SDRppConfig struct {
	Host    string   `yaml:"host"`
	Port    int      `yaml:"port"`
	Timeout Duration `yaml:"timeout"`
}

// ScheduleEntry defines a single scheduled recording task.
type ScheduleEntry struct {
	Name        string   `yaml:"name"`
	FrequencyHz int64    `yaml:"frequency_hz"`
	Mode        string   `yaml:"mode"`
	Duration    Duration `yaml:"duration"`
	Cron        string   `yaml:"cron"`
	Enabled     bool     `yaml:"enabled"`
	// Passband in Hz passed to the M command. 0 = radio default.
	Passband int `yaml:"passband"`
}

// Duration is a time.Duration that unmarshals from a YAML string like "5m" or "1h30m".
// yaml.v3 cannot natively parse Go duration strings into time.Duration (an int64),
// so we wrap it with a custom unmarshaler.
type Duration struct {
	time.Duration
}

// UnmarshalYAML implements yaml.Unmarshaler for Duration.
// It accepts both plain strings ("5m", "1h30m") and bare numbers (interpreted as nanoseconds).
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag == "!!int" || value.Tag == "!!float" {
		// Numeric value — treat as nanoseconds.
		var ns int64
		if err := value.Decode(&ns); err != nil {
			return fmt.Errorf("duration: cannot decode numeric value: %w", err)
		}
		d.Duration = time.Duration(ns)
		return nil
	}
	// String value — parse as a Go duration string.
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration: cannot decode value: %w", err)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration: %q is not a valid Go duration (examples: 30s, 5m, 1h30m): %w", s, err)
	}
	d.Duration = dur
	return nil
}

// LoadConfig reads and parses the YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Apply defaults before unmarshaling.
	cfg := &Config{
		SDRpp: SDRppConfig{
			Host:    "localhost",
			Port:    4532,
			Timeout: Duration{5 * time.Second},
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	// Fill in any zero-value defaults that weren't set in the file.
	if cfg.SDRpp.Host == "" {
		cfg.SDRpp.Host = "localhost"
	}
	if cfg.SDRpp.Port == 0 {
		cfg.SDRpp.Port = 4532
	}
	if cfg.SDRpp.Timeout.Duration == 0 {
		cfg.SDRpp.Timeout = Duration{5 * time.Second}
	}

	// Validate every schedule entry.
	for i := range cfg.Schedule {
		if err := validateEntry(i, &cfg.Schedule[i]); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

func validateEntry(idx int, e *ScheduleEntry) error {
	if e.Name == "" {
		e.Name = fmt.Sprintf("entry-%d", idx)
	}
	if e.FrequencyHz <= 0 {
		return fmt.Errorf("schedule[%d] %q: frequency_hz must be positive, got %d",
			idx, e.Name, e.FrequencyHz)
	}
	if e.Duration.Duration <= 0 {
		return fmt.Errorf("schedule[%d] %q: duration must be a positive duration string (e.g. \"5m\", \"1h30m\")",
			idx, e.Name)
	}
	if e.Cron == "" {
		return fmt.Errorf("schedule[%d] %q: cron expression is required", idx, e.Name)
	}

	e.Mode = strings.ToUpper(e.Mode)
	if e.Mode == "" {
		e.Mode = "FM"
	}
	validModes := map[string]bool{
		"USB": true, "LSB": true, "AM": true, "FM": true, "WFM": true,
		"CW": true, "CWR": true, "RTTY": true, "RTTYR": true,
		"DSB": true, "RAW": true, "NFM": true,
	}
	if !validModes[e.Mode] {
		return fmt.Errorf("schedule[%d] %q: unsupported mode %q (valid: USB LSB AM FM WFM CW CWR RTTY DSB NFM RAW)",
			idx, e.Name, e.Mode)
	}

	// Validate the cron expression by parsing it and checking it fires at least once.
	if next := NextCronTime(e.Cron); next.IsZero() {
		return fmt.Errorf("schedule[%d] %q: invalid cron expression %q", idx, e.Name, e.Cron)
	}

	return nil
}

// FormatFrequency returns a human-readable frequency string (e.g. "145.800 MHz").
func FormatFrequency(hz int64) string {
	switch {
	case hz >= 1_000_000_000:
		return fmt.Sprintf("%.3f GHz", float64(hz)/1e9)
	case hz >= 1_000_000:
		return fmt.Sprintf("%.3f MHz", float64(hz)/1e6)
	case hz >= 1_000:
		return fmt.Sprintf("%.3f kHz", float64(hz)/1e3)
	default:
		return fmt.Sprintf("%d Hz", hz)
	}
}
