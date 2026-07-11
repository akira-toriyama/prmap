package core

import (
	"regexp"
	"strconv"
)

// PRRef identifies one pull request. Host is "" for the canonical
// owner/repo#N short form (gh then uses its default host / GH_HOST); the full
// URL form captures the host so enterprise instances round-trip correctly.
type PRRef struct {
	Host   string
	Owner  string
	Repo   string
	Number int
}

// String renders the ref as "owner/repo#N" for diagnostics. The host is
// intentionally omitted — messages stay short and the short form is what a
// human recognizes.
func (r PRRef) String() string {
	return r.Owner + "/" + r.Repo + "#" + strconv.Itoa(r.Number)
}

// Every captured segment (host, owner, repo) forbids '/', whitespace and '#',
// so a segment can never smuggle in a path separator or a second ref. A whole
// segment could still be ".." syntactically; that is neutralized downstream —
// the cache confines paths under its root and rejects any escape, and gh
// receives its args as an argv (no shell). The number is captured as digits and
// range-checked separately so an overflow is a validation error, not a silent
// wrap.
var (
	shortRefRe = regexp.MustCompile(`^([^/\s#]+)/([^/\s#]+)#([0-9]+)$`)
	urlRefRe   = regexp.MustCompile(`^https?://([^/\s#]+)/([^/\s#]+)/([^/\s#]+)/pull/([0-9]+)(?:[/?#].*)?$`)
)

// ParseRef parses a pull-request reference. It is pure — no git, no network. It
// accepts two forms:
//
//	owner/repo#N                         (Host "")
//	https://host/owner/repo/pull/N[...]  (Host captured; trailing /files,
//	                                      ?query and #fragment tolerated)
//
// A malformed ref returns *Error{Code: CodeValidation, ID: "bad-ref"} so the
// caller exits 2 before any network call.
func ParseRef(s string) (PRRef, error) {
	if m := shortRefRe.FindStringSubmatch(s); m != nil {
		return buildRef("", m[1], m[2], m[3], s)
	}
	if m := urlRefRe.FindStringSubmatch(s); m != nil {
		return buildRef(m[1], m[2], m[3], m[4], s)
	}
	return PRRef{}, Validationf("bad-ref",
		"not a pull-request reference: %q (want owner/repo#N or a PR URL)", s)
}

func buildRef(host, owner, repo, num, src string) (PRRef, error) {
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 {
		return PRRef{}, Validationf("bad-ref",
			"invalid pull-request number in %q (want a positive integer)", src)
	}
	return PRRef{Host: host, Owner: owner, Repo: repo, Number: n}, nil
}
