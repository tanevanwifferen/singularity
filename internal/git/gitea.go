package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// teaBin is Gitea's official CLI. Everything we do against a Gitea or Forgejo
// instance goes through it, so tea owns the credentials and we never read its
// token file.
const teaBin = "tea"

// giteaProbeTimeout bounds the unauthenticated instance probe. Detection runs
// on hosts we know nothing about, so this is the only thing standing between
// DetectRemoteProvider and a hung UI.
const giteaProbeTimeout = 2 * time.Second

// teaTimeout bounds an authenticated tea invocation (these talk to the
// instance, so they need more room than the probe).
const teaTimeout = 30 * time.Second

// --- test seams -------------------------------------------------------------
//
// The package has no dependency-injection container; these package-level vars
// are the seam. Tests swap them out (and restore via t.Cleanup); production
// code must never reassign them.

// cliResult is the outcome of one forge CLI invocation. Stdout stays separate
// from Stderr so JSON responses remain parsable when tea logs to stderr.
type cliResult struct {
	Stdout string
	Stderr string
	Err    error
}

// Message returns the most useful human-readable line from a run, preferring
// stderr (where tea reports errors) over stdout.
func (r cliResult) Message() string {
	for _, s := range []string{r.Stderr, r.Stdout} {
		if t := strings.TrimSpace(s); t != "" {
			return firstLine(t)
		}
	}
	if r.Err != nil {
		return r.Err.Error()
	}
	return "no output"
}

