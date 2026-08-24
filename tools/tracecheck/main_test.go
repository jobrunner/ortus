package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write drops a Go source file into a temp package directory. The checker parses
// files off disk, so fixtures are real files rather than in-memory sources.
func write(t *testing.T, dir, src string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

const header = "package fixture\n\nimport \"context\"\n\ntype S struct{ tracer T }\n\n" +
	"type T interface{ Start(ctx context.Context, n string) (context.Context, sp) }\n" +
	"type sp interface{ End() }\n"

func TestApplicationMethodWithSpanPasses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fixture")
	write(t, dir, header+`
func (s *S) Do(ctx context.Context) error {
	ctx, span := s.tracer.Start(ctx, "S.Do")
	defer span.End()
	_ = ctx
	return nil
}
`)
	var f findings
	if err := checkApplication(dir, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.lines) != 0 {
		t.Fatalf("expected no findings, got %v", f.lines)
	}
}

func TestApplicationMethodWithoutSpanFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fixture")
	write(t, dir, header+`
func (s *S) Do(ctx context.Context) error { return nil }
`)
	var f findings
	if err := checkApplication(dir, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.lines) != 1 || !strings.Contains(f.lines[0], "S.Do") {
		t.Fatalf("expected one finding naming S.Do, got %v", f.lines)
	}
}

// A method may delegate its span to a same-package helper — the pattern the
// gazetteer uses via beginSection. The checker must follow that, otherwise the
// gate would push callers into duplicating span setup in every method.
func TestDelegatedSpanPasses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fixture")
	write(t, dir, header+`
func (s *S) begin(ctx context.Context, n string) (context.Context, sp) {
	return s.tracer.Start(ctx, n)
}

func (s *S) Do(ctx context.Context) error {
	ctx, span := s.begin(ctx, "S.Do")
	defer span.End()
	_ = ctx
	return nil
}
`)
	var f findings
	if err := checkApplication(dir, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.lines) != 0 {
		t.Fatalf("expected no findings, got %v", f.lines)
	}
}

// Unexported methods and methods without a context are outside the rule: they are
// not request entry points, so requiring spans there would only add noise.
func TestUnexportedAndContextlessAreIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fixture")
	write(t, dir, header+`
func (s *S) helper(ctx context.Context) error { return nil }
func (s *S) Ready() bool                      { return true }
`)
	var f findings
	if err := checkApplication(dir, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.lines) != 0 {
		t.Fatalf("expected no findings, got %v", f.lines)
	}
}

func TestExemptionNeedsAReason(t *testing.T) {
	cases := []struct {
		name       string
		comment    string
		wantExempt bool
	}{
		{"with reason", "//tracecheck:ignore launches a goroutine and returns at once; the work is traced in run", true},
		// A bare marker must not silence the gate: an exemption nobody had to
		// justify is exactly what this rule is meant to prevent.
		{"bare marker", "//tracecheck:ignore", false},
		{"too short", "//tracecheck:ignore nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "fixture")
			write(t, dir, header+"\n"+tc.comment+"\nfunc (s *S) Do(ctx context.Context) error { return nil }\n")
			var f findings
			if err := checkApplication(dir, &f); err != nil {
				t.Fatal(err)
			}
			if tc.wantExempt {
				if len(f.lines) != 0 || len(f.exempt) != 1 {
					t.Fatalf("expected a recorded exemption, got findings=%v exempt=%v", f.lines, f.exempt)
				}
				return
			}
			if len(f.lines) != 1 {
				t.Fatalf("expected the gate to still fire, got findings=%v exempt=%v", f.lines, f.exempt)
			}
		})
	}
}

func TestDecoratorNeedsSpanAndAssertion(t *testing.T) {
	const decoHeader = "package fixture\n\nimport \"context\"\n\n" +
		"type Iface interface{ Do(ctx context.Context) error }\n" +
		"type T interface{ Start(ctx context.Context, n string) (context.Context, sp) }\n" +
		"type sp interface{ End() }\n" +
		"type TracedX struct{ tracer T }\n"

	t.Run("span and assertion present", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "fixture")
		write(t, dir, decoHeader+`
var _ Iface = (*TracedX)(nil)

func (t *TracedX) Do(ctx context.Context) error {
	ctx, span := t.tracer.Start(ctx, "X.Do")
	defer span.End()
	_ = ctx
	return nil
}
`)
		var f findings
		if err := checkDecorators(dir, &f); err != nil {
			t.Fatal(err)
		}
		if len(f.lines) != 0 {
			t.Fatalf("expected no findings, got %v", f.lines)
		}
	})

	t.Run("missing assertion is reported", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "fixture")
		write(t, dir, decoHeader+`
func (t *TracedX) Do(ctx context.Context) error {
	ctx, span := t.tracer.Start(ctx, "X.Do")
	defer span.End()
	_ = ctx
	return nil
}
`)
		var f findings
		if err := checkDecorators(dir, &f); err != nil {
			t.Fatal(err)
		}
		if len(f.lines) != 1 || !strings.Contains(f.lines[0], "Zusicherung") {
			t.Fatalf("expected a missing-assertion finding, got %v", f.lines)
		}
	})

	t.Run("decorator method without a span is reported", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "fixture")
		write(t, dir, decoHeader+`
var _ Iface = (*TracedX)(nil)

func (t *TracedX) Do(ctx context.Context) error { return nil }
`)
		var f findings
		if err := checkDecorators(dir, &f); err != nil {
			t.Fatal(err)
		}
		if len(f.lines) != 1 || !strings.Contains(f.lines[0], "TracedX.Do") {
			t.Fatalf("expected a missing-span finding for TracedX.Do, got %v", f.lines)
		}
	})
}

// The real repository must satisfy its own gate — this is the regression test for
// the gap that motivated the tool (six gazetteer entry points with no spans).
func TestRepositoryItselfIsClean(t *testing.T) {
	root := filepath.Join("..", "..")
	var f findings
	if err := checkApplication(filepath.Join(root, "internal", "application"), &f); err != nil {
		t.Fatal(err)
	}
	if err := checkDecorators(filepath.Join(root, "internal", "adapters"), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.lines) != 0 {
		t.Fatalf("repository violates its own trace-coverage gate:\n  %s", strings.Join(f.lines, "\n  "))
	}
}
