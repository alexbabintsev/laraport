package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Host key policies.
const (
	HostKeyAcceptNew = "accept-new" // trust unknown hosts on first use (default)
	HostKeyStrict    = "strict"     // refuse hosts not already in known_hosts
)

// Settings are application-wide preferences. Every zero value means "use the
// default", so an absent `settings:` block keeps the built-in behaviour.
type Settings struct {
	DownloadsDir   string `yaml:"downloads_dir,omitempty"`    // where dumps/archives are saved (default ~/Downloads)
	HostKeyCheck   string `yaml:"host_key_check,omitempty"`   // accept-new (default) | strict
	WrapLogs       bool   `yaml:"wrap_logs,omitempty"`        // start log/output screens with line wrapping on
	HideStopped    bool   `yaml:"hide_stopped,omitempty"`     // list running containers only
	StatsInterval  int    `yaml:"stats_interval,omitempty"`   // Stats refresh, seconds (default 2)
	NoSQLHistory   bool   `yaml:"no_sql_history,omitempty"`   // do not persist SQL query history
	SQLHistorySize int    `yaml:"sql_history_size,omitempty"` // queries kept per database (default 200)
}

// Defaults.
const (
	DefaultStatsInterval  = 2
	DefaultSQLHistorySize = 200
	maxStatsInterval      = 60
	maxSQLHistorySize     = 10000
)

// Validate checks settings entered in the UI.
func (s Settings) Validate() error {
	var errs []string
	if s.HostKeyCheck != "" && s.HostKeyCheck != HostKeyAcceptNew && s.HostKeyCheck != HostKeyStrict {
		errs = append(errs, "host key check must be accept-new or strict")
	}
	if s.StatsInterval < 0 || s.StatsInterval > maxStatsInterval {
		errs = append(errs, "stats interval must be between 1 and 60 seconds")
	}
	if s.SQLHistorySize < 0 || s.SQLHistorySize > maxSQLHistorySize {
		errs = append(errs, "SQL history size must be between 1 and 10000")
	}
	if d := strings.TrimSpace(s.DownloadsDir); d != "" {
		p := expandHome(d)
		if !filepath.IsAbs(p) {
			errs = append(errs, "downloads directory must be an absolute path (or start with ~/)")
		} else if st, err := os.Stat(p); err == nil && !st.IsDir() {
			errs = append(errs, "downloads directory is a file")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Downloads returns the downloads directory with ~ expanded.
func (s Settings) Downloads() string {
	if d := strings.TrimSpace(s.DownloadsDir); d != "" {
		return expandHome(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "Downloads"
	}
	return filepath.Join(home, "Downloads")
}

// StrictHostKeys reports whether unknown SSH hosts must be refused.
func (s Settings) StrictHostKeys() bool { return s.HostKeyCheck == HostKeyStrict }

// StatsEvery returns the Stats refresh interval.
func (s Settings) StatsEvery() time.Duration {
	n := s.StatsInterval
	if n <= 0 || n > maxStatsInterval {
		n = DefaultStatsInterval
	}
	return time.Duration(n) * time.Second
}

// SQLHistoryLimit returns how many queries are kept per database (0 when
// history is disabled).
func (s Settings) SQLHistoryLimit() int {
	if s.NoSQLHistory {
		return 0
	}
	if s.SQLHistorySize <= 0 || s.SQLHistorySize > maxSQLHistorySize {
		return DefaultSQLHistorySize
	}
	return s.SQLHistorySize
}
