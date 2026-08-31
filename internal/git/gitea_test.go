package git

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// recordedCall is one invocation captured by the fake CLI runner.
type recordedCall struct {
	Dir  string
	Name string
	Args []string
}

// fakeCLI installs a fake forge-CLI runner for the duration of a test. The
// handler receives each call and returns what the CLI would have printed;
// returning a zero cliResult means "succeeded with no output". Calls are
// recorded in order.
func fakeCLI(t *testing.T, handler func(recordedCall) cliResult) *[]recordedCall {
	t.Helper()
	calls := &[]recordedCall{}
	prev := runForgeCommand
	runForgeCommand = func(dir, name string, args ...string) cliResult {
		*calls = append(*calls, recordedCall{Dir: dir, Name: name, Args: append([]string(nil), args...)})
		if handler == nil {
			return cliResult{}
		}
		return handler(recordedCall{Dir: dir, Name: name, Args: args})
	}
	t.Cleanup(func() { runForgeCommand = prev })
	return calls
}

// installedCLIs restricts the set of binaries the package believes are on
// $PATH, so no test ever depends on whether tea/gh/glab is really installed.
func installedCLIs(t *testing.T, names ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	prev := hasForgeCLI
	hasForgeCLI = func(name string) bool { return set[name] }
	t.Cleanup(func() { hasForgeCLI = prev })
}

// stubRemote pins the origin URL and git config values a repo reports.
func stubRemote(t *testing.T, originURL string, config map[string]string) {
	t.Helper()
	prevOrigin, prevConfig := originRemoteURL, gitConfigGet
	originRemoteURL = func(string) string { return originURL }
	gitConfigGet = func(_, key string) string { return config[key] }
	t.Cleanup(func() {
		originRemoteURL, gitConfigGet = prevOrigin, prevConfig
	})
	resetGiteaProbeCache()
	t.Cleanup(resetGiteaProbeCache)
}

