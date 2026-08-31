package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// printAgentEvent writes one AgentEvent to stdout/stderr in the standard
// singl streaming format. Returns true if the stream is done.
func printAgentEvent(ev service.AgentEvent, prefix string) (done bool, exitCode int) {
	switch ev.Kind {
	case service.AgentEventStarted:
		fmt.Fprintf(os.Stderr, "[singl:started] %sagent %s state=%s\n", prefix, ev.AgentID, ev.State.String())
	case service.AgentEventOutput:
		if ev.Output == nil || ev.Output.Content == "" {
			return false, 0
		}
		switch ev.Output.Source {
		case "user_input":
			fmt.Printf("%s[prompt] %s\n", prefix, ev.Output.Content)
		case "tool_use":
			fmt.Printf("%s[tool:%s] %s\n", prefix, ev.Output.ToolName, ev.Output.Content)
		case "tool_result":
			if ev.Output.Content != "" {
				fmt.Printf("%s[result] %s\n", prefix, ev.Output.Content)
			}
		case "system":
			fmt.Printf("%s[system] %s\n", prefix, ev.Output.Content)
		case "error":
			fmt.Fprintf(os.Stderr, "%s[error] %s\n", prefix, ev.Output.Content)
		default:
			fmt.Printf("%s%s\n", prefix, ev.Output.Content)
		}
	case service.AgentEventState:
		fmt.Fprintf(os.Stderr, "[singl:state] %s%s\n", prefix, ev.State.String())
	case service.AgentEventError:
		fmt.Fprintf(os.Stderr, "[singl:error] %s%s\n", prefix, ev.Err)
		return true, 1
	case service.AgentEventComplete:
		fmt.Fprintf(os.Stderr, "[singl:complete] %s\n", strings.TrimRight(prefix, " "))
		return true, 0
	}
	return false, 0
}

// runAgentsWatch streams live output from a single agent.
func runAgentsWatch(ctx context.Context, args []string) int {
	if globals.json {
		fmt.Fprintln(os.Stderr, "error: --json is not supported for streaming watch; use `singl agents output --id <id>` for buffered JSON")
		return 1
	}
	fs := flag.NewFlagSet("agents-watch", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" {
		fmt.Fprintln(os.Stderr, "error: --id is required")
		return 2
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ch, cancel, err := c.AgentSubscribe(ctx, *id)
	if err != nil {
		return die(err)
	}
	defer cancel()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return 0
			}
			if done, code := printAgentEvent(ev, ""); done {
				return code
			}
		case <-ctx.Done():
			return 0
		}
	}
}

// runAgentsWatchAll streams events from all agents.
func runAgentsWatchAll(ctx context.Context, _ []string) int {
	if globals.json {
		fmt.Fprintln(os.Stderr, "error: --json is not supported for streaming watch-all")
		return 1
	}
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ch, cancel, err := c.AgentSubscribeAll(ctx)
	if err != nil {
		return die(err)
	}
	defer cancel()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return 0
			}
			prefix := "[" + ev.AgentID + "] "
			done, code := printAgentEvent(ev, prefix) // one agent completing doesn't stop the stream…
			if done && ev.AgentID == "" {
				return code // …but a transport-level error (no agent attached) does
			}
		case <-ctx.Done():
			return 0
		}
	}
}

// runAgentsChat sends a message to a running agent then streams its response.
func runAgentsChat(ctx context.Context, args []string) int {
	if globals.json {
		fmt.Fprintln(os.Stderr, "error: --json is not supported for chat streaming; use `singl agents input` then `singl agents output` separately")
		return 1
	}
	fs := flag.NewFlagSet("agents-chat", flag.ContinueOnError)
	id := fs.String("id", "", "agent ID (required)")
	message := fs.String("message", "", "message to send (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *message == "" {
		fmt.Fprintln(os.Stderr, "error: --id and --message are required")
		return 2
	}
	fmt.Fprintf(os.Stderr, "[singl:chat] agent %s\n", *id)
	c, err := newStreamClient()
	if err != nil {
		return die(err)
	}
	defer c.Disconnect()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	tctx, cancel := withTimeout(ctx)
	if err := c.AgentSendInput(tctx, *id, *message); err != nil {
		cancel()
		return die(err)
	}
	cancel()

	ch, streamCancel, err := c.AgentSubscribe(ctx, *id)
	if err != nil {
		return die(err)
	}
	defer streamCancel()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return 0
			}
			if done, code := printAgentEvent(ev, ""); done {
				return code
			}
		case <-ctx.Done():
			return 0
		}
	}
}
