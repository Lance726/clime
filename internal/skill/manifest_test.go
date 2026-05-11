package skill

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAddSkill(t *testing.T) {
	t.Parallel()
	m := &Manifest{}

	m.AddSkill(InstalledSkill{
		Name:   "my-skill",
		Source: "owner/repo",
		Path:   "my-skill",
	})
	if len(m.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(m.Skills))
	}

	// Update existing skill.
	m.AddSkill(InstalledSkill{
		Name:        "my-skill",
		Description: "updated",
		Source:      "owner/repo",
		Path:        "my-skill",
	})
	if len(m.Skills) != 1 {
		t.Fatalf("expected 1 skill after update, got %d", len(m.Skills))
	}
	if m.Skills[0].Description != "updated" {
		t.Fatalf("expected description 'updated', got %q", m.Skills[0].Description)
	}
}

func TestRemoveSkill(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Skills: []InstalledSkill{
			{Name: "skill-a"},
			{Name: "skill-b"},
		},
	}

	if !m.RemoveSkill("skill-a") {
		t.Fatal("expected RemoveSkill to return true")
	}
	if len(m.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(m.Skills))
	}

	if m.RemoveSkill("missing") {
		t.Fatal("expected RemoveSkill to return false for missing skill")
	}
}

func TestGetSkill(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Skills: []InstalledSkill{{Name: "my-skill", Source: "owner/repo"}},
	}

	s, ok := m.GetSkill("my-skill")
	if !ok {
		t.Fatal("expected to find skill")
	}
	if s.Source != "owner/repo" {
		t.Fatalf("expected source owner/repo, got %s", s.Source)
	}

	_, ok = m.GetSkill("missing")
	if ok {
		t.Fatal("expected not to find missing skill")
	}
}

func TestSourceEntryUnmarshalLegacyScalar(t *testing.T) {
	t.Parallel()
	data := []byte(`
sources:
  - owner/repo
  - https://intra.example/skills.yaml
`)
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("yaml.Unmarshal error = %v", err)
	}
	if len(m.Sources) != 2 {
		t.Fatalf("Sources length = %d, want 2", len(m.Sources))
	}
	for _, s := range m.Sources {
		if s.Type != SourceTypeGit {
			t.Errorf("legacy entry %q has type %q, want git", s.Name, s.Type)
		}
	}
}

func TestSourceEntryUnmarshalStructured(t *testing.T) {
	t.Parallel()
	data := []byte(`
sources:
  - name: my-team
    type: remote-yaml
    url: https://intra.example/skills.yaml
`)
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("yaml.Unmarshal error = %v", err)
	}
	if len(m.Sources) != 1 {
		t.Fatalf("Sources length = %d, want 1", len(m.Sources))
	}
	s := m.Sources[0]
	if s.Name != "my-team" || s.Type != SourceTypeRemoteYAML || s.URL != "https://intra.example/skills.yaml" {
		t.Errorf("unexpected SourceEntry: %+v", s)
	}
}

func TestAddSourceRejectsNameCollision(t *testing.T) {
	t.Parallel()
	m := &Manifest{}
	if err := m.AddSource(SourceEntry{Name: "my-team", Type: SourceTypeGit}); err != nil {
		t.Fatalf("AddSource (initial) error = %v", err)
	}
	err := m.AddSource(SourceEntry{Name: "my-team", Type: SourceTypeRemoteYAML, URL: "https://x/skills.yaml"})
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected collision error, got %v", err)
	}
}

func TestAddSourceRefreshesSameIdentity(t *testing.T) {
	t.Parallel()
	m := &Manifest{}
	if err := m.AddSource(SourceEntry{Name: "my-team", Type: SourceTypeRemoteYAML, URL: "https://x/skills.yaml"}); err != nil {
		t.Fatalf("AddSource (initial) error = %v", err)
	}
	if err := m.AddSource(SourceEntry{Name: "my-team", Type: SourceTypeRemoteYAML, URL: "https://x/skills.yaml"}); err != nil {
		t.Fatalf("AddSource (refresh) error = %v", err)
	}
	if len(m.Sources) != 1 {
		t.Fatalf("Sources length = %d, want 1", len(m.Sources))
	}
}

func TestRenameSourceCascadesToSkills(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Skills: []InstalledSkill{
			{Name: "code-review", Source: "my-team"},
			{Name: "simplify", Source: "anthropics/claude-code"},
		},
		Sources: []SourceEntry{
			{Name: "my-team", Type: SourceTypeRemoteYAML, URL: "https://x/skills.yaml"},
			{Name: "anthropics/claude-code", Type: SourceTypeGit},
		},
	}
	if err := m.RenameSource("my-team", "corp-team"); err != nil {
		t.Fatalf("RenameSource error = %v", err)
	}

	if m.Sources[0].Name != "corp-team" {
		t.Errorf("Sources[0].Name = %q, want corp-team", m.Sources[0].Name)
	}
	if m.Skills[0].Source != "corp-team" {
		t.Errorf("Skills[0].Source = %q, want corp-team", m.Skills[0].Source)
	}
	if m.Skills[1].Source != "anthropics/claude-code" {
		t.Errorf("Skills[1].Source unexpectedly changed: %q", m.Skills[1].Source)
	}
}

func TestRenameSourceRejectsCollision(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Sources: []SourceEntry{
			{Name: "my-team", Type: SourceTypeRemoteYAML, URL: "https://x/skills.yaml"},
			{Name: "corp-team", Type: SourceTypeRemoteYAML, URL: "https://y/skills.yaml"},
		},
	}
	err := m.RenameSource("my-team", "corp-team")
	if err == nil || !strings.Contains(err.Error(), "already uses that name") {
		t.Fatalf("expected collision error, got %v", err)
	}
	// Sources should be untouched on error.
	if m.Sources[0].Name != "my-team" {
		t.Errorf("Sources[0].Name changed despite error: %q", m.Sources[0].Name)
	}
}

func TestFindSource(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Sources: []SourceEntry{{Name: "my-team", Type: SourceTypeRemoteYAML, URL: "https://x/skills.yaml"}},
	}
	if got := m.FindSource("my-team"); got == nil || got.URL != "https://x/skills.yaml" {
		t.Errorf("FindSource(my-team) = %+v", got)
	}
	if got := m.FindSource("missing"); got != nil {
		t.Errorf("FindSource(missing) = %+v, want nil", got)
	}
}
