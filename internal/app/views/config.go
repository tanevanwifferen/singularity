package views

import (
	"fmt"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/config"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// ConfigSavedMsg is sent when the config has been saved successfully.
type ConfigSavedMsg struct{}

// configFieldKind is the type of a config field.
type configFieldKind int

const (
	configFieldText   configFieldKind = iota
	configFieldMasked                 // password / token — displayed as ****
	configFieldBool
)

// configField describes one editable field in the config form.
type configField struct {
	label string
	kind  configFieldKind
	get   func(*config.Config) string
	set   func(*config.Config, string)
}

// configTab groups related fields under a tab header.
type configTab struct {
	name   string
	fields []configField
}

// ConfigView is a TUI form for editing application settings.
type ConfigView struct {
	cfg    *config.Config
	width  int
	height int

	tabs     []configTab
	tabIdx   int // active tab (0-based)
	fieldIdx int // active field within tab

	editing bool   // whether the cursor is inside a field input
	editBuf string // edit buffer for current field

	statusMsg string
	errMsg    string
}

// NewConfigView creates a new config editing view.
func NewConfigView(cfg *config.Config) *ConfigView {
	v := &ConfigView{
		cfg:    cfg,
		width:  80,
		height: 24,
	}
	v.tabs = v.buildTabs()
	return v
}

func (v *ConfigView) buildTabs() []configTab {
	return []configTab{
		{
			name: "Jira",
			fields: []configField{
				{
					label: "Base URL",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.Jira.BaseURL },
					set:   func(c *config.Config, val string) { c.Jira.BaseURL = val; c.Jira.Enabled = val != "" },
				},
				{
					label: "Email",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.Jira.Email },
					set:   func(c *config.Config, val string) { c.Jira.Email = val },
				},
				{
					label: "API Token",
					kind:  configFieldMasked,
					get:   func(c *config.Config) string { return c.Jira.APIToken },
					set:   func(c *config.Config, val string) { c.Jira.APIToken = val },
				},
				{
					label: "Default Project",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.Jira.DefaultProject },
					set:   func(c *config.Config, val string) { c.Jira.DefaultProject = val },
				},
			},
		},
		{
			name: "AI",
			fields: []configField{
				{
					label: "Provider",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.AI.Provider },
					set:   func(c *config.Config, val string) { c.AI.Provider = val },
				},
				{
					label: "Model",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.AI.Model },
					set:   func(c *config.Config, val string) { c.AI.Model = val },
				},
				{
					label: "Commit Style",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.AI.CommitStyle },
					set:   func(c *config.Config, val string) { c.AI.CommitStyle = val },
				},
			},
		},
		{
			name: "Git",
			fields: []configField{
				{
					label: "Default Branch",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.Git.DefaultBranch },
					set:   func(c *config.Config, val string) { c.Git.DefaultBranch = val },
				},
				{
					label: "Fetch Interval (s)",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return fmt.Sprintf("%d", c.Git.FetchInterval) },
					set: func(c *config.Config, val string) {
						var n int
						fmt.Sscanf(val, "%d", &n)
						if n > 0 {
							c.Git.FetchInterval = n
						}
					},
				},
			},
		},
		{
			name: "Forge",
			fields: []configField{
				{
					label: "Default Host",
					kind:  configFieldText,
					get:   func(c *config.Config) string { return c.Forge.DefaultHost },
					set:   func(c *config.Config, val string) { c.Forge.DefaultHost = val },
				},
				{
					label: "Token",
					kind:  configFieldMasked,
					get:   func(c *config.Config) string { return c.Forge.Token },
					set:   func(c *config.Config, val string) { c.Forge.Token = val },
				},
			},
		},
	}
}

// Init initializes the config view.
func (v *ConfigView) Init() tea.Cmd {
	return nil
}

// activeTab returns the currently active tab.
func (v *ConfigView) activeTab() *configTab {
	if v.tabIdx >= 0 && v.tabIdx < len(v.tabs) {
		return &v.tabs[v.tabIdx]
	}
	return nil
}

// activeField returns the currently active field, or nil.
func (v *ConfigView) activeField() *configField {
	tab := v.activeTab()
	if tab == nil || v.fieldIdx < 0 || v.fieldIdx >= len(tab.fields) {
		return nil
	}
	return &tab.fields[v.fieldIdx]
}

// Update handles messages and input.
func (v *ConfigView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width = msg.Width
		v.height = msg.Height

	case tea.KeyMsg:
		// Editing a field
		if v.editing {
			return v, v.handleEditKey(msg)
		}
		return v, v.handleNavKey(msg)
	}
	return v, nil
}

// handleNavKey handles navigation when not editing.
func (v *ConfigView) handleNavKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "left", "h", "shift+tab":
		v.tabIdx = (v.tabIdx - 1 + len(v.tabs)) % len(v.tabs)
		v.fieldIdx = 0
		v.statusMsg = ""
		v.errMsg = ""

	case "right", "l", "tab":
		v.tabIdx = (v.tabIdx + 1) % len(v.tabs)
		v.fieldIdx = 0
		v.statusMsg = ""
		v.errMsg = ""

	case "up", "k":
		tab := v.activeTab()
		if tab != nil {
			v.fieldIdx = (v.fieldIdx - 1 + len(tab.fields)) % len(tab.fields)
		}

	case "down", "j":
		tab := v.activeTab()
		if tab != nil {
			v.fieldIdx = (v.fieldIdx + 1) % len(tab.fields)
		}

	case "enter", " ":
		field := v.activeField()
		if field != nil {
			v.editing = true
			v.editBuf = field.get(v.cfg)
		}

	case "s":
		return v.saveConfig()
	}
	return nil
}