// teaLoginsJSON is what `tea logins list --output json` prints.
func teaLoginsJSON(logins ...teaLogin) string {
	b, err := json.Marshal(logins)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- detection layering -----------------------------------------------------

func TestProviderFromURLKnownHosts(t *testing.T) {
	// The probe must never fire for a host we already recognise, so no CLI is
	// installed and no HTTP client is reachable during this test.
	installedCLIs(t)
	fakeCLI(t, nil)
	resetGiteaProbeCache()
	t.Cleanup(resetGiteaProbeCache)

	tests := []struct {
		url      string
		expected RemoteProvider
	}{
		// GitHub / GitLab must behave exactly as before.
		{"https://github.com/owner/repo.git", ProviderGitHub},
		{"git@github.com:owner/repo.git", ProviderGitHub},
		{"https://gitlab.com/group/repo.git", ProviderGitLab},
		{"git@gitlab.example.com:group/repo.git", ProviderGitLab},
		// Gitea / Forgejo known hosts.
		{"https://codeberg.org/owner/repo.git", ProviderGitea},
		{"git@codeberg.org:owner/repo.git", ProviderGitea},
		{"https://gitea.com/owner/repo.git", ProviderGitea},
		{"https://gitea.example.com/owner/repo.git", ProviderGitea},
		{"https://forgejo.example.com/owner/repo.git", ProviderGitea},
		// Nothing recognisable and nothing to probe with.
		{"https://git.example.com/owner/repo.git", ProviderUnknown},
		{"", ProviderUnknown},
	}

	for _, tt := range tests {
		if got := providerFromURL(tt.url); got != tt.expected {
			t.Errorf("providerFromURL(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

func TestParseForgeOverride(t *testing.T) {
	tests := []struct {
		value    string
		expected RemoteProvider
	}{
		{"gitea", ProviderGitea},
		{"Forgejo", ProviderGitea},
		{"  tea  ", ProviderGitea},
		{"github", ProviderGitHub},
		{"gh", ProviderGitHub},
		{"gitlab", ProviderGitLab},
		{"glab", ProviderGitLab},
		{"", ProviderUnknown},
		{"bitbucket", ProviderUnknown},
	}
	for _, tt := range tests {
		if got := parseForgeOverride(tt.value); got != tt.expected {
			t.Errorf("parseForgeOverride(%q) = %q, want %q", tt.value, got, tt.expected)
		}
	}
}

func TestDetectRemoteProviderConfigOverrideWins(t *testing.T) {
	installedCLIs(t)
	fakeCLI(t, nil)
	// A textbook GitHub URL, pinned to Gitea by the per-repo config key.
	stubRemote(t, "https://github.com/owner/repo.git", map[string]string{
		ForgeOverrideConfigKey: "gitea",
	})

	if got := DetectRemoteProvider("/repo"); got != ProviderGitea {
		t.Errorf("DetectRemoteProvider = %q, want %q (config override must win over host sniffing)", got, ProviderGitea)
	}
}

func TestDetectRemoteProviderEnvOverrideWins(t *testing.T) {
	installedCLIs(t)
	fakeCLI(t, nil)
	stubRemote(t, "https://git.example.com/owner/repo.git", nil)
	t.Setenv(ForgeOverrideEnv, "gitea")

	if got := DetectRemoteProvider("/repo"); got != ProviderGitea {
		t.Errorf("DetectRemoteProvider = %q, want %q (env override must resolve the host)", got, ProviderGitea)
	}
}

func TestDetectRemoteProviderConfigBeatsEnv(t *testing.T) {
	installedCLIs(t)
	fakeCLI(t, nil)
	stubRemote(t, "https://git.example.com/owner/repo.git", map[string]string{
		ForgeOverrideConfigKey: "gitea",
	})
	t.Setenv(ForgeOverrideEnv, "github")

	if got := DetectRemoteProvider("/repo"); got != ProviderGitea {
		t.Errorf("DetectRemoteProvider = %q, want %q (per-repo config is more specific than the env var)", got, ProviderGitea)
	}
}

func TestDetectRemoteProviderProbeFallbackViaTeaLogin(t *testing.T) {
	// An anonymous host, resolved by the free half of the probe: tea already
	// has a login for it.
	installedCLIs(t, teaBin)
	calls := fakeCLI(t, func(c recordedCall) cliResult {
		return cliResult{Stdout: teaLoginsJSON(teaLogin{
			Name: "internal", URL: "https://git.example.com", User: "dev", Default: "true",
		})}
	})
	stubRemote(t, "git@git.example.com:owner/repo.git", nil)

	if got := DetectRemoteProvider("/repo"); got != ProviderGitea {
		t.Fatalf("DetectRemoteProvider = %q, want %q", got, ProviderGitea)
	}
	if len(*calls) == 0 {
		t.Fatal("expected the probe to consult tea's login list")
	}
	want := []string{"logins", "list", "--output", "json"}
	if !reflect.DeepEqual((*calls)[0].Args, want) {
		t.Errorf("probe ran %v, want %v", (*calls)[0].Args, want)
	}
}

func TestDetectRemoteProviderProbeFallbackViaVersionEndpoint(t *testing.T) {
	// tea knows nothing about the host, so the probe falls through to the
	// unauthenticated version endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"version":"1.24.0"}`))
	}))
	defer srv.Close()

	installedCLIs(t, teaBin)
	fakeCLI(t, func(recordedCall) cliResult { return cliResult{Stdout: "[]"} })

	prev := giteaProbeClient
	giteaProbeClient = srv.Client()
	t.Cleanup(func() { giteaProbeClient = prev })

	host := strings.TrimPrefix(srv.URL, "http://")
	stubRemote(t, "http://"+host+"/owner/repo.git", nil)

	if got := DetectRemoteProvider("/repo"); got != ProviderGitea {
		t.Errorf("DetectRemoteProvider = %q, want %q", got, ProviderGitea)
	}
}

func TestDetectRemoteProviderUnknownStaysUnknown(t *testing.T) {
	// The version endpoint answers, but not like a Gitea instance.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	installedCLIs(t, teaBin)
	fakeCLI(t, func(recordedCall) cliResult { return cliResult{Stdout: "[]"} })

	prev := giteaProbeClient
	giteaProbeClient = srv.Client()
	t.Cleanup(func() { giteaProbeClient = prev })

	host := strings.TrimPrefix(srv.URL, "http://")
	stubRemote(t, "http://"+host+"/owner/repo.git", nil)

	if got := DetectRemoteProvider("/repo"); got != ProviderUnknown {
		t.Errorf("DetectRemoteProvider = %q, want %q", got, ProviderUnknown)
	}
}

func TestDetectRemoteProviderNoOriginIsUnknown(t *testing.T) {
	installedCLIs(t)
	fakeCLI(t, nil)
	stubRemote(t, "", nil)

	if got := DetectRemoteProvider("/repo"); got != ProviderUnknown {
		t.Errorf("DetectRemoteProvider = %q, want %q", got, ProviderUnknown)
	}
}

func TestProbeGiteaHostIsMemoized(t *testing.T) {
	installedCLIs(t, teaBin)
	calls := fakeCLI(t, func(recordedCall) cliResult { return cliResult{Stdout: "[]"} })
	resetGiteaProbeCache()
	t.Cleanup(resetGiteaProbeCache)

	prev := giteaProbeClient
	giteaProbeClient = &http.Client{Transport: failingTransport{}}
	t.Cleanup(func() { giteaProbeClient = prev })

	for i := 0; i < 3; i++ {
		if probeGiteaHost("https", "git.example.com") {
			t.Fatal("expected an unreachable host not to be classified as Gitea")
		}
	}
	if len(*calls) != 1 {
		t.Errorf("probe consulted tea %d times, want 1 (verdict should be memoized per host)", len(*calls))
	}
}

// failingTransport makes every probe request fail without touching the network.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network disabled in tests")
}

// --- remote URL parsing -----------------------------------------------------

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		url               string
		host, owner, repo string
	}{
		{"https://gitea.example.com/acme/widget.git", "gitea.example.com", "acme", "widget"},
		{"https://gitea.example.com/acme/widget", "gitea.example.com", "acme", "widget"},
		{"git@gitea.example.com:acme/widget.git", "gitea.example.com", "acme", "widget"},
		{"ssh://git@gitea.example.com:2222/acme/widget.git", "gitea.example.com", "acme", "widget"},
		{"http://Gitea.Example.COM/acme/widget", "gitea.example.com", "acme", "widget"},
		// A self-hosted web port belongs to the host; an ssh:// port does not.
		{"https://git.example.com:3000/acme/widget.git", "git.example.com:3000", "acme", "widget"},
		{"ssh://git@git.example.com:2222/acme/widget.git", "git.example.com", "acme", "widget"},
		{"https://gitea.example.com/widget", "gitea.example.com", "", ""},
		{"", "", "", ""},
	}
	for _, tt := range tests {
		host, owner, repo := parseRemoteURL(tt.url)
		if host != tt.host || owner != tt.owner || repo != tt.repo {
			t.Errorf("parseRemoteURL(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tt.url, host, owner, repo, tt.host, tt.owner, tt.repo)
		}
	}
}

