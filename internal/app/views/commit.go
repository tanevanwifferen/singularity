package views

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"
	"git-frontend/internal/theme"

	"github.com/charmbracelet/bubbletea"
)

// StagedFile represents a file in the staging area
type StagedFile struct {
	Path       string
	Additions  int
	Deletions  int
	IsNew      bool
	IsDeleted  bool
	IsModified bool
}

// EditMode represents the current editing mode
type EditMode int

const (
	StageEditMode EditMode = iota // Default: staging area navigation
	MessageEditMode                // Editing commit message
	ConfirmCommitMode             // Confirmation dialog
	HunkDiffMode                  // Viewing file diff with hunk navigation
	LineDiffMode                  // Line-level selection within a hunk
)

// CommitView displays the staging area and allows creating commits.
type CommitView struct {
	repoPath    string
	width       int
	height      int
	loading     bool
	generating  bool // AI message generation in progress
	committing  bool // Commit in progress
	err         error
	successMsg  string

	// Staged and unstaged files
	stagedFiles   []StagedFile
	unstagedFiles []StagedFile

	// Navigation state
	activeSection  int // 0 = staged, 1 = unstaged
	selectedIndex  int

	// Message editing state
	editMode      EditMode
	commitMessage string
	messageCursor int

	// Confirmation state
	confirmPending bool // Set to true after Ctrl+Enter to show confirm dialog

	// Hunk diff preview state
	hunkDiffRaw     string         // Raw diff output for the selected file
	hunkList        []git.DiffHunk // Parsed hunks for the selected file
	hunkIndex       int            // Currently selected hunk
	hunkScrollOff   int            // Scroll offset within diff display
	hunkIsStaged    bool           // Whether we are viewing staged hunks (true) or unstaged (false)
	hunkFilePath    string         // Path of the file whose hunks are displayed

	// Line-selection mode state (within a single hunk)
	lineCursor      int            // Current cursor position in hunk.Lines
	lineSelected    map[int]bool   // Set of toggled line indices (into hunk.Lines)
	lineVisual      bool           // Visual/range selection active
	lineVisualStart int            // Anchor index for visual selection
}

// NewCommitView creates a new commit view.
func NewCommitView(repoPath string) *CommitView {
	return &CommitView{
		repoPath:       repoPath,
		width:          80,
		height:         24,
		activeSection:  1, // Start on unstaged (more common to stage from)
		selectedIndex:  0,
	}
}

// SetRepoPath updates the repository path for this view.
func (v *CommitView) SetRepoPath(path string) { v.repoPath = path }

// Init initializes the commit view.
func (v *CommitView) Init() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		v.loadFiles()
		return RefreshDoneMsg{}
	}
}

// loadFiles loads staged and unstaged files from git.
func (v *CommitView) loadFiles() {
	v.err = nil

	// Load staged files using git diff --cached --name-only --numstat
	staged, err := v.getStagedFiles()
	if err != nil {
		v.err = fmt.Errorf("failed to get staged files: %w", err)
	} else {
		v.stagedFiles = staged
	}

	// Load unstaged files using git diff --name-only --numstat
	unstaged, err := v.getUnstagedFiles()
	if err != nil {
		v.err = fmt.Errorf("failed to get unstaged files: %w", err)
	} else {
		v.unstagedFiles = unstaged
	}

	v.loading = false
}

// getStagedFiles returns files that are staged for commit.
func (v *CommitView) getStagedFiles() ([]StagedFile, error) {
	var files []StagedFile

	// Get staged files with status using git diff --cached --name-status
	cmd := exec.Command("git", "-C", v.repoPath, "diff", "--cached", "--name-status")
	output, err := cmd.Output()
	if err != nil {
		// No staged files is not an error
		if strings.Contains(err.Error(), "exit status 1") && len(output) == 0 {
			return files, nil
		}
		return nil, err
	}

	statusMap := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		statusMap[parts[1]] = parts[0]
	}

	// Get numstat for additions/deletions
	numstatCmd := exec.Command("git", "-C", v.repoPath, "diff", "--cached", "--numstat")
	numstatOutput, err := numstatCmd.Output()
	if err != nil {
		return nil, err
	}

	numstatMap := make(map[string]struct{ additions, deletions int })
	scanner = bufio.NewScanner(strings.NewReader(string(numstatOutput)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		path := parts[2]

		additions := 0
		deletions := 0
		if parts[0] != "-" {
			additions, _ = strconv.Atoi(parts[0])
		}
		if parts[1] != "-" {
			deletions, _ = strconv.Atoi(parts[1])
		}
		numstatMap[path] = struct{ additions, deletions int }{additions, deletions}
	}

	// Build file list
	for path, status := range statusMap {
		sf := StagedFile{Path: path}
		if nums, ok := numstatMap[path]; ok {
			sf.Additions = nums.additions
			sf.Deletions = nums.deletions
		}
		switch status {
		case "A":
			sf.IsNew = true
		case "D":
			sf.IsDeleted = true
		case "M", "R", "C":
			sf.IsModified = true
		}
		files = append(files, sf)
	}

	return files, nil
}

