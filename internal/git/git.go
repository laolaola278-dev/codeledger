package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// runGit runs a git command and returns trimmed stdout string.
func runGit(dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// runGitOutput runs a git command and returns raw stdout bytes.
func runGitOutput(dir string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// IsGitRepo checks whether the given directory is inside a git work tree.
func IsGitRepo(dir string) bool {
	out, err := runGit(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// ChangedFiles returns a sorted list of changed files (working tree + staged).
// Uses --porcelain=v1 -z for NUL-delimited machine-readable output.
func ChangedFiles(dir string) ([]string, error) {
	if !IsGitRepo(dir) {
		return nil, fmt.Errorf("not a git repository")
	}
	data, err := runGitOutput(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseStatusNull(data), nil
}

// parseStatusNull parses NUL-delimited git status --porcelain=v1 -z output.
func parseStatusNull(data []byte) []string {
	records := bytes.Split(data, []byte{0})
	files := make([]string, 0, len(records))
	seen := make(map[string]bool)
	for _, rec := range records {
		if len(rec) < 4 {
			continue
		}
		line := string(rec)
		path := line[3:]
		if strings.Contains(path, " -> ") {
			parts := strings.SplitN(path, " -> ", 2)
			path = parts[len(parts)-1]
		}
		path = unquotePath(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	sort.Strings(files)
	return files
}

// unquotePath handles git's quoted path format (e.g. "\"file with spaces\"").
func unquotePath(p string) string {
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		if unquoted, err := strconv.Unquote(p); err == nil {
			return unquoted
		}
	}
	return p
}

// Diff returns the full diff output for the working tree (and optionally staged).
// If cached is true, returns only staged diff (diff --cached).
func Diff(dir string, cached bool) (string, error) {
	if !IsGitRepo(dir) {
		return "", fmt.Errorf("not a git repository")
	}
	args := []string{"diff"}
	if cached {
		args = append(args, "--cached")
	}
	out, err := runGit(dir, args...)
	if err != nil {
		return "", err
	}
	return out, nil
}

// DiffNameOnly returns a list of file names that have changes.
// If cached is true, returns only staged file names.
func DiffNameOnly(dir string, cached bool) ([]string, error) {
	if !IsGitRepo(dir) {
		return nil, fmt.Errorf("not a git repository")
	}
	if cached {
		out, err := runGit(dir, "diff", "--cached", "--name-only")
		if err != nil {
			return nil, err
		}
		if out == "" {
			return nil, nil
		}
		return strings.Fields(out), nil
	}
	// Non-cached: combine unstaged and staged file names with dedup
	seen := make(map[string]bool)
	var files []string
	for _, args := range [][]string{{"diff", "--name-only"}, {"diff", "--cached", "--name-only"}} {
		out, err := runGit(dir, args...)
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(out) {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	return files, nil
}

// DiffStat returns the diff --stat output (working tree + cached).
func DiffStat(dir string) (string, error) {
	if !IsGitRepo(dir) {
		return "", fmt.Errorf("not a git repository")
	}
	var parts []string
	if out, err := runGit(dir, "diff", "--stat"); err == nil && out != "" {
		parts = append(parts, out)
	}
	if out, err := runGit(dir, "diff", "--cached", "--stat"); err == nil && out != "" {
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n"), nil
}

// CurrentBranch returns the current branch name.
func CurrentBranch(dir string) (string, error) {
	if !IsGitRepo(dir) {
		return "", fmt.Errorf("not a git repository")
	}
	return runGit(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// CurrentCommit returns the short commit hash of HEAD.
func CurrentCommit(dir string) (string, error) {
	if !IsGitRepo(dir) {
		return "", fmt.Errorf("not a git repository")
	}
	return runGit(dir, "rev-parse", "--short", "HEAD")
}

// FullDiff returns both working-tree and staged diff combined.
func FullDiff(dir string) (string, error) {
	if !IsGitRepo(dir) {
		return "", fmt.Errorf("not a git repository")
	}
	var parts []string
	if out, err := runGit(dir, "diff"); err == nil && out != "" {
		parts = append(parts, out)
	}
	if out, err := runGit(dir, "diff", "--cached"); err == nil && out != "" {
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n"), nil
}