// runForgeCommand runs a forge CLI in dir (empty = the process working
// directory) with a timeout, and never returns until the command is reaped.
var runForgeCommand = func(dir, name string, args ...string) cliResult {
	ctx, cancel := context.WithTimeout(context.Background(), teaTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return cliResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

// hasForgeCLI reports whether a forge CLI is on $PATH.
var hasForgeCLI = func(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// giteaProbeClient performs the unauthenticated instance probe. Its timeout is
// what keeps provider detection from ever hanging on an unreachable host.
var giteaProbeClient = &http.Client{Timeout: giteaProbeTimeout}

// --- tea login discovery ----------------------------------------------------

// teaLogin mirrors one entry of `tea logins list --output json`.
type teaLogin struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	SSHHost string `json:"ssh_host"`
	User    string `json:"user"`
	Default string `json:"default"`
}

// isDefault reports whether this is tea's default login. tea emits the flag as
// the string "true", not a JSON bool.
func (l teaLogin) isDefault() bool {
	return strings.EqualFold(strings.TrimSpace(l.Default), "true")
}

// host returns the hostname the login points at.
func (l teaLogin) host() string {
	if h := hostFromURL(l.URL); h != "" {
		return h
	}
	return strings.ToLower(strings.TrimSpace(l.SSHHost))
}

// teaLogins reads the logins tea has stored locally. This is a config-file
// read — no network — which is what makes it cheap enough for detection.
// Returns an empty slice (not an error) when tea is installed but has no
// logins configured.
func teaLogins() ([]teaLogin, error) {
	if !hasForgeCLI(teaBin) {
		return nil, errTeaNotInstalled("")
	}
	res := runForgeCommand("", teaBin, "logins", "list", "--output", "json")
	out := strings.TrimSpace(res.Stdout)
	if res.Err != nil {
		// tea exits non-zero with "no available login" on a fresh install.
		if noLoginsConfigured(res) {
			return nil, nil
		}
		return nil, fmt.Errorf("tea logins list failed: %s", res.Message())
	}
	if out == "" || noLoginsConfigured(res) {
		return nil, nil
	}
	var logins []teaLogin
	if err := json.Unmarshal([]byte(out), &logins); err != nil {
		return nil, fmt.Errorf("tea logins list: could not parse output: %w", err)
	}
	return logins, nil
}

// noLoginsConfigured recognises tea's "nothing set up yet" response.
func noLoginsConfigured(res cliResult) bool {
	combined := strings.ToLower(res.Stdout + res.Stderr)
	return strings.Contains(combined, "no available login")
}

// teaLoginForHost returns the tea login configured for host, if any.
func teaLoginForHost(host string) (teaLogin, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return teaLogin{}, false
	}
	logins, err := teaLogins()
	if err != nil {
		return teaLogin{}, false
	}
	for _, l := range logins {
		if l.host() == host || strings.EqualFold(strings.TrimSpace(l.SSHHost), host) {
			return l, true
		}
	}
	return teaLogin{}, false
}

// teaDefaultLogin returns tea's default login, falling back to the first one.
func teaDefaultLogin() (teaLogin, bool) {
	logins, err := teaLogins()
	if err != nil || len(logins) == 0 {
		return teaLogin{}, false
	}
	for _, l := range logins {
		if l.isDefault() {
			return l, true
		}
	}
	return logins[0], true
}

// --- instance probing -------------------------------------------------------

// giteaProbeCache memoises probe verdicts per host. Detection runs once per
// repo (and once per branch during a pipeline sweep), so without this a single
// unreachable host would cost a fresh 2s timeout every time.
var (
	giteaProbeMu    sync.Mutex
	giteaProbeCache = map[string]bool{}
)

// resetGiteaProbeCache clears the memo. Test helper.
func resetGiteaProbeCache() {
	giteaProbeMu.Lock()
	defer giteaProbeMu.Unlock()
	giteaProbeCache = map[string]bool{}
}

// probeGiteaHost reports whether host looks like a Gitea or Forgejo instance.
// It consults tea's local config first (free, no network); only when that says
// nothing does it make one short unauthenticated GET of the version endpoint.
// Every failure — including the timeout — means "not Gitea", so the caller
// falls back to ProviderUnknown rather than blocking.
func probeGiteaHost(scheme, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if scheme != "http" {
		scheme = "https"
	}

	giteaProbeMu.Lock()
	cached, ok := giteaProbeCache[host]
	giteaProbeMu.Unlock()
	if ok {
		return cached
	}

	verdict := false
	if _, found := teaLoginForHost(host); found {
		verdict = true
	} else {
		verdict = giteaVersionEndpointOK(scheme, host)
	}

	giteaProbeMu.Lock()
	giteaProbeCache[host] = verdict
	giteaProbeMu.Unlock()
	return verdict
}

// giteaVersionEndpointOK does the one network call detection is allowed to
// make: GET <scheme>://<host>/api/v1/version, which every Gitea and Forgejo
// instance answers with {"version":"..."} without authentication.
func giteaVersionEndpointOK(scheme, host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), giteaProbeTimeout)
	defer cancel()

	url := fmt.Sprintf("%s://%s/api/v1/version", scheme, host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")

	resp, err := giteaProbeClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	return strings.TrimSpace(body.Version) != ""
}

// --- status & actionable errors ---------------------------------------------