// getUnstagedFiles returns files that have unstaged changes.
func (v *CommitView) getUnstagedFiles() ([]StagedFile, error) {
	var files []StagedFile

	// Get unstaged files with status
	cmd := exec.Command("git", "-C", v.repoPath, "diff", "--name-status")
	output, err := cmd.Output()
	if err != nil {
		// No unstaged files
		if strings.Contains(err.Error(), "exit status 1") && len(output) == 0 {
			return files, nil
		}
		return nil, err
	}

	statusMap := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		statusMap[parts[1]] = parts[0]
	}

	// Get numstat for additions/deletions
	numstatCmd := exec.Command("git", "-C", v.repoPath, "diff", "--numstat")
	numstatOutput, err := numstatCmd.Output()
	if err != nil {
		return nil, err
	}

	numstatMap := make(map[string]struct{ additions, deletions int })
	scanner = bufio.NewScanner(strings.NewReader(string(numstatOutput)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		path := parts[2]

		additions := 0
		deletions := 0
		if parts[0] != "-" {
			additions, _ = strconv.Atoi(parts[0])
		}
		if parts[1] != "-" {
			deletions, _ = strconv.Atoi(parts[1])
		}
		numstatMap[path] = struct{ additions, deletions int }{additions, deletions}
	}

	// Build file list
	for path, status := range statusMap {
		sf := StagedFile{Path: path}
		if nums, ok := numstatMap[path]; ok {
			sf.Additions = nums.additions
			sf.Deletions = nums.deletions
		}
		switch status {
		case "A":
			sf.IsNew = true
		case "D":
			sf.IsDeleted = true
		case "M", "R", "C":
			sf.IsModified = true
		}
		files = append(files, sf)
	}

	return files, nil
}

// stageFile stages a single file.
func (v *CommitView) stageFile(path string) error {
	cmd := exec.Command("git", "-C", v.repoPath, "add", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage file: %w", err)
	}
	return nil
}

// unstageFile unstages a single file.
func (v *CommitView) unstageFile(path string) error {
	cmd := exec.Command("git", "-C", v.repoPath, "restore", "--staged", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unstage file: %w", err)
	}
	return nil
}

// stageAll stages all unstaged files.
func (v *CommitView) stageAll() error {
	cmd := exec.Command("git", "-C", v.repoPath, "add", "-A")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage all files: %w", err)
	}
	return nil
}

// unstageAll unstages all staged files.
func (v *CommitView) unstageAll() error {
	cmd := exec.Command("git", "-C", v.repoPath, "restore", "--staged", ".")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unstage all files: %w", err)
	}
	return nil
}

// Update handles update events.
func (v *CommitView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle message editing mode
		if v.editMode == MessageEditMode {
			return v.handleMessageEdit(msg)
		}

		// Handle confirmation mode
		if v.editMode == ConfirmCommitMode {
			return v.handleConfirmMode(msg)
		}

		// Handle line-selection mode
		if v.editMode == LineDiffMode {
			return v.handleLineDiffMode(msg)
		}

		// Handle hunk diff preview mode
		if v.editMode == HunkDiffMode {
			return v.handleHunkDiffMode(msg)
		}

		// Stage edit mode - navigation and staging
		switch msg.String() {
		case "r":
			v.loading = true
			return v, func() tea.Msg {
				v.loadFiles()
				return RefreshDoneMsg{}
			}

		case "tab":
			// Switch between staged and unstaged sections
			if v.activeSection == 0 {
				v.activeSection = 1
			} else {
				v.activeSection = 0
			}
			v.selectedIndex = 0

		case "up", "k":
			if v.selectedIndex > 0 {
				v.selectedIndex--
			}

		case "down", "j":
			currentLen := len(v.unstagedFiles)
			if v.activeSection == 0 {
				currentLen = len(v.stagedFiles)
			}
			if v.selectedIndex < currentLen-1 {
				v.selectedIndex++
			}

		case " ":
			// Toggle stage/unstage selected file
			if v.activeSection == 0 && v.selectedIndex < len(v.stagedFiles) {
				// Unstage selected
				path := v.stagedFiles[v.selectedIndex].Path
				if err := v.unstageFile(path); err != nil {
					v.err = err
				}
				v.loadFiles()
				// Adjust selection if needed
				if v.selectedIndex >= len(v.stagedFiles) && v.selectedIndex > 0 {
					v.selectedIndex = len(v.stagedFiles) - 1
				}
			} else if v.activeSection == 1 && v.selectedIndex < len(v.unstagedFiles) {
				// Stage selected
				path := v.unstagedFiles[v.selectedIndex].Path
				if err := v.stageFile(path); err != nil {
					v.err = err
				}
				v.loadFiles()
				// Adjust selection if needed
				if v.selectedIndex >= len(v.unstagedFiles) && v.selectedIndex > 0 {
					v.selectedIndex = len(v.unstagedFiles) - 1
				}
			}

		case "a":
			// Stage all unstaged files
			if err := v.stageAll(); err != nil {
				v.err = err
			}
			v.loadFiles()
			v.activeSection = 0
			v.selectedIndex = 0

		case "u":
			// Unstage all staged files
			if err := v.unstageAll(); err != nil {
				v.err = err
			}
			v.loadFiles()
			v.activeSection = 1
			v.selectedIndex = 0

		case "enter":
			// Open hunk diff preview for the selected file
			v.openHunkDiff()

		case "c":
			// Enter message editing mode if there are staged files
			if len(v.stagedFiles) > 0 {
				v.editMode = MessageEditMode
				v.commitMessage = ""
				v.messageCursor = 0
			}
		}

	case RefreshDoneMsg:
		v.loading = false

	case AIGenDoneMsg:
		v.generating = false
		v.commitMessage = msg.Message
		v.messageCursor = len(v.commitMessage)

	case AIGenErrorMsg:
		v.generating = false
		v.err = fmt.Errorf("AI generation failed: %s", msg.Error)

	case CommitSuccessMsg:
		v.committing = false
		v.successMsg = fmt.Sprintf("Successfully committed: %s", msg.Message)
		v.commitMessage = ""
		v.messageCursor = 0
		v.editMode = StageEditMode
		v.loadFiles()
		// Switch to staged section and reset selection
		v.activeSection = 0
		v.selectedIndex = 0

	case CommitErrorMsg:
		v.committing = false
		v.err = fmt.Errorf("Commit failed: %s", msg.Error)
		v.editMode = MessageEditMode
	}

	return v, nil
}

