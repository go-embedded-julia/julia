package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// The field that holds a Memory's elements is named in memory.go alone: every
// other file, tests included, reaches the elements through Len, Get, Set,
// Slice, All and SetAll, and makes a Memory with newMemory, so that the
// representation can change one element type at a time (#81). A selector
// `.elems` that is not a method call, or a `elems:` key of a literal, anywhere
// else is a use of the field.
func TestMemoryStoreIsPrivate(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	for _, name := range files {
		if name == "memory.go" {
			continue
		}
		fset := token.NewFileSet()
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range memoryStoreUses(f) {
			t.Errorf("%s:%d uses the field of Memory that holds its elements", name, fset.Position(v).Line)
		}
	}
}

// memoryStores are the fields of Memory that hold its elements: the boxed
// store and the typed ones (one per kind in typedKinds).
var memoryStores = map[string]bool{"elems": true, "u8": true}

// memoryStoreUses are the places in f that name one of those fields: a selector
// that is not the function of a call, and a key of a composite literal.
func memoryStoreUses(f *ast.File) []token.Pos {
	var uses []token.Pos
	called := map[*ast.SelectorExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				called[sel] = true
			}
		case *ast.SelectorExpr:
			if memoryStores[x.Sel.Name] && !called[x] {
				uses = append(uses, x.Sel.Pos())
			}
		case *ast.KeyValueExpr:
			if k, ok := x.Key.(*ast.Ident); ok && memoryStores[k.Name] {
				uses = append(uses, k.Pos())
			}
		}
		return true
	})
	return uses
}

// The check finds what it is for: a field use, a literal key, and no method call.
func TestMemoryStoreUsesFound(t *testing.T) {
	src := "package p\nfunc f(m *M) { _ = m.elems; _ = &M{elems: nil}; w.elems(1); _ = m.u8; _ = M{u8: nil}; w.u8(1) }\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := memoryStoreUses(f); len(got) != 4 {
		t.Errorf("%d uses found in a file with two field reads, two literal keys and two method calls, want 4", len(got))
	}
}
