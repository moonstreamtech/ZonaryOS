// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Command ciaudit is CI Checklist item 4, "RLS / Permission Drift Audit" -
// a codified version of the manual trace that closed Open Points item 37
// (see docs/DEVELOPMENT.md's Authorization checklist): for every firm-
// scoped table, confirm Row-Level Security is actually enabled on it
// (Never-Violate Rule 3), and for every Go function that accepts a firmID
// parameter, confirm it traces to a permission.IsMember/Has/IsOwner check
// before touching anything - the exact bug class that shipped once and was
// only caught by a later manual audit.
//
// This is deliberately a static, grep/AST-level heuristic, not a full data-
// flow analysis: a function can legitimately accept firmID without calling
// a check itself (a request-parsing helper, an internal writer only
// reachable after its caller already checked, a low-level primitive like
// db.WithFirmContext whose whole job is to let its caller's closure do that
// check). Those are marked with a "ciaudit:ignore-firmid-check: <reason>"
// line in the function's doc comment - reviewed in code review like any
// other suppression, not invisible. A new firmID-accepting function with
// neither a check nor a documented reason for skipping one fails the build.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const ignoreMarker = "ciaudit:ignore-firmid-check"

// permissionCheckNames are the calls this audit accepts as proof that a
// firmID-accepting function actually checked the caller's standing in that
// firm, per internal/permission's own exported surface (see
// docs/DEVELOPMENT.md's Authorization checklist) - IsOwner counts alongside
// IsMember/Has since owner-gated functions (Permission Audit Mode) check
// IsMember then IsOwner instead of Has.
var permissionCheckNames = map[string]bool{
	"IsMember": true,
	"Has":      true,
	"IsOwner":  true,
}

func main() {
	repoRoot, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciaudit:", err)
		os.Exit(2)
	}

	var failures []string

	rlsFailures, err := auditRLSTables(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciaudit: RLS table audit:", err)
		os.Exit(2)
	}
	failures = append(failures, rlsFailures...)

	firmIDFailures, err := auditFirmIDFunctions(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciaudit: firmID function audit:", err)
		os.Exit(2)
	}
	failures = append(failures, firmIDFailures...)

	if len(failures) > 0 {
		fmt.Println("RLS / Permission Drift Audit FAILED:")
		for _, f := range failures {
			fmt.Println("  -", f)
		}
		os.Exit(1)
	}

	fmt.Println("RLS / Permission Drift Audit passed")
}

// repoRoot locates the repository root by walking up from the working
// directory until go.mod is found - lets this run from the repo root (as
// `go run ./cmd/ciaudit` in CI does) without hardcoding an absolute path.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// --- RLS table audit -------------------------------------------------

var (
	createTableRe = regexp.MustCompile(`(?i)CREATE TABLE\s+(\w+)\s*\(`)
	enableRLSRe   = regexp.MustCompile(`(?i)ALTER TABLE\s+(\w+)\s+ENABLE ROW LEVEL SECURITY`)
)

// auditRLSTables reads every migrations/*.up.sql file and flags any table
// that declares a firm_id column but is never given an ENABLE ROW LEVEL
// SECURITY statement anywhere in the migrations directory (Never-Violate
// Rule 3) - the same "does this table isolate by firm_id alone, with no
// RLS policy backing it" question the item 37 audit asked of application
// code, asked here of the schema instead.
func auditRLSTables(repoRoot string) ([]string, error) {
	migrationsDir := filepath.Join(repoRoot, "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return nil, err
	}

	firmScopedTables := map[string]bool{}
	rlsEnabledTables := map[string]bool{}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return nil, err
		}
		text := string(content)

		for _, m := range enableRLSRe.FindAllStringSubmatch(text, -1) {
			rlsEnabledTables[m[1]] = true
		}

		for _, loc := range createTableRe.FindAllStringSubmatchIndex(text, -1) {
			tableName := text[loc[2]:loc[3]]
			bodyStart := loc[1]
			bodyEnd := findMatchingCreateTableEnd(text, bodyStart)
			body := text[bodyStart:bodyEnd]
			if regexp.MustCompile(`(?i)\bfirm_id\b`).MatchString(body) {
				firmScopedTables[tableName] = true
			}
		}
	}

	var failures []string
	tableNames := make([]string, 0, len(firmScopedTables))
	for t := range firmScopedTables {
		tableNames = append(tableNames, t)
	}
	sort.Strings(tableNames)
	for _, t := range tableNames {
		if !rlsEnabledTables[t] {
			failures = append(failures, fmt.Sprintf(
				"table %q has a firm_id column but no `ALTER TABLE %s ENABLE ROW LEVEL SECURITY` found in migrations/*.up.sql (Never-Violate Rule 3)",
				t, t))
		}
	}
	return failures, nil
}

// findMatchingCreateTableEnd finds the end of a CREATE TABLE's column-list
// parenthesis, starting just after its opening "(" at index start (already
// consumed by createTableRe). Tracks nesting depth so a column default like
// `gen_random_uuid()` doesn't prematurely close the block.
func findMatchingCreateTableEnd(text string, start int) int {
	depth := 1
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(text)
}

// --- firmID-accepting function audit ----------------------------------

// auditFirmIDFunctions walks every non-test .go file under internal/,
// finds functions/methods with a parameter literally named firmID, and
// flags any that neither call a permission check themselves nor carry a
// documented ciaudit:ignore-firmid-check suppression.
func auditFirmIDFunctions(repoRoot string) ([]string, error) {
	var failures []string
	root := filepath.Join(repoRoot, "internal")

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		rel, _ := filepath.Rel(repoRoot, path)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if !hasFirmIDParam(fn) {
				continue
			}
			if docHasIgnoreMarker(fn.Doc) {
				continue
			}
			if !bodyCallsPermissionCheck(fn.Body) {
				failures = append(failures, fmt.Sprintf(
					"%s:%d: func %s takes a firmID parameter but its body never calls permission.IsMember/Has/IsOwner - "+
						"either add the check (see docs/DEVELOPMENT.md's Authorization checklist) or add a "+
						"\"%s: <reason>\" line to its doc comment",
					rel, fset.Position(fn.Pos()).Line, fn.Name.Name, ignoreMarker))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(failures)
	return failures, nil
}

func hasFirmIDParam(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "firmID" {
				return true
			}
		}
	}
	return false
}

func docHasIgnoreMarker(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	return strings.Contains(doc.Text(), ignoreMarker)
}

// bodyCallsPermissionCheck walks fn's entire body, including nested
// closures (e.g. the WithFirmContext callback every handler in this
// codebase uses), looking for a call to one of permissionCheckNames -
// either qualified (permission.IsMember(...), the normal case for callers
// outside that package) or unqualified (IsMember(...), how internal/
// permission's own functions call each other).
func bodyCallsPermissionCheck(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if permissionCheckNames[fun.Name] {
				found = true
			}
		case *ast.SelectorExpr:
			if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "permission" && permissionCheckNames[fun.Sel.Name] {
				found = true
			}
		}
		return true
	})
	return found
}
