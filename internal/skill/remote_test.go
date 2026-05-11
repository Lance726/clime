package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsRemoteManifestURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"https://example.com/skills.yaml", true},
		{"http://example.com/skills.yml", true},
		{"https://example.com/skills.yaml.bak", false},
		{"https://example.com/path/", false},
		{"owner/repo", false},
		{"https://github.com/owner/repo", false},
		{"git@github.com:owner/repo.git", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsRemoteManifestURL(c.in); got != c.want {
			t.Errorf("IsRemoteManifestURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFetchRemoteManifest(t *testing.T) {
	t.Parallel()
	body := `name: my-team
skills:
  - name: code-review
    description: Reviews PRs
    url: https://example.com/code-review.tar.gz
    updated_at: 2026-05-07T14:30:00Z
  - name: deploy-helper
    url: https://example.com/deploy-helper.tar.gz
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	rm, err := FetchRemoteManifest(srv.URL + "/skills.yaml")
	if err != nil {
		t.Fatalf("FetchRemoteManifest error = %v", err)
	}
	if rm.Name != "my-team" {
		t.Errorf("Name = %q, want my-team", rm.Name)
	}
	if len(rm.Skills) != 2 {
		t.Fatalf("Skills length = %d, want 2", len(rm.Skills))
	}
	want := time.Date(2026, 5, 7, 14, 30, 0, 0, time.UTC)
	if !rm.Skills[0].UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", rm.Skills[0].UpdatedAt, want)
	}
	if !rm.Skills[1].UpdatedAt.IsZero() {
		t.Errorf("expected UpdatedAt to be zero for entry without updated_at, got %v", rm.Skills[1].UpdatedAt)
	}
}

func TestFetchRemoteManifestRequiresName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("skills: []\n"))
	}))
	defer srv.Close()

	_, err := FetchRemoteManifest(srv.URL + "/skills.yaml")
	if err == nil || !strings.Contains(err.Error(), "no top-level name") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestFetchRemoteManifestNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchRemoteManifest(srv.URL + "/skills.yaml")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected HTTP 404 error, got %v", err)
	}
}

func TestDownloadTarball(t *testing.T) {
	t.Parallel()
	tarball := buildTarGz(t, map[string]string{
		"SKILL.md":  "---\nname: x\n---\n",
		"helper.sh": "echo hi",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	defer srv.Close()

	files, err := DownloadTarball(srv.URL + "/x.tar.gz")
	if err != nil {
		t.Fatalf("DownloadTarball error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("file count = %d, want 2", len(files))
	}
	if string(files["SKILL.md"]) != "---\nname: x\n---\n" {
		t.Errorf("SKILL.md content unexpected: %q", files["SKILL.md"])
	}
}

func TestDownloadTarballStripsWrapperDir(t *testing.T) {
	t.Parallel()
	tarball := buildTarGz(t, map[string]string{
		"skill-doctor/SKILL.md":             "---\nname: skill-doctor\n---\n",
		"skill-doctor/scripts/run.sh":       "#!/bin/sh\n",
		"skill-doctor/sub-skills/x/SKILL.md": "nested",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	defer srv.Close()

	files, err := DownloadTarball(srv.URL + "/x.tar.gz")
	if err != nil {
		t.Fatalf("DownloadTarball error = %v", err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Fatalf("expected SKILL.md at root after stripping wrapper, got keys %v", keys(files))
	}
	if _, ok := files["scripts/run.sh"]; !ok {
		t.Errorf("expected scripts/run.sh after stripping, got keys %v", keys(files))
	}
	if _, ok := files["skill-doctor/SKILL.md"]; ok {
		t.Errorf("wrapper dir was not stripped: %v", keys(files))
	}
}

func TestDownloadTarballKeepsFlatLayout(t *testing.T) {
	t.Parallel()
	tarball := buildTarGz(t, map[string]string{
		"SKILL.md":      "ok",
		"scripts/run.sh": "#!/bin/sh\n",
		"helper.txt":    "hi",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	defer srv.Close()

	files, err := DownloadTarball(srv.URL + "/x.tar.gz")
	if err != nil {
		t.Fatalf("DownloadTarball error = %v", err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Errorf("expected SKILL.md at root, got keys %v", keys(files))
	}
}

func TestCommonTopDir(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"all under same dir", map[string][]byte{"a/b": nil, "a/c/d": nil}, "a"},
		{"mixed dirs", map[string][]byte{"a/b": nil, "x/y": nil}, ""},
		{"root file present", map[string][]byte{"SKILL.md": nil, "a/b": nil}, ""},
		{"single entry under dir", map[string][]byte{"a/b": nil}, "a"},
		{"empty", map[string][]byte{}, ""},
	}
	for _, c := range cases {
		if got := commonTopDir(c.files); got != c.want {
			t.Errorf("%s: commonTopDir = %q, want %q", c.name, got, c.want)
		}
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDownloadTarballSkipsUnsafePaths(t *testing.T) {
	t.Parallel()
	tarball := buildTarGz(t, map[string]string{
		"SKILL.md":   "ok",
		"../escape":  "bad",
		"/abs/path":  "bad",
		"sub/../ok":  "ok-ish",   // cleaned to "ok" — allowed by Clean
		"sub/normal": "ok-norm",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	defer srv.Close()

	files, err := DownloadTarball(srv.URL + "/x.tar.gz")
	if err != nil {
		t.Fatalf("DownloadTarball error = %v", err)
	}
	if _, ok := files["../escape"]; ok {
		t.Error("expected ../escape to be skipped")
	}
	if _, ok := files["/abs/path"]; ok {
		t.Error("expected /abs/path to be skipped")
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Error("expected SKILL.md to be present")
	}
	if _, ok := files["sub/normal"]; !ok {
		t.Error("expected sub/normal to be present")
	}
}

func TestIsSafeTarPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"SKILL.md", true},
		{"sub/file.txt", true},
		{"", false},
		{"/abs/path", false},
		{"..", false},
		{"../escape", false},
		{"sub/../../escape", false},
	}
	for _, c := range cases {
		if got := isSafeTarPath(c.in); got != c.want {
			t.Errorf("isSafeTarPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// buildTarGz builds an in-memory tar.gz containing the given files.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar WriteHeader: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}
