package main

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want [4]uint16
		ok   bool
	}{
		{"0.1.0.0", [4]uint16{0, 1, 0, 0}, true},
		{"1", [4]uint16{1, 0, 0, 0}, true},
		{"1.2.3", [4]uint16{1, 2, 3, 0}, true},
		{" 1.2.3.4 ", [4]uint16{1, 2, 3, 4}, true},
		{"1.2.3.4.5", [4]uint16{}, false},
		{"", [4]uint16{}, false},
		{"1.x.3.4", [4]uint16{}, false},
		{"1.2.3.65536", [4]uint16{}, false},
	}
	for _, tc := range tests {
		got, err := parseVersion(tc.in)
		if tc.ok {
			if err != nil {
				t.Errorf("parseVersion(%q) returned error: %v", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("parseVersion(%q) = %v, want %v", tc.in, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("parseVersion(%q) = %v, want an error", tc.in, got)
		}
	}
}

func TestVersionInfoLanguages(t *testing.T) {
	vi, err := versionInfo([4]uint16{0, 1, 0, 0}, "flowdrop-desktop.exe")
	if err != nil {
		t.Fatalf("versionInfo: %v", err)
	}

	table := vi.Table()
	if len(table) != 2 {
		t.Fatalf("got %d language tables, want 2 (0x0409, 0x0804)", len(table))
	}
	for _, lang := range []uint16{langEN, langZH} {
		st := table[lang]
		if st == nil {
			t.Fatalf("missing language table for 0x%04x", lang)
		}
		checks := map[string]string{
			version.FileVersion:      "0.1.0.0",
			version.ProductVersion:   "0.1.0.0",
			version.ProductName:      "FlowDrop",
			version.FileDescription:  "FlowDrop 流转",
			version.CompanyName:      "FlowDrop",
			version.OriginalFilename: "flowdrop-desktop.exe",
			version.InternalName:     "flowdrop-desktop",
		}
		for key, want := range checks {
			if got := (*st)[key]; got != want {
				t.Errorf("lang 0x%04x %s = %q, want %q", lang, key, got, want)
			}
		}
	}
}

// stampedResourceSet mirrors what stampEXE builds, without touching a real exe.
func stampedResourceSet(t *testing.T, originalFilename string) *winres.ResourceSet {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	ico, err := winres.NewIconFromResizedImage(img, iconSizes)
	if err != nil {
		t.Fatalf("NewIconFromResizedImage: %v", err)
	}

	vi, err := versionInfo([4]uint16{0, 1, 0, 0}, originalFilename)
	if err != nil {
		t.Fatalf("versionInfo: %v", err)
	}

	rs := &winres.ResourceSet{}
	if err := rs.SetIcon(winres.ID(1), ico); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}
	rs.SetVersionInfo(vi)
	if err := rs.Set(winres.RT_MANIFEST, winres.ID(1), winres.LCIDDefault, []byte(manifestXML)); err != nil {
		t.Fatalf("Set manifest: %v", err)
	}
	return rs
}

func TestVerifyResourcesAcceptsStampedSet(t *testing.T) {
	for _, name := range []string{"flowdrop-desktop.exe", "flowdrop-desktop-arm64.exe"} {
		rs := stampedResourceSet(t, name)
		out := &bytes.Buffer{}
		if problems := verifyResources(rs, out); len(problems) != 0 {
			t.Errorf("%s: unexpected problems %v\n%s", name, problems, out.String())
		}
		log := out.String()
		for _, want := range []string{"RT_ICON", "RT_GROUP_ICON", "RT_VERSION", "RT_MANIFEST", `ProductName="FlowDrop"`} {
			if !strings.Contains(log, want) {
				t.Errorf("%s: report is missing %q\n%s", name, want, log)
			}
		}
	}
}

func TestVerifyResourcesRejectsEmptySet(t *testing.T) {
	problems := verifyResources(&winres.ResourceSet{}, &bytes.Buffer{})
	if len(problems) == 0 {
		t.Fatal("verifyResources accepted an exe with no resources")
	}
	joined := strings.Join(problems, "; ")
	for _, want := range []string{"RT_ICON", "RT_GROUP_ICON", "RT_MANIFEST", "RT_VERSION"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems are missing %q: %s", want, joined)
		}
	}
}

func TestManifestHasDPISettings(t *testing.T) {
	for _, want := range []string{
		`level="asInvoker"`,
		`uiAccess="false"`,
		`>true/pm</dpiAware>`,
		`>PerMonitorV2,PerMonitor</dpiAwareness>`,
		`{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}`,
		`<longPathAware`,
	} {
		if !strings.Contains(manifestXML, want) {
			t.Errorf("manifest is missing %q", want)
		}
	}
	if strings.Contains(manifestXML, "requireAdministrator") {
		t.Error("manifest must not request requireAdministrator")
	}
}
