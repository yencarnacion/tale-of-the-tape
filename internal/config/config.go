package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App       App       `yaml:"app"`
	Storage   Storage   `yaml:"storage"`
	Import    Import    `yaml:"import"`
	Massive   Massive   `yaml:"massive"`
	Chart     Chart     `yaml:"chart"`
	Analytics Analytics `yaml:"analytics"`
}
type App struct {
	Name     string `yaml:"name"`
	Addr     string `yaml:"addr"`
	Timezone string `yaml:"timezone"`
}
type Storage struct {
	Path        string        `yaml:"path"`
	ImportsPath string        `yaml:"imports_path"`
	BackupsPath string        `yaml:"backups_path"`
	BusyTimeout time.Duration `yaml:"busy_timeout"`
}
type Import struct {
	DefaultBroker       string  `yaml:"default_broker"`
	AssumedTimezone     string  `yaml:"assumed_timezone"`
	CalendarAttribution string  `yaml:"calendar_attribution"`
	ScratchTolerance    float64 `yaml:"scratch_tolerance"`
	MaximumUploadMB     int64   `yaml:"maximum_upload_mb"`
}
type Massive struct {
	APIKey             string        `yaml:"api_key"`
	RequestTimeout     time.Duration `yaml:"request_timeout"`
	MaximumRetries     int           `yaml:"maximum_retries"`
	ChartBarSize       string        `yaml:"chart_bar_size"`
	ChartPaddingBefore time.Duration `yaml:"chart_padding_before"`
	ChartPaddingAfter  time.Duration `yaml:"chart_padding_after"`
	PreferNBBO         bool          `yaml:"prefer_nbbo_for_excursions"`
	FallbackToTrades   bool          `yaml:"fallback_to_trades"`
	PersistRawQuotes   bool          `yaml:"persist_raw_quotes"`
	PersistRawTrades   bool          `yaml:"persist_raw_trades"`
}
type Chart struct {
	IncludeExtendedHours bool     `yaml:"include_extended_hours"`
	DefaultTimeframe     string   `yaml:"default_timeframe"`
	EnabledIndicators    []string `yaml:"enabled_indicators"`
}
type Analytics struct {
	KellyMinimumSample        int    `yaml:"kelly_minimum_sample"`
	DefaultDateRange          string `yaml:"default_date_range"`
	IncludeCommissionsAndFees bool   `yaml:"include_commissions_and_fees"`
}

func Defaults() Config {
	return Config{App: App{"tale-of-the-tape", "127.0.0.1:3000", "America/New_York"}, Storage: Storage{"data/tale-of-the-tape.db", "data/imports", "backups", 5 * time.Second}, Import: Import{"thinkorswim", "America/New_York", "exit_date", .01, 25}, Massive: Massive{"", 30 * time.Second, 6, "1m", 30 * time.Minute, 30 * time.Minute, true, true, false, false}, Chart: Chart{true, "1m", []string{"vwap", "sma9", "sma20", "ema9", "ema20", "bollinger20", "volume"}}, Analytics: Analytics{30, "month_to_date", true}}
}
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && os.Getenv(strings.TrimSpace(k)) == "" {
			_ = os.Setenv(strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), "\"'"))
		}
	}
	return s.Err()
}
func Load(path string) (Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	if v := os.Getenv("MASSIVE_API_KEY"); v != "" {
		c.Massive.APIKey = v
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.App.Name == "" {
		return fmt.Errorf("app.name is required")
	}
	host, _, err := net.SplitHostPort(c.App.Addr)
	if err != nil {
		return fmt.Errorf("app.addr: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("app.addr must be a loopback address")
	}
	if _, err := time.LoadLocation(c.App.Timezone); err != nil {
		return fmt.Errorf("app.timezone: %w", err)
	}
	if _, err := time.LoadLocation(c.Import.AssumedTimezone); err != nil {
		return fmt.Errorf("import.assumed_timezone: %w", err)
	}
	if c.Storage.Path == "" || c.Storage.BusyTimeout <= 0 {
		return fmt.Errorf("storage.path and positive busy_timeout required")
	}
	if c.Import.MaximumUploadMB < 1 {
		return fmt.Errorf("import.maximum_upload_mb must be positive")
	}
	return nil
}
