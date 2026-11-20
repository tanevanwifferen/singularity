package engine

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Operator questions.
//
// An agent asks through the ask_questions MCP tool (internal/questionmcp):
// one call carries one or more questions and forms a batch. While any
// question of any of its batches is unanswered the agent reports AgentBlocked;
// the tool call returns — and the agent carries on — only once every question
// in the batch has an answer. Nothing here times out: a question waits for
// the operator for as long as the agent's process lives.
//
// The MCP server polls its batch (QuestionBatch) instead of holding a request
// open, so a wait of hours does not depend on any connection surviving it.
// That poll doubles as a liveness signal: a batch nobody has polled for
// questionStaleAfter belongs to a tool call that is gone, and is dropped.

// Errors the question API returns. They are sentinels because the service
// layer maps them onto its own (not found, conflict, invalid request).
var (
	ErrQuestionNotFound = errors.New("question not found")
	ErrQuestionAnswered = errors.New("question already answered")
	ErrQuestionInvalid  = errors.New("invalid question")
)

// maxQuestionsPerBatch bounds one ask_questions call. It is a guard against
// a runaway tool call, not a product limit.
const maxQuestionsPerBatch = 20

// questionStaleAfter is how long a batch may go unpolled before its tool
// call is taken for dead; questionReapInterval is how often that is checked.
// Variables so tests can shorten them.
var (
	questionStaleAfter   = 60 * time.Second
	questionReapInterval = 10 * time.Second
)

