package main

import (
	"flag"
	"testing"
)

// TestSmartRouteResolution covers the routing default and its overrides.
//
// The default used to be "off once the user pins model OR effort", which meant
// --model also suppressed the classifier's effort choice and its summary. The
// classifier now runs while any of its three outputs is still unpinned, and is
// skipped only when both model and effort are given. The explicit flags win in
// both directions (--no-smart-route strongest).
func TestSmartRouteResolution(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		model, effort string
		want          bool
	}{
		{"default on", nil, "", "", true},
		{"pinned model still routes for effort and summary", []string{"--model", "sonnet"}, "sonnet", "", true},
		{"pinned effort still routes for model and summary", []string{"--effort", "high"}, "", "high", true},
		{"both pinned leaves nothing to decide", []string{"--model", "sonnet", "--effort", "high"}, "sonnet", "high", false},
		{"no-smart-route", []string{"--no-smart-route"}, "", "", false},
		{"explicit on beats both-pinned default-off", []string{"--smart-route", "--model", "sonnet", "--effort", "high"}, "sonnet", "high", true},
		{"no-smart-route beats pinned-nothing default-on", []string{"--no-smart-route", "--model", "sonnet"}, "sonnet", "", false},
		{"explicit false", []string{"--smart-route=false"}, "", "", false},
		{"explicit true", []string{"--smart-route=true"}, "", "", true},
		{"no-smart-route beats explicit on", []string{"--smart-route", "--no-smart-route"}, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.String("model", "", "")
			fs.String("effort", "", "")
			resolve := smartRouteFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if got := resolve(tc.model, tc.effort); got != tc.want {
				t.Errorf("resolve(%v, model=%q, effort=%q) = %v, want %v",
					tc.args, tc.model, tc.effort, got, tc.want)
			}
		})
	}
}