func TestSchemeAndHost(t *testing.T) {
	tests := []struct {
		url, scheme, host string
	}{
		{"http://git.example.com/a/b", "http", "git.example.com"},
		{"https://git.example.com/a/b", "https", "git.example.com"},
		// SSH remotes still probe over https — there is no SSH API endpoint.
		{"git@git.example.com:a/b.git", "https", "git.example.com"},
		{"http://git.example.com:3000/a/b", "http", "git.example.com:3000"},
	}
	for _, tt := range tests {
		scheme, host := schemeAndHost(tt.url)
		if scheme != tt.scheme || host != tt.host {
			t.Errorf("schemeAndHost(%q) = (%q,%q), want (%q,%q)", tt.url, scheme, host, tt.scheme, tt.host)
		}
	}
}

// --- tea command construction ------------------------------------------------

func TestGiteaPullCreateArgs(t *testing.T) {
	got := giteaPullCreateArgs("feature/x", "main", "Add widget", "## Summary\n\n- adds it", "dev")
	want := []string{
		"pulls", "create",
		"--head", "feature/x",
		"--base", "main",
		"--title", "Add widget",
		"--description", "## Summary\n\n- adds it",
		"--assignees", "dev",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("giteaPullCreateArgs = %v, want %v", got, want)
	}
}

