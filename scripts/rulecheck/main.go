// rulecheck enforces direct import boundaries and annotated test sleeps.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func forbidden(file, imp string) bool {
	if within(file, "internal/ui") {
		if strings.HasPrefix(imp, "wpapp/internal/render/") {
			return true
		}
		for _, root := range []string{"platform", "desktop/win", "desktop/mac", "desktop/linux"} {
			if within(imp, "wpapp/internal/"+root) {
				return true
			}
		}
	}
	return within(file, "internal/config") &&
		(imp == "wpapp/internal/app" || within(imp, "wpapp/internal/ui"))
}

func check(file string, src any) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.ParseComments)
	if err != nil {
		return []string{err.Error()}
	}
	file = filepath.ToSlash(file)
	var violations []string
	report := func(pos token.Pos, message string) {
		violations = append(violations, fmt.Sprintf("%s:%d: %s", file, fset.Position(pos).Line, message))
	}
	test := strings.HasSuffix(file, "_test.go")
	timeName := ""
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !test && forbidden(file, path) {
			report(imp.Pos(), "forbidden direct import: "+path)
		}
		if path == "time" {
			timeName = "time"
			if imp.Name != nil {
				timeName = imp.Name.Name
			}
		}
	}
	if !test || timeName == "" || (!within(file, "cmd") && !within(file, "internal")) {
		return violations
	}
	annotated := make(map[int]bool)
	for _, group := range f.Comments {
		for _, comment := range group.List {
			for offset, line := range strings.Split(comment.Text, "\n") {
				if strings.Contains(line, "sleep:") {
					annotated[fset.Position(comment.Pos()).Line+offset] = true
				}
			}
		}
	}
	ast.Inspect(f, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sleep := false
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			name, ok := fn.X.(*ast.Ident)
			sleep = ok && name.Name == timeName && name.Obj == nil && fn.Sel.Name == "Sleep"
		case *ast.Ident:
			sleep = timeName == "." && fn.Name == "Sleep" && fn.Obj == nil
		}
		line := fset.Position(call.Pos()).Line
		if sleep && !annotated[line] && !annotated[line-1] {
			report(call.Pos(), "time.Sleep requires a sleep: comment on the same or preceding line")
		}
		return true
	})
	return violations
}

func main() {
	failed := false
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			for _, violation := range check(path, nil) {
				fmt.Fprintln(os.Stderr, violation)
				failed = true
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s:1: %v\n", root, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
