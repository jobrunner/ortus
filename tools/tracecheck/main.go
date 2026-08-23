// Command tracecheck is the trace-coverage gate: it fails the build when a code
// path that should be visible in a trace is not.
//
// Why this exists: the gazetteer feature shipped six public service methods with
// no spans at all. Nothing was broken, every test passed, and the gap only
// surfaced when a trace of a slow request showed 828 of 829 ms as empty space.
// Instrumentation has no failing test to protect it, so it needs a gate instead.
//
// Two rules, both derived from the code rather than an allowlist, so a newly
// added method is covered the moment it is written:
//
//  1. Every exported method on an application service that takes a
//     context.Context must open a span — directly, or through a helper in the
//     same package that opens one (e.g. gazetteer's beginSection).
//
//  2. Every method of a Traced* decorator in the adapters must open a span, and
//     each decorator must carry a compile-time assertion that it satisfies its
//     port interface. The assertion is what turns "the port grew a method and
//     the decorator silently stopped covering it" into a compile error.
//
// Usage: go run ./tools/tracecheck [rootdir]
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// findings collects rule violations as human-readable lines, so the gate can
// report every problem in one run instead of failing on the first.
type findings struct {
	lines []string
	// exempt records the justified //tracecheck:ignore cases. They are printed on
	// success so the exemptions stay visible instead of accumulating unnoticed.
	exempt []string
}

func (f *findings) addf(pos string, format string, args ...any) {
	f.lines = append(f.lines, fmt.Sprintf("%s: %s", pos, fmt.Sprintf(format, args...)))
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	var f findings
	appDir := filepath.Join(root, "internal", "application")
	adaptersDir := filepath.Join(root, "internal", "adapters")

	if err := checkApplication(appDir, &f); err != nil {
		fmt.Fprintf(os.Stderr, "tracecheck: %v\n", err)
		os.Exit(2)
	}
	if err := checkDecorators(adaptersDir, &f); err != nil {
		fmt.Fprintf(os.Stderr, "tracecheck: %v\n", err)
		os.Exit(2)
	}

	if len(f.lines) > 0 {
		sort.Strings(f.lines)
		fmt.Fprintf(os.Stderr, "❌ trace-coverage: %d Verstoß/Verstöße\n\n", len(f.lines))
		for _, l := range f.lines {
			fmt.Fprintf(os.Stderr, "  %s\n", l)
		}
		fmt.Fprint(os.Stderr, "\nJede exportierte ctx-Methode eines Application-Service und jede\n"+
			"Methode eines Traced*-Decorators muss einen Span öffnen. Ohne Span ist\n"+
			"der Pfad im Trace unsichtbar und beim Debugging nicht auffindbar.\n")
		os.Exit(1)
	}
	fmt.Println("✅ trace-coverage ok — alle Application-Einstiegspunkte und Traced*-Decorators öffnen Spans.")
	if len(f.exempt) > 0 {
		sort.Strings(f.exempt)
		fmt.Printf("   %d begründete Ausnahme(n):\n", len(f.exempt))
		for _, e := range f.exempt {
			fmt.Printf("     · %s\n", e)
		}
	}
}

// packagesIn parses every non-test Go package under dir, keyed by directory.
func packagesIn(dir string) (map[string][]*ast.File, *token.FileSet, error) {
	// Resolve and verify the root before walking it: the path comes from argv, so
	// normalizing it here means the walk starts from a known directory rather than
	// from whatever relative traversal was passed in.
	root, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", root)
	}

	fset := token.NewFileSet()
	out := map[string][]*ast.File{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// ParseComments is required: the //tracecheck:ignore exemption lives in a
		// doc comment, and without this mode fn.Doc is always nil.
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		pkgDir := filepath.Dir(path)
		out[pkgDir] = append(out[pkgDir], file)
		return nil
	})
	return out, fset, err
}

// startsSpanDirectly reports whether the function body contains a call whose
// selector is Start with a first argument named ctx — i.e. tracer.Start(ctx, …).
// Matching on the selector rather than a resolved type keeps the checker free of
// go/types (and of build-tag and cgo complications in this repo).
func startsSpanDirectly(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Start" || len(call.Args) == 0 {
			return true
		}
		// Only count Start calls that thread the context, so an unrelated
		// Start (a background worker, a server) is not mistaken for a span.
		if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == "ctx" {
			found = true
			return false
		}
		return true
	})
	return found
}

// calleeNames returns the plain function names called in the body, used to see
// whether a method delegates its span to a same-package helper.
func calleeNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	if fn.Body == nil {
		return names
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident: // helper(…)
			names[f.Name] = true
		case *ast.SelectorExpr: // s.helper(…)
			names[f.Sel.Name] = true
		}
		return true
	})
	return names
}

// ignoreMarker is the in-code exemption. It must carry a reason, and it lives on
// the method's doc comment rather than in an external allowlist so the
// justification is visible in review, right where the decision applies.
const ignoreMarker = "//tracecheck:ignore"

