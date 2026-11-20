package views

import (
	"context"
	"strings"
	"testing"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
	"gitlab.com/tanevanwifferen1/singularity/internal/service/fake"

	tea "github.com/charmbracelet/bubbletea"
)

// stubQuestions is an in-memory QuestionService for the view.
type stubQuestions struct {
	service.QuestionService
	questions []service.Question
	answered  map[string]string
}

func (s *stubQuestions) List(context.Context) ([]service.Question, error) {
	return append([]service.Question(nil), s.questions...), nil
}

func (s *stubQuestions) Answer(_ context.Context, id, answer string) (service.Question, error) {
	for i := range s.questions {
		if s.questions[i].ID == id {
			s.questions[i].Answered, s.questions[i].Answer = true, answer
			s.answered[id] = answer
			return s.questions[i], nil
		}
	}
	return service.Question{}, service.ErrNotFound
}

func newQuestionsFixture(t *testing.T) (*QuestionsView, *stubQuestions) {
	t.Helper()
	stub := &stubQuestions{
		answered: map[string]string{},
		questions: []service.Question{
			{ID: "q1", BatchID: "b1", AgentID: "agent-1", Text: "Which database?", AskedAt: time.Now()},
			{ID: "q2", BatchID: "b1", AgentID: "agent-1", Text: "Keep the v1 API?", AskedAt: time.Now()},
			{ID: "q3", BatchID: "b2", AgentID: "agent-2", Text: "Delete the old migrations?", AskedAt: time.Now()},
		},
	}
	svc := fake.New()
	svc.Question = stub
	v := NewQuestionsView()
	v.SetServices(svc)
	v.SetSize(120, 40)
	drain(v, v.Init())
	return v, stub
}

// drain runs a command and feeds whatever it returns back into the view,
// until the view stops asking for more.
func drain(v *QuestionsView, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = v.Update(msg)
	}
}

func typeKeys(v *QuestionsView, text string) {
	for _, r := range text {
		drain(v, second(v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})))
	}
}

func second(_ tea.Model, cmd tea.Cmd) tea.Cmd { return cmd }

func press(v *QuestionsView, key tea.KeyType) {
	drain(v, second(v.Update(tea.KeyMsg{Type: key})))
}

func TestQuestionsViewListsAndAnswers(t *testing.T) {
	v, stub := newQuestionsFixture(t)

	out := v.View()
	for _, want := range []string{"3 open", "q1", "Which database?", "q3", "Press enter to answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q:\n%s", want, out)
		}
	}
	if v.CapturesInput() {
		t.Fatal("view captures input before an answer is being typed")
	}

	// Answer q1: the input opens, takes text — including keys that are
	// navigation outside it — and submits on enter.
	press(v, tea.KeyEnter)
	if !v.CapturesInput() {
		t.Fatal("enter did not open the answer input")
	}
	typeKeys(v, "just pick one")
	press(v, tea.KeyEnter)
	if stub.answered["q1"] != "just pick one" {
		t.Fatalf("q1 answer = %q, want the typed text", stub.answered["q1"])
	}

	// The agent still waits on q2, from the same call: the view moves on to
	// it and keeps the input open.
	if !v.CapturesInput() || v.target.ID != "q2" {
		t.Fatalf("after answering q1: answering=%v target=%s, want the input open on q2", v.CapturesInput(), v.target.ID)
	}
	typeKeys(v, "yes")
	press(v, tea.KeyEnter)
	if stub.answered["q2"] != "yes" {
		t.Fatalf("q2 answer = %q", stub.answered["q2"])
	}
	// q3 belongs to another agent: the input closes rather than jumping to it.
	if v.CapturesInput() {
		t.Error("input stayed open after the agent's last question was answered")
	}
	if !strings.Contains(v.View(), "the agent continues") {
		t.Errorf("no confirmation that the agent continues:\n%s", v.View())
	}
	if _, ok := stub.answered["q3"]; ok {
		t.Error("q3 was answered without being asked about")
	}
}

func TestQuestionsViewEscAndEmptyAnswer(t *testing.T) {
	v, stub := newQuestionsFixture(t)

	press(v, tea.KeyEnter)
	press(v, tea.KeyEnter) // nothing typed
	if len(stub.answered) != 0 || !v.CapturesInput() {
		t.Fatalf("an empty answer was sent (answered=%v, answering=%v)", stub.answered, v.CapturesInput())
	}
	typeKeys(v, "never mind")
	press(v, tea.KeyEsc)
	if v.CapturesInput() || len(stub.answered) != 0 {
		t.Fatalf("esc did not cancel the answer (answered=%v)", stub.answered)
	}
}

// TestQuestionsViewDropsVanishedTarget: the agent can stop waiting while an
// answer is being typed; the view must not send it into the void.
func TestQuestionsViewDropsVanishedTarget(t *testing.T) {
	v, stub := newQuestionsFixture(t)
	press(v, tea.KeyEnter)
	typeKeys(v, "half an ans")

	stub.questions = stub.questions[2:] // agent-1 was killed
	drain(v, v.RefreshCmd())
	if v.CapturesInput() {
		t.Fatal("input still open on a question that no longer exists")
	}
	if !strings.Contains(v.View(), "q1 is gone") {
		t.Errorf("no notice that the question is gone:\n%s", v.View())
	}
}

func TestQuestionsViewEmpty(t *testing.T) {
	v := NewQuestionsView()
	svc := fake.New()
	svc.Question = &stubQuestions{answered: map[string]string{}}
	v.SetServices(svc)
	drain(v, v.Init())
	if !strings.Contains(v.View(), "No agent is waiting") {
		t.Errorf("empty view:\n%s", v.View())
	}
	press(v, tea.KeyEnter) // must not panic or open an input
	if v.CapturesInput() {
		t.Error("enter opened an input with nothing to answer")
	}
}