// handleMessageEdit handles key events in message editing mode.
func (v *CommitView) handleMessageEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+g":
		// Generate AI commit message
		if !v.generating && len(v.stagedFiles) > 0 {
			v.generating = true
			return v, func() tea.Msg {
				suggestion, err := git.SuggestCommitMessage(v.repoPath)
				if err != nil {
					return AIGenErrorMsg{err.Error()}
				}
				return AIGenDoneMsg{suggestion}
			}
		}
		return v, nil

	case "ctrl+s", "ctrl+enter":
		// Request commit with confirmation if message is not empty
		if len(v.commitMessage) > 0 && len(v.stagedFiles) > 0 {
			v.editMode = ConfirmCommitMode
			v.confirmPending = true
		}
		return v, nil

	case "esc":
		// Exit message editing mode without committing
		v.editMode = StageEditMode
		return v, nil

	case "enter":
		// Add newline to message
		v.commitMessage += "\n"
		v.messageCursor = len(v.commitMessage)
		return v, nil

	case "backspace":
		if v.messageCursor > 0 {
			// Handle multi-line deletion
			if v.messageCursor > 0 && v.commitMessage[v.messageCursor-1] == '\n' {
				v.commitMessage = v.commitMessage[:v.messageCursor-1] + v.commitMessage[v.messageCursor:]
			} else if v.messageCursor > 0 {
				v.commitMessage = v.commitMessage[:v.messageCursor-1] + v.commitMessage[v.messageCursor:]
			}
			v.messageCursor--
		}
		return v, nil

	case "ctrl+w":
		v.commitMessage, v.messageCursor = components.DeleteWord(v.commitMessage, v.messageCursor)
		return v, nil

	case "left":
		if v.messageCursor > 0 {
			v.messageCursor--
		}
		return v, nil

	case "right":
		if v.messageCursor < len(v.commitMessage) {
			v.messageCursor++
		}
		return v, nil

	case "home":
		// Move to start of current line
		for v.messageCursor > 0 && v.commitMessage[v.messageCursor-1] != '\n' {
			v.messageCursor--
		}
		return v, nil

	case "end":
		// Move to end of current line
		for v.messageCursor < len(v.commitMessage) && v.commitMessage[v.messageCursor] != '\n' {
			v.messageCursor++
		}
		return v, nil

	default:
		// Handle text input
		if msg.Paste && len(msg.Runes) > 0 {
			pasted := string(msg.Runes)
			before := v.commitMessage[:v.messageCursor]
			after := v.commitMessage[v.messageCursor:]
			v.commitMessage = before + pasted + after
			v.messageCursor += len([]rune(pasted))
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 && r <= 126 {
				// Insert character at cursor position
				before := v.commitMessage[:v.messageCursor]
				after := v.commitMessage[v.messageCursor:]
				v.commitMessage = before + string(r) + after
				v.messageCursor++
			}
		}
	}
	return v, nil
}

// handleConfirmMode handles key events in confirmation mode.
func (v *CommitView) handleConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		// Confirm commit
		v.committing = true
		v.confirmPending = false
		return v, func() tea.Msg {
			err := v.executeCommit()
			if err != nil {
				return CommitErrorMsg{err.Error()}
			}
			return CommitSuccessMsg{v.commitMessage}
		}

	case "n", "N", "esc":
		// Cancel commit
		v.editMode = MessageEditMode
		v.confirmPending = false
		return v, nil

	case "enter":
		// Confirm commit (same as y)
		v.committing = true
		v.confirmPending = false
		return v, func() tea.Msg {
			err := v.executeCommit()
			if err != nil {
				return CommitErrorMsg{err.Error()}
			}
			return CommitSuccessMsg{v.commitMessage}
		}
	}
	return v, nil
}

// executeCommit performs the actual git commit.
func (v *CommitView) executeCommit() error {
	// Use git commit with message
	cmd := exec.Command("git", "-C", v.repoPath, "commit", "-m", v.commitMessage)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	return nil
}

// AIGenDoneMsg is sent when AI generation completes
type AIGenDoneMsg struct {
	Message string
}

// AIGenErrorMsg is sent when AI generation fails
type AIGenErrorMsg struct {
	Error string
}

// CommitSuccessMsg is sent when commit succeeds
type CommitSuccessMsg struct {
	Message string
}

// CommitErrorMsg is sent when commit fails
type CommitErrorMsg struct {
	Error string
}