func TestGiteaPullCreateArgsOmitsEmptyFlags(t *testing.T) {
	// No source, target or assignee: tea's own defaults must be left alone.
	got := giteaPullCreateArgs("", "", "T", "D", "")
	want := []string{"pulls", "create", "--title", "T", "--description", "D"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("giteaPullCreateArgs = %v, want %v", got, want)
	}
}

func TestCreateGiteaPRBuildsTeaCommand(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
	calls := fakeCLI(t, func(c recordedCall) cliResult {
		if len(c.Args) > 1 && c.Args[0] == "logins" {
			return cliResult{Stdout: teaLoginsJSON(teaLogin{
				Name: "example", URL: "https://gitea.example.com", User: "dev", Default: "true",
			})}
		}
		return cliResult{Stdout: "https://gitea.example.com/acme/widget/pulls/42\n"}
	})

	mr, err := createGiteaPR("/repo", "feature/x", "main", "Add widget", "body text")
	if err != nil {
		t.Fatalf("createGiteaPR failed: %v", err)
	}

	var create recordedCall
	for _, c := range *calls {
		if len(c.Args) > 1 && c.Args[0] == "pulls" {
			create = c
		}
	}
	if create.Name != teaBin {
		t.Fatalf("expected a %s invocation, got calls %+v", teaBin, *calls)
	}
	if create.Dir != "/repo" {
		t.Errorf("tea ran in %q, want the repo path so it can discover the login from the remote", create.Dir)
	}
	want := []string{
		"pulls", "create",
		"--head", "feature/x",
		"--base", "main",
		"--title", "Add widget",
		"--description", "body text",
		"--assignees", "dev",
	}
	if !reflect.DeepEqual(create.Args, want) {
		t.Errorf("tea args = %v, want %v", create.Args, want)
	}

	if mr.Number != 42 {
		t.Errorf("MR number = %d, want 42", mr.Number)
	}
	if mr.URL != "https://gitea.example.com/acme/widget/pulls/42" {
		t.Errorf("MR URL = %q", mr.URL)
	}
	if mr.Author != "dev" {
		t.Errorf("MR author = %q, want dev", mr.Author)
	}
	if mr.SourceBranch != "feature/x" || mr.TargetBranch != "main" {
		t.Errorf("MR branches = %q → %q, want feature/x → main", mr.SourceBranch, mr.TargetBranch)
	}
}

func TestCreateGiteaPRErrorsAreActionable(t *testing.T) {
	t.Run("tea missing", func(t *testing.T) {
		installedCLIs(t)
		fakeCLI(t, nil)
		stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)

		_, err := createGiteaPR("/repo", "feature/x", "main", "T", "D")
		if err == nil {
			t.Fatal("expected an error when tea is not installed")
		}
		if !strings.Contains(err.Error(), "not installed") {
			t.Errorf("error %q should say tea is not installed", err)
		}
	})

	t.Run("no login for host", func(t *testing.T) {
		installedCLIs(t, teaBin)
		stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
		fakeCLI(t, func(recordedCall) cliResult {
			return cliResult{Stdout: teaLoginsJSON(teaLogin{
				Name: "other", URL: "https://other.example.org", User: "dev",
			})}
		})

		_, err := createGiteaPR("/repo", "feature/x", "main", "T", "D")
		if err == nil {
			t.Fatal("expected an error when tea has no login for the host")
		}
		msg := err.Error()
		if !strings.Contains(msg, "tea logins add") || !strings.Contains(msg, "gitea.example.com") {
			t.Errorf("error %q should name the exact tea logins add command for the host", msg)
		}
	})

	t.Run("repo not found", func(t *testing.T) {
		installedCLIs(t, teaBin)
		stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
		fakeCLI(t, func(c recordedCall) cliResult {
			if c.Args[0] == "logins" {
				return cliResult{Stdout: teaLoginsJSON(teaLogin{
					Name: "example", URL: "https://gitea.example.com", User: "dev",
				})}
			}
			return cliResult{Stderr: "404 Not Found", Err: errors.New("exit status 1")}
		})

		_, err := createGiteaPR("/repo", "feature/x", "main", "T", "D")
		if err == nil {
			t.Fatal("expected an error when the instance does not know the repo")
		}
		if !strings.Contains(err.Error(), "acme/widget") || !strings.Contains(err.Error(), "not found") {
			t.Errorf("error %q should name the missing repository", err)
		}
	})
}

