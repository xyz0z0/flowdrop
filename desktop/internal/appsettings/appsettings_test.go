package appsettings

import (
	"encoding/json"
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

	want := Settings{Mode: ModeLocal, BindAddress: "192.168.1.20", Port: 9090}
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
	if err := os.WriteFile(path, []byte(`{"mode":"local","bind_address":"0.0.0.0","port":8080}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(path); err == nil || !found {
		t.Fatalf("Load(wildcard) = found=%v err=%v, want found=true with error", found, err)
	}
}

func TestSaveRefusesInvalidSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	if err := Save(path, Settings{Mode: ModeLocal, BindAddress: "0.0.0.0", Port: 8080}); err == nil {
		t.Fatal("Save accepted a wildcard bind address")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Save wrote a file for invalid settings (err=%v)", err)
	}
}

func TestSettingsFileHasNoPasswordField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	if err := Save(path, Settings{Mode: ModeLocal, BindAddress: "192.168.1.5", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(b))
	// The desktop no longer stores a password at all. The one credential the
	// file may carry is the native device token (desktop_device_token), which is
	// only ever replayed to its own Hub.
	for _, forbidden := range []string{"password", "passwd", "secret", "csrf"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("desktop settings contain %q: %s", forbidden, b)
		}
	}
}

func TestFileWithoutModeIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	// A file with no mode field no longer means "local": the mode is required.
	if err := os.WriteFile(path, []byte(`{"bind_address":"192.168.1.20","port":8080}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(path); err == nil || !found {
		t.Fatalf("Load(no mode) = found=%v err=%v, want found=true with an error", found, err)
	}
}

func TestDualModeRoundTripAndListeners(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.json")
	want := Settings{
		Mode:                    ModeLocal,
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

	// Saving the single-address mode again must omit the additional field: single
	// address mode writes no such field at all.
	single := Settings{Mode: ModeLocal, BindAddress: "192.168.1.20", Port: 9090}
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
	if got, found, err := Load(path); err != nil || !found {
		t.Fatalf("Load(single) = found=%v err=%v", found, err)
	} else if got.DualMode() || got.AdditionalListen() == nil || len(got.AdditionalListen()) != 0 {
		t.Fatalf("single-address settings = %+v, want a non-nil empty additional listener set", got)
	}
}

func TestValidateDualModeRules(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		want string
	}{
		{"dual", Settings{Mode: ModeLocal, BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"100.64.0.2"}}, ""},
		{"duplicate", Settings{Mode: ModeLocal, BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"192.168.1.2"}}, "duplicated"},
		{"two extras", Settings{Mode: ModeLocal, BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"100.64.0.2", "100.64.0.3"}}, "at most one"},
		{"wildcard extra", Settings{Mode: ModeLocal, BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"0.0.0.0"}}, "wildcard"},
		{"ipv6 extra", Settings{Mode: ModeLocal, BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{"fd7a::1"}}, "IPv4"},
		{"empty extra", Settings{Mode: ModeLocal, BindAddress: "192.168.1.2", Port: 8080, AdditionalBindAddresses: []string{""}}, "empty"},
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

func TestValidateMode(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		want string
	}{
		{"empty is rejected", Settings{BindAddress: "192.168.1.2", Port: 8080}, "运行模式"},
		{"explicit local", Settings{BindAddress: "192.168.1.2", Port: 8080, Mode: ModeLocal}, ""},
		{"remote", Settings{BindAddress: "192.168.1.2", Port: 8080, Mode: ModeRemote, RemoteURL: "http://192.168.31.184:8080"}, ""},
		{"unknown mode", Settings{BindAddress: "192.168.1.2", Port: 8080, Mode: "cloud"}, "无效"},
		{"remote without url", Settings{BindAddress: "192.168.1.2", Port: 8080, Mode: ModeRemote}, "需要填写"},
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

func TestValidateRemoteURL(t *testing.T) {
	valid := []string{
		"http://192.168.31.184:8080",
		"https://hub.example.com",
		"http://192.168.31.184:8080/",
		"http://10.0.0.5:8080//",
	}
	for _, raw := range valid {
		if err := ValidateRemoteURL(raw); err != nil {
			t.Fatalf("ValidateRemoteURL(%q) = %v, want nil", raw, err)
		}
	}
	invalid := []struct {
		raw  string
		want string
	}{
		{"", "需要填写"},
		{"   ", "需要填写"},
		{"192.168.31.184:8080", "http"},
		{"ftp://192.168.31.184:8080", "http"},
		{"http://", "主机名"},
		{"http:///inbox", "主机名"},
		{"http://192.168.31.184:8080/inbox", "路径"},
		{"http://192.168.31.184:8080/?token=x", "查询参数"},
		{"http://192.168.31.184:8080?token=x", "查询参数"},
		{"http://192.168.31.184:8080/#inbox", "# 片段"},
		{"http://user:pass@192.168.31.184:8080", "用户名"},
		{"http://user@192.168.31.184:8080", "用户名"},
	}
	for _, tc := range invalid {
		err := ValidateRemoteURL(tc.raw)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("ValidateRemoteURL(%q) = %v, want substring %q", tc.raw, err, tc.want)
		}
	}
}

