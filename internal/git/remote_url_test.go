package git

import "testing"

func TestExtractURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r/pull/1\n":                                           "https://github.com/o/r/pull/1",
		"Creating...\nView merge request: https://gl.com/o/r/-/merge_requests/2.\n": "https://gl.com/o/r/-/merge_requests/2",
		"no url here\nat all":                                                       "",
		"":                                                                          "",
	}
	for in, want := range cases {
		if got := ExtractURL(in); got != want {
			t.Errorf("ExtractURL(%q) = %q, want %q", in, got, want)
		}
	}
}