// openHunkDiff loads the diff for the currently selected file and enters hunk diff mode.
func (v *CommitView) openHunkDiff() {
	var filePath string
	var rawDiff string
	var err error
	isStaged := false

	if v.activeSection == 0 && v.selectedIndex < len(v.stagedFiles) {
		filePath = v.stagedFiles[v.selectedIndex].Path
		rawDiff, err = git.GetStagedFileDiff(v.repoPath, filePath)
		isStaged = true
	} else if v.activeSection == 1 && v.selectedIndex < len(v.unstagedFiles) {
		filePath = v.unstagedFiles[v.selectedIndex].Path
		rawDiff, err = git.GetUnstagedFileDiff(v.repoPath, filePath)
		isStaged = false
	} else {
		return
	}

	if err != nil {
		v.err = err
		return
	}
	if rawDiff == "" {
		return
	}

	hunks := git.ParseHunks(rawDiff)
	if len(hunks) == 0 {
		return
	}

	v.hunkDiffRaw = rawDiff
	v.hunkList = hunks
	v.hunkIndex = 0
	v.hunkScrollOff = 0
	v.hunkIsStaged = isStaged
	v.hunkFilePath = filePath
	v.editMode = HunkDiffMode
}

// handleHunkDiffMode handles key events in hunk diff preview mode.
func (v *CommitView) handleHunkDiffMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Return to file list
		v.editMode = StageEditMode
		v.hunkList = nil
		v.hunkDiffRaw = ""
		v.hunkFilePath = ""
		return v, nil

	case "up", "k":
		// Move to previous hunk
		if v.hunkIndex > 0 {
			v.hunkIndex--
			v.hunkScrollOff = 0
		}
		return v, nil

	case "down", "j":
		// Move to next hunk
		if v.hunkIndex < len(v.hunkList)-1 {
			v.hunkIndex++
			v.hunkScrollOff = 0
		}
		return v, nil

	case " ":
		// Stage or unstage the selected hunk
		if v.hunkIndex >= len(v.hunkList) {
			return v, nil
		}
		hunk := v.hunkList[v.hunkIndex]

		if v.hunkIsStaged {
			// Unstage this hunk
			if err := git.UnstageHunk(v.repoPath, v.hunkFilePath, hunk); err != nil {
				v.err = err
				return v, nil
			}
		} else {
			// Stage this hunk
			if err := git.StageHunk(v.repoPath, v.hunkFilePath, hunk); err != nil {
				v.err = err
				return v, nil
			}
		}

		// Refresh the diff and files after staging/unstaging
		v.loadFiles()
		v.refreshHunkDiff()
		return v, nil

	case "l":
		// Enter line-selection mode for the current hunk
		if v.hunkIndex < len(v.hunkList) {
			v.enterLineMode()
		}
		return v, nil
	}

	return v, nil
}

// enterLineMode transitions from HunkDiffMode to LineDiffMode for the current hunk.
func (v *CommitView) enterLineMode() {
	hunk := v.hunkList[v.hunkIndex]
	v.lineSelected = make(map[int]bool)
	v.lineVisual = false
	v.lineVisualStart = 0

	// Position cursor on the first selectable (+ or -) line.
	v.lineCursor = 0
	for i, line := range hunk.Lines {
		if line.LineType == "+" || line.LineType == "-" {
			v.lineCursor = i
			break
		}
	}
	v.editMode = LineDiffMode
}

// handleLineDiffMode handles key events in line-selection mode.
func (v *CommitView) handleLineDiffMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	hunk := v.hunkList[v.hunkIndex]

	switch msg.String() {
	case "esc":
		// Return to hunk diff mode.
		v.editMode = HunkDiffMode
		v.lineSelected = nil
		v.lineVisual = false
		return v, nil

	case "up", "k":
		// Move cursor up to previous selectable line.
		for i := v.lineCursor - 1; i >= 0; i-- {
			if hunk.Lines[i].LineType == "+" || hunk.Lines[i].LineType == "-" {
				v.lineCursor = i
				break
			}
		}
		if v.lineVisual {
			v.applyVisualSelection(hunk)
		}
		return v, nil

	case "down", "j":
		// Move cursor down to next selectable line.
		for i := v.lineCursor + 1; i < len(hunk.Lines); i++ {
			if hunk.Lines[i].LineType == "+" || hunk.Lines[i].LineType == "-" {
				v.lineCursor = i
				break
			}
		}
		if v.lineVisual {
			v.applyVisualSelection(hunk)
		}
		return v, nil

	case "v":
		// Toggle visual selection mode.
		if v.lineVisual {
			v.lineVisual = false
		} else {
			v.lineVisual = true
			v.lineVisualStart = v.lineCursor
			// Select the anchor line.
			v.applyVisualSelection(hunk)
		}
		return v, nil

	case "x":
		// Toggle the line under the cursor.
		line := hunk.Lines[v.lineCursor]
		if line.LineType == "+" || line.LineType == "-" {
			if v.lineSelected[v.lineCursor] {
				delete(v.lineSelected, v.lineCursor)
			} else {
				v.lineSelected[v.lineCursor] = true
			}
		}
		return v, nil

	case " ":
		// Apply: stage or unstage the selected lines.
		indices := v.getSelectedLineIndices(hunk)
		if len(indices) == 0 {
			return v, nil
		}

		if v.hunkIsStaged {
			if err := git.UnstageLines(v.repoPath, v.hunkFilePath, hunk, indices); err != nil {
				v.err = err
				return v, nil
			}
		} else {
			if err := git.StageLines(v.repoPath, v.hunkFilePath, hunk, indices); err != nil {
				v.err = err
				return v, nil
			}
		}

		// Refresh and return to hunk mode.
		v.loadFiles()
		v.refreshHunkDiff()
		v.editMode = HunkDiffMode
		v.lineSelected = nil
		v.lineVisual = false
		return v, nil
	}

	return v, nil
}

