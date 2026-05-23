package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestRemoteClientRequestMethodsAreDispatched(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	daemonDir := filepath.Dir(thisFile)
	remoteMethods := remoteClientRequestMethods(t, filepath.Join(daemonDir, "..", "api", "remote.go"))
	dispatchMethods := dispatchCaseMethods(t, filepath.Join(daemonDir, "dispatch.go"))

	if len(remoteMethods) == 0 {
		t.Fatal("no RemoteClient request methods found")
	}
	for method := range remoteMethods {
		if !dispatchMethods[method] {
			t.Fatalf("RemoteClient method %q has no daemon dispatch case", method)
		}
	}
}

func remoteClientRequestMethods(t *testing.T, path string) map[string]bool {
	t.Helper()
	file := parseGoFile(t, path)
	methods := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "request" {
			return true
		}
		method, ok := stringLiteral(call.Args[1])
		if ok {
			methods[method] = true
		}
		return true
	})
	return methods
}

func dispatchCaseMethods(t *testing.T, path string) map[string]bool {
	t.Helper()
	file := parseGoFile(t, path)
	methods := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			return true
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				method, ok := stringLiteral(expr)
				if ok {
					methods[method] = true
				}
			}
			return true
		})
		return false
	})
	return methods
}

func parseGoFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}
