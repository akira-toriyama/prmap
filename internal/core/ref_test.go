package core

import "testing"

func TestParseRefValid(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want PRRef
	}{
		{"canonical", "owner/repo#123", PRRef{Host: "", Owner: "owner", Repo: "repo", Number: 123}},
		{"dotted repo", "akira-toriyama/go.work#7", PRRef{Owner: "akira-toriyama", Repo: "go.work", Number: 7}},
		{"url github", "https://github.com/cli/cli/pull/10712", PRRef{Host: "github.com", Owner: "cli", Repo: "cli", Number: 10712}},
		{"url trailing files", "https://github.com/zmkfirmware/zmk/pull/2477/files", PRRef{Host: "github.com", Owner: "zmkfirmware", Repo: "zmk", Number: 2477}},
		{"url with query", "https://github.com/o/r/pull/5?w=1", PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 5}},
		{"url with fragment", "http://ghe.corp/o/r/pull/9#diff", PRRef{Host: "ghe.corp", Owner: "o", Repo: "r", Number: 9}},
		{"enterprise host", "https://ghe.example.com/team/proj/pull/1", PRRef{Host: "ghe.example.com", Owner: "team", Repo: "proj", Number: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseRef(c.in)
			if err != nil {
				t.Fatalf("ParseRef(%q) unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("ParseRef(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestParseRefMalformed(t *testing.T) {
	cases := []string{
		"",
		"owner/repo",                      // no #N
		"owner#5",                         // no repo
		"owner/repo#0",                    // PR numbers are 1-based
		"owner/repo#-1",                   // negative
		"owner/repo#abc",                  // non-numeric
		"owner/repo#",                     // empty number
		"/repo#1",                         // empty owner
		"owner/#1",                        // empty repo
		"a/b/c#1",                         // too many segments
		"owner repo#1",                    // whitespace in ref
		"https://github.com/o/r/issues/5", // issue, not pull
		"https://github.com/o/r/pull/x",   // non-numeric url N
		"https://github.com/o/r/pull/0",   // zero url N
		"ftp://github.com/o/r/pull/1",     // wrong scheme
		"not a ref at all",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := ParseRef(in)
			if err == nil {
				t.Fatalf("ParseRef(%q) = nil error, want a bad-ref validation error", in)
			}
			ce := AsError(err)
			if ce == nil {
				t.Fatalf("ParseRef(%q) error is not *core.Error: %v", in, err)
			}
			if ce.Code != CodeValidation {
				t.Errorf("ParseRef(%q) code = %d, want %d (validation)", in, ce.Code, CodeValidation)
			}
			if ce.ID != "bad-ref" {
				t.Errorf("ParseRef(%q) id = %q, want %q", in, ce.ID, "bad-ref")
			}
		})
	}
}

func TestPRRefString(t *testing.T) {
	if got := (PRRef{Owner: "o", Repo: "r", Number: 42}).String(); got != "o/r#42" {
		t.Errorf("String() = %q, want %q", got, "o/r#42")
	}
}

// A huge N that overflows int must be rejected, not silently wrapped.
func TestParseRefNumberOverflow(t *testing.T) {
	_, err := ParseRef("owner/repo#99999999999999999999999999")
	if err == nil {
		t.Fatal("ParseRef with an overflowing number = nil error, want bad-ref")
	}
	if ce := AsError(err); ce == nil || ce.ID != "bad-ref" {
		t.Errorf("overflow error = %v, want bad-ref", err)
	}
}