// applyVisualSelection selects all selectable lines between the visual anchor and cursor.
func (v *CommitView) applyVisualSelection(hunk git.DiffHunk) {
	lo, hi := v.lineVisualStart, v.lineCursor
	if lo > hi {
		lo, hi = hi, lo
	}
	// Clear previous visual selection and reapply.
	v.lineSelected = make(map[int]bool)
	for i := lo; i <= hi; i++ {
		if hunk.Lines[i].LineType == "+" || hunk.Lines[i].LineType == "-" {
			v.lineSelected[i] = true
		}
	}
}

// getSelectedLineIndices returns the sorted list of selected line indices.
func (v *CommitView) getSelectedLineIndices(hunk git.DiffHunk) []int {
	var indices []int
	for i := 0; i < len(hunk.Lines); i++ {
		if v.lineSelected[i] {
			indices = append(indices, i)
		}
	}
	return indices
}

// refreshHunkDiff reloads the diff for the current file and updates the hunk list.
// If no hunks remain, returns to the file list.
func (v *CommitView) refreshHunkDiff() {
	var rawDiff string
	var err error

	if v.hunkIsStaged {
		rawDiff, err = git.GetStagedFileDiff(v.repoPath, v.hunkFilePath)
	} else {
		rawDiff, err = git.GetUnstagedFileDiff(v.repoPath, v.hunkFilePath)
	}

	if err != nil || rawDiff == "" {
		// No more diff for this file -- return to file list
		v.editMode = StageEditMode
		v.hunkList = nil
		v.hunkDiffRaw = ""
		v.hunkFilePath = ""
		return
	}

	hunks := git.ParseHunks(rawDiff)
	if len(hunks) == 0 {
		v.editMode = StageEditMode
		v.hunkList = nil
		v.hunkDiffRaw = ""
		v.hunkFilePath = ""
		return
	}

	v.hunkDiffRaw = rawDiff
	v.hunkList = hunks
	// Keep hunk index in bounds
	if v.hunkIndex >= len(hunks) {
		v.hunkIndex = len(hunks) - 1
	}
	v.hunkScrollOff = 0
}

// View renders the commit view.
func (v *CommitView) View() string {
	th := theme.GetTheme()

	var s strings.Builder

	// Header
	s.WriteString(th.DashboardTitle.Render(" Commit Composer "))
	s.WriteString("\n\n")

	if v.loading {
		s.WriteString(th.StatsStyle.Render(" Loading..."))
		s.WriteString("\n")
		return s.String()
	}

	// Error display
	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Error: %v", v.err)))
		s.WriteString("\n\n")
	}

	// Success message display
	if v.successMsg != "" {
		s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf(" ✓ %s", v.successMsg)))
		s.WriteString("\n\n")
		v.successMsg = "" // Clear after displaying
	}

	// Handle message editing mode
	if v.editMode == MessageEditMode {
		return v.renderMessageEditor(&s, th)
	}

	// Handle confirmation mode
	if v.editMode == ConfirmCommitMode {
		return v.renderConfirmDialog(&s, th)
	}

	// Handle line-selection mode
	if v.editMode == LineDiffMode {
		return v.renderLineDiffView(&s, th)
	}

	// Handle hunk diff preview mode
	if v.editMode == HunkDiffMode {
		return v.renderHunkDiffView(&s, th)
	}

	// Render staging area view (original content)
	return v.renderStagingView(&s, th)
}

