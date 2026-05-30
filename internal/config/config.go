package config

import (
	"fmt"
	"os"
	"time"
)

const (
	DefaultDBPath    = "/tmp/networkmon/networkmon.db"
	DefaultSocket    = "/run/networkmon/networkmond.sock"
	DefaultInterval  = 500 * time.Millisecond
	DefaultRetention = 24 * time.Hour
)

type Config struct {
	DBPath    string        `json:"db_path"`
	Socket    string        `json:"socket"`
	Interval  time.Duration `json:"-"`
	Retention time.Duration `json:"-"`
}

type WireConfig struct {
	DBPath       string `json:"db_path"`
	Socket       string `json:"socket"`
	Interval     string `json:"interval"`
	Retention    string `json:"retention"`
	IntervalMS   int64  `json:"interval_ms"`
	RetentionSec int64  `json:"retention_sec"`
}

func Default() Config {
	return Config{
		DBPath:    getenv("NETWORKMON_DB", DefaultDBPath),
		Socket:    getenv("NETWORKMON_SOCKET", DefaultSocket),
		Interval:  durationEnv("NETWORKMON_INTERVAL", DefaultInterval),
		Retention: durationEnv("NETWORKMON_RETENTION", DefaultRetention),
	}
}

func (c Config) Wire() WireConfig {
	return WireConfig{
		DBPath:       c.DBPath,
		Socket:       c.Socket,
		Interval:     c.Interval.String(),
		Retention:    c.Retention.String(),
		IntervalMS:   c.Interval.Milliseconds(),
		RetentionSec: int64(c.Retention.Seconds()),
	}
}

func (c Config) Validate() error {
	if c.DBPath == "" {
		return fmt.Errorf("db path is empty")
	}
	if c.Socket == "" {
		return fmt.Errorf("socket path is empty")
	}
	if c.Interval < 100*time.Millisecond {
		return fmt.Errorf("interval must be at least 100ms")
	}
	if c.Retention < time.Minute {
		return fmt.Errorf("retention must be at least 1m")
	}
	return nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