func TestGiteaPullNumber(t *testing.T) {
	tests := map[string]int{
		"https://gitea.example.com/acme/widget/pulls/42":    42,
		"https://gitea.example.com/acme/widget/pulls/7#top": 7,
		"https://gitea.example.com/acme/widget/issues/42":   0,
		"": 0,
	}
	for url, want := range tests {
		if got := giteaPullNumber(url); got != want {
			t.Errorf("giteaPullNumber(%q) = %d, want %d", url, got, want)
		}
	}
}

// --- Gitea Actions ----------------------------------------------------------

func TestGiteaActionRunsPath(t *testing.T) {
	got := giteaActionRunsPath("acme", "widget", "main")
	want := "/repos/acme/widget/actions/runs?limit=30&branch=main"
	if got != want {
		t.Errorf("giteaActionRunsPath = %q, want %q", got, want)
	}
	if got := giteaActionRunsPath("acme", "widget", ""); strings.Contains(got, "branch=") {
		t.Errorf("giteaActionRunsPath with no branch = %q, should not send an empty filter", got)
	}
}

func TestGiteaPipelineStatusReadsLatestRun(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
	calls := fakeCLI(t, func(c recordedCall) cliResult {
		if c.Args[0] == "logins" {
			return cliResult{Stdout: teaLoginsJSON(teaLogin{
				Name: "example", URL: "https://gitea.example.com", User: "dev",
			})}
		}
		return cliResult{Stdout: `{"total_count":1,"workflow_runs":[{
			"id":9,"head_branch":"main","head_sha":"abc123","status":"failure",
			"display_title":"CI","run_number":12,
			"html_url":"https://gitea.example.com/acme/widget/actions/runs/12",
			"run_started_at":"2026-08-31T10:00:00Z","updated_at":"2026-08-31T10:05:00Z"}]}`}
	})

	info, err := giteaPipelineStatus("/repo", "main")
	if err != nil {
		t.Fatalf("giteaPipelineStatus failed: %v", err)
	}
	if !info.HasPipeline {
		t.Fatal("expected HasPipeline=true")
	}
	if info.Status != PipelineFailed {
		t.Errorf("status = %q, want %q (Gitea says \"failure\")", info.Status, PipelineFailed)
	}
	if info.Pipeline.SHA != "abc123" || info.Pipeline.ID != 9 {
		t.Errorf("pipeline = %+v, want id 9 / sha abc123", info.Pipeline)
	}

	var api recordedCall
	for _, c := range *calls {
		if c.Args[0] == "api" {
			api = c
		}
	}
	want := []string{"api", "--method", "GET", "--login", "example",
		"/repos/acme/widget/actions/runs?limit=30&branch=main"}
	if !reflect.DeepEqual(api.Args, want) {
		t.Errorf("tea api args = %v, want %v", api.Args, want)
	}
}

func TestGiteaPipelineStatusFiltersForeignBranches(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
	fakeCLI(t, func(c recordedCall) cliResult {
		if c.Args[0] == "logins" {
			return cliResult{Stdout: teaLoginsJSON(teaLogin{
				Name: "example", URL: "https://gitea.example.com", User: "dev",
			})}
		}
		// An instance that ignores the ?branch= filter.
		return cliResult{Stdout: `{"total_count":1,"workflow_runs":[
			{"id":1,"head_branch":"other","status":"success"}]}`}
	})

	info, err := giteaPipelineStatus("/repo", "main")
	if err != nil {
		t.Fatalf("giteaPipelineStatus failed: %v", err)
	}
	if info.HasPipeline {
		t.Error("a run on another branch must not be reported as this branch's pipeline")
	}
}