// renderStagingView renders the staging area view
func (v *CommitView) renderStagingView(s *strings.Builder, th theme.Theme) string {
	// Calculate summary stats
	stagedCount := len(v.stagedFiles)
	unstagedCount := len(v.unstagedFiles)
	var stagedAdditions, stagedDeletions, unstagedAdditions, unstagedDeletions int

	for _, f := range v.stagedFiles {
		stagedAdditions += f.Additions
		stagedDeletions += f.Deletions
	}
	for _, f := range v.unstagedFiles {
		unstagedAdditions += f.Additions
		unstagedDeletions += f.Deletions
	}

	// Summary line
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	if stagedCount > 0 {
		s.WriteString(fmt.Sprintf(" %s %s files  %s +%s  %s -%s\n",
			th.DashboardAccentStyle.Render("Staged:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", stagedCount)),
			th.DashboardAccentStyle.Render("additions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", stagedAdditions)),
			th.DashboardErrorStyle.Render("deletions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", stagedDeletions))))
	} else {
		s.WriteString(fmt.Sprintf(" %s %s files\n",
			th.DashboardAccentStyle.Render("Staged:"),
			th.StatsStyle.Render("0")))
	}

	if unstagedCount > 0 {
		s.WriteString(fmt.Sprintf(" %s %s files  %s +%s  %s -%s\n",
			th.DashboardErrorStyle.Render("Unstaged:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", unstagedCount)),
			th.DashboardAccentStyle.Render("additions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", unstagedAdditions)),
			th.DashboardErrorStyle.Render("deletions:"),
			th.StatsStyle.Render(fmt.Sprintf("%d", unstagedDeletions))))
	} else {
		s.WriteString(fmt.Sprintf(" %s %s files\n",
			th.DashboardErrorStyle.Render("Unstaged:"),
			th.StatsStyle.Render("0")))
	}

	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n\n")

	// Staged section
	s.WriteString(th.StatsStyle.Render(" Staged Changes "))
	s.WriteString(" (press u to unstage all)\n")
	if stagedCount == 0 {
		s.WriteString(th.Help.Render(" No staged changes - use Space to stage files"))
	} else {
		for i, f := range v.stagedFiles {
			selected := v.activeSection == 0 && i == v.selectedIndex
			prefix := "  "
			if selected {
				prefix = " >"
			}

			statusStr := "M"
			if f.IsNew {
				statusStr = "A"
			} else if f.IsDeleted {
				statusStr = "D"
			}

			statusStyle := th.DashboardAccentStyle
			if f.IsDeleted {
				statusStyle = th.DashboardErrorStyle
			}

			line := fmt.Sprintf("%s[%s] %s", prefix, statusStyle.Render(statusStr), th.StatsStyle.Render(f.Path))

			// Show additions/deletions for modified files
			if f.Additions > 0 || f.Deletions > 0 {
				line += fmt.Sprintf(" %s+%d %s-%d",
					th.DashboardAccentStyle.Render(""),
					f.Additions,
					th.DashboardErrorStyle.Render(""),
					f.Deletions)
			}

			s.WriteString(line + "\n")
		}
	}

	s.WriteString("\n")

	// Unstaged section
	s.WriteString(th.DashboardErrorStyle.Render(" Unstaged Changes "))
	s.WriteString(" (press a to stage all)\n")
	if unstagedCount == 0 {
		s.WriteString(th.Help.Render(" No unstaged changes - working tree is clean"))
	} else {
		for i, f := range v.unstagedFiles {
			selected := v.activeSection == 1 && i == v.selectedIndex
			prefix := "  "
			if selected {
				prefix = " >"
			}

			statusStr := "M"
			if f.IsNew {
				statusStr = "?"
			} else if f.IsDeleted {
				statusStr = "D"
			}

			statusStyle := th.DashboardErrorStyle
			if f.IsNew {
				statusStyle = th.DashboardAccentStyle
			}

			line := fmt.Sprintf("%s[%s] %s", prefix, statusStyle.Render(statusStr), th.StatsStyle.Render(f.Path))

			// Show additions/deletions for modified files
			if f.Additions > 0 || f.Deletions > 0 {
				line += fmt.Sprintf(" %s+%d %s-%d",
					th.DashboardAccentStyle.Render(""),
					f.Additions,
					th.DashboardErrorStyle.Render(""),
					f.Deletions)
			}

			s.WriteString(line + "\n")
		}
	}

	s.WriteString("\n")

	// Help footer
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	if stagedCount > 0 {
		s.WriteString(th.Help.Render(" Space: Stage/Unstage   Enter: Hunk diff   a: Stage all   u: Unstage all   Tab: Switch   c: Commit   r: Refresh "))
	} else {
		s.WriteString(th.Help.Render(" Space: Stage/Unstage   Enter: Hunk diff   a: Stage all   u: Unstage all   Tab: Switch   r: Refresh "))
	}

	return s.String()
}

// renderMessageEditor renders the message editor view
func (v *CommitView) renderMessageEditor(s *strings.Builder, th theme.Theme) string {
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.DashboardAccentStyle.Render(" Write Commit Message "))
	s.WriteString("\n\n")

	// Show AI generation status
	if v.generating {
		s.WriteString(th.Help.Render(" Generating AI commit message...\n"))
	} else {
		s.WriteString(th.Help.Render(" Ctrl+G: Generate AI message\n"))
	}

	// Message input area
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	// Render message with cursor
	messageHeight := 6
	lines := strings.Split(v.commitMessage, "\n")
	for i := 0; i < messageHeight; i++ {
		if i < len(lines) {
			line := lines[i]
			s.WriteString(th.StatsStyle.Render("| " + line))
			// Show cursor on current line
			if i == v.getCurrentLineIndex() {
				// Count characters to cursor position
				cursorPos := v.messageCursor - v.getLineStart()
				lineSoFar := line[:min(cursorPos, len(line))]
				s.WriteString(th.Help.Render(lineSoFar))
				s.WriteString(th.DashboardAccentStyle.Render("█"))
				if cursorPos < len(line) {
					s.WriteString(th.Help.Render(line[cursorPos:]))
				}
			} else {
				s.WriteString("\n")
			}
		} else {
			// Empty line with blinking cursor on current line
			if i == v.getCurrentLineIndex() {
				s.WriteString(th.StatsStyle.Render("| "))
				s.WriteString(th.DashboardAccentStyle.Render("█"))
			} else {
				s.WriteString(th.StatsStyle.Render("| "))
			}
			s.WriteString("\n")
		}
	}

	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n\n")

	// Conventional commit hint
	s.WriteString(th.Help.Render(" Conventional format: type: description (e.g., feat: add new feature)\n"))
	s.WriteString(th.Help.Render(" Hint: Press Ctrl+G to generate an AI commit message\n\n"))

	// Status
	lineCount := len(lines)
	charCount := len(v.commitMessage)
	s.WriteString(fmt.Sprintf(" %s %d lines  %s %d chars\n",
		th.StatsStyle.Render("Lines:"),
		lineCount,
		th.StatsStyle.Render("Characters:"),
		charCount))

	s.WriteString("\n")

	// Help footer for message editing mode
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" Enter: New line   Ctrl+G: AI generate   Ctrl+S: Commit   Esc: Cancel "))

	return s.String()
}

