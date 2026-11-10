package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsAreSafe(t *testing.T) {
	c := Default()
	if !c.ListenIsLoopback() {
		t.Fatalf("default listen %q is not loopback", c.Listen)
	}
	if !c.CookieSecure {
		t.Fatal("default must require Secure cookies")
	}
	if c.MaxUploadBytes != 512<<20 {
		t.Fatalf("max upload = %d, want 512 MiB", c.MaxUploadBytes)
	}
	if c.PairLimitPerMinute != 5 {
		t.Fatalf("default pair limit = %d, want 5", c.PairLimitPerMinute)
	}
	if len(c.AllowedHosts) != 0 || len(c.TrustedProxies) != 0 {
		t.Fatal("defaults must not trust extra hosts or proxies")
	}
	if c.DiscoveryEnabled != nil {
		t.Fatal("discovery must default to automatic (nil), not an explicit override")
	}
	if !c.DiscoveryOn() || c.DiscoveryExplicit() {
		t.Fatal("discovery should be on-by-default but not explicitly configured")
	}
}

func TestDiscoveryConfig(t *testing.T) {
	// JSON false is an explicit opt-out and survives loading.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"discovery_enabled":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOWDROP_DATA_DIR", dir)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.DiscoveryOn() || !c.DiscoveryExplicit() {
		t.Fatalf("discovery_enabled=false not honored: %+v", c.DiscoveryEnabled)
	}

	// Env overrides the file and accepts the usual boolean spellings.
	t.Setenv("FLOWDROP_DISCOVERY_ENABLED", "true")
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.DiscoveryOn() || !c.DiscoveryExplicit() {
		t.Fatalf("FLOWDROP_DISCOVERY_ENABLED=true not honored: %+v", c.DiscoveryEnabled)
	}

	t.Setenv("FLOWDROP_DISCOVERY_ENABLED", "0")
	c, err = Load(path)
	if err != nil || c.DiscoveryOn() {
		t.Fatalf("FLOWDROP_DISCOVERY_ENABLED=0 not honored: %+v err=%v", c.DiscoveryEnabled, err)
	}

	t.Setenv("FLOWDROP_DISCOVERY_ENABLED", "not-a-bool")
	if _, err := Load(path); err == nil {
		t.Fatal("invalid FLOWDROP_DISCOVERY_ENABLED accepted")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":"127.0.0.1:9000","max_upload_bytes":1024}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOWDROP_LISTEN", "127.0.0.1:9100")
	t.Setenv("FLOWDROP_ALLOWED_HOSTS", "hub.lan, 100.64.0.1 ,, ")
	t.Setenv("FLOWDROP_DATA_DIR", dir)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9100" {
		t.Fatalf("env did not override file: %q", c.Listen)
	}
	if c.MaxUploadBytes != 1024 {
		t.Fatalf("file value lost: %d", c.MaxUploadBytes)
	}
	if len(c.AllowedHosts) != 2 || c.AllowedHosts[0] != "hub.lan" || c.AllowedHosts[1] != "100.64.0.1" {
		t.Fatalf("allowed hosts = %v", c.AllowedHosts)
	}
}

func TestMissingFileIsFine(t *testing.T) {
	t.Setenv("FLOWDROP_DATA_DIR", t.TempDir())
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing config file should not be an error: %v", err)
	}
	if c.Listen == "" {
		t.Fatal("defaults not applied")
	}
}

