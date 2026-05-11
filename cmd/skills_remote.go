package cmd

import (
	"fmt"
	"strings"
	"time"

	uicli "github.com/alperdrsnn/clime"
	"github.com/git-hulk/clime/internal/prompt"
	"github.com/git-hulk/clime/internal/skill"
)

// installFromRemoteManifest fetches a remote YAML manifest and lets the user
// pick which skills to install via tarball download.
func installFromRemoteManifest(manifest *skill.Manifest, repo string) error {
	url, existing := resolveRemoteSource(manifest, repo)

	spinner := startSpinner("Fetching skills from %q...", url)

	rm, err := skill.FetchRemoteManifest(url)
	if err != nil {
		spinner.Error(fmt.Sprintf("Failed to fetch %q", url))
		return fmt.Errorf("failed to fetch remote manifest: %w", err)
	}

	if len(rm.Skills) == 0 {
		spinner.Error(fmt.Sprintf("No skills found in %q", url))
		return fmt.Errorf("remote manifest %q has no skills defined", url)
	}

	spinner.Success(fmt.Sprintf("Found %d skill(s) in %q", len(rm.Skills), rm.Name))

	// Cascade rename if upstream name changed since last install.
	if existing != nil && existing.Name != rm.Name {
		if err := manifest.RenameSource(existing.Name, rm.Name); err != nil {
			return fmt.Errorf("upstream renamed %q to %q but local rename failed: %w", existing.Name, rm.Name, err)
		}
	}

	if err := manifest.AddSource(skill.SourceEntry{
		Name: rm.Name,
		Type: skill.SourceTypeRemoteYAML,
		URL:  url,
	}); err != nil {
		return err
	}
	if err := manifest.Save(); err != nil {
		return fmt.Errorf("failed to save skill source: %w", err)
	}

	type candidate struct {
		entry skill.RemoteSkillEntry
		label string
	}
	var candidates []candidate
	for _, s := range rm.Skills {
		if _, installed := manifest.GetSkill(s.Name); installed {
			continue
		}
		label := s.Name
		if s.Description != "" {
			label = fmt.Sprintf("%s — %s", s.Name, uicli.TruncateString(s.Description, 60))
		}
		candidates = append(candidates, candidate{entry: s, label: label})
	}

	if len(candidates) == 0 {
		terminal.Info("All skills from this source are already installed.")
		return nil
	}

	options := make([]string, len(candidates))
	for i, c := range candidates {
		options[i] = c.label
	}

	fmt.Println()
	selectedIdxs, err := multiSelectPrompt(prompt.SelectConfig{
		Label:   "Select skills to install (space to toggle, enter to confirm)",
		Options: options,
	})
	if err != nil {
		return err
	}

	if len(selectedIdxs) == 0 {
		terminal.Info("No skills selected.")
		return nil
	}

	fmt.Println()
	for _, idx := range selectedIdxs {
		entry := candidates[idx].entry
		if err := installRemoteSkillEntry(manifest, entry, rm.Name); err != nil {
			terminal.Errorf("Failed to install %q: %v", entry.Name, err)
		}
	}

	return nil
}

// updateRemoteSource re-fetches the remote manifest and re-installs each local
// skill whose upstream updated_at is newer than its local installed_at.
func updateRemoteSource(manifest *skill.Manifest, name string) error {
	entry := manifest.FindSource(name)
	if entry == nil || entry.Type != skill.SourceTypeRemoteYAML {
		return fmt.Errorf("source %q is not a remote-yaml source", name)
	}

	rm, err := skill.FetchRemoteManifest(entry.URL)
	if err != nil {
		return fmt.Errorf("failed to fetch remote manifest: %w", err)
	}

	// Cascade rename if upstream name changed.
	if rm.Name != name {
		if err := manifest.RenameSource(name, rm.Name); err != nil {
			return fmt.Errorf("upstream renamed %q to %q but local rename failed: %w", name, rm.Name, err)
		}
		name = rm.Name
	}

	var installed []skill.InstalledSkill
	for _, s := range manifest.Skills {
		if s.Source == name {
			installed = append(installed, s)
		}
	}
	if len(installed) == 0 {
		terminal.Warningf("No skills installed from %s.", name)
		return nil
	}

	upstream := make(map[string]skill.RemoteSkillEntry, len(rm.Skills))
	for _, s := range rm.Skills {
		upstream[s.Name] = s
	}

	fmt.Println()
	var removed []string
	for _, s := range installed {
		up, exists := upstream[s.Name]
		if !exists {
			removed = append(removed, s.Name)
			continue
		}
		// Update if upstream timestamp is missing (can't tell, fall back to refresh)
		// or is strictly newer than the local install timestamp.
		if !up.UpdatedAt.IsZero() && !up.UpdatedAt.After(s.InstalledAt) {
			terminal.Infof("Skill %q is up to date.", s.Name)
			continue
		}
		if err := installRemoteSkillEntry(manifest, up, name); err != nil {
			terminal.Errorf("Failed to update %q: %v", s.Name, err)
		}
	}
	if len(removed) > 0 {
		for _, rname := range removed {
			skill.Uninstall(rname)
			manifest.RemoveSkill(rname)
		}
		if err := manifest.Save(); err != nil {
			return fmt.Errorf("failed to update manifest after removing skills: %w", err)
		}
		terminal.Warningf("The following skills were removed from upstream: %s", strings.Join(removed, ", "))
	}
	return nil
}

// resolveRemoteSource returns the URL to fetch for repo. repo may be either a
// fresh URL (first install) or the name of an already-known remote-yaml source.
// existing is non-nil when repo matched a tracked source entry.
func resolveRemoteSource(manifest *skill.Manifest, repo string) (url string, existing *skill.SourceEntry) {
	if entry := manifest.FindSource(repo); entry != nil && entry.Type == skill.SourceTypeRemoteYAML {
		return entry.URL, entry
	}
	// Repo is a URL. See if a tracked entry already points at this URL.
	for i := range manifest.Sources {
		s := &manifest.Sources[i]
		if s.Type == skill.SourceTypeRemoteYAML && s.URL == repo {
			return repo, s
		}
	}
	return repo, nil
}

// installRemoteSkillEntry downloads a single skill's tarball and records it.
func installRemoteSkillEntry(manifest *skill.Manifest, entry skill.RemoteSkillEntry, sourceName string) error {
	spinner := startSpinner("Installing skill %q from %s...", entry.Name, sourceName)

	targets, err := skill.InstallFromTarball(entry.Name, entry.URL)
	if err != nil {
		spinner.Error(fmt.Sprintf("Failed to install skill %q", entry.Name))
		return fmt.Errorf("failed to install skill %q: %w", entry.Name, err)
	}

	if len(targets) == 0 {
		spinner.Stop()
		terminal.Warning("No skill directories were installed. Neither ~/.claude nor ~/.codex was found.")
		return nil
	}

	manifest.AddSkill(skill.InstalledSkill{
		Name:        entry.Name,
		Description: entry.Description,
		Source:      sourceName,
		Path:        "",
		InstalledAt: time.Now(),
	})
	if err := manifest.Save(); err != nil {
		return fmt.Errorf("skill installed but failed to update manifest: %w", err)
	}

	spinner.Success(fmt.Sprintf("Installed skill %q to %s", entry.Name, strings.Join(targets, ", ")))
	return nil
}
