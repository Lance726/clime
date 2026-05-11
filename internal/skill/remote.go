package skill

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Source type identifiers.
const (
	SourceTypeGit        = "git"
	SourceTypeRemoteYAML = "remote-yaml"
)

// maxFileSize caps a single tarball entry to avoid memory exhaustion.
const maxFileSize = 10 * 1024 * 1024 // 10 MB

// httpTimeout caps the total time a remote fetch can take, to avoid hanging
// when an upstream server stops responding.
const httpTimeout = 30 * time.Second

// httpClient is shared by all remote fetches so the timeout applies uniformly.
var httpClient = &http.Client{Timeout: httpTimeout}

// RemoteManifest is the top-level structure of a remote skills index file.
type RemoteManifest struct {
	Name   string             `yaml:"name"`
	Skills []RemoteSkillEntry `yaml:"skills"`
}

// RemoteSkillEntry describes a single skill in a remote manifest, with a
// tarball URL pointing to the skill's files.
type RemoteSkillEntry struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description,omitempty"`
	URL         string    `yaml:"url"`
	UpdatedAt   time.Time `yaml:"updated_at,omitempty"`
}

// IsRemoteManifestURL reports whether s looks like an HTTP(S) URL pointing at
// a YAML manifest file.
func IsRemoteManifestURL(s string) bool {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return false
	}
	return strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".yml")
}

// FetchRemoteManifest fetches and parses a remote YAML skills manifest.
func FetchRemoteManifest(url string) (*RemoteManifest, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch %s: HTTP %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest body: %w", err)
	}

	var m RemoteManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse remote manifest: %w", err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("remote manifest at %s has no top-level name", url)
	}
	return &m, nil
}

// DownloadTarball downloads a .tar.gz from url and returns its files as a map
// of relative path to contents. If every entry shares a single top-level
// directory (e.g. "skill-doctor/..." produced by `tar -czf x.tar.gz skill-doctor`),
// that wrapper is stripped so the SKILL.md ends up at the map root.
func DownloadTarball(url string) (map[string][]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch %s: HTTP %d", url, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip reader: %w", err)
	}
	defer gz.Close()

	files := make(map[string][]byte)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !isSafeTarPath(hdr.Name) {
			continue
		}
		buf, err := io.ReadAll(io.LimitReader(tr, maxFileSize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry %q: %w", hdr.Name, err)
		}
		if len(buf) > maxFileSize {
			return nil, fmt.Errorf("tar entry %q exceeds %d bytes", hdr.Name, maxFileSize)
		}
		files[filepath.ToSlash(filepath.Clean(hdr.Name))] = buf
	}

	if prefix := commonTopDir(files); prefix != "" {
		stripped := make(map[string][]byte, len(files))
		for k, v := range files {
			stripped[strings.TrimPrefix(k, prefix+"/")] = v
		}
		files = stripped
	}
	return files, nil
}

// commonTopDir returns the shared first path segment when every entry sits
// under the same top-level directory; otherwise returns "". Used to strip
// wrapper dirs from tarballs produced by `tar -czf x.tar.gz x/`.
func commonTopDir(files map[string][]byte) string {
	var prefix string
	first := true
	for name := range files {
		idx := strings.Index(name, "/")
		if idx <= 0 {
			return ""
		}
		seg := name[:idx]
		if first {
			prefix = seg
			first = false
		} else if seg != prefix {
			return ""
		}
	}
	return prefix
}

// isSafeTarPath rejects absolute paths and paths that escape the archive root
// (containing ".."). Tar paths use POSIX "/" separators regardless of host OS,
// so we check for a "/" prefix directly instead of using filepath.IsAbs (which
// is OS-dependent and would miss "/abs/path" on Windows). After filepath.Clean,
// ".." can only appear at the start of the result, so we only need to check
// the prefix.
func isSafeTarPath(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "/") {
		return false
	}
	cleaned := filepath.Clean(name)
	return cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}
