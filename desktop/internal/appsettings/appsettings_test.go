package appsettings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsAreUsable(t *testing.T) {
	d := Default()
	if err := d.Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v", err)
	}
	if got, want := d.Listen(), "127.0.0.1:8080"; got != want {
		t.Fatalf("Default().Listen() = %q, want %q", got, want)
	}
}

func TestValidateBindAddress(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		wantErr string
	}{
		{"loopback", "127.0.0.1", ""},
		{"lan", "192.168.1.10", ""},
		{"tailscale", "100.64.1.2", ""},
		{"wildcard v4", "0.0.0.0", "wildcard"},
		{"wildcard v6", "::", "IPv4"},
		{"empty", "", "empty"},
		{"not an ip", "example.local", "not an IP"},
		{"ipv6", "fd7a:115c:a1e0::1", "IPv4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBindAddress(tc.addr)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateBindAddress(%q) = %v, want nil", tc.addr, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateBindAddress(%q) = nil, want error containing %q", tc.addr, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateBindAddress(%q) = %v, want substring %q", tc.addr, err, tc.wantErr)
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 70000} {
		if err := ValidatePort(port); err == nil {
			t.Fatalf("ValidatePort(%d) = nil, want error", port)
		}
	}
	for _, port := range []int{1, 8080, 65535} {
		if err := ValidatePort(port); err != nil {
			t.Fatalf("ValidatePort(%d) = %v, want nil", port, err)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "desktop.json")

	if _, found, err := Load(path); err != nil || found {
		t.Fatalf("Load(missing) = found=%v err=%v, want found=false err=nil", found, err)
	}

	want := Settings{BindAddress: "192.168.1.20", Port: 9090}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("settings file mode = %o, want 600", perm)
	}
	got, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load = found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsUnsafeSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.json")
	if err := os.WriteFile(path, []byte(`{"bind_address":"0.0.0.0","port":8080}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(path); err == nil || !found {
		t.Fatalf("Load(wildcard) = found=%v err=%v, want found=true with error", found, err)
	}
}

func TestSaveRefusesInvalidSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	if err := Save(path, Settings{BindAddress: "0.0.0.0", Port: 8080}); err == nil {
		t.Fatal("Save accepted a wildcard bind address")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Save wrote a file for invalid settings (err=%v)", err)
	}
}

func TestSettingsFileHasNoPasswordField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	if err := Save(path, Settings{BindAddress: "192.168.1.5", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(b))
	for _, forbidden := range []string{"password", "passwd", "token", "secret"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("desktop settings contain %q: %s", forbidden, b)
		}
	}
}

func TestLegacySingleAddressFileStillLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.json")
	// Exactly what the previous desktop version wrote: no additional field.
	legacy := "{\"bind_address\":\"192.168.1.20\",\"port\":8080}"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load(legacy) = found=%v err=%v", found, err)
	}
	if got.DualMode() {
		t.Fatalf("legacy settings became dual mode: %+v", got)
	}
	if got.BindAddress != "192.168.1.20" || got.Port != 8080 {
		t.Fatalf("legacy settings = %+v, want the old bind/port unchanged", got)
	}
	if extra := got.AdditionalListen(); extra == nil || len(extra) != 0 {
		t.Fatalf("AdditionalListen(legacy) = %#v, want a non-nil empty slice (explicit single mode)", extra)
	}
}

func TestDualModeRoundTripAndListeners(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.json")
	want := Settings{
		BindAddress:             "192.168.1.20",
		Port:                    9090,
		AdditionalBindAddresses: []string{"100.64.1.2"},
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"additional_bind_addresses": [`) {
		t.Fatalf("dual settings were not persisted explicitly: %s", raw)
	}
	got, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load = found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
	if !got.DualMode() {
		t.Fatal("DualMode = false for a saved dual configuration")
	}
	if all := got.AllBindAddresses(); !reflect.DeepEqual(all, []string{"192.168.1.20", "100.64.1.2"}) {
		t.Fatalf("AllBindAddresses = %v", all)
	}
	if extra := got.AdditionalListen(); !reflect.DeepEqual(extra, []string{"100.64.1.2:9090"}) {
		t.Fatalf("AdditionalListen = %v", extra)
	}

	// Saving the single-address mode again must omit the additional field so the
	// file stays compatible with older desktop builds.
	single := Settings{BindAddress: "192.168.1.20", Port: 9090}
	if err := Save(path, single); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "additional_bind_addresses") {
		t.Fatalf("single-address settings kept a stale additional field: %s", raw)
	}
}

func TestValidateDualModeRules(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		want string
	}{
		{"dual", Settings{BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"100.64.0.2"}}, ""},
		{"duplicate", Settings{BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"192.168.1.2"}}, "duplicated"},
		{"two extras", Settings{BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"100.64.0.2", "100.64.0.3"}}, "at most one"},
		{"wildcard extra", Settings{BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"0.0.0.0"}}, "wildcard"},
		{"ipv6 extra", Settings{BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"fd7a::1"}}, "IPv4"},
		{"empty extra", Settings{BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{""}}, "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate(%+v) = %v, want nil", tc.s, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate(%+v) = %v, want substring %q", tc.s, err, tc.want)
			}
		})
	}
}