// renderConfirmDialog renders the commit confirmation dialog
func (v *CommitView) renderConfirmDialog(s *strings.Builder, th theme.Theme) string {
	s.WriteString(th.StatsStyle.Render(" ═══════════════════════════════════════════════════════ "))
	s.WriteString("\n\n")

	s.WriteString(th.DashboardTitle.Render(" Confirm Commit "))
	s.WriteString("\n\n")

	// Show commit message preview
	s.WriteString(th.StatsStyle.Render(" Commit message:\n"))
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	// Word wrap the message for display
	lines := strings.Split(v.commitMessage, "\n")
	for _, line := range lines {
		if len(line) > 60 {
			// Word wrap
			words := strings.Fields(line)
			currentLen := 0
			for _, word := range words {
				if currentLen+len(word)+1 > 60 {
					s.WriteString("\n")
					currentLen = 0
				}
				s.WriteString(word + " ")
				currentLen += len(word) + 1
			}
			s.WriteString("\n")
		} else {
			s.WriteString(th.Help.Render(line + "\n"))
		}
	}

	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n\n")

	// File count
	stagedCount := len(v.stagedFiles)
	s.WriteString(fmt.Sprintf(" %s %d file(s)\n",
		th.DashboardAccentStyle.Render("Will commit:"),
		stagedCount))

	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ═══════════════════════════════════════════════════════ "))
	s.WriteString("\n\n")

	// Confirmation prompt
	s.WriteString(th.DashboardAccentStyle.Render(" Commit this change? [Y/n] "))

	return s.String()
}

// renderHunkDiffView renders the hunk diff preview with selectable hunks
func (v *CommitView) renderHunkDiffView(s *strings.Builder, th theme.Theme) string {
	// Title
	sectionLabel := "unstaged"
	if v.hunkIsStaged {
		sectionLabel = "staged"
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Hunk Diff: %s (%s) ", v.hunkFilePath, sectionLabel)))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	s.WriteString(fmt.Sprintf(" Hunk %s of %s\n",
		th.DashboardAccentStyle.Render(fmt.Sprintf("%d", v.hunkIndex+1)),
		th.StatsStyle.Render(fmt.Sprintf("%d", len(v.hunkList)))))
	s.WriteString("\n")

	// Render all hunks with the selected one highlighted
	maxLines := v.height - 10
	if maxLines < 10 {
		maxLines = 10
	}
	linesRendered := 0

	for hIdx, hunk := range v.hunkList {
		if linesRendered >= maxLines {
			break
		}

		isSelected := hIdx == v.hunkIndex

		// Hunk header line
		headerPrefix := "  "
		if isSelected {
			headerPrefix = "> "
		}

		action := "Space: stage"
		if v.hunkIsStaged {
			action = "Space: unstage"
		}

		headerLine := fmt.Sprintf("%s%s", headerPrefix, hunk.Header)
		if isSelected {
			s.WriteString(th.DashboardAccentStyle.Render(headerLine))
			s.WriteString(th.Help.Render(fmt.Sprintf("  [%s]", action)))
		} else {
			s.WriteString(th.InfoStyle.Render(headerLine))
		}
		s.WriteString("\n")
		linesRendered++

		// Render hunk lines
		for _, line := range hunk.Lines {
			if linesRendered >= maxLines {
				remaining := len(hunk.Lines) - linesRendered
				if remaining > 0 {
					s.WriteString(th.Help.Render(fmt.Sprintf("    ... %d more lines", remaining)))
					s.WriteString("\n")
				}
				break
			}

			linePrefix := "    "
			if isSelected {
				linePrefix = "  | "
			}

			content := line.Content
			// Truncate long lines
			displayWidth := v.width - 8
			if displayWidth < 20 {
				displayWidth = 20
			}
			if len(content) > displayWidth {
				content = content[:displayWidth-3] + "..."
			}

			switch line.LineType {
			case "+":
				s.WriteString(th.DashboardAccentStyle.Render(linePrefix + content))
			case "-":
				s.WriteString(th.DashboardErrorStyle.Render(linePrefix + content))
			case " ":
				s.WriteString(th.Help.Render(linePrefix + content))
			case "\\":
				s.WriteString(th.Help.Render(linePrefix + content))
			}
			s.WriteString("\n")
			linesRendered++
		}

		// Separator between hunks
		if hIdx < len(v.hunkList)-1 && linesRendered < maxLines {
			s.WriteString(th.StatsStyle.Render("  ---"))
			s.WriteString("\n")
			linesRendered++
		}
	}

	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" Up/Down: Select hunk   Space: Stage/Unstage hunk   l: Line select   Esc: Back to file list "))

	return s.String()
}

