package devops

import (
	"strings"
	"testing"
)

func TestSlugify_Basic(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Add a button", "add-a-button"},
		{"Add A Button!", "add-a-button"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"Special$$$Chars&&&Here", "special-chars-here"},
		{"   ", "feature"},
		{"a", "a"},
		{"A very long feature description that should definitely be truncated to fit within the git branch name length limit of 60 characters",
			"a-very-long-feature-description-that-should-definitely-be-tr"},
	}
	for _, c := range cases {
		got := slugify(c.input)
		if got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestSlugify_ValidGitBranch(t *testing.T) {
	inputs := []string{
		"Add a re-categorize button!",
		"Fix the search (mobile)",
		"Dark mode [experimental]",
		"Update CSS for 2025",
	}
	for _, in := range inputs {
		slug := slugify(in)
		if slug == "" {
			t.Errorf("slugify(%q) is empty", in)
		}
		if strings.Contains(slug, " ") {
			t.Errorf("slugify(%q) = %q, contains spaces", in, slug)
		}
		if slug != strings.ToLower(slug) {
			t.Errorf("slugify(%q) = %q, not lowercase", in, slug)
		}
	}
}
