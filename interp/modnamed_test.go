package interp

import "github.com/go-embedded-julia/julia/expr"

// modNamed is the first module named name that is bound under Main, in
// binding-name order, nil when there is none. vm.modules holds the root
// modules only, so a test that defines `module X` finds it here.
func modNamed(vm *VM, name expr.Symbol) *Module {
	seen := map[*Module]bool{}
	var walk func(m *Module) *Module
	walk = func(m *Module) *Module {
		if seen[m] {
			return nil
		}
		seen[m] = true
		m.mu.RLock()
		names := make([]expr.Symbol, 0, len(m.bindings))
		for n := range m.bindings {
			names = append(names, n)
		}
		m.mu.RUnlock()
		sortSymbols(names)
		for _, n := range names {
			sub, ok := m.own(n).Value.(*Module)
			if !ok || sub.Parent != m {
				continue
			}
			if sub.Name == name {
				return sub
			}
			if r := walk(sub); r != nil {
				return r
			}
		}
		return nil
	}
	return walk(vm.Main)
}

func sortSymbols(s []expr.Symbol) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