// GiteaStatus describes the local tea setup for one repository's host.
type GiteaStatus struct {
	Host      string `json:"host"`
	Installed bool   `json:"installed"`
	HasLogin  bool   `json:"has_login"`
	LoginName string `json:"login_name,omitempty"`
	User      string `json:"user,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// GiteaCLIStatus reports whether tea is installed and whether it has a login
// for the repo's origin host, with an actionable hint when it does not.
func GiteaCLIStatus(repoPath string) *GiteaStatus {
	host := hostFromURL(originRemoteURL(repoPath))
	st := &GiteaStatus{Host: host, Installed: hasForgeCLI(teaBin)}
	if !st.Installed {
		st.Hint = teaInstallHint(host)
		return st
	}
	login, ok := teaLoginForHost(host)
	if !ok {
		st.Hint = teaLoginHint(host)
		return st
	}
	st.HasLogin = true
	st.LoginName = login.Name
	st.User = login.User
	return st
}

// teaInstallHint phrases the "tea is missing" remedy.
func teaInstallHint(host string) string {
	if host == "" {
		return "tea is not installed — install Gitea's CLI (https://gitea.com/gitea/tea) to use Gitea repositories"
	}
	return fmt.Sprintf("tea is not installed — install Gitea's CLI (https://gitea.com/gitea/tea) to use %s", host)
}

// teaLoginHint phrases the "no login for this host" remedy as the exact
// command to run.
func teaLoginHint(host string) string {
	if host == "" {
		return "no tea login configured — run: tea logins add --name <host> --url https://<host> --token <token>"
	}
	return fmt.Sprintf("no tea login for %s — run: tea logins add --name %s --url https://%s --token <token>",
		host, host, host)
}

// errTeaNotInstalled reports a missing tea binary in the package's error style.
func errTeaNotInstalled(host string) error {
	return fmt.Errorf("tea not installed: %s", teaInstallHint(host))
}

// errTeaNoLogin reports that tea has no credentials for host.
func errTeaNoLogin(host string) error {
	return fmt.Errorf("tea not authenticated: %s", teaLoginHint(host))
}

// errGiteaRepoNotFound reports that the instance does not know this repo.
func errGiteaRepoNotFound(owner, repo, host string) error {
	return fmt.Errorf("repository %s/%s not found on %s: check the origin remote and that your tea login has access",
		owner, repo, host)
}

// looksLikeNotFound recognises a 404 in tea's output.
func looksLikeNotFound(res cliResult) bool {
	combined := strings.ToLower(res.Stdout + res.Stderr)
	return strings.Contains(combined, "404") || strings.Contains(combined, "not found")
}

// --- repository context -----------------------------------------------------

// giteaRepo is the resolved context needed for any tea call: which instance,
// which repository, and which login to use.
type giteaRepo struct {
	Host  string
	Owner string
	Name  string
	Login teaLogin
}

// resolveGiteaRepo derives the Gitea context for a repo path, returning an
// actionable error when tea is missing, has no login for the host, or the
// origin remote cannot be parsed.
func resolveGiteaRepo(repoPath string) (*giteaRepo, error) {
	url := originRemoteURL(repoPath)
	host, owner, name := parseRemoteURL(url)
	if host == "" || owner == "" || name == "" {
		return nil, fmt.Errorf("could not determine Gitea owner/repo from origin remote %q", url)
	}
	if !hasForgeCLI(teaBin) {
		return nil, errTeaNotInstalled(host)
	}
	login, ok := teaLoginForHost(host)
	if !ok {
		return nil, errTeaNoLogin(host)
	}
	return &giteaRepo{Host: host, Owner: owner, Name: name, Login: login}, nil
}

// --- pull requests ----------------------------------------------------------

// giteaPullCreateArgs builds the argument list for `tea pulls create`. Flag
// names are taken verbatim from `tea pulls create --help` (tea 0.15.1):
// --head / --base / --title / --description / --assignees. Ordering is fixed
// so the command is assertable in tests.
func giteaPullCreateArgs(source, target, title, description, assignee string) []string {
	args := []string{"pulls", "create"}
	if source != "" {
		args = append(args, "--head", source)
	}
	if target != "" {
		args = append(args, "--base", target)
	}
	args = append(args, "--title", title, "--description", description)
	if assignee != "" {
		args = append(args, "--assignees", assignee)
	}
	return args
}

// createGiteaPull opens a pull request through tea and returns its URL.
// An empty source lets tea default to the current branch; an empty target lets
// it default to the repository's default branch.
func createGiteaPull(repoPath, source, target, title, description string) (string, *giteaRepo, error) {
	gr, err := resolveGiteaRepo(repoPath)
	if err != nil {
		return "", nil, err
	}

	// tea has no "@me" token like gh/glab; the login's own user is the
	// equivalent.
	args := giteaPullCreateArgs(source, target, title, description, gr.Login.User)
	res := runForgeCommand(repoPath, teaBin, args...)
	if res.Err != nil {
		if looksLikeNotFound(res) {
			return "", nil, errGiteaRepoNotFound(gr.Owner, gr.Name, gr.Host)
		}
		return "", nil, fmt.Errorf("tea pulls create failed: %s", res.Message())
	}
	return extractURL(res.Stdout + "\n" + res.Stderr), gr, nil
}

// giteaPullNumber extracts the pull index from a Gitea PR URL
// (https://host/owner/repo/pulls/42).
func giteaPullNumber(url string) int {
	idx := strings.LastIndex(url, "/pulls/")
	if idx < 0 {
		return 0
	}
	tail := url[idx+len("/pulls/"):]
	if cut := strings.IndexAny(tail, "/?#"); cut >= 0 {
		tail = tail[:cut]
	}
	n, err := strconv.Atoi(strings.TrimSpace(tail))
	if err != nil {
		return 0
	}
	return n
}

// --- authenticated API access via tea ---------------------------------------

// teaAPI performs an authenticated Gitea API GET/POST through `tea api`, which
// reuses the stored login so we never handle the token ourselves. The path is
// relative to /api/v1 (tea prefixes it).
func teaAPI(repoPath string, gr *giteaRepo, method, path string, out interface{}) error {
	args := []string{"api", "--method", method}
	if gr.Login.Name != "" {
		args = append(args, "--login", gr.Login.Name)
	}
	args = append(args, path)

	res := runForgeCommand(repoPath, teaBin, args...)
	if res.Err != nil {
		if looksLikeNotFound(res) {
			return errGiteaAPIUnavailable(path, res)
		}
		return fmt.Errorf("tea api %s failed: %s", path, res.Message())
	}
	if out == nil {
		return nil
	}
	body := strings.TrimSpace(res.Stdout)
	if body == "" {
		return fmt.Errorf("tea api %s returned an empty response", path)
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return fmt.Errorf("tea api %s: could not parse response: %w", path, err)
	}
	return nil
}

// errGiteaNoActions marks an instance that does not expose the Actions API.
type errGiteaNoActions struct {
	path   string
	detail string
}

func (e *errGiteaNoActions) Error() string {
	return fmt.Sprintf("Gitea Actions is not available on this instance (%s): %s", e.path, e.detail)
}

// errGiteaAPIUnavailable turns a 404 into the not-supported signal callers use
// to report "no Actions here" instead of an empty pipeline.
func errGiteaAPIUnavailable(path string, res cliResult) error {
	return &errGiteaNoActions{path: path, detail: res.Message()}
}

// --- Gitea Actions ----------------------------------------------------------

// giteaActionRun mirrors the fields of Gitea's ActionWorkflowRun that we use.
// Field names match the Gitea API (as shipped in tea's SDK): the list response
// is {"total_count":N,"workflow_runs":[...]}, and a run carries `status` only
// — there is no separate `conclusion` as on GitHub.
type giteaActionRun struct {
	ID           int64  `json:"id"`
	HeadBranch   string `json:"head_branch"`
	HeadSHA      string `json:"head_sha"`
	Status       string `json:"status"`
	DisplayTitle string `json:"display_title"`
	RunNumber    int64  `json:"run_number"`
	HTMLURL      string `json:"html_url"`
	URL          string `json:"url"`
	StartedAt    string `json:"run_started_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// giteaActionRuns is the list response shape.
type giteaActionRuns struct {
	TotalCount int64            `json:"total_count"`
	Runs       []giteaActionRun `json:"workflow_runs"`
}

// giteaActionRunsPath builds the Actions list endpoint. The branch filter is
// sent as a query parameter and re-applied client-side, so the result is
// correct whether or not the instance honours it.
func giteaActionRunsPath(owner, repo, branch string) string {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs?limit=30", owner, repo)
	if branch != "" {
		path += "&branch=" + branch
	}
	return path
}

// latestGiteaRun returns the most recent Actions run for a branch, or nil when
// the branch has never run.
func latestGiteaRun(repoPath, branch string, gr *giteaRepo) (*giteaActionRun, error) {
	var resp giteaActionRuns
	path := giteaActionRunsPath(gr.Owner, gr.Name, branch)
	if err := teaAPI(repoPath, gr, http.MethodGet, path, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Runs {
		run := resp.Runs[i]
		if branch != "" && run.HeadBranch != "" && run.HeadBranch != branch {
			continue
		}
		return &run, nil
	}
	return nil, nil
}

// giteaRunStatus maps Gitea's Actions run states onto PipelineStatus. Gitea
// uses waiting/blocked before a run starts and failure/cancelled at the end,
// none of which match our vocabulary literally.
func giteaRunStatus(status string) PipelineStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return PipelineSuccess
	case "failure", "failed", "error":
		return PipelineFailed
	case "running", "in_progress":
		return PipelineRunning
	case "waiting", "blocked", "queued", "pending":
		return PipelinePending
	case "cancelled", "canceled":
		return PipelineCanceled
	case "skipped":
		return PipelineSkipped
	default:
		return PipelineStatus(strings.ToLower(strings.TrimSpace(status)))
	}
}

