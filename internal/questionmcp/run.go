package questionmcp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/daemon"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// Run implements the `singularity mcp-questions` subcommand. It is launched
// by an agent's CLI from the MCP config the engine wrote for that agent
// (internal/engine/mcp_config.go), not by users.
func Run(args []string) int {
	fs := flag.NewFlagSet("mcp-questions", flag.ContinueOnError)
	agentID := fs.String("agent", "", "id of the agent this server asks on behalf of")
	server := fs.String("server", "", "daemon endpoint (default: the local unix socket)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *agentID == "" {
		fmt.Fprintln(os.Stderr, "mcp-questions: --agent is required")
		return 2
	}

	endpoint := *server
	if endpoint == "" {
		endpoint = "unix://" + daemon.DefaultPaths().Socket
	}
	c := client.NewClient(endpoint)
	if !strings.HasPrefix(endpoint, "unix://") {
		// A TCP daemon wants its bearer token, resolved as singl does.
		tok := strings.TrimSpace(os.Getenv("SINGULARITY_TOKEN"))
		if tok == "" {
			if data, err := os.ReadFile(daemon.DefaultPaths().Token); err == nil {
				tok = strings.TrimSpace(string(data))
			}
		}
		c.SetAuthToken(tok)
	}

	// SIGTERM/SIGINT end the session the same way stdin closing does, so
	// open questions are withdrawn rather than left behind.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	srv := &Server{AgentID: *agentID, Asker: clientAsker{c}}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, os.Stdin, os.Stdout) }()
	select {
	case err := <-done:
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp-questions: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		// Serve is blocked reading stdin and cannot be interrupted; its
		// in-flight calls see ctx and withdraw their questions. Give them
		// the moment that takes, then go.
		srv.waitCalls()
	}
	return 0
}

// clientAsker adapts the daemon client to Asker.
type clientAsker struct{ c *client.Client }

func (a clientAsker) Ask(ctx context.Context, agentID string, questions []string) (string, error) {
	batch, err := a.c.QuestionAsk(ctx, agentID, questions)
	return batch.ID, err
}

func (a clientAsker) Poll(ctx context.Context, batchID string) ([]QA, bool, bool, error) {
	batch, err := a.c.QuestionBatch(ctx, batchID)
	if errors.Is(err, service.ErrNotFound) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	answers := make([]QA, len(batch.Questions))
	for i, q := range batch.Questions {
		answers[i] = QA{Question: q.Text, Answer: q.Answer}
	}
	return answers, batch.Done, false, nil
}

func (a clientAsker) Cancel(ctx context.Context, batchID string) error {
	return a.c.QuestionCancel(ctx, batchID)
}
