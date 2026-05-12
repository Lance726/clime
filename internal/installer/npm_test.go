package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-hulk/clime/internal/plugin"
)

func TestNpmInstallerUpdate(t *testing.T) {
	t.Parallel()

	var ranNpmUpdate bool
	n := &NpmInstaller{
		Package: "@myorg/clime-deploy",
		runNpmUpdate: func(pkg string) error {
			ranNpmUpdate = true
			if pkg != "@myorg/clime-deploy" {
				t.Fatalf("pkg = %q, want %q", pkg, "@myorg/clime-deploy")
			}
			return nil
		},
		pluginBinDir: func() (string, error) {
			return "/tmp/clime-plugin-test", nil
		},
		getVersion: func(pkg string) (string, error) {
			return plugin.VersionLatest, nil
		},
	}

	entry := plugin.ManifestEntry{
		Name:    "deploy",
		Version: plugin.VersionLatest,
		Type:    plugin.SourceTypeNpm,
		Source:  "@myorg/clime-deploy",
	}
	result, err := n.Update("deploy", entry)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !ranNpmUpdate {
		t.Fatal("npm update should run for npm source")
	}
	if !result.Updated {
		t.Fatal("Update() should mark updated for npm source")
	}
	if result.LatestVersion != plugin.VersionLatest {
		t.Fatalf("LatestVersion = %q, want %q", result.LatestVersion, plugin.VersionLatest)
	}
	wantPath := filepath.Join("/tmp/clime-plugin-test", "clime-deploy")
	if result.Path != wantPath {
		t.Fatalf("Path = %q, want %q", result.Path, wantPath)
	}
}

func TestNpmInstallerUpdateUpToDate(t *testing.T) {
	t.Parallel()

	n := &NpmInstaller{
		Package: "@myorg/clime-deploy",
		runNpmUpdate: func(pkg string) error {
			return nil
		},
		pluginBinDir: func() (string, error) {
			return "/tmp/clime-plugin-test", nil
		},
		getVersion: func(pkg string) (string, error) {
			return "1.2.3", nil
		},
	}

	entry := plugin.ManifestEntry{
		Name:    "deploy",
		Version: "1.2.3",
		Type:    plugin.SourceTypeNpm,
		Source:  "@myorg/clime-deploy",
	}
	result, err := n.Update("deploy", entry)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if result.Updated {
		t.Fatal("Update() should not mark updated when semver version is unchanged")
	}
}

func TestLocateNpmInstalledBinaryPrefersClimePrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustTouch(t, filepath.Join(dir, "clime-deploy"))
	mustTouch(t, filepath.Join(dir, "deploy"))

	path, err := locateNpmInstalledBinary(dir, "@myorg/clime-deploy", "deploy", "clime-deploy", map[string]struct{}{})
	if err != nil {
		t.Fatalf("locateNpmInstalledBinary() error = %v", err)
	}
	if want := filepath.Join(dir, "clime-deploy"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestLocateNpmInstalledBinaryFallsBackToName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustTouch(t, filepath.Join(dir, "codex"))

	path, err := locateNpmInstalledBinary(dir, "@openai/codex", "codex", "clime-codex", map[string]struct{}{})
	if err != nil {
		t.Fatalf("locateNpmInstalledBinary() error = %v", err)
	}
	if want := filepath.Join(dir, "codex"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestLocateNpmInstalledBinaryDiscoversNewBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustTouch(t, filepath.Join(dir, "existing"))
	before := snapshotDirEntries(dir)
	mustTouch(t, filepath.Join(dir, "weirdname"))

	path, err := locateNpmInstalledBinary(dir, "some-package", "tool", "clime-tool", before)
	if err != nil {
		t.Fatalf("locateNpmInstalledBinary() error = %v", err)
	}
	if want := filepath.Join(dir, "weirdname"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestLocateNpmInstalledBinaryErrorsWhenNoBinaryCreated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustTouch(t, filepath.Join(dir, "preexisting"))
	before := snapshotDirEntries(dir)

	_, err := locateNpmInstalledBinary(dir, "openai/codex", "codex", "clime-codex", before)
	if err == nil {
		t.Fatal("expected error when npm install produced no new binary")
	}
	if !strings.Contains(err.Error(), "did not create a binary") {
		t.Fatalf("error = %q, want it to mention missing binary", err)
	}
	if !strings.Contains(err.Error(), "openai/codex") {
		t.Fatalf("error = %q, want it to reference the package", err)
	}
}

func TestLocateNpmInstalledBinaryErrorsOnAmbiguousNewBinaries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	before := snapshotDirEntries(dir)
	mustTouch(t, filepath.Join(dir, "tsc"))
	mustTouch(t, filepath.Join(dir, "tsserver"))

	_, err := locateNpmInstalledBinary(dir, "typescript", "ts", "clime-ts", before)
	if err == nil {
		t.Fatal("expected error when multiple new binaries match nothing")
	}
	if !strings.Contains(err.Error(), "tsc") || !strings.Contains(err.Error(), "tsserver") {
		t.Fatalf("error = %q, want it to list both binaries", err)
	}
}

func TestNormalizeNpmPackageName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"openai/codex", "@openai/codex"},
		{"  openai/codex  ", "@openai/codex"},
		{"@openai/codex", "@openai/codex"},
		{"@openai/codex@1.0.0", "@openai/codex@1.0.0"},
		{"lodash", "lodash"},
		{"lodash@4.17.0", "lodash@4.17.0"},
		{"git+https://github.com/openai/codex.git", "git+https://github.com/openai/codex.git"},
		{"github:openai/codex", "github:openai/codex"},
		{"file:./local", "file:./local"},
		{"./local-package", "./local-package"},
		{"/abs/path/pkg", "/abs/path/pkg"},
		{"", ""},
	}
	for _, c := range cases {
		got := normalizeNpmPackageName(c.in)
		if got != c.want {
			t.Errorf("normalizeNpmPackageName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewNpmInstallerNormalizesPackage(t *testing.T) {
	t.Parallel()
	n := NewNpmInstaller("openai/codex")
	if n.Package != "@openai/codex" {
		t.Fatalf("Package = %q, want %q", n.Package, "@openai/codex")
	}
}

func mustTouch(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func TestNpmInstallerPluginType(t *testing.T) {
	t.Parallel()
	n := NewNpmInstaller("@myorg/clime-deploy")
	if n.PluginType() != plugin.SourceTypeNpm {
		t.Fatalf("PluginType() = %q, want %q", n.PluginType(), plugin.SourceTypeNpm)
	}
	if n.Source() != "@myorg/clime-deploy" {
		t.Fatalf("Source() = %q, want %q", n.Source(), "@myorg/clime-deploy")
	}
}
