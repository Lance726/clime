package installer

import (
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/git-hulk/clime/internal/plugin"
)

// NpmInstaller installs plugins from npm global packages.
type NpmInstaller struct {
	Package         string
	runNpmInstall   func(pkg string) error
	runNpmUpdate    func(pkg string) error
	runNpmUninstall func(pkg string) error
	npmGlobalBinDir func() (string, error)
	pluginBinDir    func() (string, error)
	getVersion      func(pkg string) (string, error)
}

// NewNpmInstaller returns an NpmInstaller for the given npm package.
// A bare "owner/repo" string is treated as a scoped package and rewritten to
// "@owner/repo"; npm would otherwise resolve it as a GitHub shorthand, which
// is rarely what callers want when they specify --npm.
func NewNpmInstaller(pkg string) *NpmInstaller {
	return &NpmInstaller{
		Package:         normalizeNpmPackageName(pkg),
		runNpmInstall:   runNpmGlobalInstall,
		runNpmUpdate:    runNpmGlobalUpdate,
		runNpmUninstall: runNpmGlobalUninstall,
		npmGlobalBinDir: npmGlobalBinDir,
		pluginBinDir:    plugin.PluginBinDir,
		getVersion:      getNpmInstalledVersion,
	}
}

// normalizeNpmPackageName prepends "@" to bare "owner/repo" strings so npm
// treats them as scoped registry packages rather than GitHub shorthand.
// Inputs that are already scoped, unscoped, URLs, protocol-prefixed
// (git+https://, github:, file:, etc.), or local paths are returned as-is.
func normalizeNpmPackageName(pkg string) string {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" || strings.HasPrefix(pkg, "@") {
		return pkg
	}
	if strings.ContainsAny(pkg, ":\\") || strings.HasPrefix(pkg, ".") || strings.HasPrefix(pkg, "/") {
		return pkg
	}
	if strings.Count(pkg, "/") == 1 {
		return "@" + pkg
	}
	return pkg
}

func (n *NpmInstaller) Install(name string) (string, error) {
	if _, err := osexec.LookPath("npm"); err != nil {
		return "", fmt.Errorf("npm is not installed or not on PATH: %w", err)
	}

	npmBinDir, err := n.npmGlobalBinDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine npm global bin directory: %w", err)
	}
	before := snapshotDirEntries(npmBinDir)

	if err := n.runNpmInstall(n.Package); err != nil {
		return "", fmt.Errorf("npm install failed: %w", err)
	}

	binName := plugin.BinPrefix + name
	binaryPath, err := locateNpmInstalledBinary(npmBinDir, n.Package, name, binName, before)
	if err != nil {
		return "", err
	}

	installDir, err := n.pluginBinDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", err
	}

	linkPath := filepath.Join(installDir, binName)
	os.Remove(linkPath)
	if err := os.Symlink(binaryPath, linkPath); err != nil {
		return "", fmt.Errorf("failed to create symlink: %w", err)
	}

	version, err := n.getVersion(n.Package)
	if err != nil {
		version = plugin.VersionLatest
	}

	return version, nil
}

func (n *NpmInstaller) Update(name string, current plugin.ManifestEntry) (*UpdateResult, error) {
	if err := n.runNpmUpdate(n.Package); err != nil {
		return nil, fmt.Errorf("failed to update npm plugin %q: %w", n.Package, err)
	}

	version, err := n.getVersion(n.Package)
	if err != nil {
		version = plugin.VersionLatest
	}

	installDir, err := n.pluginBinDir()
	if err != nil {
		return nil, err
	}

	updated := true
	if current.Version != "" && semverRe.MatchString(version) &&
		normalizeVersion(current.Version) == normalizeVersion(version) {
		updated = false
	}

	return &UpdateResult{
		Name:           name,
		Source:         n.Package,
		CurrentVersion: current.Version,
		LatestVersion:  version,
		Updated:        updated,
		Path:           filepath.Join(installDir, plugin.BinPrefix+name),
	}, nil
}

func (n *NpmInstaller) Uninstall(name string, entry plugin.ManifestEntry) error {
	cmd := osexec.Command("npm", "uninstall", "-g", n.Package)
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: npm uninstall -g %s failed: %v\n%s", n.Package, err, string(output))
	}
	return removePluginBinary(name)
}

func (n *NpmInstaller) DetectVersion(name string) string {
	version, err := n.getVersion(n.Package)
	if err != nil {
		return plugin.VersionLatest
	}
	return version
}

func (n *NpmInstaller) PluginType() string { return plugin.SourceTypeNpm }
func (n *NpmInstaller) Source() string     { return n.Package }

// npm helper functions

func runNpmGlobalInstall(pkg string) error {
	cmd := osexec.Command("npm", "install", "-g", pkg)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm install failed: %w\n%s", err, string(output))
	}
	return nil
}

func runNpmGlobalUpdate(pkg string) error {
	cmd := osexec.Command("npm", "update", "-g", pkg)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm update failed: %w\n%s", err, string(output))
	}
	return nil
}

func runNpmGlobalUninstall(pkg string) error {
	cmd := osexec.Command("npm", "uninstall", "-g", pkg)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm uninstall failed: %w\n%s", err, string(output))
	}
	return nil
}

func npmGlobalBinDir() (string, error) {
	out, err := osexec.Command("npm", "prefix", "-g").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get npm global prefix: %w", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "bin"), nil
}

// snapshotDirEntries returns the set of entry names directly under dir, or an
// empty set if the directory cannot be read.
func snapshotDirEntries(dir string) map[string]struct{} {
	set := make(map[string]struct{})
	entries, err := os.ReadDir(dir)
	if err != nil {
		return set
	}
	for _, e := range entries {
		set[e.Name()] = struct{}{}
	}
	return set
}

// locateNpmInstalledBinary picks the binary that an npm install created.
// It prefers clime-<name>, then <name>, then falls back to a single new entry
// added to npmBinDir during the install. The error explains why no candidate
// was found, including any unexpected new entries.
func locateNpmInstalledBinary(npmBinDir, pkg, name, binName string, before map[string]struct{}) (string, error) {
	if path := filepath.Join(npmBinDir, binName); fileExists(path) {
		return path, nil
	}
	if path := filepath.Join(npmBinDir, name); fileExists(path) {
		return path, nil
	}

	after := snapshotDirEntries(npmBinDir)
	var added []string
	for entry := range after {
		if _, existed := before[entry]; !existed {
			added = append(added, entry)
		}
	}
	sort.Strings(added)

	switch len(added) {
	case 0:
		return "", fmt.Errorf("npm install of %q did not create a binary in %q; the package may not provide a CLI (check that the package name is correct, e.g. a scoped package starting with \"@\")", pkg, npmBinDir)
	case 1:
		return filepath.Join(npmBinDir, added[0]), nil
	default:
		return "", fmt.Errorf("npm install of %q created multiple binaries in %q (%s); none matched %q or %q — rerun with a plugin name matching one of them", pkg, npmBinDir, strings.Join(added, ", "), binName, name)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// getNpmInstalledVersion returns the actual installed version of an npm package.
func getNpmInstalledVersion(pkg string) (string, error) {
	out, err := osexec.Command("npm", "list", "-g", pkg, "--json").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get npm package version: %w", err)
	}

	var result struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("failed to parse npm list output: %w", err)
	}

	dep, ok := result.Dependencies[pkg]
	if !ok {
		return "", fmt.Errorf("package %s not found in npm list output", pkg)
	}
	if dep.Version == "" {
		return "", fmt.Errorf("version not found in npm list output")
	}
	return dep.Version, nil
}
