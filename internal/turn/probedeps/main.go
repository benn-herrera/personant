// Command probedeps lists every identifier probe_phases.go uses that is
// defined elsewhere in internal/turn: the surface a real runtime inversion
// must provide. Throwaway probe tooling; run via `just probe-deps`.
package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
)

const (
	pkgDir    = "internal/turn"
	probeFile = "probe_phases.go"
)

type entry struct {
	kind, name, site string
	exported         bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "probedeps:", err)
		os.Exit(1)
	}
}

func run() error {
	bp, err := build.Default.ImportDir(pkgDir, 0)
	if err != nil {
		return fmt.Errorf("list %s: %w", pkgDir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	var probe *ast.File
	for _, name := range bp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(pkgDir, name), nil, 0)
		if err != nil {
			return err
		}
		files = append(files, f)
		if name == probeFile {
			probe = f
		}
	}
	if probe == nil {
		return fmt.Errorf("%s not in %s", probeFile, pkgDir)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(bp.ImportPath, fset, files, info)
	if err != nil {
		return fmt.Errorf("type-check: %w", err)
	}

	owners := fieldOwners(pkg)
	seen := map[types.Object]bool{}
	var entries []entry
	for id, obj := range info.Uses {
		if obj.Pkg() != pkg || seen[obj] {
			continue
		}
		if fset.File(id.Pos()) != fset.File(probe.Pos()) {
			continue
		}
		def := fset.Position(obj.Pos())
		if filepath.Base(def.Filename) == probeFile {
			continue
		}
		seen[obj] = true
		e := entry{name: obj.Name(), exported: obj.Exported(),
			site: fmt.Sprintf("%s:%d", filepath.Base(def.Filename), def.Line)}
		switch o := obj.(type) {
		case *types.Func:
			e.kind = "func"
			if recv := o.Type().(*types.Signature).Recv(); recv != nil {
				e.kind = "method"
				e.name = typeName(recv.Type()) + "." + o.Name()
			}
		case *types.Var:
			e.kind = "var"
			if o.IsField() {
				e.kind = "field"
				e.name = owners[o] + "." + o.Name()
			}
		case *types.Const:
			e.kind = "const"
		case *types.TypeName:
			e.kind = "type"
		default:
			continue
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind < entries[j].kind
		}
		return entries[i].name < entries[j].name
	})

	counts := map[string][2]int{}
	var kinds []string
	for _, e := range entries {
		c, ok := counts[e.kind]
		if !ok {
			kinds = append(kinds, e.kind)
		}
		if e.exported {
			c[0]++
		} else {
			c[1]++
		}
		counts[e.kind] = c
		vis := "unexported"
		if e.exported {
			vis = "exported"
		}
		fmt.Printf("%-6s %-10s %-45s %s\n", e.kind, vis, e.name, e.site)
	}
	fmt.Println()
	fmt.Printf("%-6s %8s %10s\n", "kind", "exported", "unexported")
	var te, tu int
	for _, k := range kinds {
		fmt.Printf("%-6s %8d %10d\n", k, counts[k][0], counts[k][1])
		te += counts[k][0]
		tu += counts[k][1]
	}
	fmt.Printf("%-6s %8d %10d\n", "total", te, tu)
	return nil
}

// fieldOwners maps each struct field of the package's named types to its
// owning type's name. Fields of anonymous nested structs are not mapped.
func fieldOwners(pkg *types.Package) map[*types.Var]string {
	owners := map[*types.Var]string{}
	scope := pkg.Scope()
	for _, n := range scope.Names() {
		tn, ok := scope.Lookup(n).(*types.TypeName)
		if !ok {
			continue
		}
		st, ok := tn.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := range st.NumFields() {
			owners[st.Field(i)] = n
		}
	}
	return owners
}

func typeName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return t.String()
}
