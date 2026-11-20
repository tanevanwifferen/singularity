package views

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/app/components"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// questionsLoadedMsg carries one refresh pass: the questions in play and the
// summary of every agent, so a row can say who is asking.
type questionsLoadedMsg struct {
	questions []service.Question
	agents    map[string]string
	err       error
}

// questionAnsweredMsg is the result of submitting an answer.
type questionAnsweredMsg struct {
	question service.Question
	err      error
}

// QuestionsView lists what agents stopped to ask (their ask_questions tool)
// and takes the answers. An agent stays blocked until every question of its
// call is answered, so answered questions stay listed, ticked off, for as
// long as a sibling is still open.
type QuestionsView struct {
	viewBase

	questions []service.Question
	agents    map[string]string
	cursor    int
	// selectedID follows the selected question across refreshes, which
	// replace the whole list.
	selectedID string

	// answering is the input mode; target is the question it is for,
	// captured on open so a refresh landing behind the input cannot
	// retarget the answer at whatever the cursor has since moved to.
	answering bool
	target    service.Question
	input     components.TextInput

	loading   bool
	err       error
	statusMsg string
}

// NewQuestionsView creates the questions view.
func NewQuestionsView() *QuestionsView {
	return &QuestionsView{viewBase: viewBase{width: 80, height: 24}}
}

// Init loads the list.
func (v *QuestionsView) Init() tea.Cmd {
	cmd := v.RefreshCmd()
	v.loading = cmd != nil
	return cmd
}

// RefreshCmd fetches the questions and the agent summaries. The app runs it
// on the stream tick and on every agent event while this view is on screen:
// a question appearing or being answered always changes an agent's state.
func (v *QuestionsView) RefreshCmd() tea.Cmd {
	svc := v.services
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		questions, err := svc.Question.List(v.ctx())
		if err != nil {
			return questionsLoadedMsg{err: err}
		}
		agents := make(map[string]string)
		// The summaries are decoration: without them a row shows the id.
		if snaps, aerr := svc.Agent.List(v.ctx()); aerr == nil {
			for _, s := range snaps {
				agents[s.ID] = s.Summary
			}
		}
		return questionsLoadedMsg{questions: questions, agents: agents}
	}
}

// Refresh satisfies the app's synchronous refresh hook.
func (v *QuestionsView) Refresh() error {
	cmd := v.RefreshCmd()
	if cmd == nil {
		return service.ErrUnavailable
	}
	msg := cmd().(questionsLoadedMsg)
	v.applyQuestions(msg)
	return msg.err
}

// Update handles messages for the questions view.
func (v *QuestionsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v.handleKey(msg)
	case questionsLoadedMsg:
		v.applyQuestions(msg)
	case questionAnsweredMsg:
		return v, v.applyAnswered(msg)
	}
	return v, nil
}

func (v *QuestionsView) applyQuestions(msg questionsLoadedMsg) {
	v.loading = false
	v.err = msg.err
	if msg.err != nil {
		return
	}
	v.questions = msg.questions
	v.agents = msg.agents
	v.cursor = v.indexOf(v.selectedID, v.cursor)
	v.syncSelection()

	// The question being answered can vanish underneath the input: the
	// agent was killed, or withdrew it. Say so instead of sending an
	// answer nobody is waiting for.
	if v.answering && v.indexOf(v.target.ID, -1) < 0 {
		v.answering = false
		v.statusMsg = fmt.Sprintf("Question %s is gone — the agent stopped waiting for it", v.target.ID)
	}
}

func (v *QuestionsView) applyAnswered(msg questionAnsweredMsg) tea.Cmd {
	if msg.err != nil {
		v.statusMsg = fmt.Sprintf("Answer refused: %v", msg.err)
		return v.RefreshCmd()
	}
	for i := range v.questions {
		if v.questions[i].ID == msg.question.ID {
			v.questions[i] = msg.question
		}
	}
	// Move on to the next open question of the same call and keep the input
	// open: the agent only continues once they are all answered.
	if next := v.nextOpenInBatch(msg.question.BatchID); next >= 0 {
		v.cursor = next
		v.syncSelection()
		v.startAnswering()
		v.statusMsg = fmt.Sprintf("Answered %s — the agent still waits on this one", msg.question.ID)
	} else {
		v.answering = false
		v.statusMsg = fmt.Sprintf("Answered %s — all answered, the agent continues", msg.question.ID)
	}
	return v.RefreshCmd()
}

