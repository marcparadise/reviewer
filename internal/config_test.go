package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultDataDir(t *testing.T) {
	cases := []struct {
		name string
		xdg  string
		home string
		want func(xdg, home string) string
	}{
		{
			name: "xdg set",
			xdg:  "/tmp/cfg-xdg",
			home: "/tmp/cfg-home",
			want: func(xdg, home string) string { return filepath.Join(xdg, "reviewer") },
		},
		{
			name: "xdg unset falls back to home",
			xdg:  "",
			home: "/tmp/cfg-home",
			want: func(xdg, home string) string { return filepath.Join(home, ".config", "reviewer") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.xdg)
			t.Setenv("HOME", tc.home)
			got := DefaultDataDir()
			want := tc.want(tc.xdg, tc.home)
			if got != want {
				t.Errorf("DefaultDataDir() = %q, want %q", got, want)
			}
		})
	}
}

func TestNewFromViperPortPrecedence(t *testing.T) {
	t.Run("default when nothing set", func(t *testing.T) {
		dir := t.TempDir()
		v := viper.New()
		if err := InitViper(v, dir); err != nil {
			t.Fatalf("InitViper: %v", err)
		}
		if got := NewFromViper(v, dir).Port; got != 8091 {
			t.Errorf("Port = %d, want 8091", got)
		}
	})

	t.Run("config.json overrides default", func(t *testing.T) {
		dir := t.TempDir()
		cfgWriteConfigJSON(t, dir, `{"port": 9090}`)
		v := viper.New()
		if err := InitViper(v, dir); err != nil {
			t.Fatalf("InitViper: %v", err)
		}
		if got := NewFromViper(v, dir).Port; got != 9090 {
			t.Errorf("Port = %d, want 9090", got)
		}
	})

	t.Run("env overrides config.json", func(t *testing.T) {
		dir := t.TempDir()
		cfgWriteConfigJSON(t, dir, `{"port": 9090}`)
		t.Setenv("REVIEWER_PORT", "9999")
		v := viper.New()
		if err := InitViper(v, dir); err != nil {
			t.Fatalf("InitViper: %v", err)
		}
		if got := NewFromViper(v, dir).Port; got != 9999 {
			t.Errorf("Port = %d, want 9999", got)
		}
	})
}

func TestInitViperMissingConfigFile(t *testing.T) {
	dir := t.TempDir()
	v := viper.New()
	if err := InitViper(v, dir); err != nil {
		t.Fatalf("InitViper with no config.json: %v", err)
	}
}

func TestInitViperMalformedConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgWriteConfigJSON(t, dir, `{not valid json`)
	v := viper.New()
	if err := InitViper(v, dir); err == nil {
		t.Fatal("InitViper with malformed config.json: want error, got nil")
	}
}

func TestConfigBindAddrAndReviewerURL(t *testing.T) {
	c := &Config{Port: 4321}
	if got, want := c.BindAddr(), "127.0.0.1:4321"; got != want {
		t.Errorf("BindAddr() = %q, want %q", got, want)
	}
	if got, want := c.ReviewerURL(), "http://127.0.0.1:4321"; got != want {
		t.Errorf("ReviewerURL() = %q, want %q", got, want)
	}
}

func TestConfigEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	c := &Config{DataDir: filepath.Join(dir, "data"), Port: 8080}
	if err := c.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{c.DataDir, c.ReposDir()} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}
}

func TestNewFromViperEmptyDataDirUsesDefault(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	cfg := NewFromViper(viper.New(), "")
	want := filepath.Join(xdg, "reviewer")
	if cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if got := cfg.DBPath(); got != filepath.Join(want, "reviewer.db") {
		t.Errorf("DBPath() = %q, want %q", got, filepath.Join(want, "reviewer.db"))
	}
}

func TestConfigEnsureDirsFailsWhenDataDirIsFile(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(dataDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	c := &Config{DataDir: dataDir, Port: 8080}
	if err := c.EnsureDirs(); err == nil {
		t.Fatal("EnsureDirs: want error, got nil")
	}
}

func cfgWriteConfigJSON(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
}