// Question is one question an agent asked the operator.
type Question struct {
	ID         string     `json:"id"`
	BatchID    string     `json:"batch_id"`
	AgentID    string     `json:"agent_id"`
	Text       string     `json:"text"`
	Answered   bool       `json:"answered"`
	Answer     string     `json:"answer,omitempty"`
	AskedAt    time.Time  `json:"asked_at"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
}

// QuestionBatch is the questions of one ask_questions call. Done means every
// one of them is answered, which is what releases the agent.
type QuestionBatch struct {
	ID        string     `json:"id"`
	AgentID   string     `json:"agent_id"`
	Questions []Question `json:"questions"`
	Done      bool       `json:"done"`
}

// questionBatch is the engine's own record of a batch. Guarded by Engine.qMu.
type questionBatch struct {
	id        string
	agent     *Agent
	questions []*Question
	lastPoll  time.Time
}

func (b *questionBatch) open() int {
	n := 0
	for _, q := range b.questions {
		if !q.Answered {
			n++
		}
	}
	return n
}

func (b *questionBatch) snapshot() QuestionBatch {
	out := QuestionBatch{ID: b.id, AgentID: b.agent.ID, Questions: make([]Question, len(b.questions))}
	for i, q := range b.questions {
		out.Questions[i] = *q
	}
	out.Done = b.open() == 0
	return out
}

// AskQuestions records a batch of questions for the agent and marks it
// blocked. It returns at once; the caller polls QuestionBatch for the answers.
func (e *Engine) AskQuestions(agentID string, texts []string) (QuestionBatch, error) {
	agent := e.getAgent(agentID)
	if agent == nil {
		return QuestionBatch{}, fmt.Errorf("agent not found: %s", agentID)
	}
	if agent.processExited() {
		return QuestionBatch{}, fmt.Errorf("%w: agent %s is no longer running", ErrQuestionInvalid, agentID)
	}
	var cleaned []string
	for _, t := range texts {
		if t = strings.TrimSpace(t); t != "" {
			cleaned = append(cleaned, t)
		}
	}
	if len(cleaned) == 0 {
		return QuestionBatch{}, fmt.Errorf("%w: at least one question is required", ErrQuestionInvalid)
	}
	if len(cleaned) > maxQuestionsPerBatch {
		return QuestionBatch{}, fmt.Errorf("%w: at most %d questions per call", ErrQuestionInvalid, maxQuestionsPerBatch)
	}

	now := time.Now()
	e.qMu.Lock()
	e.qSeq++
	batch := &questionBatch{id: fmt.Sprintf("b%d", e.qSeq), agent: agent, lastPoll: now}
	for _, t := range cleaned {
		e.qSeq++
		batch.questions = append(batch.questions, &Question{
			ID:      fmt.Sprintf("q%d", e.qSeq),
			BatchID: batch.id,
			AgentID: agentID,
			Text:    t,
			AskedAt: now,
		})
	}
	e.batches[batch.id] = batch
	snap := batch.snapshot()
	if !e.reaperRunning {
		e.reaperRunning = true
		go e.reapStaleQuestions()
	}
	e.syncOpenQuestionsLocked(agent)
	e.qMu.Unlock()

	for _, q := range snap.Questions {
		agent.appendOutput("system", fmt.Sprintf("Question %s: %s", q.ID, q.Text))
	}
	playSound(agent.soundCfg)
	agent.notifyChanged()
	return snap, nil
}

// QuestionBatch reports a batch's current answers and records the poll. A
// batch that is done is handed out once and then forgotten.
func (e *Engine) QuestionBatch(batchID string) (QuestionBatch, error) {
	e.qMu.Lock()
	defer e.qMu.Unlock()
	batch, ok := e.batches[batchID]
	if !ok {
		return QuestionBatch{}, fmt.Errorf("%w: batch %s", ErrQuestionNotFound, batchID)
	}
	batch.lastPoll = time.Now()
	snap := batch.snapshot()
	if snap.Done {
		delete(e.batches, batchID)
	}
	return snap, nil
}

// AnswerQuestion records the operator's answer to one question. The agent
// stays blocked until the last question of its batch is answered.
func (e *Engine) AnswerQuestion(questionID, answer string) (Question, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return Question{}, fmt.Errorf("%w: answer is required", ErrQuestionInvalid)
	}

	e.qMu.Lock()
	var found *Question
	var agent *Agent
	for _, b := range e.batches {
		for _, q := range b.questions {
			if q.ID == questionID {
				found, agent = q, b.agent
			}
		}
	}
	if found == nil {
		e.qMu.Unlock()
		return Question{}, fmt.Errorf("%w: %s", ErrQuestionNotFound, questionID)
	}
	if found.Answered {
		e.qMu.Unlock()
		return Question{}, fmt.Errorf("%w: %s", ErrQuestionAnswered, questionID)
	}
	now := time.Now()
	found.Answered = true
	found.Answer = answer
	found.AnsweredAt = &now
	out := *found
	e.syncOpenQuestionsLocked(agent)
	e.qMu.Unlock()

	agent.appendOutput("user_input", fmt.Sprintf("Answer %s: %s", out.ID, truncateForLog(answer, promptLogLimit)))
	agent.notifyChanged()
	return out, nil
}

// AnswerAgentQuestions answers every open question of the agent with the
// same text and reports how many there were. It is the queue's `answer`
// verb, which addresses a task rather than a question.
func (e *Engine) AnswerAgentQuestions(agentID, answer string) (int, error) {
	n := 0
	for _, q := range e.ListQuestions() {
		if q.AgentID != agentID || q.Answered {
			continue
		}
		if _, err := e.AnswerQuestion(q.ID, answer); err != nil {
			if errors.Is(err, ErrQuestionAnswered) || errors.Is(err, ErrQuestionNotFound) {
				continue // answered or withdrawn between the list and here
			}
			return n, err
		}
		n++
	}
	return n, nil
}

// CancelQuestionBatch withdraws a batch whose tool call ended without the
// answers (the agent was interrupted, or its MCP server is shutting down).
// Unknown batches are not an error: withdrawing twice is harmless.
func (e *Engine) CancelQuestionBatch(batchID string) {
	e.dropBatches(func(b *questionBatch) bool { return b.id == batchID }, "withdrawn by the agent")
}

// ListQuestions returns every question of every live batch, oldest first:
// the open ones, plus answered ones whose batch still waits on a sibling.
func (e *Engine) ListQuestions() []Question {
	e.qMu.Lock()
	var out []Question
	for _, b := range e.batches {
		for _, q := range b.questions {
			out = append(out, *q)
		}
	}
	e.qMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return questionSeq(out[i].ID) < questionSeq(out[j].ID) })
	return out
}

// OpenQuestionText returns the agent's unanswered questions as one string,
// one per line, or "" when it has none.
func (e *Engine) OpenQuestionText(agentID string) string {
	var lines []string
	for _, q := range e.ListQuestions() {
		if q.AgentID == agentID && !q.Answered {
			lines = append(lines, q.Text)
		}
	}
	return strings.Join(lines, "\n")
}

// questionSeq is the numeric part of a "q<n>" id, the order questions were asked in.
func questionSeq(id string) int64 {
	var n int64
	fmt.Sscanf(strings.TrimPrefix(id, "q"), "%d", &n)
	return n
}

// dropAgentQuestions forgets every batch of an agent whose process is gone.
func (e *Engine) dropAgentQuestions(agentID string) {
	e.dropBatches(func(b *questionBatch) bool { return b.agent.ID == agentID }, "")
}

// dropBatches removes the batches match selects and re-derives the affected
// agents' blocked state. A non-empty note is logged to those agents' output.
func (e *Engine) dropBatches(match func(*questionBatch) bool, note string) {
	e.qMu.Lock()
	dropped := make(map[*Agent]int) // open questions dropped, per agent
	for id, b := range e.batches {
		if match(b) {
			dropped[b.agent] += b.open()
			delete(e.batches, id)
		}
	}
	for a := range dropped {
		e.syncOpenQuestionsLocked(a)
	}
	e.qMu.Unlock()

	for a, n := range dropped {
		if n > 0 && note != "" {
			a.appendOutput("system", fmt.Sprintf("%d open question(s) %s", n, note))
		}
		a.notifyChanged()
	}
}

// reapStaleQuestions drops batches whose MCP server stopped polling. It runs
// only while there are batches and ends itself when the last one is gone.
func (e *Engine) reapStaleQuestions() {
	for {
		time.Sleep(questionReapInterval)
		cutoff := time.Now().Add(-questionStaleAfter)
		e.dropBatches(func(b *questionBatch) bool { return b.lastPoll.Before(cutoff) },
			"dropped: the agent stopped waiting for them")

		e.qMu.Lock()
		if len(e.batches) == 0 {
			e.reaperRunning = false
			e.qMu.Unlock()
			return
		}
		e.qMu.Unlock()
	}
}

// syncOpenQuestionsLocked recounts the agent's open questions and pushes the
// number into it. Caller holds qMu, which is what orders concurrent updates.
func (e *Engine) syncOpenQuestionsLocked(agent *Agent) {
	n := 0
	for _, b := range e.batches {
		if b.agent == agent {
			n += b.open()
		}
	}
	agent.setOpenQuestions(n)
}

// setOpenQuestions records how many of the agent's questions are unanswered,
// keeps the blocked-time account, and tells a backend that cares
// (BlockAwareBackend) when the agent starts or stops waiting.
func (a *Agent) setOpenQuestions(n int) {
	a.mu.Lock()
	was := a.openQuestions > 0
	a.openQuestions = n
	now := n > 0
	if now && !was {
		a.blockedSince = time.Now()
	}
	if !now && was {
		a.blockedTotal += time.Since(a.blockedSince)
	}
	a.mu.Unlock()

	if now == was {
		return
	}
	aware, ok := a.backend.(BlockAwareBackend)
	if !ok {
		return
	}
	a.stdinMu.Lock()
	if a.stdin != nil {
		_, _ = a.stdin.Write(aware.BlockedInput(now))
	}
	a.stdinMu.Unlock()
}

// blockedFor reports the total time the agent has spent waiting on operator
// questions, the current wait included, and whether it is waiting right now.
func (a *Agent) blockedFor() (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openQuestions > 0 {
		return a.blockedTotal + time.Since(a.blockedSince), true
	}
	return a.blockedTotal, false
}

// notifyChanged tells the engine's observers this agent changed.
func (a *Agent) notifyChanged() {
	if a.notify != nil {
		a.notify()
	}
}

// enforceTimeout kills the agent once it has been working for timeout. Time
// spent blocked on operator questions does not count: the deadline moves out
// by however long the agent has waited, and never fires while it is waiting.
func (e *Engine) enforceTimeout(agent *Agent, timeout time.Duration) {
	start := time.Now()
	for {
		blocked, waiting := agent.blockedFor()
		wait := time.Second
		if !waiting {
			wait = time.Until(start.Add(timeout + blocked))
			if wait <= 0 {
				// terminate(), not a bare kill(false): a timeout firing while
				// the agent is still being smart-routed has no subprocess yet,
				// so kill's nil-cmd branch would leave the state untouched and
				// the classifier would go on to start the agent unbounded —
				// silently voiding the timeout it was just declared to enforce.
				agent.terminate()
				agent.appendOutput("system", fmt.Sprintf("Agent killed: timeout after %s", timeout))
				return
			}
		}
		select {
		case <-time.After(wait):
		case <-agent.Done():
			return
		}
	}
}
