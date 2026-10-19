// Package schemas holds the Qase case definitions for steve-tests.
package schemas

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rancher/steve-tests/extensions/qase"
)

const suitePrefix = "Go Automation/Steve/"

// TestSchemaMatchesSuite keeps steve_schemas.yaml in step with the code: every
// top-level test under tests/ needs a case in its package's suite, and every
// case needs a test. Without a case, a test's result never reaches Qase.
func TestSchemaMatchesSuite(t *testing.T) {
	suites, err := qase.ReadSchemaFile("steve_schemas.yaml")
	if err != nil {
		t.Fatal(err)
	}

	inSchema := map[string]string{} // title -> suite
	for _, suite := range suites {
		if len(suite.Projects) == 0 {
			t.Errorf("suite %q lists no Qase projects", suite.Suite)
		}
		for _, c := range suite.Cases {
			if previous, ok := inSchema[c.Title]; ok {
				t.Errorf("case %s appears in both %q and %q", c.Title, previous, suite.Suite)
			}
			inSchema[c.Title] = suite.Suite
			if c.Priority == nil || c.Type == nil || c.IsFlaky == nil || c.Automation == nil {
				t.Errorf("case %s must set priority, type, is_flaky, and automation", c.Title)
			}
		}
	}

	inCode := topLevelTests(t, filepath.Join("..", "tests"))
	var missing, stale, misplaced []string
	for name, pkg := range inCode {
		suite, ok := inSchema[name]
		switch {
		case !ok:
			missing = append(missing, name+" (suite "+suitePrefix+pkg+")")
		case suite != suitePrefix+pkg:
			misplaced = append(misplaced, name+" is in "+suite+", want "+suitePrefix+pkg)
		}
	}
	for name := range inSchema {
		if _, ok := inCode[name]; !ok {
			stale = append(stale, name)
		}
	}

	report := func(what string, names []string) {
		if len(names) > 0 {
			sort.Strings(names)
			t.Errorf("%s:\n  %s", what, strings.Join(names, "\n  "))
		}
	}
	report("tests missing from steve_schemas.yaml", missing)
	report("cases in steve_schemas.yaml with no matching test", stale)
	report("cases in the wrong suite", misplaced)
}

// topLevelTests maps each Test function under root to its package directory.
// Names must be unique across packages because Qase matches cases by title.
func topLevelTests(t *testing.T, root string) map[string]string {
	t.Helper()
	tests := map[string]string{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		pkg := filepath.Base(filepath.Dir(path))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTestFunc(fn) {
				continue
			}
			if previous, ok := tests[fn.Name.Name]; ok && previous != pkg {
				t.Errorf("%s is defined in both tests/%s and tests/%s; Qase needs unique names", fn.Name.Name, previous, pkg)
			}
			tests[fn.Name.Name] = pkg
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tests) == 0 {
		t.Fatalf("found no tests under %s", root)
	}
	return tests
}

func isTestFunc(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	if !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return false
	}
	if rest := name[len("Test"):]; rest != "" && rest[0] >= 'a' && rest[0] <= 'z' {
		return false
	}
	params := fn.Type.Params.List
	if len(params) != 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}
