// Command winres stamps a FlowDrop Windows desktop executable with an
// application icon, VS_VERSION_INFO and an application manifest (DPI aware,
// asInvoker, long path aware).
//
// It is a pure-Go post-processing step: it patches the .rsrc section of an
// already built exe, so the desktop build needs neither CGO, nor mingw/windres,
// nor the wails3 CLI, and no .syso file lands in the desktop module.
//
// Usage (from this directory):
//
//	go run . -exe ../../dist/flowdrop-desktop.exe \
//	         -icon ../../desktop/assets/icon.png \
//	         -arch amd64 -version 0.1.0.0
//
// Read the resources back and check them (what scripts/build-desktop-windows.sh
// does after stamping, and how the exe is validated by hand):
//
//	go run . -verify -exe ../../dist/flowdrop-desktop.exe
//
// The exe is replaced atomically: resources are written to a temporary file next
// to it and then renamed over it. Any failure exits non-zero and leaves the
// original exe untouched.
package main

import (
	"debug/pe"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

const (
	// defaultVersion is the FileVersion/ProductVersion stamped into the exe.
	defaultVersion = "0.1.0.0"

	// productName is used for ProductName, CompanyName and FileDescription.
	productName = "FlowDrop"

	// internalName is the OriginalFilename-free binary name.
	internalName = "flowdrop-desktop"

	// LCIDs used for the version resource: en-US and zh-CN.
	langEN = 0x0409
	langZH = 0x0804
)

// iconSizes are the embedded ICO sizes. Explorer picks the closest match for
// small/medium/large/extra-large views, so the bitmap is never stretched.
var iconSizes = []int{256, 128, 64, 48, 32, 16}

// manifestXML is embedded as RT_MANIFEST/1.
//
//   - PerMonitorV2 (falling back to PerMonitor) keeps the WebView2 window crisp
//     when it moves between monitors with different scale factors; dpiAware
//     "true/pm" is the pre-Windows-10 fallback for the same setting.
//   - asInvoker: FlowDrop needs no elevation and must not trigger a UAC prompt.
//   - longPathAware lets paths beyond MAX_PATH be used when the OS allows it.
//   - supportedOS Win10 covers Windows 10 and 11.
//
// Written as raw XML because winres' helper emits "permonitorv2,system", while
// the per-monitor fallback we want is PerMonitor.
const manifestXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2,PerMonitor</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </windowsSettings>
  </application>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
</assembly>
`

func main() {
	exePath := flag.String("exe", "", "path to the built Windows .exe (required)")
	iconPath := flag.String("icon", "../../desktop/assets/icon.png", "application icon (PNG, 256x256)")
	arch := flag.String("arch", "", "target architecture: amd64 or arm64 (checked against the PE header)")
	verStr := flag.String("version", defaultVersion, "file/product version, e.g. 0.1.0.0")
	verifyOnly := flag.Bool("verify", false, "only read back and check the resources of an already stamped exe")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s -exe <path> [-icon <png>] [-arch amd64|arm64] [-version 0.1.0.0]\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "       %s -verify -exe <path>\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *exePath == "" {
		flag.Usage()
		fatal(errors.New("-exe is required"))
	}

	var err error
	if *verifyOnly {
		err = verifyEXE(*exePath)
	} else {
		err = stampEXE(*exePath, *iconPath, *arch, *verStr)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "winres: %v\n", err)
	os.Exit(1)
}

// stampEXE adds icon, version info and manifest to exePath, atomically.
func stampEXE(exePath, iconPath, arch, verStr string) error {
	fileVer, err := parseVersion(verStr)
	if err != nil {
		return err
	}
	if err := checkArch(exePath, arch); err != nil {
		return err
	}

	src, err := os.Open(exePath)
	if err != nil {
		return err
	}
	defer src.Close()

	signed, err := winres.IsSignedEXE(src)
	if err != nil {
		return fmt.Errorf("%s: not a usable PE image: %w", exePath, err)
	}
	if signed {
		return fmt.Errorf("%s is Authenticode-signed: refusing to modify it (stamp first, sign afterwards)", exePath)
	}

	// A plain `go build` exe has no .rsrc section at all; that is the normal
	// case, not an error. When the exe was already stamped once (a rebuild with
	// unchanged inputs may leave the previously stamped file in place), the
	// existing resources are only ours and are replaced, so stamping stays
	// idempotent. Anything we did not write makes us stop rather than silently
	// drop it.
	if err := checkOwnedResources(src); err != nil && !errors.Is(err, winres.ErrNoResources) {
		return fmt.Errorf("%s: %w", exePath, err)
	}
	rs := &winres.ResourceSet{}

	ico, err := loadIcon(iconPath)
	if err != nil {
		return err
	}
	if err := rs.SetIcon(winres.ID(1), ico); err != nil {
		return fmt.Errorf("%s: adding icon: %w", exePath, err)
	}

	vi, err := versionInfo(fileVer, filepath.Base(exePath))
	if err != nil {
		return err
	}
	rs.SetVersionInfo(vi)

	if err := rs.Set(winres.RT_MANIFEST, winres.ID(1), winres.LCIDDefault, []byte(manifestXML)); err != nil {
		return fmt.Errorf("%s: adding manifest: %w", exePath, err)
	}

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(exePath), filepath.Base(exePath)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Remove the temporary file on any path that does not end with the rename.
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if err := rs.WriteToEXE(tmp, src); err != nil {
		if errors.Is(err, winres.ErrSignedPE) {
			return fmt.Errorf("%s is Authenticode-signed: refusing to modify it", exePath)
		}
		return fmt.Errorf("%s: writing resources: %w", exePath, err)
	}
	if err := tmp.Sync(); err != nil {
		return err
	}

	// os.CreateTemp uses 0600; the exe must stay executable for the packaging
	// step and for "just copy it to Windows".
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exePath); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, exePath); err != nil {
		return err
	}
	committed = true

	fi, err := os.Stat(exePath)
	if err != nil {
		return err
	}

	fmt.Printf("stamped: %s\n", exePath)
	fmt.Printf("resources: %d (RT_ICON x%d, RT_GROUP_ICON, RT_VERSION x2, RT_MANIFEST)\n", rs.Count(), len(iconSizes))
	fmt.Printf("version: %s  size: %d bytes  mode: %s\n", verStr, fi.Size(), fi.Mode().Perm())
	return nil
}

// parseVersion turns "0.1.0.0" into the 4 uint16 words of a version resource.
func parseVersion(s string) ([4]uint16, error) {
	var v [4]uint16
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) < 1 || len(parts) > 4 {
		return v, fmt.Errorf("invalid -version %q: want 1 to 4 dot-separated numbers, e.g. %s", s, defaultVersion)
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 16)
		if err != nil {
			return v, fmt.Errorf("invalid -version %q: %q is not a number between 0 and 65535", s, p)
		}
		v[i] = uint16(n)
	}
	return v, nil
}

// versionInfo builds the VS_VERSION_INFO structure for both en-US and zh-CN.
func versionInfo(v [4]uint16, originalFilename string) (version.Info, error) {
	vi := version.Info{
		FileVersion:    v,
		ProductVersion: v,
		Type:           version.App,
	}
	verText := fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])

	props := [][2]string{
		{version.ProductName, productName},
		{version.FileDescription, "FlowDrop 流转"},
		{version.CompanyName, productName},
		{version.OriginalFilename, originalFilename},
		{version.InternalName, internalName},
	}

	for _, lang := range []uint16{langEN, langZH} {
		if err := vi.Set(lang, version.FileVersion, verText); err != nil {
			return version.Info{}, err
		}
		if err := vi.Set(lang, version.ProductVersion, verText); err != nil {
			return version.Info{}, err
		}
		for _, p := range props {
			if err := vi.Set(lang, p[0], p[1]); err != nil {
				return version.Info{}, err
			}
		}
	}
	return vi, nil
}

// ownedTypes are the resource types this tool owns, i.e. the ones it may
// replace in a previously stamped exe.
var ownedTypes = map[winres.Identifier]bool{
	winres.RT_ICON:       true,
	winres.RT_GROUP_ICON: true,
	winres.RT_VERSION:    true,
	winres.RT_MANIFEST:   true,
}

// checkOwnedResources returns winres.ErrNoResources when the exe has no resource
// section, nil when everything it contains belongs to this tool, and an error
// when the exe carries resources we would have to throw away.
func checkOwnedResources(src io.ReadSeeker) error {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rs, err := winres.LoadFromEXE(src)
	if err != nil {
		return err
	}

	var foreign string
	rs.Walk(func(typeID, resID winres.Identifier, langID uint16, data []byte) bool {
		if !ownedTypes[typeID] {
			foreign = fmt.Sprintf("type %s id %s", identString(typeID), identString(resID))
			return false
		}
		return true
	})
	if foreign != "" {
		return fmt.Errorf("refusing to drop a resource this tool did not write: %s", foreign)
	}

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return nil
}

// loadIcon decodes the PNG and resizes it to every size in iconSizes.
func loadIcon(path string) (*winres.Icon, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, format, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if format != "png" {
		return nil, fmt.Errorf("%s: unsupported image format %q, want png", path, format)
	}

	ico, err := winres.NewIconFromResizedImage(img, iconSizes)
	if err != nil {
		return nil, fmt.Errorf("%s: building icon: %w", path, err)
	}
	return ico, nil
}

var peMachine = map[string]uint16{
	"amd64": pe.IMAGE_FILE_MACHINE_AMD64,
	"arm64": pe.IMAGE_FILE_MACHINE_ARM64,
}

// checkArch makes sure the exe was really built for the requested arch, so a
// stale or mis-targeted binary cannot be published.
func checkArch(exePath, arch string) error {
	if arch == "" {
		return nil
	}
	want, ok := peMachine[arch]
	if !ok {
		return fmt.Errorf("unknown -arch %q, want amd64 or arm64", arch)
	}
	f, err := pe.Open(exePath)
	if err != nil {
		return fmt.Errorf("%s: %w", exePath, err)
	}
	defer f.Close()

	if got := f.FileHeader.Machine; got != want {
		return fmt.Errorf("%s: PE machine is %#04x, but -arch %s was requested (expected %#04x)", exePath, got, arch, want)
	}
	return nil
}

// verifyEXE reads the resources back and exits non-zero when anything that
// stampEXE writes is missing or wrong.
func verifyEXE(exePath string) error {
	f, err := os.Open(exePath)
	if err != nil {
		return err
	}
	defer f.Close()

	rs, err := winres.LoadFromEXE(f)
	if err != nil {
		return fmt.Errorf("%s: %w", exePath, err)
	}

	problems := verifyResources(rs, os.Stdout)
	if len(problems) > 0 {
		return fmt.Errorf("%s: %d check(s) failed: %s", exePath, len(problems), strings.Join(problems, "; "))
	}
	fmt.Printf("verified: %s\n", exePath)
	return nil
}

// verifyResources prints what the resource set contains and returns the failed
// checks (empty when the resources are exactly what stampEXE produces).
func verifyResources(rs *winres.ResourceSet, out io.Writer) []string {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	fmt.Fprintf(out, "resources: %d\n", rs.Count())

	iconIDs := 0
	rs.WalkType(winres.RT_ICON, func(resID winres.Identifier, langID uint16, data []byte) bool {
		iconIDs++
		fmt.Fprintf(out, "  RT_ICON       id=%-3s lang=0x%04x %d bytes\n", identString(resID), langID, len(data))
		return true
	})
	if iconIDs == 0 {
		fail("no RT_ICON resource")
	}
	if iconIDs != len(iconSizes) {
		fail("RT_ICON count is %d, want %d", iconIDs, len(iconSizes))
	}

	groups := 0
	rs.WalkType(winres.RT_GROUP_ICON, func(resID winres.Identifier, langID uint16, data []byte) bool {
		groups++
		fmt.Fprintf(out, "  RT_GROUP_ICON id=%-3s lang=0x%04x %d bytes\n", identString(resID), langID, len(data))
		return true
	})
	if groups == 0 {
		fail("no RT_GROUP_ICON resource")
	}

	manifestOK := false
	rs.WalkType(winres.RT_MANIFEST, func(resID winres.Identifier, langID uint16, data []byte) bool {
		fmt.Fprintf(out, "  RT_MANIFEST   id=%-3s lang=0x%04x %d bytes\n", identString(resID), langID, len(data))
		text := string(data)
		manifestOK = true
		for _, want := range []string{
			`level="asInvoker"`,
			`<dpiAware`,
			`true/pm`,
			`PerMonitorV2,PerMonitor`,
			`{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}`,
			`<longPathAware`,
		} {
			if !strings.Contains(text, want) {
				fail("manifest is missing %s", want)
			}
		}
		if strings.Contains(text, "requireAdministrator") {
			fail("manifest requests requireAdministrator")
		}
		return true
	})
	if !manifestOK {
		fail("no RT_MANIFEST resource")
	}

	versions := 0
	rs.WalkType(winres.RT_VERSION, func(resID winres.Identifier, langID uint16, data []byte) bool {
		versions++
		vi, err := version.FromBytes(data)
		if err != nil {
			fail("RT_VERSION lang=0x%04x: %v", langID, err)
			return true
		}
		verText := fmt.Sprintf("%d.%d.%d.%d", vi.FileVersion[0], vi.FileVersion[1], vi.FileVersion[2], vi.FileVersion[3])
		prodText := fmt.Sprintf("%d.%d.%d.%d", vi.ProductVersion[0], vi.ProductVersion[1], vi.ProductVersion[2], vi.ProductVersion[3])
		st := vi.Table().GetMainTranslation()
		fmt.Fprintf(out, "  RT_VERSION    id=%-3s lang=0x%04x FileVersion=%s ProductVersion=%s ProductName=%q FileDescription=%q OriginalFilename=%q\n",
			identString(resID), langID, verText, prodText, st[version.ProductName], st[version.FileDescription], st[version.OriginalFilename])

		if verText != defaultVersion && verText != "" {
			fail("RT_VERSION FileVersion is %q, want %q", verText, defaultVersion)
		}
		if prodText != defaultVersion && prodText != "" {
			fail("RT_VERSION ProductVersion is %q, want %q", prodText, defaultVersion)
		}
		if st[version.ProductName] != productName {
			fail("RT_VERSION ProductName is %q, want %q", st[version.ProductName], productName)
		}
		if st[version.FileDescription] == "" {
			fail("RT_VERSION FileDescription is empty")
		}
		if st[version.OriginalFilename] == "" {
			fail("RT_VERSION OriginalFilename is empty")
		}
		if st[version.InternalName] != internalName {
			fail("RT_VERSION InternalName is %q, want %q", st[version.InternalName], internalName)
		}
		return true
	})
	if versions == 0 {
		fail("no RT_VERSION resource")
	}

	return problems
}

// identString renders a resource identifier for log output.
func identString(id winres.Identifier) string {
	switch v := id.(type) {
	case winres.ID:
		return strconv.FormatUint(uint64(v), 10)
	case winres.Name:
		return string(v)
	default:
		return fmt.Sprintf("%v", id)
	}
}
