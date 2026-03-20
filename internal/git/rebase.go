package git

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// RebaseOperation represents a type of rebase operation
type RebaseOperation int

const (
	RebasePick RebaseOperation = iota
	RebaseReword
	RebaseEdit
	RebaseSquash
	RebaseFixup
	RebaseDrop
)

func (op RebaseOperation) String() string {
	switch op {
	case RebasePick:
		return "pick"
	case RebaseReword:
		return "reword"
	case RebaseEdit:
		return "edit"
	case RebaseSquash:
		return "squash"
	case RebaseFixup:
		return "fixup"
	case RebaseDrop:
		return "drop"
	default:
		return "pick"
	}
}

func (op RebaseOperation) Shortcut() string {
	switch op {
	case RebasePick:
		return "p"
	case RebaseReword:
		return "r"
	case RebaseEdit:
		return "e"
	case RebaseSquash:
		return "s"
	case RebaseFixup:
		return "f"
	case RebaseDrop:
		return "d"
	default:
		return "p"
	}
}

// RebaseCommit represents a commit in an interactive rebase
type RebaseCommit struct {
	SHA     string `json:"sha"`
	ShortSHA string `json:"short_sha"`
	Short   string `json:"short"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Operation RebaseOperation `json:"operation"`
}

// GetRebasePlan generates a rebase plan for commits
func GetRebasePlan(repoPath, from, to string) ([]RebaseCommit, error) {
	// Get commits in range
	cmd := exec.Command("git", "-C", repoPath, "log", "--format=%H|%s|%an|%ad", "--date=short", fmt.Sprintf("%s..%s", from, to))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commits: %w", err)
	}

	var commits []RebaseCommit
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}

		commits = append(commits, RebaseCommit{
			SHA:        parts[0],
			ShortSHA:   parts[0][:7],
			Message:    parts[1],
			Author:     parts[2],
			Date:       parts[3],
			Operation:  RebasePick,
		})
	}

	return commits, nil
}

// GenerateTodoList generates a todo list for interactive rebase
func GenerateTodoList(commits []RebaseCommit) string {
	var lines []string
	for _, commit := range commits {
		line := fmt.Sprintf("%s %s %s", commit.Operation.String(), commit.ShortSHA, commit.Message)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ParseTodoList parses a todo list back into commits
func ParseTodoList(todo string) ([]RebaseCommit, error) {
	var commits []RebaseCommit
	scanner := bufio.NewScanner(strings.NewReader(todo))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			continue
		}

		op := parseOperation(parts[0])
		sha := parts[1]
		message := parts[2]

		commits = append(commits, RebaseCommit{
			SHA:        sha,
			ShortSHA:   sha[:7],
			Message:    message,
			Operation:  op,
		})
	}

	return commits, nil
}

func parseOperation(opStr string) RebaseOperation {
	switch opStr {
	case "pick", "p":
		return RebasePick
	case "reword", "r":
		return RebaseReword
	case "edit", "e":
		return RebaseEdit
	case "squash", "s":
		return RebaseSquash
	case "fixup", "f":
		return RebaseFixup
	case "drop", "d":
		return RebaseDrop
	default:
		return RebasePick
	}
}

// StartInteractiveRebase starts an interactive rebase with a todo list
func StartInteractiveRebase(repoPath, baseBranch string, commits []RebaseCommit) error {
	// Generate the todo file content
	todo := GenerateTodoList(commits)

	// Use git rebase -i with --exec for automation
	// For full interactive, we'd need $EDITOR
	args := []string{"-C", repoPath, "rebase", "-i", "--no-autosquash", baseBranch}

	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(todo)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rebase failed: %w\n%s", err, output)
	}

	return nil
}

// ContinueRebase continues an in-progress rebase
func ContinueRebase(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "rebase", "--continue")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rebase continue failed: %w\n%s", err, output)
	}
	return nil
}

// AbortRebase aborts the current rebase
func AbortRebase(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "rebase", "--abort")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rebase abort failed: %w\n%s", err, output)
	}
	return nil
}

// SkipRebase skips the current commit during rebase
func SkipRebase(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "rebase", "--skip")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rebase skip failed: %w\n%s", err, output)
	}
	return nil
}

// GetRebaseStatus returns the current rebase status
func GetRebaseStatus(repoPath string) (inProgress bool, commit string, err error) {
	// Check if rebase is in progress
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-dir")
	output, err := cmd.Output()
	if err != nil {
		return false, "", err
	}

	gitDir := strings.TrimSpace(string(output))

	// Check for rebase in progress
	rebaseMerge := gitDir + "/rebase-merge"
	rebaseApply := gitDir + "/rebase-apply"

	if _, err := os.Stat(rebaseMerge); err == nil {
		// Rebase merge in progress
		inProgress = true
		// Get current commit if available
		if data, err := os.ReadFile(rebaseMerge + "/current"); err == nil {
			commit = strings.TrimSpace(string(data))
		}
		return true, commit, nil
	}

	if _, err := os.Stat(rebaseApply); err == nil {
		// Rebase apply in progress
		inProgress = true
		return true, "", nil
	}

	return false, "", nil
}

// ExecuteRebaseStep executes a single rebase step
func ExecuteRebaseStep(repoPath, op string, commitSHA string) error {
	// For automated rebase steps, use git rebase with specific commands
	switch op {
	case "drop":
		// Drop is handled by omitting the commit from the sequence
		return nil
	case "edit":
		// Stop for editing
		cmd := exec.Command("git", "-C", repoPath, "rebase", "--edit-todo")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("edit step failed: %w\n%s", err, output)
		}
		return nil
	default:
		// pick, reword, squash, fixup - continue with defaults
		return ContinueRebase(repoPath)
	}
}

// SquashCommits squashes the last N commits
func SquashCommits(repoPath string, count int, message string) error {
	// Interactive rebase to squash last N commits
	cmd := exec.Command("git", "-C", repoPath, "reset", "--soft", fmt.Sprintf("HEAD~%d", count))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("squash reset failed: %w\n%s", err, output)
	}

	// Commit with message
	cmd = exec.Command("git", "-C", repoPath, "commit", "-m", message)
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("squash commit failed: %w\n%s", err, output)
	}

	return nil
}

// ReorderCommits reorders commits using rebase
func ReorderCommits(repoPath, from string, commits []string) error {
	// Get current branch
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(string(output))

	// Cherry-pick commits in new order
	for _, sha := range commits {
		cmd = exec.Command("git", "-C", repoPath, "cherry-pick", "--quiet", sha)
		if err := cmd.Run(); err != nil {
			// If failed, abort and return error
			AbortRebase(repoPath)
			return fmt.Errorf("reorder failed at %s", sha)
		}
	}

	return nil
}

// MoveCommit moves a commit to a different branch
func MoveCommit(repoPath, fromBranch, toBranch, commitSHA string) error {
	// Ensure we're on the target branch
	cmd := exec.Command("git", "-C", repoPath, "checkout", toBranch)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("checkout failed: %w", err)
	}

	// Cherry-pick the commit
	cmd = exec.Command("git", "-C", repoPath, "cherry-pick", commitSHA)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cherry-pick failed: %w\n%s", err, output)
	}

	return nil
}

// EditCommitMessage edits a commit message
func EditCommitMessage(repoPath, sha, newMessage string) error {
	cmd := exec.Command("git", "-C", repoPath, "commit", "--amend", "-m", newMessage)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("amend failed: %w\n%s", err, output)
	}
	return nil
}
