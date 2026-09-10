package jira

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeJira serves the handful of endpoints the client touches and records the
// paths and search bodies it saw.
type fakeJira struct {
	paths       []string
	searchBody  map[string]any
	fieldStatus int
}

func (f *fakeJira) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.paths = append(f.paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rest/api/2/field":
			if f.fieldStatus != 0 {
				w.WriteHeader(f.fieldStatus)
				io.WriteString(w, `{"errorMessages":["nope"]}`)
				return
			}
			io.WriteString(w, `[
				{"id":"summary","schema":{"type":"string"}},
				{"id":"customfield_10020","name":"Sprint","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}
			]`)
		case r.URL.Path == "/rest/api/2/search/jql":
			body, _ := io.ReadAll(r.Body)
			f.searchBody = map[string]any{}
			if err := json.Unmarshal(body, &f.searchBody); err != nil {
				t.Errorf("search body: %v", err)
			}
			// Mirrors the real response: no `total`, one cursor page.
			io.WriteString(w, `{"isLast":true,"issues":[
				{"key":"P-1","fields":{"summary":"one","description":"plain",
				 "customfield_10020":[{"name":"Sprint 9","state":"active"}]}},
				{"key":"P-2","fields":{"summary":"two"}}
			]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"errorMessages":["not found"]}`)
		}
	})
}

func TestSearchIssues_UsesV2JQLEndpointAndResolvedSprintField(t *testing.T) {
	fake := &fakeJira{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	// Trailing slash on purpose: config values are pasted from the browser.
	c := NewClient(srv.URL+"/", "user@example.com", "token")

	res, err := c.SearchIssues("project = P", 25)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}

	if got := fake.paths[len(fake.paths)-1]; got != "/rest/api/2/search/jql" {
		t.Errorf("search path: got %q", got)
	}
	fields, _ := fake.searchBody["fields"].([]any)
	var names []string
	for _, f := range fields {
		names = append(names, f.(string))
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "customfield_10020") {
		t.Errorf("fields missing resolved sprint id: %v", names)
	}
	if strings.Contains(joined, ",sprint") || strings.HasSuffix(joined, "sprint") {
		t.Errorf("fields still ask for the bare name %q: %v", "sprint", names)
	}
	if fake.searchBody["maxResults"] != float64(25) {
		t.Errorf("maxResults: got %v", fake.searchBody["maxResults"])
	}

	if res.Total != 2 {
		t.Errorf("Total: got %d, want 2 (page size, endpoint reports no total)", res.Total)
	}
	if len(res.Issues) != 2 {
		t.Fatalf("Issues: got %d, want 2", len(res.Issues))
	}
	if res.Issues[0].Sprint != "Sprint 9" {
		t.Errorf("Sprint: got %q, want %q", res.Issues[0].Sprint, "Sprint 9")
	}
	if res.Issues[0].Description != "plain" {
		t.Errorf("Description: got %q", res.Issues[0].Description)
	}
}

func TestSearchIssues_OmitsMaxResultsWhenUnset(t *testing.T) {
	fake := &fakeJira{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	c := NewClient(srv.URL, "user@example.com", "token")
	if _, err := c.SearchIssues("project = P", 0); err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if _, ok := fake.searchBody["maxResults"]; ok {
		t.Errorf("maxResults sent for a non-positive limit: %v", fake.searchBody["maxResults"])
	}
}

func TestSprintFieldID_ResolvesOnceAndToleratesFailure(t *testing.T) {
	fake := &fakeJira{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	c := NewClient(srv.URL, "", "pat")
	if got := c.sprintFieldID(); got != "customfield_10020" {
		t.Fatalf("sprintFieldID: got %q", got)
	}
	c.sprintFieldID()
	var lookups int
	for _, p := range fake.paths {
		if p == "/rest/api/2/field" {
			lookups++
		}
	}
	if lookups != 1 {
		t.Errorf("field catalogue fetched %d times, want 1", lookups)
	}

	broken := &fakeJira{fieldStatus: http.StatusForbidden}
	bsrv := httptest.NewServer(broken.handler(t))
	defer bsrv.Close()
	bc := NewClient(bsrv.URL, "", "pat")
	if got := bc.sprintFieldID(); got != "" {
		t.Errorf("failed lookup: got %q, want empty", got)
	}
}