// handleEditKey handles input while editing a field.
func (v *ConfigView) handleEditKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		// Commit edit
		field := v.activeField()
		if field != nil {
			field.set(v.cfg, v.editBuf)
		}
		v.editing = false
		v.editBuf = ""
		return v.saveConfig()

	case "esc":
		v.editing = false
		v.editBuf = ""

	case "backspace":
		if len(v.editBuf) > 0 {
			v.editBuf = v.editBuf[:len(v.editBuf)-1]
		}

	case "ctrl+w":
		v.editBuf = components.DeleteWordEnd(v.editBuf)

	default:
		if msg.Paste && len(msg.Runes) > 0 {
			v.editBuf += string(msg.Runes)
		} else if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 32 {
				v.editBuf += string(r)
			}
		}
	}
	return nil
}

// saveConfig persists the config to disk.
func (v *ConfigView) saveConfig() tea.Cmd {
	cfg := v.cfg
	return func() tea.Msg {
		if err := config.SaveDefaultConfig(cfg); err != nil {
			v.errMsg = fmt.Sprintf("Save failed: %v", err)
			return nil
		}
		v.statusMsg = "Config saved"
		return ConfigSavedMsg{}
	}
}

// CapturesInput returns true when the view is editing a field.
func (v *ConfigView) CapturesInput() bool {
	return v.editing
}

// CapturesKey returns true for keys the config view needs.
func (v *ConfigView) CapturesKey(key string) bool {
	// Capture tab so it cycles config tabs, not TUI views
	return key == "tab" || key == "shift+tab"
}

// SetSize updates the view dimensions.
func (v *ConfigView) SetSize(width, height int) {
	v.width = width
	v.height = height
}

// ShortHelp returns a short help string.
func (v *ConfigView) ShortHelp() string {
	if v.editing {
		return "Enter: confirm   Esc: cancel   Backspace: delete"
	}
	return "←/→/Tab: switch tab   ↑/↓/j/k: navigate   Enter: edit   s: save"
}

// KeyBindings returns the key bindings for help overlay.
func (v *ConfigView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "←/→", Description: "Switch config tab"},
		{Key: "↑/↓ / j/k", Description: "Navigate fields"},
		{Key: "Enter", Description: "Edit selected field"},
		{Key: "Esc", Description: "Cancel editing"},
		{Key: "s", Description: "Save config"},
	}
}

// View renders the config form.
func (v *ConfigView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	// Title
	s.WriteString(th.DashboardTitle.Render(" Settings "))
	s.WriteString("\n\n")

	// Tab bar
	for i, tab := range v.tabs {
		if i > 0 {
			s.WriteString("  ")
		}
		if i == v.tabIdx {
			s.WriteString(th.DashboardAccentStyle.Render(fmt.Sprintf("[ %s ]", tab.name)))
		} else {
			s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("  %s  ", tab.name)))
		}
	}
	s.WriteString("\n")
	s.WriteString(th.BorderStyle.Render(strings.Repeat("─", v.width-2)))
	s.WriteString("\n\n")

	// Fields for active tab
	tab := v.activeTab()
	if tab != nil {
		for i, field := range tab.fields {
			selected := i == v.fieldIdx

			prefix := "  "
			labelStyle := th.BranchStyle
			if selected {
				prefix = " >"
				labelStyle = th.SelectedBranchStyle
			}

			// Value display
			var valStr string
			if v.editing && selected {
				// Show edit buffer with cursor
				if field.kind == configFieldMasked {
					valStr = strings.Repeat("*", len(v.editBuf)) + "█"
				} else {
					valStr = v.editBuf + "█"
				}
			} else {
				raw := field.get(v.cfg)
				if field.kind == configFieldMasked && raw != "" {
					valStr = strings.Repeat("*", min(len(raw), 12))
				} else {
					valStr = raw
				}
				if valStr == "" {
					valStr = th.MutedTextStyle.Render("(not set)")
				}
			}

			s.WriteString(fmt.Sprintf("%s %-22s %s\n",
				labelStyle.Render(prefix),
				labelStyle.Render(field.label+":"),
				th.StatsStyle.Render(valStr),
			))
		}
	}

	s.WriteString("\n")

	// Status / error messages
	if v.errMsg != "" {
		s.WriteString(th.DashboardErrorStyle.Render(" " + v.errMsg + " "))
		s.WriteString("\n")
	} else if v.statusMsg != "" {
		s.WriteString(th.DashboardAccentStyle.Render(" " + v.statusMsg + " "))
		s.WriteString("\n")
	}

	s.WriteString("\n")
	s.WriteString(th.BorderStyle.Render(strings.Repeat("─", v.width-2)))
	s.WriteString("\n")
	s.WriteString(th.Help.Render(" ←/→: tab   ↑/↓: field   Enter: edit   Esc: cancel   s: save "))

	return s.String()
}

// min is a local helper for Go versions without built-in min.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