func TestPairLimit(t *testing.T) {
	// A non-positive value falls back to the default instead of disabling the
	// limit, so pairing-code guessing cannot be unbounded by a typo.
	c := Default()
	c.DataDir = t.TempDir()
	c.PairLimitPerMinute = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("zero pair limit rejected: %v", err)
	}
	if c.PairLimitPerMinute != 5 {
		t.Fatalf("zero pair limit = %d, want the 5 default", c.PairLimitPerMinute)
	}

	// The env var is the operator-facing name; the retired login one is gone.
	dir := t.TempDir()
	t.Setenv("FLOWDROP_DATA_DIR", dir)
	t.Setenv("FLOWDROP_PAIR_LIMIT_PER_MINUTE", "9")
	loaded, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.PairLimitPerMinute != 9 {
		t.Fatalf("FLOWDROP_PAIR_LIMIT_PER_MINUTE not applied: %d", loaded.PairLimitPerMinute)
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty listen", func(c *Config) { c.Listen = "" }},
		{"listen without port", func(c *Config) { c.Listen = "127.0.0.1" }},
		{"zero upload limit", func(c *Config) { c.MaxUploadBytes = 0 }},
		{"negative free space", func(c *Config) { c.MinFreeBytes = -1 }},
		{"bad proxy", func(c *Config) { c.TrustedProxies = []string{"not-an-ip"} }},
		{"bad host", func(c *Config) { c.AllowedHosts = []string{"http://evil"} }},
		{"zero session ttl", func(c *Config) { c.SessionTTLHours = 0 }},
	}
	for _, tc := range cases {
		c := Default()
		c.DataDir = t.TempDir()
		tc.mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate accepted invalid config", tc.name)
		}
	}
}

func TestListenIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"[::1]:8080":     true,
		"0.0.0.0:8080":   false,
		"192.168.1.5:80": false,
	} {
		c := Default()
		c.Listen = addr
		if got := c.ListenIsLoopback(); got != want {
			t.Errorf("%s: loopback = %v, want %v", addr, got, want)
		}
	}
}

// TestPlaintextLANModeRequiresSpecificListen guards the accepted HTTP/LAN mode:
// turning cookies non-Secure is only allowed when the operator names the exact
// interface, so a typo cannot silently publish plaintext HTTP on every NIC.
func TestPlaintextLANModeRequiresSpecificListen(t *testing.T) {
	ok := Default()
	ok.DataDir = t.TempDir()
	ok.Listen = "192.168.31.184:8080"
	ok.AllowedHosts = []string{"flowdrop.lan"}
	ok.CookieSecure = false
	if err := ok.Validate(); err != nil {
		t.Fatalf("explicit LAN IP with cookie_secure=false rejected: %v", err)
	}

	for _, listen := range []string{"0.0.0.0:8080", ":8080", "[::]:8080"} {
		c := Default()
		c.DataDir = t.TempDir()
		c.Listen = listen
		c.AllowedHosts = []string{"flowdrop.lan"}
		c.CookieSecure = false
		if err := c.Validate(); err == nil {
			t.Errorf("listen=%q with cookie_secure=false was accepted; want rejection", listen)
		}
	}

	// TLS/loopback deployments are unaffected by the wildcard rule.
	for _, listen := range []string{"0.0.0.0:8080", "[::]:8443"} {
		c := Default()
		c.DataDir = t.TempDir()
		c.Listen = listen
		c.CookieSecure = true
		if err := c.Validate(); err != nil {
			t.Errorf("listen=%q with cookie_secure=true rejected: %v", listen, err)
		}
	}
}

func TestAdditionalListenConfigAndDedupe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{
		"listen": "127.0.0.1:8080",
		"additional_listen": ["127.0.0.2:8080", "127.0.0.3:8081", "127.0.0.2:8080"]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOWDROP_DATA_DIR", dir)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Listens()
	want := []string{"127.0.0.1:8080", "127.0.0.2:8080", "127.0.0.3:8081"}
	if len(got) != len(want) {
		t.Fatalf("Listens() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Listens() = %v, want %v (primary must stay first, duplicates dropped)", got, want)
		}
	}

	// The env var is comma-separated and replaces the file list entirely.
	t.Setenv("FLOWDROP_ADDITIONAL_LISTEN", "127.0.0.4:9000, 127.0.0.5:9000")
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got = c.Listens()
	want = []string{"127.0.0.1:8080", "127.0.0.4:9000", "127.0.0.5:9000"}
	if len(got) != len(want) {
		t.Fatalf("env Listens() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env Listens() = %v, want %v (same port on different IPs must be kept)", got, want)
		}
	}

	// Equivalent spellings collapse, while same-port/different-IP stays.
	c = Default()
	c.Listen = "127.0.0.1:8080"
	c.AdditionalListen = []string{"127.0.0.1:08080", "127.0.0.2:8080", "127.0.0.1:8080"}
	got = c.Listens()
	want = []string{"127.0.0.1:8080", "127.0.0.2:8080"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("canonical Listens() = %v, want %v", got, want)
	}
}

