package core

import "testing"

func TestIsGeneratedPositive(t *testing.T) {
	// Every pattern class must collapse. A false NEGATIVE only costs a little
	// noise, so this list is the contract the heuristic must always cover.
	generated := []string{
		// exact lockfile / manifest basenames
		"pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json",
		"yarn.lock", "bun.lockb", "bun.lock", "deno.lock",
		"Cargo.lock", "go.sum", "go.work.sum", "poetry.lock",
		"Pipfile.lock", "pdm.lock", "uv.lock", "Gemfile.lock",
		"composer.lock", "mix.lock", "gradle.lockfile", "packages.lock.json",
		"pubspec.lock", "Podfile.lock", "Package.resolved", "Cartfile.resolved",
		"flake.lock", "vcpkg-lock.json", "conan.lock", "cabal.project.freeze",
		".terraform.lock.hcl",
		// nested lockfile keeps matching on the basename
		"frontend/pnpm-lock.yaml", "a/b/c/go.sum",
		// minified / snapshot suffixes
		"app.min.js", "app.min.mjs", "app.min.css",
		"foo.snap", "bar.snapshot", "dir/keycode_events.snapshot",
		// restricted source-map double-suffixes
		"bundle.js.map", "x.mjs.map", "y.cjs.map", "t.ts.map", "s.css.map",
		// generated-code suffixes
		"api.pb.go", "svc.pb.cc", "svc.pb.h", "model.g.dart", "model.freezed.dart",
		"foo_pb2.py", "foo_pb2_grpc.py",
		// path-segment matches
		"src/__snapshots__/Comp.test.js.snap", "pkg/__pycache__/mod.cpython-311.pyc",
		"__snapshots__/x", "a/__pycache__/b",
	}
	for _, p := range generated {
		t.Run(p, func(t *testing.T) {
			if !IsGenerated(p) {
				t.Errorf("IsGenerated(%q) = false, want true", p)
			}
		})
	}
}

func TestIsGeneratedNegative(t *testing.T) {
	// A false POSITIVE hides real review signal, so these must NEVER collapse.
	real := []string{
		"go.mod", "package.json", "requirements.txt",
		"src/main.js", "app.map", "treasure.map", // bare .map is not a source map
		"foo.min.js.txt", "my.lock.go", "README.md",
		"vendor/thing.go", "dist/index.html", "build/out.o", // dir names are NOT segment-collapsed
		"node_modules/pkg/index.js",
		"some.lock", // bare *.lock is not collapsed
		"cargo.lock.rs",
		"Snapshot.java",       // .snapshot is a suffix, not a substring
		"foo.pb.txt",          // .pb.go/cc/h only
		"__snapshots__.go",    // segment match is a whole path component, not a substring
		"a/b__pycache__/c.py", // not a whole segment
	}
	for _, p := range real {
		t.Run(p, func(t *testing.T) {
			if IsGenerated(p) {
				t.Errorf("IsGenerated(%q) = true, want false", p)
			}
		})
	}
}
