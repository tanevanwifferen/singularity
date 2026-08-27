package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func cmdForge(ctx context.Context, verb string, args []string) int {
	switch verb {
	case "info":
		return runForgeInfo(ctx, args)
	case "auth":
		return runForgeAuth(ctx, args)
	case "provider":
		return runForgeProvider(ctx, args)
	default:
		return nounHelp("forge", verb)
	}
}

func runForgeInfo(ctx context.Context, _ []string) int {
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	info, err := c.ForgeDetect(tctx)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(info)
	}
	auth := "no"
	if info.HasAuth {
		auth = "yes"
	}
	md := "## Forge\n\n"
	md += fmt.Sprintf("Type: `%s`  \n", info.Type)
	md += fmt.Sprintf("Authenticated: %s  \n", auth)
	if info.User != "" {
		md += fmt.Sprintf("User: `%s`  \n", info.User)
	}
	if info.APIURL != "" {
		md += fmt.Sprintf("API URL: `%s`  \n", info.APIURL)
	}
	return renderMarkdown(md)
}

func runForgeAuth(ctx context.Context, _ []string) int {
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	auth, err := c.ForgeDetectAuth(tctx)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(auth)
	}
	valid := "no"
	if auth.Valid {
		valid = "yes"
	}
	md := "## Forge auth\n\n"
	md += fmt.Sprintf("Type: `%s`  \n", auth.Type)
	md += fmt.Sprintf("Valid: %s  \n", valid)
	if auth.Username != "" {
		md += fmt.Sprintf("Username: `%s`  \n", auth.Username)
	}
	if auth.APIURL != "" {
		md += fmt.Sprintf("API URL: `%s`  \n", auth.APIURL)
	}
	// Do not print auth.AuthToken — credential in output.
	return renderMarkdown(md)
}

func runForgeProvider(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("forge-provider", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo path")
	if code, done := parseArgs(fs, args); done {
		return code
	}
	repoPath := repoArg(*repo)
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "error: --repo is required (or set --repo globally)")
		return 2
	}
	c, err := newClient()
	if err != nil {
		return die(err)
	}
	tctx, cancel := withTimeout(ctx)
	defer cancel()
	provider, err := c.ForgeDetectProvider(tctx, repoPath)
	if err != nil {
		return die(err)
	}
	if globals.json {
		return printJSON(map[string]string{"provider": string(provider)})
	}
	md := "## Forge provider\n\n"
	md += fmt.Sprintf("Provider: `%s`  \n", string(provider))
	return renderMarkdown(md)
}