func TestAdditionalListenValidation(t *testing.T) {
	valid := Default()
	valid.DataDir = t.TempDir()
	valid.CookieSecure = false
	valid.Listen = "127.0.0.1:8080"
	valid.AdditionalListen = []string{"127.0.0.2:8080", "100.64.0.5:8080", "[::1]:8080"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid additional listeners rejected: %v", err)
	}

	bad := []string{
		"",
		"127.0.0.2",
		"hub.local:8080",
		"0.0.0.0:8080",
		":8080",
		"[::]:8080",
		"127.0.0.2:notaport",
		"127.0.0.2:70000",
	}
	for _, addr := range bad {
		c := Default()
		c.DataDir = t.TempDir()
		c.CookieSecure = true
		c.Listen = "127.0.0.1:8080"
		c.AdditionalListen = []string{addr}
		if err := c.Validate(); err == nil {
			t.Errorf("additional_listen %q was accepted; want rejection", addr)
		}
	}

	// The plaintext-LAN wildcard rule applies to the whole listen set.
	c := Default()
	c.DataDir = t.TempDir()
	c.CookieSecure = false
	c.Listen = "192.168.1.10:8080"
	c.AdditionalListen = []string{"0.0.0.0:8081"}
	if err := c.Validate(); err == nil {
		t.Fatal("wildcard additional listen with cookie_secure=false was accepted")
	}
}

// withoutAutoBindEnv unset FLOWDROP_AUTO_BIND for one test so file and default
// assertions cannot be masked by the developer's shell, and restores it after.
func withoutAutoBindEnv(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("FLOWDROP_AUTO_BIND")
	if err := os.Unsetenv("FLOWDROP_AUTO_BIND"); err != nil {
		t.Fatalf("unset FLOWDROP_AUTO_BIND: %v", err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv("FLOWDROP_AUTO_BIND", old)
		} else {
			os.Unsetenv("FLOWDROP_AUTO_BIND")
		}
	})
}

// TestAutoBindConfig pins the auto_bind parsing rules: JSON, env, env-over-file
// precedence, the invalid-value error and the off-by-default regression.
func TestAutoBindConfig(t *testing.T) {
	withoutAutoBindEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FLOWDROP_DATA_DIR", dir)

	// JSON true enables it.
	write(`{"auto_bind":true}`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.AutoBind {
		t.Fatalf("auto_bind=true not honored: %v", c.AutoBind)
	}

	// A missing field keeps the safe default (off).
	write(`{}`)
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.AutoBind {
		t.Fatal("auto_bind must stay off when the field is absent")
	}

	// JSON true plus an explicit env opt-out: the env wins, like every other
	// field, so an operator can disable it without editing the file.
	t.Setenv("FLOWDROP_AUTO_BIND", "false")
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.AutoBind {
		t.Fatal("FLOWDROP_AUTO_BIND=false did not override auto_bind=true")
	}

	// Env alone enables it, including the file that omits the field.
	write(`{"listen":"127.0.0.1:8080"}`)
	t.Setenv("FLOWDROP_AUTO_BIND", "true")
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.AutoBind {
		t.Fatal("FLOWDROP_AUTO_BIND=true not honored")
	}

	// An unparsable value is an error that names the variable, not a silent
	// fallback: a typo must never leave auto binding unexpectedly on.
	t.Setenv("FLOWDROP_AUTO_BIND", "banana")
	if _, err := Load(path); err == nil {
		t.Fatal("FLOWDROP_AUTO_BIND=banana accepted")
	} else if !strings.Contains(err.Error(), "FLOWDROP_AUTO_BIND") {
		t.Fatalf("error does not name FLOWDROP_AUTO_BIND: %v", err)
	}
}

// TestAutoBindDefaultIsOff is the regression guard for the shipped default: an
// unconfigured Hub binds only the addresses the operator wrote down.
func TestAutoBindDefaultIsOff(t *testing.T) {
	withoutAutoBindEnv(t)
	if Default().AutoBind {
		t.Fatal("Default() must not auto bind")
	}
}
