package views

import (
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// The continue modal: more rounds for a flow that finished without being
// accepted. Split from flow_keys.go and flow_render.go only for length; this
// is FlowsView.
//
// It is a one-field form rather than a ConfirmPrompt because a continue takes
// a number — how many more rounds — and rather than the start modal's
// multi-field form because that number is the only thing on offer. Goal,
// review focus, work dir and both option blocks are the flow's own and are
// not re-specifiable: rounds recorded against one goal would stop meaning
// anything under another.

const (
	// flowContinueDefaultRounds is what the modal prefills, and it is the
	// daemon's own default for a continue — the same 3 `flow continue`
	// sends when --rounds is left off. Prefilled rather than left empty so
	// the "new cap" line has something to show before the user types.
	flowContinueDefaultRounds = 3

	// flowRoundCeiling is the 1..20 every flow's cap is validated against.
	// Shown here, never enforced: the daemon knows the flow's current cap
	// and answers a number that will not fit by naming the largest one
	// that would, which no local range check could do. The start modal's
	// help line quotes the same ceiling.
	flowRoundCeiling = 20
)

// openContinueModal offers the selected flow more rounds, or says in the
// flash line why that flow is not something to continue.
//
// The state check is made here rather than left to the service so the
// refusal costs nothing and reads as a fact about the flow under the cursor;
// the two refusals that need the record the daemon holds — a cap already at
// the ceiling, and a work dir that has since been removed — are deliberately
// left to Flow.Continue and land in the flash line as ordinary answers.
func (v *FlowsView) openContinueModal() {
	f, ok := v.selectedFlow()
	if !ok {
		v.statusMsg = "No flow selected"
		return
	}
	if v.services == nil {
		v.statusMsg = "Flow service unavailable"
		return
	}
	if why := whyNotContinuable(f); why != "" {
		v.statusMsg = why
		return
	}
	v.showContinue = true
	v.continueTarget = f
	v.continueRounds.Set(strconv.Itoa(flowContinueDefaultRounds))
}

// whyNotContinuable reports why the flow may not be continued, or "" if it
// may. The wording mirrors what the flow manager would have said, because an
// operator who reads this line and then tries the CLI must not be told two
// different things.
func whyNotContinuable(f FlowInfo) string {
	switch {
	case f.State == service.FlowAccepted:
		return fmt.Sprintf("Flow %s was accepted — its work passed review, so there is nothing to continue", f.ID)
	case !f.State.Terminal():
		return fmt.Sprintf("Flow %s is %s — only a flow that has finished without being accepted can be "+
			"continued; wait for it, or cancel it first with 'c'", f.ID, f.State)
	}
	return ""
}

// handleContinueInput drives the modal: enter submits, esc abandons,
// everything else is the round field's — there is only one field, so there is
// no tab order to keep.
func (v *FlowsView) handleContinueInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		v.showContinue = false
		return nil
	case "enter":
		return v.submitContinue()
	default:
		v.continueRounds.HandleKey(msg)
		return nil
	}
}

// submitContinue hands the round count to the service. Only nonsense the view
// can name locally is caught here — a round count that is not a number, the
// same check the start modal makes — because everything else depends on the
// flow's record: the cap it is at now, the ceiling it must stay under, and
// whether its work dir is still on disk. Those come back as flash messages.
func (v *FlowsView) submitContinue() tea.Cmd {
	text := strings.TrimSpace(v.continueRounds.Value)
	rounds := 0
	if text != "" {
		n, err := strconv.Atoi(text)
		if err != nil {
			v.statusMsg = fmt.Sprintf("Extra rounds %q is not a number", text)
			return nil
		}
		rounds = n
	}
	svc := v.services
	if svc == nil {
		v.statusMsg = "Flow service unavailable"
		return nil
	}

	flowID := v.continueTarget.ID
	v.showContinue = false
	return func() tea.Msg {
		f, err := svc.Flow.Continue(v.ctx(), flowID, rounds)
		if err != nil {
			return flowActionMsg{action: "continue", err: err}
		}
		// What the continue actually bought, the way `flow continue`
		// reports it: the raised cap and the round the flow resumes at.
		return flowActionMsg{action: "continue", flowID: f.ID,
			note: fmt.Sprintf("Flow %s continued — capped at %d rounds, resuming at round %d",
				f.ID, f.MaxRounds, len(f.Rounds)+1)}
	}
}

// continuePlan is the effective extra-round count for the text in the field:
// the daemon's default for an empty field (which is what a 0 on the wire
// means), and not-ok for anything that is not a number. Negatives parse and
// are passed through, so the service's own complaint is what the operator
// sees — the same pass-through `flow continue --rounds` makes.
func (v *FlowsView) continuePlan() (extra int, ok bool) {
	text := strings.TrimSpace(v.continueRounds.Value)
	if text == "" {
		return flowContinueDefaultRounds, true
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	if n == 0 {
		return flowContinueDefaultRounds, true
	}
	if n < 0 {
		return n, false
	}
	return n, true
}

// renderContinueModal renders the continue form: which flow, where it
// stopped, what the cap becomes, and which round comes next — plus the fact
// that nothing else changes, which is the whole difference between continuing
// a flow and starting another one against the same tree.
func (v *FlowsView) renderContinueModal() string {
	th := theme.GetTheme()
	width := modalWidth(v.width)
	f := v.continueTarget

	newCap := "—"
	next := "—"
	if extra, ok := v.continuePlan(); ok {
		newCap = fmt.Sprintf("%d rounds (was %d)", f.MaxRounds+extra, f.MaxRounds)
		next = fmt.Sprintf("round %d of %d", f.Round+1, f.MaxRounds+extra)
	}

	lines := []string{
		"",
		fmt.Sprintf("  %-14s %s — %s", "Flow:", f.ID, clipText(f.Label, max(width-24, 8))),
		fmt.Sprintf("  %-14s %s  %s", "Stopped:", f.State, f.rounds()),
		"",
		fmt.Sprintf("> %-14s %s", "Extra rounds:", v.continueRounds.RenderPlain()),
		fmt.Sprintf("  %-14s %s", "New cap:", newCap),
		fmt.Sprintf("  %-14s %s", "Resumes at:", next),
		"",
		"  The goal, the review focus, the work dir and the model",
		"  options stay the flow's own — none of them is being",
		"  re-specified. Every round already on the record keeps its",
		"  findings, and the next round's fixer is composed from them.",
	}
	if v.statusMsg != "" {
		lines = append(lines, "", "  "+v.statusMsg)
	}
	lines = append(lines, "", "  Enter: Continue  Esc: Cancel")
	return renderModal("Continue Flow With More Rounds", lines, width) + "\n" +
		th.Help.Render(fmt.Sprintf(" Extra rounds are added to the cap, which may not pass %d.", flowRoundCeiling))
}