// toPipeline converts a Gitea Actions run into the shared Pipeline shape.
func (r *giteaActionRun) toPipeline(branch string) *Pipeline {
	ref := r.HeadBranch
	if ref == "" {
		ref = branch
	}
	status := giteaRunStatus(r.Status)
	webURL := r.HTMLURL
	if webURL == "" {
		webURL = r.URL
	}
	name := r.DisplayTitle
	if name == "" {
		name = fmt.Sprintf("run #%d", r.RunNumber)
	}

	p := &Pipeline{
		ID:     r.ID,
		Ref:    ref,
		SHA:    r.HeadSHA,
		Status: status,
		WebURL: webURL,
		Jobs:   []Job{{ID: r.ID, Name: name, Status: status, WebURL: webURL}},
	}
	for _, ts := range []string{r.StartedAt, r.CreatedAt} {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			p.CreatedAt = t
			break
		}
	}
	if t, err := time.Parse(time.RFC3339, r.UpdatedAt); err == nil {
		p.UpdatedAt = t
	}
	return p
}

// giteaPipelineStatus reports the Actions state of a branch on a Gitea remote.
// A missing Actions API yields an explicit "not supported" detail rather than
// an empty result that reads like "no pipelines".
func giteaPipelineStatus(repoPath, branch string) (*PipelineInfo, error) {
	info := &PipelineInfo{Branch: branch}

	gr, err := resolveGiteaRepo(repoPath)
	if err != nil {
		info.Detail = err.Error()
		return info, err
	}

	run, err := latestGiteaRun(repoPath, branch, gr)
	if err != nil {
		var noActions *errGiteaNoActions
		if errors.As(err, &noActions) {
			// A 404 here is either "Actions is off on this instance" or
			// "your login cannot see this repo" — name both.
			info.Detail = fmt.Sprintf(
				"Gitea Actions not available for %s/%s on %s (Actions disabled, or the repository is not visible to your tea login)",
				gr.Owner, gr.Name, gr.Host)
			return info, nil
		}
		info.Detail = err.Error()
		return info, err
	}
	if run == nil {
		return info, nil
	}

	info.Pipeline = run.toPipeline(branch)
	info.HasPipeline = true
	info.Status = info.Pipeline.Status
	return info, nil
}

// retryGiteaPipeline re-runs the latest Actions run for a branch.
func retryGiteaPipeline(repoPath, branch string) error {
	gr, err := resolveGiteaRepo(repoPath)
	if err != nil {
		return err
	}
	run, err := latestGiteaRun(repoPath, branch, gr)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("no pipeline found for branch %s", branch)
	}
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/rerun", gr.Owner, gr.Name, run.ID)
	if err := teaAPI(repoPath, gr, http.MethodPost, path, nil); err != nil {
		return fmt.Errorf("retry gitea pipeline: %w", err)
	}
	return nil
}

// --- small helpers ----------------------------------------------------------

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
}
