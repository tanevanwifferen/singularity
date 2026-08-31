package main

import (
	"flag"
	"testing"
)

// TestSmartRouteResolution covers the routing default and its overrides:
// on by default, off once the user pins model or effort, and the explicit
// flags win in both directions (--no-smart-route strongest).
func TestSmartRouteResolution(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		model, effort string
		want          bool
	}{
		{"default on", nil, "", "", true},
		{"model pins routing off", []string{"--model", "sonnet"}, "sonnet", "", false},
		{"effort pins routing off", []string{"--effort", "high"}, "", "high", false},
		{"no-smart-route", []string{"--no-smart-route"}, "", "", false},
		{"explicit on beats effort default-off", []string{"--smart-route", "--effort", "high"}, "", "high", true},
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
