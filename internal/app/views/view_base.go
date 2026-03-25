package views

// viewBase provides common fields and methods shared by all views.
// Embed this struct to get SetSize, GetRepoPath, and SetRepoPath for free.
type viewBase struct {
	width    int
	height   int
	repoPath string
}

// SetSize updates the view dimensions.
func (b *viewBase) SetSize(width, height int) {
	b.width = width
	b.height = height
}

// GetRepoPath returns the repository path.
func (b *viewBase) GetRepoPath() string {
	return b.repoPath
}

// SetRepoPath updates the repository path.
func (b *viewBase) SetRepoPath(path string) {
	b.repoPath = path
}
