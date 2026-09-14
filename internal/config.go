package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// loopbackHost is the only address the server binds to. Remote access is
// intentionally unsupported until it can be done safely (with authentication),
// so the bind host is a constant rather than a configurable value.
const loopbackHost = "127.0.0.1"

type Config struct {
	DataDir string
	Port    int
}

func DefaultDataDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "reviewer")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "reviewer")
}

// NewFromViper builds a Config from viper values plus the resolved data dir,
// applying built-in defaults where viper has no value. The bind host is not
// read from config — the server is loopback-only by design.
func NewFromViper(v *viper.Viper, dataDir string) *Config {
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	port := v.GetInt("port")
	if port == 0 {
		port = 8091
	}
	return &Config{DataDir: dataDir, Port: port}
}

// InitViper configures a viper instance to load config.json from dataDir and
// bind environment variables with the REVIEWER_ prefix.
func InitViper(v *viper.Viper, dataDir string) error {
	v.SetConfigName("config")
	v.SetConfigType("json")
	v.AddConfigPath(dataDir)
	v.SetEnvPrefix("REVIEWER")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return err
		}
	}
	return nil
}

func (c *Config) ReposDir() string {
	return filepath.Join(c.DataDir, "repos")
}

func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "reviewer.db")
}

// BindAddr is the host:port the server listens on. Always loopback.
func (c *Config) BindAddr() string {
	return fmt.Sprintf("%s:%d", loopbackHost, c.Port)
}

func (c *Config) ReviewerURL() string {
	return fmt.Sprintf("http://%s:%d", loopbackHost, c.Port)
}

func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.ReposDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