func TestRemoteURLIsTrimmedAndOnlyKeptInRemoteMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	remote := Settings{BindAddress: "192.168.1.20", Port: 8080, Mode: ModeRemote, RemoteURL: "http://192.168.31.184:8080/"}
	if err := Save(path, remote); err != nil {
		t.Fatalf("Save(remote): %v", err)
	}
	got, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load = found=%v err=%v", found, err)
	}
	if !got.IsRemote() || got.RemoteURL != "http://192.168.31.184:8080" {
		t.Fatalf("remote settings = %+v, want the address without a trailing slash", got)
	}

	// A non-remote save must not persist a leftover remote URL.
	stale := Settings{Mode: ModeLocal, BindAddress: "192.168.1.20", Port: 8080, RemoteURL: "http://192.168.31.184:8080"}
	if err := Save(path, stale); err != nil {
		t.Fatalf("Save(local): %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "remote_url") || strings.Contains(string(raw), "192.168.31.184") {
		t.Fatalf("local settings kept a remote URL: %s", raw)
	}
	if got, _, err = Load(path); err != nil || got.IsRemote() || got.RemoteURL != "" {
		t.Fatalf("Load(local) = %+v err=%v, want local mode and no remote URL", got, err)
	}

	// A hand-edited file with both fields must load as local and stay usable.
	if err := os.WriteFile(path, []byte(`{"bind_address":"192.168.1.20","port":8080,"mode":"local","remote_url":"http://192.168.31.184:8080"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, found, err = Load(path)
	if err != nil || !found {
		t.Fatalf("Load(mixed) = found=%v err=%v", found, err)
	}
	if got.IsRemote() || got.RemoteURL != "" {
		t.Fatalf("mixed settings = %+v, want the remote URL ignored in local mode", got)
	}
}

// validDeviceToken is the exact shape the Hub issues: 32 random bytes in
// base64url raw encoding (no padding), i.e. 43 characters.
const validDeviceToken = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"

func TestDeviceIdentityRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	want := Settings{
		Mode:               ModeLocal,
		BindAddress:        "192.168.1.20",
		Port:               8080,
		DesktopDeviceID:    "dev_9xY-z_1",
		DesktopDeviceToken: validDeviceToken,
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load = found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
}

func TestInvalidDeviceTokenIsDropped(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"too short", validDeviceToken[:42]},
		{"too long", validDeviceToken + "A"},
		{"padding", validDeviceToken[:42] + "="},
		{"plus", validDeviceToken[:42] + "+"},
		{"slash instead of dash", validDeviceToken[:42] + "/"},
		{"space", validDeviceToken[:42] + " "},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "desktop.json")
			saved := Settings{
				Mode:               ModeLocal,
				BindAddress:        "192.168.1.20",
				Port:               8080,
				DesktopDeviceID:    "dev_1",
				DesktopDeviceToken: tc.token,
			}
			if err := Save(path, saved); err != nil {
				t.Fatalf("Save: %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.token != "" && strings.Contains(string(raw), tc.token) {
				t.Fatalf("Save wrote the invalid token %q: %s", tc.token, raw)
			}
			got, found, err := Load(path)
			if err != nil || !found {
				t.Fatalf("Load = found=%v err=%v", found, err)
			}
			if got.DesktopDeviceToken != "" {
				t.Fatalf("Load kept invalid token %q", got.DesktopDeviceToken)
			}
			// Everything else survives untouched.
			if got.BindAddress != "192.168.1.20" || got.Port != 8080 || got.DesktopDeviceID != "dev_1" {
				t.Fatalf("invalid token disturbed other fields: %+v", got)
			}
		})
	}
}

func TestInvalidDeviceIDIsDropped(t *testing.T) {
	for _, id := range []string{"has space", "quote\"", strings.Repeat("a", 65), "a/b", "é"} {
		path := filepath.Join(t.TempDir(), "desktop.json")
		if err := os.WriteFile(path, []byte(`{"mode":"local","bind_address":"192.168.1.20","port":8080,"desktop_device_id":`+mustJSON(t, id)+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		got, found, err := Load(path)
		if err != nil || !found {
			t.Fatalf("Load(bad id %q) = found=%v err=%v", id, found, err)
		}
		if got.DesktopDeviceID != "" {
			t.Fatalf("Load kept invalid device id %q", got.DesktopDeviceID)
		}
		if got.BindAddress != "192.168.1.20" || got.Port != 8080 {
			t.Fatalf("invalid device id disturbed other fields: %+v", got)
		}
	}
}

// TestDeviceIdentityIsIndependentPerField pins that a file with only one of the
// two values still loads; the missing one stays empty instead of failing.
func TestDeviceIdentityIsIndependentPerField(t *testing.T) {
	onlyToken := filepath.Join(t.TempDir(), "token-only.json")
	if err := os.WriteFile(onlyToken, []byte(`{"mode":"local","bind_address":"192.168.1.20","port":8080,"desktop_device_token":"`+validDeviceToken+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, found, err := Load(onlyToken)
	if err != nil || !found {
		t.Fatalf("Load(token only) = found=%v err=%v", found, err)
	}
	if got.DesktopDeviceID != "" || got.DesktopDeviceToken != validDeviceToken {
		t.Fatalf("token-only settings = %+v", got)
	}

	onlyID := filepath.Join(t.TempDir(), "id-only.json")
	if err := os.WriteFile(onlyID, []byte(`{"mode":"local","bind_address":"192.168.1.20","port":8080,"desktop_device_id":"dev_1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, found, err = Load(onlyID)
	if err != nil || !found {
		t.Fatalf("Load(id only) = found=%v err=%v", found, err)
	}
	if got.DesktopDeviceID != "dev_1" || got.DesktopDeviceToken != "" {
		t.Fatalf("id-only settings = %+v", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%v): %v", v, err)
	}
	return string(b)
}
