package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// parseArgs parses a verb's flags. done=true means the caller must return
// the given code: 0 when the user asked for help (--help/-h — the flag
// package already printed the flag reference), 2 on an actual parse error.
func parseArgs(fs *flag.FlagSet, args []string) (code int, done bool) {
	err := fs.Parse(args)
	if err == nil {
		return 0, false
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0, true
	}
	return 2, true
}

// isHelpVerb reports whether the verb position asks for help: a bare noun
// invocation, `help`, `--help`, or `-h`.
func isHelpVerb(verb string) bool {
	return verb == "" || verb == "help" || verb == "--help" || verb == "-h"
}

// nounHelp prints the per-noun usage (verbs + flags) to stderr. It is the
// shared default case of every noun dispatcher: help requests exit 0,
// unknown verbs exit 2 with the same usage text.
func nounHelp(noun, verb string) int {
	usage, ok := nounUsage[noun]
	if !ok {
		fmt.Fprintf(os.Stderr, "no help available for %q\n", noun)
		return 2
	}
	if isHelpVerb(verb) {
		fmt.Fprint(os.Stderr, usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown %s verb: %q\n\n%s", noun, verb, usage)
	return 2
}

// nounUsage maps each noun to its verb + flag reference. Every verb also
// answers `--help` itself (via its flag set) with the same flag list.
var nounUsage = map[string]string{
	"agents": `Usage: singl agents <verb> [flags]

Verbs:
  list       [--full]                                     list all agents (compact JSON; --full adds task prompt)
  get        --id <id> [--last N] [--full]                one agent snapshot (+ last N output entries)
  spawn      --workdir <dir> --prompt <task> [--model M] [--effort low|medium|high]
             [--smart-route] [--max-turns N] [--timeout SECS] [--backend claude|pi]
  resume     --id <id> --message <text> [spawn flags]     new agent inheriting history
  kill       --id <id>                                    terminate the agent subprocess
  remove     --id <id>                                    drop the agent from the registry
  output     --id <id> [--offset N] [--tail N]            buffered output entries
  input      --id <id> --message <text>                   send input to a running agent
  wait       --id <id> [--id ...] [--timeout SECS] [--interval SECS] [--any]
                                                          block until agent(s) reach a terminal state
  watch      --id <id>                                    stream live output (no --json)
  watch-all                                               stream all agents' events (no --json)
  chat       --id <id> --message <text>                   send input then stream the response (no --json)
  stats                                                   engine-wide counters
`,
	"worktrees": `Usage: singl worktrees <verb> [flags]

Verbs:
  list    [--repo P]
  create  --repo P --path <dir> --branch <name> [--create-branch] [--start-point REF]
  remove  --repo P --path <dir> [--force]
  lock    --repo P --path <dir>
  unlock  --repo P --path <dir>
  prune   --repo P
`,
	"branches": `Usage: singl branches <verb> [flags]

Verbs:
  list      [--repo P]
  checkout  --repo P --branch <name>
  create    --repo P --branch <name> [--start-point REF]
  delete    --repo P --branch <name> [--force]
  head      [--repo P]
  compare   --repo P --base <ref> --head <ref>
`,
	"repos": `Usage: singl repos <verb> [flags]

Verbs:
  info  [--repo P]
  open  --path <dir>
  find  --path <dir>       find the enclosing git root
`,
	"diff": `Usage: singl diff <verb> [flags]

Verbs:
  workdir     [--repo P]                                  working-tree summary (staged + unstaged)
  branch      --repo P --base <ref> --head <ref>          file-level branch diff
  file        --repo P --base <ref> --head <ref> --file F text patch for one file
  staged      --repo P --file F                           staged diff for one file
  unstaged    --repo P --file F                           unstaged diff for one file
  merge-base  --repo P --base <ref> --head <ref>          common ancestor SHA
  all-repos   --project <handle>                          workdir summary for every repo
`,
	"commit": `Usage: singl commit <verb> [flags]

Verbs:
  suggest      [--repo P]                       AI one-line message (falls back to unstaged diff)
  generate     [--repo P]                       structured AI message (type/scope/subject/body)
  stage        --repo P --file F [--file F2 ...] | --all
                                                stage files into the index (git add)
  create       --repo P --message "..."         commit the staged changes, returns hash
  files        --repo P --hash <sha>            files touched by a commit
  diff         --repo P --hash <sha>            full diff of a commit
  file-diff    --repo P --hash <sha> --file F   one file's diff in a commit
  cherry-pick  --repo P --hash <sha>
  reset        --repo P --hash <sha> [--mode soft|mixed|hard]
  amend        --repo P --message "..."         rewrite the last commit's message
`,
	"mr": `Usage: singl mr <verb> [flags]

Verbs:
  title   --repo P --source <branch> --target <branch>   AI-generated MR title
  desc    --repo P --source <branch> --target <branch>   AI-generated MR description
  create  --repo P --source <branch> --target <branch> --title "..." [--desc "..."] [--reviewers a,b]
                                                          create MR via forge API
  cli     --repo P [--base <branch>]                      create MR via gh/glab CLI
`,
	"rebase": `Usage: singl rebase <verb> [flags]

Verbs:
  plan       --repo P --base <ref> --current <ref>   commits that would be rebased
  status     [--repo P]                               is a rebase in progress?
  continue   [--repo P]
  skip       [--repo P]
  abort      [--repo P]
  onto-main  [--repo P]                               streaming rebase onto origin/main
  todo       --repo P --base <ref> --current <ref>    AI-generated interactive-rebase todo
  context    --repo P --main <branch> [--conflicts f1,f2]
`,
	"stash": `Usage: singl stash <verb> [flags]

Verbs:
  list       [--repo P]
  get        --repo P --index N
  create     [--repo P] [--message "..."] [--untracked]
  apply      --repo P --index N [--pop]
  pop        --repo P --index N
  drop       --repo P --index N
  clear      --repo P
  list-all   --project <handle>
  all        --project <handle> [--message "..."] [--untracked]   stash in every repo
  apply-all  --project <handle> [--pop] [--message "..."]
`,
	"sync": `Usage: singl sync <verb> [flags]

Verbs:
  fetch            [--repo P] [--remote R]     (streaming)
  pull             [--repo P]                  (streaming)
  push             [--repo P] [--force]        (streaming)
  pull-rebase      [--repo P]                  (streaming)
  set-upstream     [--repo P] [--remote R]
  upstream-status  [--repo P]
  last-fetch       [--repo P]
  all              --project <handle> [--force]
`,
	"pipeline": `Usage: singl pipeline <verb> [flags]

Verbs:
  status  [--repo P] [--branch <name>]   CI pipeline status (all tracked branches if no --branch)
`,
	"project": `Usage: singl project <verb> [flags]

Verbs:
  list                                        configured project keys
  load          --name <key>                  load a project, returns its handle
  status        --project <handle>
  info          --project <handle>
  refresh       --project <handle>
  branch-check  --project <handle> --branch <name>
  context       --project <handle>
  workflows     list|create|discover ...      multi-repo feature workflows:
    workflows list     --project <handle>
    workflows create   --project <handle> --branch <name> [--base-dir D]
    workflows discover --project <handle>    (streaming)
`,
	"forge": `Usage: singl forge <verb> [flags]

Verbs:
  info                   detected forge (provider, API URL, user) without token
  auth                   full auth detection result
  provider  [--repo P]   provider for one repo's origin remote (github/gitlab)
`,
	"jira": `Usage: singl jira <verb> [flags]

Verbs:
  search   --jql <query> [--max N]
  get      --key PROJ-123
  mine     [--project KEY]
  create   --project KEY --summary "..." [--type Task] [--desc "..."] [--priority P]
  update   --key PROJ-123 --field <name> --value <v>
  comment  --key PROJ-123 --body "..."
  link     --from PROJ-1 --to PROJ-2 --type blocks
  ai       refine|stories|review ...   AI workflows that spawn an agent
`,
}
