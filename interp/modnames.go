package interp

import (
	"slices"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// bindingFlagPublicP is BINDING_FLAG_PUBLICP (julia.h:786): the name was
// declared public or exported.
const bindingFlagPublicP = 0x4

// isPublic is whether b's name is public: declared so, or exported (Core's
// and Base's bindings are exported at boot without a declaration).
func (b *Binding) isPublic() bool { return b.flags&bindingFlagPublicP != 0 || b.Exports }

// moduleNames is jl_module_names(m, all, imported, usings)
// (module.c:1969-2016), the names Base.names lists, as a Vector{Symbol}:
// m's own public names, and with all its other globals and constants too
// (hidden #-names and deprecated ones included); with imported, the names
// it imported; with usings, those `using A: x` brought in and those the
// modules it uses export. Main's globals are listed as public, eval and
// include aside. julia lists a module's bindings in the order they were
// made; the VM keeps them by name, so they are listed sorted, which is
// what names gives after its sort!.
func (vm *VM) moduleNames(a []any) (any, error) {
	m, ok := a[0].(*Module)
	if !ok {
		return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
	}
	flag := func(v any) bool { i, _ := v.(int32); return i != 0 }
	all, imported, usings := flag(a[1]), flag(a[2]), flag(a[3])
	// a name can come twice (imported, and exported by a module m uses), as
	// julia lists it
	var out []any
	add := func(name expr.Symbol) { out = append(out, name) }
	for _, b := range sortedBindings(m) {
		if b.stdName {
			continue
		}
		_, kind := vm.bindingKind(b)
		if b.constImport {
			kind = bkImported
		}
		hidden := len(b.Name) > 0 && b.Name[0] == '#'
		mainPublic := m == vm.Main && b.Name != "eval" && b.Name != "include"
		deprecated := b.depFlags(^uint64(0))&bkFlagDeprecated != 0
		isGlobal := kind == bkGlobal || kind == bkConst || kind == bkDeclared
		if (b.isPublic() || (imported && kind == bkImported) || (usings && kind == bkExplicit) ||
			(isGlobal && (all || mainPublic))) && (all || (!deprecated && !hidden)) {
			add(b.Name)
		}
	}
	if usings {
		for i := len(m.usings) - 1; i >= 0; i-- {
			for _, b := range sortedBindings(m.usings[i]) {
				if b.Exports && (all || b.depFlags(^uint64(0))&bkFlagDeprecated == 0) {
					add(b.Name)
				}
			}
		}
	}
	return vm.newVector(core.SymbolType, out), nil
}

// sortedBindings is m's own bindings, by name.
func sortedBindings(m *Module) []*Binding {
	m.mu.RLock()
	bs := make([]*Binding, 0, len(m.bindings))
	for _, b := range m.bindings {
		bs = append(bs, b)
	}
	m.mu.RUnlock()
	slices.SortFunc(bs, func(x, y *Binding) int {
		switch {
		case x.Name < y.Name:
			return -1
		case x.Name > y.Name:
			return 1
		}
		return 0
	})
	return bs
}
