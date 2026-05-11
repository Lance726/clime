package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	uicli "github.com/alperdrsnn/clime"
	"github.com/git-hulk/clime/internal/prompt"
	"github.com/git-hulk/clime/internal/skill"
	"github.com/spf13/cobra"
)

func init() {
	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsUninstallCmd)
	rootCmd.AddCommand(skillsCmd)
}

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage AI agent skills from GitHub repositories or local paths",
	Long: "Install skills from GitHub repositories or local paths into ~/.claude/skills and ~/.codex/skills " +
		"for use with Claude Code and Codex.",
	RunE: skillsListCmd.RunE,
}

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed skills and their sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := skill.LoadManifest()
		if err != nil {
			return fmt.Errorf("failed to load skills manifest: %w", err)
		}

		if len(manifest.Skills) == 0 {
			terminal.Warning("No skills installed.")
			terminal.Info("Install skills with: clime skills install")
			return nil
		}

		fmt.Println()
		fmt.Printf("  %s %s\n\n",
			uicli.BoldColor.Sprint("Installed Skills"),
			uicli.DimColor.Sprintf("(%d total)", len(manifest.Skills)),
		)

		headers := []string{"NAME", "DESCRIPTION", "SOURCE"}
		const descWidth = 50
		var rows [][]string
		for _, s := range manifest.Skills {
			desc := s.Description
			if desc == "" {
				desc = "—"
			}
			desc = uicli.TruncateString(desc, descWidth)
			source := s.Source
			if entry := manifest.FindSource(s.Source); entry != nil {
				source = fmt.Sprintf("%s (%s)", entry.Name, entry.Type)
			}
			rows = append(rows, []string{s.Name, desc, source})
		}

		colWidths := make([]int, len(headers))
		for i, h := range headers {
			colWidths[i] = len(h)
		}
		for _, row := range rows {
			for i, cell := range row {
				if len(cell) > colWidths[i] {
					colWidths[i] = len(cell)
				}
			}
		}

		const gap = 2
		const indent = "  "

		fmt.Print(indent)
		for i, h := range headers {
			if i > 0 {
				fmt.Print(strings.Repeat(" ", gap))
			}
			fmt.Print(uicli.BoldColor.Sprintf("%-*s", colWidths[i], h))
		}
		fmt.Println()

		fmt.Print(indent)
		for i, w := range colWidths {
			if i > 0 {
				fmt.Print(strings.Repeat(" ", gap))
			}
			fmt.Print(strings.Repeat("-", w))
		}
		fmt.Println()

		for _, row := range rows {
			fmt.Print(indent)
			for i, cell := range row {
				if i > 0 {
					fmt.Print(strings.Repeat(" ", gap))
				}
				fmt.Printf("%-*s", colWidths[i], cell)
			}
			fmt.Println()
		}

		return nil
	},
}

type sourceAction int

const (
	actionBrowseInstall sourceAction = iota
	actionRemoveSource
	actionUpdate
)

const newRepoOption = "Enter a new repository..."

var (
	selectPrompt       = prompt.Select
	multiSelectPrompt  = prompt.MultiSelect
	inputPrompt        = prompt.Input
	skillsActionRunner = runSkillsSourceAction
)

var skillsInstallCmd = &cobra.Command{
	Use:   "install [owner/repo|path]",
	Short: "Install skills from a GitHub repository or local path",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := skill.LoadManifest()
		if err != nil {
			return fmt.Errorf("failed to load skills manifest: %w", err)
		}

		if len(args) > 0 {
			return runSkillsSourceAction(manifest, args[0], actionBrowseInstall)
		}

		return runInteractiveSkillsInstall(manifest)
	},
}