// renderLineDiffView renders the line-selection view for the current hunk.
func (v *CommitView) renderLineDiffView(s *strings.Builder, th theme.Theme) string {
	sectionLabel := "unstaged"
	if v.hunkIsStaged {
		sectionLabel = "staged"
	}
	s.WriteString(th.DashboardTitle.Render(fmt.Sprintf(" Line Select: %s (%s) ", v.hunkFilePath, sectionLabel)))
	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	hunk := v.hunkList[v.hunkIndex]
	s.WriteString(fmt.Sprintf(" Hunk %s of %s  |  ",
		th.DashboardAccentStyle.Render(fmt.Sprintf("%d", v.hunkIndex+1)),
		th.StatsStyle.Render(fmt.Sprintf("%d", len(v.hunkList)))))

	selectedCount := len(v.lineSelected)
	s.WriteString(fmt.Sprintf("%s lines selected", th.DashboardAccentStyle.Render(fmt.Sprintf("%d", selectedCount))))
	if v.lineVisual {
		s.WriteString(th.DashboardErrorStyle.Render("  [VISUAL]"))
	}
	s.WriteString("\n\n")

	// Hunk header
	s.WriteString(th.InfoStyle.Render("  " + hunk.Header))
	s.WriteString("\n")

	// Render hunk lines with selection indicators.
	maxLines := v.height - 12
	if maxLines < 10 {
		maxLines = 10
	}

	for i, line := range hunk.Lines {
		if i >= maxLines {
			remaining := len(hunk.Lines) - i
			if remaining > 0 {
				s.WriteString(th.Help.Render(fmt.Sprintf("    ... %d more lines", remaining)))
				s.WriteString("\n")
			}
			break
		}

		isCursor := i == v.lineCursor
		isSelected := v.lineSelected[i]
		isSelectable := line.LineType == "+" || line.LineType == "-"

		// Build prefix: cursor indicator + selection marker
		cursor := " "
		if isCursor {
			cursor = ">"
		}
		sel := " "
		if isSelected {
			sel = "*"
		} else if isSelectable {
			sel = "."
		}
		prefix := cursor + sel + " "

		content := line.Content
		// Truncate long lines.
		displayWidth := v.width - 8
		if displayWidth < 20 {
			displayWidth = 20
		}
		if len(content) > displayWidth {
			content = content[:displayWidth-3] + "..."
		}

		switch line.LineType {
		case "+":
			if isSelected || isCursor {
				s.WriteString(th.DashboardAccentStyle.Render(prefix + content))
			} else {
				s.WriteString(th.DashboardAccentStyle.Render("   " + content))
			}
		case "-":
			if isSelected || isCursor {
				s.WriteString(th.DashboardErrorStyle.Render(prefix + content))
			} else {
				s.WriteString(th.DashboardErrorStyle.Render("   " + content))
			}
		case " ":
			s.WriteString(th.Help.Render("   " + content))
		case "\\":
			s.WriteString(th.Help.Render("   " + content))
		}
		s.WriteString("\n")
	}

	s.WriteString("\n")
	s.WriteString(th.StatsStyle.Render(" ──────────────────────────────────────────────── "))
	s.WriteString("\n")

	action := "stage"
	if v.hunkIsStaged {
		action = "unstage"
	}
	s.WriteString(th.Help.Render(fmt.Sprintf(" j/k: Move   x: Toggle line   v: Visual select   Space: %s selected   Esc: Back ", action)))

	return s.String()
}

// getCurrentLineIndex returns the current line index based on cursor position
func (v *CommitView) getCurrentLineIndex() int {
	if v.commitMessage == "" {
		return 0
	}
	lineIndex := 0
	for i := 0; i < v.messageCursor && i < len(v.commitMessage); i++ {
		if v.commitMessage[i] == '\n' {
			lineIndex++
		}
	}
	return lineIndex
}

// getLineStart returns the cursor position at the start of the current line
func (v *CommitView) getLineStart() int {
	for i := v.messageCursor - 1; i >= 0; i-- {
		if v.commitMessage[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// ShortHelp returns a short help string.
func (v *CommitView) ShortHelp() string {
	if v.editMode == MessageEditMode {
		return "Enter: New line  Ctrl+G: AI generate  Ctrl+S: Commit  Esc: Cancel"
	}
	if v.editMode == ConfirmCommitMode {
		return "Y/n: Confirm/Cancel commit"
	}
	if v.editMode == LineDiffMode {
		return "j/k: Move  x: Toggle  v: Visual  Space: Apply  Esc: Back to hunk"
	}
	if v.editMode == HunkDiffMode {
		return "Up/Down: Select hunk  Space: Stage/Unstage hunk  l: Line mode  Esc: Back"
	}
	return "Space: Toggle  Enter: Hunk diff  a/u: Stage/Unstage all  Tab: Switch  c: Commit"
}

// SetSize updates the view dimensions.
func (v *CommitView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// GetRepoPath returns the repository path.
func (v *CommitView) GetRepoPath() string {
	return v.repoPath
}

// Refresh reloads file data.
func (v *CommitView) Refresh() error {
	v.loadFiles()
	return v.err
}

// KeyBindings returns the keybindings for this view.
func (v *CommitView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "r", Description: "Refresh staging area"},
		{Key: "Tab", Description: "Switch between staged/unstaged sections"},
		{Key: "↑/k", Description: "Navigate up"},
		{Key: "↓/j", Description: "Navigate down"},
		{Key: "Space", Description: "Stage/unstage selected file"},
		{Key: "Enter", Description: "View file diff with hunk navigation"},
		{Key: "l", Description: "Enter line-selection mode (in hunk view)"},
		{Key: "x", Description: "Toggle line (in line mode)"},
		{Key: "v", Description: "Visual range select (in line mode)"},
		{Key: "a", Description: "Stage all files"},
		{Key: "u", Description: "Unstage all files"},
		{Key: "c", Description: "Write commit message (when files staged)"},
		{Key: "Esc", Description: "Cancel / Go back"},
	}
}
