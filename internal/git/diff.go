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
	Status      string `json:"status"`      // A=Added, M=Modified, D=Deleted, R=Renamed, C=Copied
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	Similarity  int    `json:"similarity"` // For renames/copies (0-100)
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
	// Run git diff --numstat to get additions/deletions per file
	cmd := exec.Command("git", "-C", repoPath, "diff", "--numstat", fmt.Sprintf("%s..%s", branchA, branchB))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get diff stats: %w", err)
	}

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
		file.Status = "M" // Default to modified, could be enhanced to detect A/D

		// Detect file status from path markers (git diff --name-status would be better)
		if strings.HasPrefix(parts[2], "a/") {
			file.Status = "A"
			file.NewPath = strings.TrimPrefix(parts[2], "a/")
		} else if strings.HasPrefix(parts[2], "d/") {
			file.Status = "D"
			file.NewPath = strings.TrimPrefix(parts[2], "d/")
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
