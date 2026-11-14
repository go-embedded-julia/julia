package interp

import (
	"errors"
	"reflect"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// The two bodies made while Base is built are goBodies: a name and a capture,
// from which newGoBody makes the func again.
func TestGoBodies(t *testing.T) {
	vm := bootedVM(t)
	method := func(fn *Function) (*goBody, func() (any, error)) {
		t.Helper()
		ms := fn.Table.Methods(vm.World())
		if len(ms) != 1 {
			t.Fatalf("%d methods, want 1", len(ms))
		}
		b, ok := ms[0].Def.(*goBody)
		if !ok {
			t.Fatalf("the body is a %T, want a *goBody", ms[0].Def)
		}
		return b, func() (any, error) { return vm.callMethod(ms[0], nil, fn, nil) }
	}

	// Base.JuliaSyntax.enable_in_core!: no capture, returns nothing
	vm.defineJuliaSyntax(vm.Base)
	js := vm.Base.own("JuliaSyntax").Value.(*Module)
	enable, call := method(js.own("enable_in_core!").Value.(*Function))
	if enable.Name != "enable_in_core!" || enable.Capture != nil {
		t.Errorf("enable_in_core! is %q capturing %v", enable.Name, enable.Capture)
	}
	if v, err := call(); v != nil || err != nil {
		t.Errorf("enable_in_core!() = %v, %v", v, err)
	}

	// the compiler dependency loop: captures Base; Base here has none of what
	// it reads, which is the error the body gives
	loop := expr.New("for", expr.Symbol("_include_dependency!"), expr.Symbol("_compiler_require_dependencies"))
	fn := vm.replaceCompilerDependencyLoop(loop, vm.Base).(*expr.Expr).Args[0].(*Function)
	record, call := method(fn)
	if record.Name != "record_compiler_dependencies" || record.Capture != any(vm.Base) {
		t.Errorf("the loop is %q capturing %v", record.Name, record.Capture)
	}
	if _, err := call(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("the loop on a Base without _included_files: %v", err)
	}

	// made again from its name and capture, a body does what the first did
	again, err := newGoBody(record.Name, record.Capture)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != record.Name || again.Capture != record.Capture {
		t.Errorf("made again: %q capturing %v", again.Name, again.Capture)
	}
	if _, err := again.fn(vm, nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("the body made again: %v", err)
	}
	if again, err := newGoBody("enable_in_core!", nil); err != nil {
		t.Error(err)
	} else if v, err := again.fn(vm, nil); v != nil || err != nil {
		t.Errorf("enable_in_core! made again = %v, %v", v, err)
	}

	// an image is input: a name nothing has, a capture of the wrong kind
	for _, c := range []struct {
		name    string
		capture any
	}{
		{"no_such_body", nil},
		{"record_compiler_dependencies", nil},
		{"record_compiler_dependencies", int64(1)},
		{"enable_in_core!", vm.Base},
	} {
		if _, err := newGoBody(c.name, c.capture); !errors.Is(err, ErrUnsupported) {
			t.Errorf("newGoBody(%q, %v): %v", c.name, c.capture, err)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("mustGoBody of a name nothing has did not panic")
		}
	}()
	mustGoBody("no_such_body", nil)
}

// Every Intrinsic and Builtin reachable from the VM is found again, by its
// number and by its name, in the boot of another VM; and no Go func the walk
// meets is left unaccounted for: 92 + 60 + the goMethods boot made + the
// writers of the two streams.
func TestFuncsRebindByName(t *testing.T) {
	a, b := bootedVM(t), bootedVM(t)
	st := walkImage(a, vmImageSpec)
	requireClassified(t, st)

	n := 0
	for id, x := range a.intrinsics {
		if x == nil {
			continue
		}
		n++
		fn, err := b.intrinsicFn(int32(id))
		if err != nil || fn == nil {
			t.Errorf("intrinsic %d (%s): %v", id, x.Name, err)
		}
		if b.intrinsics[id].Name != x.Name {
			t.Errorf("intrinsic %d is %s here and %s there", id, x.Name, b.intrinsics[id].Name)
		}
	}
	if n != st.Funcs["interp.Intrinsic.Fn"] {
		t.Errorf("%d intrinsics, the walk met %d Go funcs of Intrinsic", n, st.Funcs["interp.Intrinsic.Fn"])
	}
	for _, id := range []int32{-1, int32(len(a.intrinsics)), 1 << 20} {
		if _, err := a.intrinsicFn(id); !errors.Is(err, ErrUnsupported) {
			t.Errorf("intrinsic %d: %v", id, err)
		}
	}

	// a Builtin is a binding of Core: the walk meets no other
	distinct := map[*Builtin]bool{}
	for name, bind := range a.Core.bindings {
		x, ok := bind.Value.(*Builtin)
		if !ok {
			continue
		}
		distinct[x] = true
		fn, err := b.builtinFn(name)
		if err != nil {
			t.Errorf("builtin %s: %v", name, err)
			continue
		}
		// the four memoryref builtins atomic memory lacks share one closure
		// literal; every other is its own func
		if got, want := reflect.ValueOf(fn).Pointer(), reflect.ValueOf(x.Fn).Pointer(); got != want {
			t.Errorf("builtin %s is another func in the other VM", name)
		}
	}
	if len(distinct) != st.Funcs["interp.Builtin.Fn"] {
		t.Errorf("%d builtins bound in Core, the walk met %d Go funcs of Builtin", len(distinct), st.Funcs["interp.Builtin.Fn"])
	}
	for _, name := range []expr.Symbol{"no_such_builtin", "Int"} {
		if _, err := a.builtinFn(name); !errors.Is(err, ErrUnsupported) {
			t.Errorf("builtin %s: %v", name, err)
		}
	}

	// a Boot()-only VM holds the goMethods boot made and no goBody: those
	// come with New(), as boot objects
	if st.Funcs["dispatch.Method.Def"] != 9 || st.Funcs["interp.goBody.fn"] != 0 {
		t.Errorf("Method.Def funcs %d, goBody funcs %d; want 9 and 0", st.Funcs["dispatch.Method.Def"], st.Funcs["interp.goBody.fn"])
	}
}
