package core

import (
	"path"
	"strings"
)

// IsGenerated reports whether p is a generated / lock / snapshot file whose
// diff carries no review signal and should be collapsed out of the file map.
//
// The heuristic is deliberately HIGH-PRECISION: a false positive hides real
// code an agent needs to review, which is worse than a false negative (a little
// noise). So it matches only well-known exact basenames, a short list of
// derived-file suffixes, and whole path segments — never broad globs like
// bare *.lock or *.map, and never directory names like vendor/ or dist/ whose
// hand-written contents are still reviewable.
//
// It is pure: p is a forward-slash path (as GitHub returns it), matched with
// stdlib path, not filepath, so behaviour is OS-independent.
func IsGenerated(p string) bool {
	base := path.Base(p)
	if _, ok := generatedBasenames[base]; ok {
		return true
	}
	for _, suf := range generatedSuffixes {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if _, ok := generatedSegments[seg]; ok {
			return true
		}
	}
	return false
}

// generatedBasenames are exact file names (case-sensitive on the basename):
// lockfiles and generated manifests across ecosystems.
var generatedBasenames = map[string]struct{}{
	"pnpm-lock.yaml":       {},
	"package-lock.json":    {},
	"npm-shrinkwrap.json":  {},
	"yarn.lock":            {},
	"bun.lockb":            {},
	"bun.lock":             {},
	"deno.lock":            {},
	"Cargo.lock":           {},
	"go.sum":               {},
	"go.work.sum":          {},
	"poetry.lock":          {},
	"Pipfile.lock":         {},
	"pdm.lock":             {},
	"uv.lock":              {},
	"Gemfile.lock":         {},
	"composer.lock":        {},
	"mix.lock":             {},
	"gradle.lockfile":      {},
	"packages.lock.json":   {},
	"pubspec.lock":         {},
	"Podfile.lock":         {},
	"Package.resolved":     {},
	"Cartfile.resolved":    {},
	"flake.lock":           {},
	"vcpkg-lock.json":      {},
	"conan.lock":           {},
	"cabal.project.freeze": {},
	".terraform.lock.hcl":  {},
}

// generatedSuffixes match on the basename: minified bundles, test snapshots,
// restricted source-map double-suffixes (NOT bare .map, to spare treasure.map /
// app.map), and conservative generated-code suffixes.
var generatedSuffixes = []string{
	".min.js", ".min.mjs", ".min.css",
	".snap", ".snapshot",
	".js.map", ".mjs.map", ".cjs.map", ".ts.map", ".css.map",
	".pb.go", ".pb.cc", ".pb.h", ".g.dart", ".freezed.dart",
	"_pb2.py", "_pb2_grpc.py",
}

// generatedSegments match a whole path component (never a substring): tool
// output directories that never contain hand-written review targets.
var generatedSegments = map[string]struct{}{
	"__snapshots__": {},
	"__pycache__":   {},
}