func runInteractiveSkillsInstall(manifest *skill.Manifest) error {
	sources := uniqueSkillSources(manifest)
	if len(sources) == 0 {
		fmt.Println()
		repo, err := inputPrompt("Enter repository (owner/repo)")
		if err != nil {
			return err
		}
		return skillsActionRunner(manifest, repo, actionBrowseInstall)
	}

	labels := make([]string, len(sources))
	for i, s := range sources {
		labels[i] = formatSourceLabel(s)
	}
	options := append(labels, pluginSkillsOption, newRepoOption)
	showSourceSpacer := true
	for {
		if showSourceSpacer {
			fmt.Println()
		} else {
			showSourceSpacer = true
		}
		idx, err := selectPrompt(prompt.SelectConfig{
			Label:   "Select a skill source",
			Options: options,
		})
		if err != nil {
			if errors.Is(err, prompt.ErrBack) {
				showSourceSpacer = false
				continue
			}
			return err
		}

		if options[idx] == pluginSkillsOption {
			err := installFromPluginSkills(manifest)
			if errors.Is(err, prompt.ErrBack) {
				showSourceSpacer = false
				continue
			}
			return err
		}

		if options[idx] == newRepoOption {
			repo, err := inputPrompt("Enter repository (owner/repo)")
			if err != nil {
				return err
			}
			return skillsActionRunner(manifest, repo, actionBrowseInstall)
		}

		repo := sources[idx].Name
		label := labels[idx]
		showActionSpacer := true
		for {
			action, err := pickSourceAction(label, showActionSpacer)
			if err != nil {
				if errors.Is(err, prompt.ErrBack) {
					showSourceSpacer = false
					break
				}
				return err
			}

			err = skillsActionRunner(manifest, repo, action)
			if errors.Is(err, prompt.ErrBack) {
				showActionSpacer = false
				continue
			}
			return err
		}
	}
}

func uniqueSkillSources(manifest *skill.Manifest) []skill.SourceEntry {
	// Collect unique sources from installed skills and tracked sources, preserving order.
	seen := make(map[string]bool)
	var sources []skill.SourceEntry
	for _, s := range manifest.Skills {
		if s.Source == "" || seen[s.Source] {
			continue
		}
		seen[s.Source] = true
		if entry := manifest.FindSource(s.Source); entry != nil {
			sources = append(sources, *entry)
		} else {
			// Skill references a source that's missing from Sources (e.g. file
			// edited by hand). Default to git so the menu still works.
			sources = append(sources, skill.SourceEntry{Name: s.Source, Type: skill.SourceTypeGit})
		}
	}
	for _, s := range manifest.Sources {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		sources = append(sources, s)
	}
	return sources
}

func formatSourceLabel(s skill.SourceEntry) string {
	if s.Type == "" {
		return s.Name
	}
	return fmt.Sprintf("%s (%s)", s.Name, s.Type)
}

func runSkillsSourceAction(manifest *skill.Manifest, repo string, action sourceAction) error {
	// Skip strict validation for sources we already track so users can refer to
	// remote-yaml sources by their bare name (e.g. "my-team") from the menu.
	entry := manifest.FindSource(repo)
	if entry == nil {
		if err := validateSkillRepoSource(repo); err != nil {
			return err
		}
	}

	// Remove flow is identical regardless of source type — handle it first.
	if action == actionRemoveSource {
		return removeSource(manifest, repo)
	}

	isRemote := (entry != nil && entry.Type == skill.SourceTypeRemoteYAML) ||
		(entry == nil && skill.IsRemoteManifestURL(repo))

	if isRemote {
		if action == actionUpdate {
			return updateRemoteSource(manifest, repo)
		}
		return installFromRemoteManifest(manifest, repo)
	}

	if action == actionUpdate {
		return updateSource(manifest, repo)
	}
	return installFromRepo(manifest, repo)
}

func validateSkillRepoSource(repo string) error {
	if repo == "" {
		return fmt.Errorf("invalid repo format: expected owner/repo, URL, or local path, got %q", repo)
	}
	if _, ok, err := skill.LocalRepoDir(repo); err != nil {
		return err
	} else if ok {
		return nil
	}
	if skill.IsRemoteManifestURL(repo) {
		return nil
	}
	if !strings.Contains(repo, "/") {
		return fmt.Errorf("invalid repo format: expected owner/repo, URL, or local path, got %q", repo)
	}
	return nil
}

