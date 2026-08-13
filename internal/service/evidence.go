package service

import (
	"fmt"
	"os"
	"strings"

	"github.com/codeledger/codeledger/internal/git"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
)

// GitEvidence contains the git context captured when a task is completed.
type GitEvidence struct {
	RepoRoot     string
	Commit       string
	ChangedFiles []string
	DiffStat     string
	Diff         string
	Error        string
}

func scanGitProject(root string) *GitEvidence {
	ev := &GitEvidence{}
	if !git.IsGitRepo(root) {
		ev.Error = "not a git repository"
		return ev
	}

	ev.RepoRoot = root
	ev.Commit, _ = git.CurrentCommit(root)
	ev.ChangedFiles, _ = git.ChangedFiles(root)
	ev.DiffStat, _ = git.DiffStat(root)
	ev.Diff, _ = git.FullDiff(root)
	return ev
}

// AddEvidence appends evidence content to a task's .md evidence file.
// If the file does not exist, it is created. The evidence path is added to
// Task.Evidence if not already present. An evidence.added event is logged.
func AddEvidence(s *store.Store, taskID, evidenceType, content string) error {
	tl, err := s.ReadTasks()
	if err != nil {
		return err
	}
	idx, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return err
	}

	if err := s.EnsureEvidenceDir(); err != nil {
		return err
	}

	section := fmt.Sprintf("\n## Evidence: %s\n\n%s\n", evidenceType, strings.TrimSpace(content))
	f, err := os.OpenFile(s.EvidencePath(taskID), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open evidence file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(section); err != nil {
		return fmt.Errorf("failed to write evidence: %w", err)
	}

	// Ensure the .md path is in Task.Evidence
	relPath := s.EvidenceRelPath(taskID)
	if !containsString(task.Evidence, relPath) {
		task.Evidence = append(task.Evidence, relPath)
	}
	task.UpdatedAt = util.NowRFC3339()
	tl.Tasks[idx] = *task
	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventEvidenceAdded, taskID, task.Title, "evidence type: "+evidenceType)
	return s.AppendEvent(evt)
}

func recordEvidence(s *store.Store, task *model.Task, gitEv *GitEvidence) error {
	if err := s.EnsureEvidenceDir(); err != nil {
		return err
	}

	content := renderEvidence(task, gitEv)
	if err := os.WriteFile(s.EvidencePath(task.ID), []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write evidence: %w", err)
	}

	evt := model.NewEvent(model.EventEvidenceRecorded, task.ID, task.Title, "evidence written to "+s.EvidenceRelPath(task.ID))
	return s.AppendEvent(evt)
}

// renderEvidence builds the Markdown evidence file content.
// The full diff is NOT included here; it goes to a separate .diff file
// when --capture-diff is used. Only the diffstat summary is shown.
func renderEvidence(task *model.Task, gitEv *GitEvidence) string {
	var b strings.Builder
	b.WriteString("# Evidence: " + task.ID + "\n\n")
	b.WriteString(fmt.Sprintf("- Status: %s\n", task.Status))
	b.WriteString(fmt.Sprintf("- Completed At: %s\n", task.CompletedAt))
	b.WriteString(fmt.Sprintf("- Files: %s\n", commaList(task.Files)))
	b.WriteString(fmt.Sprintf("- Test Command: %s\n", task.Test.Command))
	b.WriteString(fmt.Sprintf("- Test Result: %s\n", task.Test.Result))
	b.WriteString(fmt.Sprintf("- Note: %s\n", task.Notes))
	b.WriteString(fmt.Sprintf("- Evidence: %s\n", commaList(task.Evidence)))
	b.WriteString("\n")

	b.WriteString("## Git\n\n")
	if gitEv.Error != "" {
		b.WriteString("- Error: " + gitEv.Error + "\n")
	}
	if gitEv.Commit != "" {
		b.WriteString(fmt.Sprintf("- Commit: %s\n", gitEv.Commit))
	}
	b.WriteString(fmt.Sprintf("- Changed Files: %s\n", commaList(gitEv.ChangedFiles)))
	if gitEv.DiffStat != "" {
		b.WriteString("\n### Diff Stat\n\n```\n" + gitEv.DiffStat + "\n```\n")
	}

	return b.String()
}

func commaList(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}

// containsString checks if a string slice contains a given string.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// dedupStrings returns a new slice with duplicates removed, preserving order.
func dedupStrings(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range items {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		result = append(result, s)
	}
	return result
}