func (v *QuestionsView) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if v.answering {
		switch msg.String() {
		case "esc":
			v.answering = false
		case "enter":
			answer := strings.TrimSpace(v.input.Value)
			if answer == "" {
				v.statusMsg = "An answer cannot be empty"
				return v, nil
			}
			return v, v.answerCmd(v.target.ID, answer)
		default:
			v.input.HandleKey(msg)
		}
		return v, nil
	}

	switch msg.String() {
	case "j", "down":
		if v.cursor < len(v.questions)-1 {
			v.cursor++
			v.syncSelection()
		}
	case "k", "up":
		if v.cursor > 0 {
			v.cursor--
			v.syncSelection()
		}
	case "enter", "a":
		if q, ok := v.selected(); ok {
			if q.Answered {
				v.statusMsg = fmt.Sprintf("%s is already answered", q.ID)
			} else {
				v.startAnswering()
			}
		}
	case "o":
		if q, ok := v.selected(); ok {
			agentID := q.AgentID
			return v, func() tea.Msg { return OpenAgentMsg{AgentID: agentID} }
		}
	case "r":
		v.statusMsg = ""
		return v, v.RefreshCmd()
	}
	return v, nil
}

func (v *QuestionsView) answerCmd(questionID, answer string) tea.Cmd {
	svc := v.services
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		q, err := svc.Question.Answer(v.ctx(), questionID, answer)
		return questionAnsweredMsg{question: q, err: err}
	}
}

func (v *QuestionsView) startAnswering() {
	q, ok := v.selected()
	if !ok {
		return
	}
	v.answering = true
	v.target = q
	v.input.Clear()
	v.statusMsg = ""
}

func (v *QuestionsView) selected() (service.Question, bool) {
	if v.cursor < 0 || v.cursor >= len(v.questions) {
		return service.Question{}, false
	}
	return v.questions[v.cursor], true
}

// syncSelection clamps the cursor and records which question it is on.
func (v *QuestionsView) syncSelection() {
	if v.cursor >= len(v.questions) {
		v.cursor = len(v.questions) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
	v.selectedID = ""
	if q, ok := v.selected(); ok {
		v.selectedID = q.ID
	}
}

// indexOf returns the position of the question with that id, or fallback.
func (v *QuestionsView) indexOf(id string, fallback int) int {
	for i, q := range v.questions {
		if q.ID == id {
			return i
		}
	}
	return fallback
}

func (v *QuestionsView) nextOpenInBatch(batchID string) int {
	for i, q := range v.questions {
		if q.BatchID == batchID && !q.Answered {
			return i
		}
	}
	return -1
}

// OpenCount is the number of questions still waiting for an answer.
func (v *QuestionsView) OpenCount() int {
	n := 0
	for _, q := range v.questions {
		if !q.Answered {
			n++
		}
	}
	return n
}

// CapturesInput reports whether the answer input owns the keyboard.
func (v *QuestionsView) CapturesInput() bool { return v.answering }

// ShortHelp returns the view's one-line key reference.
func (v *QuestionsView) ShortHelp() string {
	if v.answering {
		return "enter:send answer  esc:cancel"
	}
	return "enter/a:answer  o:open agent  j/k:move  r:refresh"
}

// KeyBindings returns the view's bindings for the help overlay.
func (v *QuestionsView) KeyBindings() []components.KeyBinding {
	return []components.KeyBinding{
		{Key: "Enter / a", Description: "Answer the selected question"},
		{Key: "o", Description: "Open the agent that asked it"},
		{Key: "j / k", Description: "Move down / up"},
		{Key: "r", Description: "Refresh questions"},
	}
}

// View renders the question list, the selected question in full, and the
// answer input.
func (v *QuestionsView) View() string {
	th := theme.GetTheme()
	var s strings.Builder

	s.WriteString(th.DashboardTitle.Render(" Questions "))
	if n := v.OpenCount(); n > 0 {
		s.WriteString(th.WarningStyle.Render(fmt.Sprintf("  %d open", n)))
	}
	if v.loading {
		s.WriteString(th.MutedTextStyle.Render("  loading…"))
	}
	s.WriteString("\n\n")

	if v.err != nil {
		s.WriteString(th.DashboardErrorStyle.Render(fmt.Sprintf(" Could not load questions: %v", v.err)) + "\n")
		return s.String()
	}
	if len(v.questions) == 0 {
		s.WriteString(th.MutedTextStyle.Render(" No agent is waiting for an answer.") + "\n\n")
		s.WriteString(th.MutedTextStyle.Render(" Agents ask through their ask_questions tool and show up as blocked until you answer.") + "\n")
		if v.statusMsg != "" {
			s.WriteString("\n " + th.StatsStyle.Render(v.statusMsg) + "\n")
		}
		return s.String()
	}

	first, last := v.window()
	for i := first; i < last; i++ {
		s.WriteString(v.renderRow(v.questions[i], i == v.cursor) + "\n")
	}
	if last < len(v.questions) {
		s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf("   … %d more", len(v.questions)-last)) + "\n")
	}

	s.WriteString("\n" + v.renderDetail())
	if v.statusMsg != "" {
		s.WriteString("\n " + th.StatsStyle.Render(v.statusMsg) + "\n")
	}
	return s.String()
}