func pickSourceAction(repo string, showSpacer bool) (sourceAction, error) {
	choices := []struct {
		label  string
		action sourceAction
	}{
		{"Browse and install skills", actionBrowseInstall},
		{"Update installed skills", actionUpdate},
		{"Remove source and its installed skills", actionRemoveSource},
	}

	options := make([]string, len(choices))
	for i, c := range choices {
		options[i] = c.label
	}

	if showSpacer {
		fmt.Println()
	}
	idx, err := selectPrompt(prompt.SelectConfig{
		Label:   fmt.Sprintf("Action for %s", repo),
		Options: options,
	})
	if err != nil {
		return 0, err
	}
	return choices[idx].action, nil
}

// removeSource uninstalls all skills from the given source and removes it from the manifest.
func removeSource(manifest *skill.Manifest, repo string) error {
	var names []string
	for _, s := range manifest.Skills {
		if s.Source == repo {
			names = append(names, s.Name)
		}
	}

	// Nothing to do — repo is neither tracked nor referenced by any skill.
	if len(names) == 0 && manifest.FindSource(repo) == nil {
		terminal.Warningf("Source %s is not tracked; nothing to remove.", repo)
		return nil
	}

	fmt.Println()
	for _, name := range names {
		if err := uninstallByName(manifest, name); err != nil {
			terminal.Errorf("Failed to uninstall %q: %v", name, err)
		}
	}

	manifest.RemoveSource(repo)
	if err := manifest.Save(); err != nil {
		return fmt.Errorf("failed to update manifest: %w", err)
	}

	terminal.Successf("Removed source %s.", repo)
	return nil
}

// updateSource re-installs all skills from the given source with the latest files.
func updateSource(manifest *skill.Manifest, repo string) error {
	var installed []skill.InstalledSkill
	for _, s := range manifest.Skills {
		if s.Source == repo {
			installed = append(installed, s)
		}
	}

	if len(installed) == 0 {
		terminal.Warningf("No skills installed from %s.", repo)
		return nil
	}

	// Resolve the repo once and reuse it for all skill updates.
	dir, cleanup, err := skill.PrepareRepoDir(repo)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Println()
	var removed []string
	for _, s := range installed {
		entry := &skill.SkillEntry{
			Name:        s.Name,
			Description: s.Description,
			Path:        s.Path,
		}
		if err := installSkillEntry(manifest, entry, repo, dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				removed = append(removed, s.Name)
				continue
			}
			terminal.Errorf("Failed to update %q: %v", s.Name, err)
		}
	}
	if len(removed) > 0 {
		for _, name := range removed {
			skill.Uninstall(name)
			manifest.RemoveSkill(name)
		}
		if err := manifest.Save(); err != nil {
			return fmt.Errorf("failed to update manifest after removing skills: %w", err)
		}
		terminal.Warningf("The following skills were removed from upstream: %s", strings.Join(removed, ", "))
	}
	return nil
}

func installSkillEntry(manifest *skill.Manifest, entry *skill.SkillEntry, repo string, localDir string) error {
	spinner := startSpinner("Installing skill %q from %s...", entry.Name, repo)

	targets, err := skill.InstallFromDir(entry.Name, localDir, entry.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			spinner.Stop()
		} else {
			spinner.Error(fmt.Sprintf("Failed to install skill %q", entry.Name))
		}
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
		Source:      repo,
		Path:        entry.Path,
		InstalledAt: time.Now(),
	})
	if err := manifest.Save(); err != nil {
		return fmt.Errorf("skill installed but failed to update manifest: %w", err)
	}

	spinner.Success(fmt.Sprintf("Installed skill %q to %s", entry.Name, strings.Join(targets, ", ")))
	return nil
}

