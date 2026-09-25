package jira

import (
	"testing"
)

func TestFlattenDesc(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain string", `"just text"`, "just text"},
		{"null", `null`, ""},
		{"empty", ``, ""},
		{
			"adf",
			`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Bump AGP"},{"type":"text","text":" to 8.5"}]}]}`,
			"Bump AGP to 8.5",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := flattenDesc([]byte(c.in)); got != c.want {
				t.Fatalf("flattenDesc(%s) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
