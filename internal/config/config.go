package config

import (
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr          string
	DatabasePath  string
	PublicURL     string
	MasterKey     []byte
	SessionSecret []byte
	SMTPHost      string
	SMTPPort      int
	SMTPUser      string
	SMTPPassword  string
	SMTPFrom      string
	PollInterval  time.Duration
	ProbeInterval time.Duration
	ModelInterval time.Duration
	EmailInterval time.Duration
}

func Load() (Config, error) {
	c := Config{
		Addr:          env("RATEWATCH_ADDR", ":8080"),
		DatabasePath:  env("RATEWATCH_DB", "ratewatch.db"),
		PublicURL:     env("RATEWATCH_PUBLIC_URL", "http://localhost:8080"),
		SMTPHost:      os.Getenv("RATEWATCH_SMTP_HOST"),
		SMTPUser:      os.Getenv("RATEWATCH_SMTP_USER"),
		SMTPPassword:  os.Getenv("RATEWATCH_SMTP_PASSWORD"),
		SMTPFrom:      os.Getenv("RATEWATCH_SMTP_FROM"),
		PollInterval:  duration("RATEWATCH_POLL_INTERVAL", 45*time.Second),
		ProbeInterval: duration("RATEWATCH_PROBE_INTERVAL", 5*time.Minute),
		ModelInterval: duration("RATEWATCH_MODEL_INTERVAL", 30*time.Minute),
		EmailInterval: duration("RATEWATCH_EMAIL_INTERVAL", 10*time.Minute),
	}
	c.SMTPPort, _ = strconv.Atoi(env("RATEWATCH_SMTP_PORT", "587"))
	var err error
	c.MasterKey, err = secret("RATEWATCH_MASTER_KEY")
	if err != nil {
		return Config{}, err
	}
	c.SessionSecret, err = secret("RATEWATCH_SESSION_SECRET")
	if err != nil {
		return Config{}, err
	}
	return c, nil
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func duration(k string, fallback time.Duration) time.Duration {
	if v, e := time.ParseDuration(os.Getenv(k)); e == nil {
		return v
	}
	return fallback
}
func secret(k string) ([]byte, error) {
	v := os.Getenv(k)
	if v == "" {
		return nil, errors.New(k + " is required (base64 encoded 32+ bytes)")
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) < 32 {
		return nil, errors.New(k + " must be base64 encoded and at least 32 bytes")
	}
	return b[:32], nil
}