// installFromRepo fetches skills from a repo and lets the user pick which to install.
func installFromRepo(manifest *skill.Manifest, repo string) error {
	spinner := startSpinner("Fetching skills from %q...", repo)

	repoManifest, err := skill.FetchRepoManifest(repo)
	if err != nil {
		spinner.Error(fmt.Sprintf("Failed to fetch %q", repo))
		return fmt.Errorf("failed to fetch skills: %w", err)
	}

	if len(repoManifest.Skills) == 0 {
		spinner.Error(fmt.Sprintf("No skills found in %q", repo))
		return fmt.Errorf("repository %q has no skills defined", repo)
	}

	spinner.Success(fmt.Sprintf("Found %d skill(s) in %q", len(repoManifest.Skills), repo))

	// Record the source so it appears in future interactive menus.
	if err := manifest.AddSource(skill.SourceEntry{Name: repo, Type: skill.SourceTypeGit}); err != nil {
		return err
	}
	if err := manifest.Save(); err != nil {
		return fmt.Errorf("failed to save skill source: %w", err)
	}

	// Filter out already-installed skills.
	type candidate struct {
		entry skill.SkillEntry
		label string
	}
	var candidates []candidate
	for _, s := range repoManifest.Skills {
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
		terminal.Info("All skills from this repository are already installed.")
		return nil
	}

	// Multi-select skills to install.
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

	// Resolve the repo once and reuse it for all skill installations.
	dir, cleanup, err := skill.PrepareRepoDir(repo)
	if err != nil {
		return err
	}
	defer cleanup()

	// Install each selected skill.
	fmt.Println()
	for _, idx := range selectedIdxs {
		entry := candidates[idx].entry
		if err := installSkillEntry(manifest, &entry, repo, dir); err != nil {
			terminal.Errorf("Failed to install %q: %v", entry.Name, err)
		}
	}

	return nil
}

var skillsUninstallCmd = &cobra.Command{
	Use:   "uninstall [skill-name]",
	Short: "Uninstall a previously installed skill",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := skill.LoadManifest()
		if err != nil {
			return fmt.Errorf("failed to load skills manifest: %w", err)
		}

		if len(args) == 0 {
			return interactiveUninstall(manifest)
		}

		return uninstallByName(manifest, args[0])
	},
}

func uninstallByName(manifest *skill.Manifest, name string) error {
	if _, exists := manifest.GetSkill(name); !exists {
		return fmt.Errorf("skill %q is not installed", name)
	}

	spinner := startSpinner("Removing skill %q...", name)

	targets, err := skill.Uninstall(name)
	if err != nil {
		spinner.Error(fmt.Sprintf("Failed to remove skill %q", name))
		return fmt.Errorf("failed to remove skill %q: %w", name, err)
	}

	manifest.RemoveSkill(name)
	if err := manifest.Save(); err != nil {
		return fmt.Errorf("skill removed but failed to update manifest: %w", err)
	}

	spinner.Success(fmt.Sprintf("Removed skill %q from %s", name, strings.Join(targets, ", ")))
	return nil
}

func interactiveUninstall(manifest *skill.Manifest) error {
	if len(manifest.Skills) == 0 {
		terminal.Warning("No skills installed.")
		return nil
	}

	options := make([]string, len(manifest.Skills))
	for i, s := range manifest.Skills {
		label := s.Name
		if s.Description != "" {
			label = fmt.Sprintf("%s — %s", s.Name, uicli.TruncateString(s.Description, 60))
		}
		options[i] = label
	}

	showSpacer := true
	for {
		if showSpacer {
			fmt.Println()
		} else {
			showSpacer = true
		}
		selectedIdxs, err := multiSelectPrompt(prompt.SelectConfig{
			Label:   "Select skills to uninstall (space to toggle, enter to confirm)",
			Options: options,
		})
		if err != nil {
			if errors.Is(err, prompt.ErrBack) {
				showSpacer = false
				continue
			}
			return err
		}

		if len(selectedIdxs) == 0 {
			terminal.Info("No skills selected.")
			return nil
		}

		// Collect names before uninstalling, since uninstallByName modifies manifest.Skills.
		names := make([]string, len(selectedIdxs))
		for i, idx := range selectedIdxs {
			names[i] = manifest.Skills[idx].Name
		}

		fmt.Println()
		for _, name := range names {
			if err := uninstallByName(manifest, name); err != nil {
				terminal.Errorf("Failed to uninstall %q: %v", name, err)
			}
		}

		return nil
	}
}
