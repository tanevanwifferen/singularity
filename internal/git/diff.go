package git

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FileChange represents a changed file in a diff
type FileChange struct {
	Status     string `json:"status"` // A=Added, M=Modified, D=Deleted, R=Renamed, C=Copied
	OldPath    string `json:"old_path"`
	NewPath    string `json:"new_path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	Similarity int    `json:"similarity"` // For renames/copies (0-100)
}

// BranchDiff holds the result of comparing two branches at file level
type BranchDiff struct {
	FilesChanged   int          `json:"files_changed"`
	TotalAdditions int          `json:"total_additions"`
	TotalDeletions int          `json:"total_deletions"`
	Files          []FileChange `json:"files"`
	BranchA        string       `json:"branch_a"`
	BranchB        string       `json:"branch_b"`
}

// GetBranchDiff compares two branches and returns file-level diff statistics
func GetBranchDiff(repoPath, branchA, branchB string) (*BranchDiff, error) {
	revRange := fmt.Sprintf("%s..%s", branchA, branchB)

	// Get name-status for accurate file statuses (A/M/D/R)
	nameStatusCmd := exec.Command("git", "-C", repoPath, "diff", "--name-status", revRange)
	nameStatusOut, _ := nameStatusCmd.Output()
	statusMap := parseNameStatus(string(nameStatusOut))

	// Get numstat for line counts
	numstatCmd := exec.Command("git", "-C", repoPath, "diff", "--numstat", revRange)
	numstatOut, err := numstatCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get diff stats: %w", err)
	}

	var files []FileChange
	totalAdditions := 0
	totalDeletions := 0

	scanner := bufio.NewScanner(strings.NewReader(string(numstatOut)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}

		file := FileChange{}
		path := parts[2]

		if parts[0] == "-" && parts[1] == "-" {
			// Binary file
			file.Status = "M"
			file.NewPath = path
			if s, ok := statusMap[path]; ok {
				file.Status = s
			}
			files = append(files, file)
			continue
		}

		if parts[0] != "-" {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				file.Additions = n
				totalAdditions += n
			}
		}
		if parts[1] != "-" {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				file.Deletions = n
				totalDeletions += n
			}
		}

		file.NewPath = path
		file.Status = "M"
		if s, ok := statusMap[path]; ok {
			file.Status = s
		}
		files = append(files, file)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read diff output: %w", err)
	}

	return &BranchDiff{
		FilesChanged:   len(files),
		TotalAdditions: totalAdditions,
		TotalDeletions: totalDeletions,
		Files:          files,
		BranchA:        branchA,
		BranchB:        branchB,
	}, nil
}

// parseNameStatus parses git diff --name-status output into a path→status map.
func parseNameStatus(output string) map[string]string {
	statusMap := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		statusCode := string(parts[0][0]) // First char: A, M, D, R, C, etc.
		// For renames (R100), the new path is the last field
		path := parts[len(parts)-1]
		statusMap[path] = statusCode
	}
	return statusMap
}

// GetChangedFiles returns just the list of changed file paths
func GetChangedFiles(repoPath, branchA, branchB string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--name-only", fmt.Sprintf("%s..%s", branchA, branchB))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get changed files: %w", err)
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}

	return files, nil
}

// StagedDiff holds file-level diff statistics for staged changes
type StagedDiff struct {
	FilesChanged   int          `json:"files_changed"`
	TotalAdditions int          `json:"total_additions"`
	TotalDeletions int          `json:"total_deletions"`
	Files          []FileChange `json:"files"`
}

// UnstagedDiff holds file-level diff statistics for unstaged changes
type UnstagedDiff struct {
	FilesChanged   int          `json:"files_changed"`
	TotalAdditions int          `json:"total_additions"`
	TotalDeletions int          `json:"total_deletions"`
	Files          []FileChange `json:"files"`
}

// GetStagedFilesDiff returns file-level diff statistics for staged changes
func GetStagedFilesDiff(repoPath string) (*StagedDiff, error) {
	// Run git diff --cached --numstat to get staged additions/deletions per file
	cmd := exec.Command("git", "-C", repoPath, "diff", "--cached", "--numstat")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get staged diff stats: %w", err)
	}

	return parseNumstatOutput(string(output))
}

// GetUnstagedFilesDiff returns file-level diff statistics for unstaged changes
func GetUnstagedFilesDiff(repoPath string) (*UnstagedDiff, error) {
	// Run git diff --numstat to get unstaged additions/deletions per file
	cmd := exec.Command("git", "-C", repoPath, "diff", "--numstat")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get unstaged diff stats: %w", err)
	}

	diff, err := parseNumstatOutput(string(output))
	if err != nil {
		return nil, err
	}

	return &UnstagedDiff{
		FilesChanged:   diff.FilesChanged,
		TotalAdditions: diff.TotalAdditions,
		TotalDeletions: diff.TotalDeletions,
		Files:          diff.Files,
	}, nil
}

// GetStagedFileDiff returns the actual diff content for a specific staged file
func GetStagedFileDiff(repoPath, filePath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--cached", "--", filePath)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get staged file diff: %w", err)
	}
	return string(output), nil
}

// GetUnstagedFileDiff returns the actual diff content for a specific unstaged file
func GetUnstagedFileDiff(repoPath, filePath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--", filePath)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get unstaged file diff: %w", err)
	}
	return string(output), nil
}

// parseNumstatOutput parses git diff --numstat output into FileChange slice
func parseNumstatOutput(output string) (*StagedDiff, error) {
	var files []FileChange
	totalAdditions := 0
	totalDeletions := 0

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse numstat format: "\t\t" for binary files, "additions\tdeletions\tpath" otherwise
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}

		file := FileChange{}

		// Handle binary files (shown as -\t-\tfilename)
		if parts[0] == "-" && parts[1] == "-" {
			file.Status = "M" // Binary modified
			file.NewPath = parts[2]
			files = append(files, file)
			continue
		}

		// Parse additions
		if parts[0] != "-" {
			additions, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			file.Additions = additions
			totalAdditions += additions
		}

		// Parse deletions
		if parts[1] != "-" {
			deletions, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}
			file.Deletions = deletions
			totalDeletions += deletions
		}

		file.NewPath = parts[2]
		file.Status = "M" // Default to modified

		files = append(files, file)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read diff output: %w", err)
	}

	return &StagedDiff{
		FilesChanged:   len(files),
		TotalAdditions: totalAdditions,
		TotalDeletions: totalDeletions,
		Files:          files,
	}, nil
}

// GetFileDiff returns the diff between two branches for a specific file
func GetFileDiff(repoPath, branchA, branchB, filePath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", fmt.Sprintf("%s..%s", branchA, branchB), "--", filePath)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get file diff: %w", err)
	}
	return string(output), nil
}

// GetMergeBase returns the best common ancestor commit (merge base) of two refs.
// Returns the commit SHA, or an error if the merge base cannot be determined.
func GetMergeBase(repoPath, ref1, ref2 string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "merge-base", ref1, ref2)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get merge base of %s and %s: %w", ref1, ref2, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveRef tries to find an existing git ref for the given name.
// First tries the exact name, then "origin/<name>".
// Returns the first ref that resolves, or the original name if none found.
func ResolveRef(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "--quiet", ref)
	if err := cmd.Run(); err == nil {
		return ref
	}
	originRef := "origin/" + ref
	cmd = exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "--quiet", originRef)
	if err := cmd.Run(); err == nil {
		return originRef
	}
	return ref
}

// GetFileContent gets the raw content of a file at a specific git ref.
func GetFileContent(repoPath, ref, filePath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "show", fmt.Sprintf("%s:%s", ref, filePath))
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get content of %s at %s: %w", filePath, ref, err)
	}
	return string(output), nil
}

// FilteredDiffHunk wraps DiffHunk with a flag indicating whether all added lines
// are already present in the base branch (squash-merge false positive detection).
type FilteredDiffHunk struct {
	DiffHunk
	AlreadyInBase bool
}

// GetDeepFileDiff gets the diff between diffBase and headRef for filePath,
// and annotates each hunk as "already in base" by checking whether all added
// lines are already present in the defaultBranch version of the file. This
// detects squash-merge false positives where git reports changes that are
// actually already incorporated in the default branch.
//
// diffBase is the merge-base commit (used for computing the diff).
// defaultBranch is the current tip of the default branch (used for the
// "already in base" check — this is where squash-merged content lives).
//
// Returns the annotated hunks, the raw diff string, and any error.
func GetDeepFileDiff(repoPath, diffBase, headRef, defaultBranch, filePath string) ([]FilteredDiffHunk, string, error) {
	rawDiff, err := GetFileDiff(repoPath, diffBase, headRef, filePath)
	if err != nil {
		return nil, "", err
	}
	if rawDiff == "" {
		return nil, "", nil
	}

	hunks := ParseHunks(rawDiff)
	if len(hunks) == 0 {
		return nil, rawDiff, nil
	}

	// Get the file content at the current default branch tip to check whether
	// the branch's additions have already been incorporated (e.g. via squash merge).
	defaultContent, err := GetFileContent(repoPath, defaultBranch, filePath)
	if err != nil {
		// File doesn't exist in default branch — all hunks are genuinely new
		result := make([]FilteredDiffHunk, len(hunks))
		for i, h := range hunks {
			result[i] = FilteredDiffHunk{h, false}
		}
		return result, rawDiff, nil
	}

	// Build a set of lines from the default branch file for fast lookup
	defaultLineSet := make(map[string]bool)
	for _, line := range strings.Split(defaultContent, "\n") {
		defaultLineSet[strings.TrimRight(line, "\r")] = true
	}

	result := make([]FilteredDiffHunk, len(hunks))
	for i, hunk := range hunks {
		result[i] = FilteredDiffHunk{hunk, isHunkAlreadyInBase(hunk, defaultLineSet)}
	}
	return result, rawDiff, nil
}

// isHunkAlreadyInBase returns true if the hunk has additions and all of them
// are already present in the base file content set.
func isHunkAlreadyInBase(hunk DiffHunk, baseLineSet map[string]bool) bool {
	hasAdditions := false
	for _, line := range hunk.Lines {
		if line.LineType == "+" {
			hasAdditions = true
			content := strings.TrimRight(strings.TrimPrefix(line.Content, "+"), "\r")
			if !baseLineSet[content] {
				return false
			}
		}
	}
	return hasAdditions
}

// WorkdirStatus holds status info for a file in the working directory
type WorkdirStatus struct {
	Path              string // File path
	StagedStatus      string // Status in staging area (A/M/D/R/?)
	UnstagedStatus    string // Status in working tree (A/M/D/R/?)
	StagedAdditions   int
	StagedDeletions   int
	UnstagedAdditions int
	UnstagedDeletions int
}

// WorkdirDiff holds all working directory changes
type WorkdirDiff struct {
	Files             []WorkdirStatus
	TotalStagedAdds   int
	TotalStagedDels   int
	TotalUnstagedAdds int
	TotalUnstagedDels int
}

// GetWorkdirStatus returns the status of all changed files in the working directory
func GetWorkdirStatus(repoPath string) (*WorkdirDiff, error) {
	// Get staged diff stats
	stagedDiff, err := GetStagedFilesDiff(repoPath)
	if err != nil {
		return nil, err
	}

	// Get unstaged diff stats
	unstagedDiff, err := GetUnstagedFilesDiff(repoPath)
	if err != nil {
		return nil, err
	}

	// Get git status for accurate status indicators
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain=v1")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get workdir status: %w", err)
	}

	// Build a map of file paths to their status
	statusMap := make(map[string]*WorkdirStatus)

	// First, add all staged files
	for _, f := range stagedDiff.Files {
		statusMap[f.NewPath] = &WorkdirStatus{
			Path:            f.NewPath,
			StagedStatus:    f.Status,
			StagedAdditions: f.Additions,
			StagedDeletions: f.Deletions,
		}
	}

	// Then, add/merge unstaged files
	for _, f := range unstagedDiff.Files {
		if existing, ok := statusMap[f.NewPath]; ok {
			existing.UnstagedStatus = f.Status
			existing.UnstagedAdditions = f.Additions
			existing.UnstagedDeletions = f.Deletions
		} else {
			statusMap[f.NewPath] = &WorkdirStatus{
				Path:              f.NewPath,
				UnstagedStatus:    f.Status,
				UnstagedAdditions: f.Additions,
				UnstagedDeletions: f.Deletions,
			}
		}
	}

	// Parse porcelain status
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Porcelain format: XY path
		// X = staged status, Y = unstaged status
		// M = modified, A = added, D = deleted, R = renamed, ?? = untracked
		if len(line) < 3 {
			continue
		}

		staged := string(line[0])
		unstaged := string(line[1])
		path := line[3:] // Skip XY and space

		// Update status map entry if it exists
		if existing, ok := statusMap[path]; ok {
			if staged != " " && staged != "?" {
				existing.StagedStatus = staged
			}
			if unstaged != " " && unstaged != "?" {
				existing.UnstagedStatus = unstaged
			}
		} else {
			// New file that might only be unstaged
			statusMap[path] = &WorkdirStatus{
				Path: path,
			}
			if staged != " " && staged != "?" {
				statusMap[path].StagedStatus = staged
			}
			if unstaged != " " && unstaged != "?" {
				statusMap[path].UnstagedStatus = unstaged
			}
		}
	}

	// Convert to slice and calculate totals
	var files []WorkdirStatus
	var totalStagedAdds, totalStagedDels, totalUnstagedAdds, totalUnstagedDels int

	for _, f := range statusMap {
		files = append(files, *f)
		totalStagedAdds += f.StagedAdditions
		totalStagedDels += f.StagedDeletions
		totalUnstagedAdds += f.UnstagedAdditions
		totalUnstagedDels += f.UnstagedDeletions
	}

	return &WorkdirDiff{
		Files:             files,
		TotalStagedAdds:   totalStagedAdds,
		TotalStagedDels:   totalStagedDels,
		TotalUnstagedAdds: totalUnstagedAdds,
		TotalUnstagedDels: totalUnstagedDels,
	}, nil
}

// GetCommitFiles returns the list of files changed in a specific commit.
// Works for merge commits and initial commits.
func GetCommitFiles(repoPath, hash string) ([]FileChange, error) {
	// Use diff-tree with --no-commit-id -r to list files.
	// For merge commits, use --first-parent to compare against first parent only.
	// For initial commits, diff-tree with root flag works.
	args := []string{"-C", repoPath, "diff-tree", "--no-commit-id", "-r", "--name-status", hash}
	cmd := exec.Command("git", args...)
	statusOutput, err := cmd.Output()
	if err != nil {
		// Might be initial commit (no parent) - try with --root
		args = []string{"-C", repoPath, "diff-tree", "--no-commit-id", "-r", "--root", "--name-status", hash}
		cmd = exec.Command("git", args...)
		statusOutput, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to get commit files: %w", err)
		}
	}

	// Parse name-status output to get file statuses
	statusMap := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(statusOutput)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		path := parts[1]
		// For renames (R100), extract old and new paths
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if len(parts) >= 3 {
				statusMap[parts[2]] = status
			}
		} else {
			statusMap[path] = status
		}
	}

	// Now get numstat for addition/deletion counts
	numstatArgs := []string{"-C", repoPath, "show", "--numstat", "--format=", hash}
	cmd = exec.Command("git", numstatArgs...)
	numstatOutput, err := cmd.Output()
	if err != nil {
		// Fall back to just the status info without counts
		var files []FileChange
		for path, status := range statusMap {
			fc := FileChange{
				NewPath: path,
				Status:  normalizeStatus(status),
			}
			files = append(files, fc)
		}
		return files, nil
	}

	// Parse numstat and merge with status info
	var files []FileChange
	numstatScanner := bufio.NewScanner(strings.NewReader(string(numstatOutput)))
	seenPaths := make(map[string]bool)
	for numstatScanner.Scan() {
		line := numstatScanner.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}

		fc := FileChange{
			NewPath: parts[2],
		}

		// Parse additions
		if parts[0] != "-" {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				fc.Additions = n
			}
		}
		// Parse deletions
		if parts[1] != "-" {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				fc.Deletions = n
			}
		}

		// Look up status
		if status, ok := statusMap[fc.NewPath]; ok {
			fc.Status = normalizeStatus(status)
		} else {
			fc.Status = "M"
		}

		files = append(files, fc)
		seenPaths[fc.NewPath] = true
	}

	// Add any files from status that were not in numstat (e.g. renames)
	for path, status := range statusMap {
		if !seenPaths[path] {
			files = append(files, FileChange{
				NewPath: path,
				Status:  normalizeStatus(status),
			})
		}
	}

	return files, nil
}

// normalizeStatus converts git status codes like "R100" to single-char "R"
func normalizeStatus(status string) string {
	if len(status) == 0 {
		return "M"
	}
	switch status[0] {
	case 'A':
		return "A"
	case 'M':
		return "M"
	case 'D':
		return "D"
	case 'R':
		return "R"
	case 'C':
		return "C"
	default:
		return "M"
	}
}

// GetCommitFileDiff returns the diff content for a specific file in a commit.
// Works for merge commits and initial commits.
func GetCommitFileDiff(repoPath, hash, filePath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff-tree", "-p", "--no-commit-id", hash, "--", filePath)
	output, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(output))) == 0 {
		// Fallback for initial commits or merge commits: use show without metadata
		cmd = exec.Command("git", "-C", repoPath, "show", "--format=", hash, "--", filePath)
		output, err = cmd.Output()
		if err != nil {
			return "", fmt.Errorf("failed to get commit file diff: %w", err)
		}
	}
	return string(output), nil
}

// DiffHunk represents a single hunk in a unified diff
type DiffHunk struct {
	Header   string     // The @@ header line
	Lines    []DiffLine // All lines in this hunk (context, additions, deletions)
	OldStart int
	OldCount int
	NewStart int
	NewCount int
}

// DiffLine represents a single line within a diff hunk
type DiffLine struct {
	Content  string
	LineType string // "+", "-", " " (context)
}

// ParseHunks parses raw unified diff output into structured hunks.
// The rawDiff should be the output of git diff for a single file.
func ParseHunks(rawDiff string) []DiffHunk {
	var hunks []DiffHunk
	var current *DiffHunk

	for _, line := range strings.Split(rawDiff, "\n") {
		if strings.HasPrefix(line, "@@") {
			// Parse hunk header: @@ -oldStart,oldCount +newStart,newCount @@
			hunk := DiffHunk{Header: line}
			parseHunkHeader(line, &hunk)
			hunks = append(hunks, hunk)
			current = &hunks[len(hunks)-1]
		} else if current != nil {
			// Lines belonging to the current hunk
			dl := DiffLine{Content: line}
			if strings.HasPrefix(line, "+") {
				dl.LineType = "+"
			} else if strings.HasPrefix(line, "-") {
				dl.LineType = "-"
			} else if strings.HasPrefix(line, " ") {
				dl.LineType = " "
			} else if line == "\\ No newline at end of file" {
				dl.LineType = "\\"
			} else {
				// Non-diff line (e.g., empty trailing line) -- skip
				continue
			}
			current.Lines = append(current.Lines, dl)
		}
	}

	return hunks
}

// parseHunkHeader extracts line numbers from a hunk header like "@@ -1,5 +1,7 @@"
func parseHunkHeader(header string, hunk *DiffHunk) {
	// Find the range specifications between @@ markers
	parts := strings.SplitN(header, "@@", 3)
	if len(parts) < 2 {
		return
	}
	rangeStr := strings.TrimSpace(parts[1])
	fields := strings.Fields(rangeStr)

	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			nums := strings.TrimPrefix(f, "-")
			if idx := strings.Index(nums, ","); idx >= 0 {
				if n, err := strconv.Atoi(nums[:idx]); err == nil {
					hunk.OldStart = n
				}
				if n, err := strconv.Atoi(nums[idx+1:]); err == nil {
					hunk.OldCount = n
				}
			} else {
				if n, err := strconv.Atoi(nums); err == nil {
					hunk.OldStart = n
					hunk.OldCount = 1
				}
			}
		} else if strings.HasPrefix(f, "+") {
			nums := strings.TrimPrefix(f, "+")
			if idx := strings.Index(nums, ","); idx >= 0 {
				if n, err := strconv.Atoi(nums[:idx]); err == nil {
					hunk.NewStart = n
				}
				if n, err := strconv.Atoi(nums[idx+1:]); err == nil {
					hunk.NewCount = n
				}
			} else {
				if n, err := strconv.Atoi(nums); err == nil {
					hunk.NewStart = n
					hunk.NewCount = 1
				}
			}
		}
	}
}

// buildPatch constructs a minimal patch that can be applied with git apply.
// It includes the file header lines and a single hunk.
func buildPatch(filePath string, hunk DiffHunk) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", filePath, filePath))
	sb.WriteString(fmt.Sprintf("--- a/%s\n", filePath))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", filePath))
	sb.WriteString(hunk.Header + "\n")
	for _, line := range hunk.Lines {
		sb.WriteString(line.Content + "\n")
	}
	return sb.String()
}

// StageHunk stages a single hunk using git apply --cached.
func StageHunk(repoPath, filePath string, hunk DiffHunk) error {
	patch := buildPatch(filePath, hunk)
	cmd := exec.Command("git", "-C", repoPath, "apply", "--cached", "--unidiff-zero")
	cmd.Stdin = strings.NewReader(patch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stage hunk: %w: %s", err, string(output))
	}
	return nil
}

// UnstageHunk unstages a single hunk using git apply --cached --reverse.
func UnstageHunk(repoPath, filePath string, hunk DiffHunk) error {
	patch := buildPatch(filePath, hunk)
	cmd := exec.Command("git", "-C", repoPath, "apply", "--cached", "--reverse", "--unidiff-zero")
	cmd.Stdin = strings.NewReader(patch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to unstage hunk: %w: %s", err, string(output))
	}
	return nil
}

// buildPartialPatch constructs a patch containing only selected lines from a hunk.
// For staging: unselected "+" lines are dropped, unselected "-" lines become context.
// For unstaging (reverse=true): the same logic applies but the patch will be applied
// with --reverse, so the caller should pass the staged hunk as-is and selectedLineIndices
// referring to the lines the user wants to unstage.
//
// selectedLineIndices contains zero-based indices into hunk.Lines that the user selected.
// Only "+" and "-" lines are meaningful selections; context lines are always included.
func buildPartialPatch(filePath string, hunk DiffHunk, selectedLineIndices []int, reverse bool) string {
	selected := make(map[int]bool, len(selectedLineIndices))
	for _, idx := range selectedLineIndices {
		selected[idx] = true
	}

	// Build the filtered lines and compute new header counts.
	var patchLines []DiffLine
	oldCount := 0
	newCount := 0

	for i, line := range hunk.Lines {
		switch line.LineType {
		case " ":
			// Context lines are always included.
			patchLines = append(patchLines, line)
			oldCount++
			newCount++
		case "+":
			if selected[i] {
				// Keep as addition.
				patchLines = append(patchLines, line)
				newCount++
			} else {
				// Unselected addition: drop it entirely (it does not exist in old or new).
				// Do NOT convert to context -- the line is not present on the old side.
			}
		case "-":
			if selected[i] {
				// Keep as deletion.
				patchLines = append(patchLines, line)
				oldCount++
			} else {
				// Unselected deletion: convert to context (keep line in both old and new).
				ctx := DiffLine{
					Content:  " " + line.Content[1:],
					LineType: " ",
				}
				patchLines = append(patchLines, ctx)
				oldCount++
				newCount++
			}
		case "\\":
			// "\ No newline at end of file" -- keep as-is.
			patchLines = append(patchLines, line)
		}
	}

	// Build the header.
	header := fmt.Sprintf("@@ -%d,%d +%d,%d @@", hunk.OldStart, oldCount, hunk.NewStart, newCount)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", filePath, filePath))
	sb.WriteString(fmt.Sprintf("--- a/%s\n", filePath))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", filePath))
	sb.WriteString(header + "\n")
	for _, line := range patchLines {
		sb.WriteString(line.Content + "\n")
	}
	return sb.String()
}

// StageLines stages selected lines from a single hunk using git apply --cached.
// selectedLineIndices are zero-based indices into hunk.Lines referring to "+" or "-" lines.
func StageLines(repoPath, filePath string, hunk DiffHunk, selectedLineIndices []int) error {
	patch := buildPartialPatch(filePath, hunk, selectedLineIndices, false)
	cmd := exec.Command("git", "-C", repoPath, "apply", "--cached", "--unidiff-zero")
	cmd.Stdin = strings.NewReader(patch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stage lines: %w: %s", err, string(output))
	}
	return nil
}

// UnstageLines unstages selected lines from a single staged hunk using git apply --cached --reverse.
// selectedLineIndices are zero-based indices into hunk.Lines referring to "+" or "-" lines.
func UnstageLines(repoPath, filePath string, hunk DiffHunk, selectedLineIndices []int) error {
	patch := buildPartialPatch(filePath, hunk, selectedLineIndices, true)
	cmd := exec.Command("git", "-C", repoPath, "apply", "--cached", "--reverse", "--unidiff-zero")
	cmd.Stdin = strings.NewReader(patch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to unstage lines: %w: %s", err, string(output))
	}
	return nil
}