// exemptReason returns the justification from a //tracecheck:ignore comment, and
// ok=false when the method is not exempt. A marker without a reason is treated as
// not exempt, so the gate still fires: an unexplained exemption is the thing this
// rule exists to prevent.
func exemptReason(fn *ast.FuncDecl) (string, bool) {
	if fn.Doc == nil {
		return "", false
	}
	for _, c := range fn.Doc.List {
		if !strings.HasPrefix(c.Text, ignoreMarker) {
			continue
		}
		reason := strings.TrimSpace(strings.TrimPrefix(c.Text, ignoreMarker))
		if len(reason) < 15 {
			return "", false
		}
		return reason, true
	}
	return "", false
}

// receiverName returns the receiver type name of a method, or "" for functions.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// takesContext reports whether the function's first parameter is a context.Context.
func takesContext(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	sel, ok := fn.Type.Params.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "context" && sel.Sel.Name == "Context"
}

// checkApplication enforces rule 1: exported ctx methods on application services
// open a span, directly or via a same-package helper that does.
func checkApplication(dir string, f *findings) error {
	pkgs, fset, err := packagesIn(dir)
	if err != nil {
		return err
	}
	for pkgDir, files := range pkgs {
		spanOpeners, methods := scanPackage(files)
		for _, fn := range methods {
			if reason, ok := exemptReason(fn); ok {
				f.exempt = append(f.exempt, fmt.Sprintf("%s.%s — %s",
					receiverName(fn), fn.Name.Name, reason))
				continue
			}
			if opensSpan(fn, spanOpeners) {
				continue
			}
			f.addf(fset.Position(fn.Pos()).String(),
				"%s.%s öffnet keinen Span (weder direkt noch über einen Helfer in %s)",
				receiverName(fn), fn.Name.Name, filepath.Base(pkgDir))
		}
	}
	return nil
}

// scanPackage returns the names of functions that open a span themselves, and the
// exported ctx methods that are subject to rule 1. The first set exists because a
// method may delegate to a helper (gazetteer's beginSection) instead of calling
// Start inline.
func scanPackage(files []*ast.File) (spanOpeners map[string]bool, methods []*ast.FuncDecl) {
	spanOpeners = map[string]bool{}
	for _, file := range files {
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if startsSpanDirectly(fn) {
				spanOpeners[fn.Name.Name] = true
			}
			if fn.Recv != nil && fn.Name.IsExported() && takesContext(fn) {
				methods = append(methods, fn)
			}
		}
	}
	return spanOpeners, methods
}

// opensSpan reports whether fn starts a span itself or calls a same-package
// helper that does.
func opensSpan(fn *ast.FuncDecl, spanOpeners map[string]bool) bool {
	if startsSpanDirectly(fn) {
		return true
	}
	for callee := range calleeNames(fn) {
		if spanOpeners[callee] {
			return true
		}
	}
	return false
}

// checkDecorators enforces rule 2: every method of a Traced* type opens a span,
// and the type carries a compile-time interface assertion.
func checkDecorators(dir string, f *findings) error {
	pkgs, fset, err := packagesIn(dir)
	if err != nil {
		return err
	}
	for _, files := range pkgs {
		asserted, methods := scanDecorators(files)
		for typ, ms := range methods {
			if !asserted[typ] {
				f.addf(fset.Position(ms[0].Pos()).String(),
					"%s hat keine Interface-Zusicherung (`var _ output.X = (*%s)(nil)`); "+
						"ohne sie wächst der Port unbemerkt über den Decorator hinaus", typ, typ)
			}
			for _, fn := range ms {
				// Constructors and non-span helpers are not port methods; only
				// exported ctx methods form the decorated surface.
				if !fn.Name.IsExported() || !takesContext(fn) {
					continue
				}
				if !startsSpanDirectly(fn) {
					f.addf(fset.Position(fn.Pos()).String(),
						"%s.%s öffnet keinen Span, dekoriert aber eine Port-Methode", typ, fn.Name.Name)
				}
			}
		}
	}
	return nil
}

// scanDecorators collects, per Traced* type, whether it carries an interface
// assertion and which methods it declares.
func scanDecorators(files []*ast.File) (asserted map[string]bool, methods map[string][]*ast.FuncDecl) {
	asserted = map[string]bool{}
	methods = map[string][]*ast.FuncDecl{}
	for _, file := range files {
		for _, d := range file.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if r := receiverName(decl); strings.HasPrefix(r, "Traced") {
					methods[r] = append(methods[r], decl)
				}
			case *ast.GenDecl:
				for _, t := range assertedTypes(decl) {
					asserted[t] = true
				}
			}
		}
	}
	return asserted, methods
}

// assertedTypes returns the Traced* types a declaration asserts, i.e. those named
// in a `var _ Iface = (*TracedX)(nil)` line.
func assertedTypes(decl *ast.GenDecl) []string {
	var out []string
	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "_" {
			continue
		}
		for _, v := range vs.Values {
			if t := tracedTargetOf(v); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// tracedTargetOf extracts "TracedX" from a `(*TracedX)(nil)` conversion used in
// an interface assertion, returning "" when the expression is something else.
func tracedTargetOf(v ast.Expr) string {
	call, ok := v.(*ast.CallExpr)
	if !ok {
		return ""
	}
	paren, ok := call.Fun.(*ast.ParenExpr)
	if !ok {
		return ""
	}
	star, ok := paren.X.(*ast.StarExpr)
	if !ok {
		return ""
	}
	id, ok := star.X.(*ast.Ident)
	if !ok || !strings.HasPrefix(id.Name, "Traced") {
		return ""
	}
	return id.Name
}