func TestGiteaPipelineStatusReportsMissingActions(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
	fakeCLI(t, func(c recordedCall) cliResult {
		if c.Args[0] == "logins" {
			return cliResult{Stdout: teaLoginsJSON(teaLogin{
				Name: "example", URL: "https://gitea.example.com", User: "dev",
			})}
		}
		return cliResult{Stderr: "404 Not Found", Err: errors.New("exit status 1")}
	})

	info, err := giteaPipelineStatus("/repo", "main")
	if err != nil {
		t.Fatalf("an instance without Actions is not an error: %v", err)
	}
	if info.HasPipeline {
		t.Error("expected HasPipeline=false")
	}
	if !strings.Contains(info.Detail, "Actions not available") {
		t.Errorf("detail = %q, want an explicit not-supported message rather than an empty result", info.Detail)
	}
}

func TestGiteaRunStatusMapping(t *testing.T) {
	tests := map[string]PipelineStatus{
		"success":   PipelineSuccess,
		"failure":   PipelineFailed,
		"running":   PipelineRunning,
		"waiting":   PipelinePending,
		"blocked":   PipelinePending,
		"cancelled": PipelineCanceled,
		"skipped":   PipelineSkipped,
	}
	for in, want := range tests {
		if got := giteaRunStatus(in); got != want {
			t.Errorf("giteaRunStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRetryGiteaPipelinePostsRerun(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
	calls := fakeCLI(t, func(c recordedCall) cliResult {
		switch {
		case c.Args[0] == "logins":
			return cliResult{Stdout: teaLoginsJSON(teaLogin{
				Name: "example", URL: "https://gitea.example.com", User: "dev",
			})}
		case strings.Contains(strings.Join(c.Args, " "), "rerun"):
			return cliResult{Stdout: "{}"}
		default:
			return cliResult{Stdout: `{"workflow_runs":[{"id":77,"head_branch":"main","status":"failure"}]}`}
		}
	})

	if err := retryGiteaPipeline("/repo", "main"); err != nil {
		t.Fatalf("retryGiteaPipeline failed: %v", err)
	}

	var rerun recordedCall
	for _, c := range *calls {
		if strings.Contains(strings.Join(c.Args, " "), "rerun") {
			rerun = c
		}
	}
	want := []string{"api", "--method", "POST", "--login", "example",
		"/repos/acme/widget/actions/runs/77/rerun"}
	if !reflect.DeepEqual(rerun.Args, want) {
		t.Errorf("rerun args = %v, want %v", rerun.Args, want)
	}
}

// --- status reporting -------------------------------------------------------

func TestGiteaCLIStatusNotInstalled(t *testing.T) {
	installedCLIs(t)
	fakeCLI(t, nil)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)

	st := GiteaCLIStatus("/repo")
	if st.Installed || st.HasLogin {
		t.Fatalf("status = %+v, want installed=false has_login=false", st)
	}
	if !strings.Contains(st.Hint, "not installed") {
		t.Errorf("hint = %q, should tell the user to install tea", st.Hint)
	}
}

func TestGiteaCLIStatusNoLoginGivesExactCommand(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
	fakeCLI(t, func(recordedCall) cliResult { return cliResult{Stdout: "[]"} })

	st := GiteaCLIStatus("/repo")
	if !st.Installed || st.HasLogin {
		t.Fatalf("status = %+v, want installed=true has_login=false", st)
	}
	want := "tea logins add --name gitea.example.com --url https://gitea.example.com --token <token>"
	if !strings.Contains(st.Hint, want) {
		t.Errorf("hint = %q, want it to contain %q", st.Hint, want)
	}
}

func TestGiteaCLIStatusWithLogin(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "git@gitea.example.com:acme/widget.git", nil)
	fakeCLI(t, func(recordedCall) cliResult {
		return cliResult{Stdout: teaLoginsJSON(teaLogin{
			Name: "example", URL: "https://gitea.example.com",
			SSHHost: "gitea.example.com", User: "dev", Default: "true",
		})}
	})

	st := GiteaCLIStatus("/repo")
	if !st.Installed || !st.HasLogin {
		t.Fatalf("status = %+v, want installed=true has_login=true", st)
	}
	if st.User != "dev" || st.LoginName != "example" {
		t.Errorf("status = %+v, want user=dev login=example", st)
	}
	if st.Hint != "" {
		t.Errorf("hint = %q, want empty when everything is configured", st.Hint)
	}
}

func TestDetectProviderStatusGitea(t *testing.T) {
	installedCLIs(t, teaBin)
	stubRemote(t, "https://gitea.example.com/acme/widget.git", nil)
	fakeCLI(t, func(recordedCall) cliResult {
		return cliResult{Stdout: teaLoginsJSON(teaLogin{
			Name: "example", URL: "https://gitea.example.com", User: "dev", Default: "true",
		})}
	})

	st := DetectProviderStatus("/repo")
	if st.Provider != ProviderGitea {
		t.Fatalf("provider = %q, want %q", st.Provider, ProviderGitea)
	}
	if st.CLI != teaBin || !st.CLIInstalled || !st.HasLogin {
		t.Errorf("status = %+v, want tea installed and logged in", st)
	}
	if st.Host != "gitea.example.com" {
		t.Errorf("host = %q, want gitea.example.com", st.Host)
	}
}

func TestDetectProviderStatusUnknownSuggestsThePin(t *testing.T) {
	installedCLIs(t)
	fakeCLI(t, nil)
	stubRemote(t, "https://git.example.com/acme/widget.git", nil)

	st := DetectProviderStatus("/repo")
	if st.Provider != ProviderUnknown {
		t.Fatalf("provider = %q, want %q", st.Provider, ProviderUnknown)
	}
	if !strings.Contains(st.Hint, ForgeOverrideConfigKey) {
		t.Errorf("hint = %q, should point at the %s pin", st.Hint, ForgeOverrideConfigKey)
	}
}

// --- tea login parsing ------------------------------------------------------

func TestTeaLoginsHandlesFreshInstall(t *testing.T) {
	installedCLIs(t, teaBin)
	fakeCLI(t, func(recordedCall) cliResult {
		return cliResult{Stderr: "Error: no available login", Err: errors.New("exit status 1")}
	})

	logins, err := teaLogins()
	if err != nil {
		t.Fatalf("a tea install with no logins is not an error: %v", err)
	}
	if len(logins) != 0 {
		t.Errorf("logins = %v, want none", logins)
	}
}

func TestTeaLoginForHostMatchesSSHHost(t *testing.T) {
	installedCLIs(t, teaBin)
	fakeCLI(t, func(recordedCall) cliResult {
		return cliResult{Stdout: teaLoginsJSON(
			teaLogin{Name: "a", URL: "https://one.example.com", SSHHost: "one.example.com", User: "u1"},
			teaLogin{Name: "b", URL: "https://two.example.com", SSHHost: "ssh.two.example.com", User: "u2"},
		)}
	})

	if l, ok := teaLoginForHost("ssh.two.example.com"); !ok || l.Name != "b" {
		t.Errorf("teaLoginForHost(ssh host) = %+v ok=%v, want login b", l, ok)
	}
	if _, ok := teaLoginForHost("nope.example.com"); ok {
		t.Error("expected no login for an unknown host")
	}
}

func TestTeaDefaultLoginPrefersTheDefaultFlag(t *testing.T) {
	installedCLIs(t, teaBin)
	fakeCLI(t, func(recordedCall) cliResult {
		return cliResult{Stdout: teaLoginsJSON(
			teaLogin{Name: "a", URL: "https://one.example.com", User: "u1", Default: "false"},
			teaLogin{Name: "b", URL: "https://two.example.com", User: "u2", Default: "true"},
		)}
	})

	login, ok := teaDefaultLogin()
	if !ok || login.Name != "b" {
		t.Errorf("teaDefaultLogin = %+v ok=%v, want login b", login, ok)
	}
}
