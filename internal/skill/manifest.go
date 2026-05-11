package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// InstalledSkill tracks a skill that has been installed locally.
type InstalledSkill struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Source      string `yaml:"source"`
	// Path is the relative path inside the source repo (git only;
	// empty for remote-yaml installs).
	Path        string    `yaml:"path"`
	InstalledAt time.Time `yaml:"installed_at"`
}

// Manifest is the on-disk index of installed skills and tracked sources at
// ~/.clime/skills.yaml.
type Manifest struct {
	Skills  []InstalledSkill `yaml:"skills"`
	Sources []SourceEntry    `yaml:"sources,omitempty"`
}

// SourceEntry describes one source the user has interacted with. Name is the
// unique identifier used by InstalledSkill.Source to link back here. URL is
// only populated for SourceTypeRemoteYAML sources.
type SourceEntry struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	URL  string `yaml:"url,omitempty"`
}

// UnmarshalYAML accepts both the new structured form and the legacy scalar
// form ("sources: [<string>]"), defaulting legacy entries to git type so
// existing skills.yaml files keep loading after upgrade.
func (s *SourceEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Name = node.Value
		s.Type = SourceTypeGit
		return nil
	}
	type raw SourceEntry
	return node.Decode((*raw)(s))
}

func manifestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".clime", "skills.yaml"), nil
}

// LoadManifest reads the skills manifest from ~/.clime/skills.yaml.
// Creates the directory and an empty manifest file if they do not exist.
func LoadManifest() (*Manifest, error) {
	path, err := manifestPath()
	if err != nil {
		return nil, fmt.Errorf("failed to determine manifest path: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			m := &Manifest{}
			if err := m.Save(); err != nil {
				return nil, fmt.Errorf("failed to create skills manifest: %w", err)
			}
			return m, nil
		}
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Save writes the manifest to disk.
func (m *Manifest) Save() error {
	path, err := manifestPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// AddSkill adds or updates an installed skill entry.
func (m *Manifest) AddSkill(s InstalledSkill) {
	for i, existing := range m.Skills {
		if existing.Name == s.Name {
			m.Skills[i] = s
			return
		}
	}
	m.Skills = append(m.Skills, s)
}

// RemoveSkill removes an installed skill entry.
func (m *Manifest) RemoveSkill(name string) bool {
	for i, s := range m.Skills {
		if s.Name == name {
			m.Skills = append(m.Skills[:i], m.Skills[i+1:]...)
			return true
		}
	}
	return false
}

// GetSkill returns an installed skill by name.
func (m *Manifest) GetSkill(name string) (InstalledSkill, bool) {
	for _, s := range m.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return InstalledSkill{}, false
}

// FindSource returns the source entry by name, or nil if not found.
func (m *Manifest) FindSource(name string) *SourceEntry {
	for i := range m.Sources {
		if m.Sources[i].Name == name {
			return &m.Sources[i]
		}
	}
	return nil
}

// AddSource appends or refreshes a source entry. If a different source already
// uses the same name (different type or URL), it returns an error so callers
// can ask the user to disambiguate.
func (m *Manifest) AddSource(s SourceEntry) error {
	for i, existing := range m.Sources {
		if existing.Name != s.Name {
			continue
		}
		if existing.Type == s.Type && existing.URL == s.URL {
			m.Sources[i] = s
			return nil
		}
		return fmt.Errorf("source name %q already used by a %s source", s.Name, existing.Type)
	}
	m.Sources = append(m.Sources, s)
	return nil
}

// RemoveSource removes a source entry by name.
func (m *Manifest) RemoveSource(name string) bool {
	for i, s := range m.Sources {
		if s.Name == name {
			m.Sources = append(m.Sources[:i], m.Sources[i+1:]...)
			return true
		}
	}
	return false
}

// RenameSource changes a source's Name in both the Sources list and any
// InstalledSkill that references it. Used to handle upstream YAML name changes.
// Returns an error if a different source already uses newName, since renaming
// would otherwise produce two entries with the same name.
func (m *Manifest) RenameSource(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	for _, s := range m.Sources {
		if s.Name == newName {
			return fmt.Errorf("cannot rename %q to %q: another source already uses that name", oldName, newName)
		}
	}
	for i := range m.Sources {
		if m.Sources[i].Name == oldName {
			m.Sources[i].Name = newName
		}
	}
	for i := range m.Skills {
		if m.Skills[i].Source == oldName {
			m.Skills[i].Source = newName
		}
	}
	return nil
}