// window is the slice of rows on screen: as many as fit above the detail
// pane, scrolled so the cursor stays visible.
func (v *QuestionsView) window() (first, last int) {
	rows := v.height - 14
	if rows < 3 {
		rows = 3
	}
	if rows >= len(v.questions) {
		return 0, len(v.questions)
	}
	first = v.cursor - rows/2
	if first < 0 {
		first = 0
	}
	if first > len(v.questions)-rows {
		first = len(v.questions) - rows
	}
	return first, first + rows
}

func (v *QuestionsView) agentLabel(agentID string) string {
	if summary := v.agents[agentID]; summary != "" {
		return summary
	}
	return agentID
}

func (v *QuestionsView) renderRow(q service.Question, selected bool) string {
	th := theme.GetTheme()
	prefix := "  "
	if selected {
		prefix = " >"
	}
	mark, textStyle := th.WarningStyle.Render("?"), th.StatsStyle
	if q.Answered {
		mark, textStyle = th.MutedTextStyle.Render("✓"), th.MutedTextStyle
	}
	agent := clipText(v.agentLabel(q.AgentID), 28)
	age := formatDuration(time.Since(q.AskedAt))
	room := v.width - (len(prefix) + 1 + 1 + len(q.ID) + 2 + len([]rune(agent)) + 2 + 2 + len(age))
	text := clipText(strings.Join(strings.Fields(q.Text), " "), max(room, 10))

	line := fmt.Sprintf("%s%s %s  %s  %s  %s", prefix, mark, th.BranchStyle.Render(q.ID),
		th.MutedTextStyle.Render(agent), textStyle.Render(text), th.MutedTextStyle.Render(age))
	// Clipped rather than wrapped: the full text is in the detail pane.
	return lipgloss.NewStyle().MaxWidth(v.width).Render(line)
}

// renderDetail shows the selected question unabridged, with its answer, the
// answer input, or the hint that opens it.
func (v *QuestionsView) renderDetail() string {
	th := theme.GetTheme()
	q, ok := v.selected()
	if v.answering {
		q, ok = v.target, true
	}
	if !ok {
		return ""
	}
	wrap := lipgloss.NewStyle().Width(max(v.width-4, 20))
	var s strings.Builder
	s.WriteString(" " + th.BranchStyle.Render(q.ID) + th.MutedTextStyle.Render(
		fmt.Sprintf("  asked by %s (%s)", v.agentLabel(q.AgentID), q.AgentID)) + "\n")
	s.WriteString(indentBlock(wrap.Render(q.Text), "  ") + "\n\n")

	switch {
	case v.answering:
		s.WriteString(" " + th.DashboardAccentStyle.Render("Answer: ") + v.input.RenderPlain() + "\n")
		if open := v.openInBatch(q.BatchID); open > 1 {
			s.WriteString(th.MutedTextStyle.Render(fmt.Sprintf(
				"  %d questions from this agent are open; it continues once all are answered.", open)) + "\n")
		}
	case q.Answered:
		s.WriteString(" " + th.MutedTextStyle.Render("Answered: ") + indentBlock(wrap.Render(q.Answer), "  ")[2:] + "\n")
		s.WriteString(th.MutedTextStyle.Render("  The agent is still waiting on another question from the same call.") + "\n")
	default:
		s.WriteString(th.MutedTextStyle.Render("  Press enter to answer.") + "\n")
	}
	return s.String()
}

func (v *QuestionsView) openInBatch(batchID string) int {
	n := 0
	for _, q := range v.questions {
		if q.BatchID == batchID && !q.Answered {
			n++
		}
	}
	return n
}

// indentBlock prefixes every line of s.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
