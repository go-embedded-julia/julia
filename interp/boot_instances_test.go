package interp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// The boot objects of a VM are the ones New made, numbered by a walk of the
// VM, so that an image can name them and a loader find them in its own fresh
// VM. The walk must not follow what running code caches on the types of
// core's process-wide caches that New reaches (typeInstance sets the instance
// of a type the first time :new asks for it, undef for most): after a long
// session in one process, a VM made by New numbered one more object (an
// *core.instanceBox) at the place that type is reached, every later number
// moved by one, and the next LoadImage of an image saved in a fresh process
// failed with "object N is a *core.instanceBox, the image says
// *core.DataType" (#92; the minimal sequence on the Base image is the cell
// `[@async 1 for i in 1:3]`). The state is the process's, so the test runs in a
// process of its own, where it is known that nothing has set an instance yet.
func TestBootEnumerationIgnoresInstances(t *testing.T) {
	if os.Getenv("JLGO_BOOT_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run", "^TestBootEnumerationIgnoresInstances$", "-test.count=1")
		cmd.Env = append(os.Environ(), "JLGO_BOOT_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("in a process of its own: %v\n%s", err, out)
		}
		return
	}
	bootTypes := func() []string {
		var out []string
		for i, v := range New().bootList {
			out = append(out, fmt.Sprintf("%d %s", i, v.Type()))
		}
		return out
	}
	// an image saved in a fresh process, with a user singleton in it
	vm := bootedVM(t)
	s := vm.NewSession()
	for _, src := range []string{"struct UserS; end", "us = UserS()"} {
		if _, err := s.Eval(src); err != nil {
			t.Fatal(err)
		}
	}
	img := saveBytes(t, vm)
	before := bootTypes()
	singletons := func(when string) {
		t.Helper()
		l, err := LoadImage(bytes.NewReader(img), LoadOptions{})
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		ms := l.sessionFor(l.Main.own("L").Value.(*Module))
		for _, c := range []struct{ src, want string }{
			{"nothing", "nothing"},
			{"typeof(nothing) === Nothing", "true"},
			{"UserS() === us", "true"},
			{"UserS()", "Main.L.UserS()"},
		} {
			v, err := ms.Eval(c.src)
			check(t, when+": "+c.src, v, err, c.want)
		}
	}
	singletons("before")

	// what running code does: the instance of every type New reached that has
	// none yet is asked for, and kept on the type
	set, asker := 0, New()
	for _, v := range asker.bootList {
		d, ok := v.Interface().(*core.DataType)
		if !ok {
			continue
		}
		if _, has := d.CachedInstance(); has {
			continue
		}
		asker.typeInstance(d)
		if _, has := d.CachedInstance(); has {
			set++
		}
	}
	if set == 0 {
		t.Fatal("no cached type of the boot objects got an instance: the test proves nothing")
	}
	after := bootTypes()
	if len(after) != len(before) {
		t.Errorf("%d boot objects after %d instances were cached, %d before", len(after), set, len(before))
	}
	for i := 0; i < len(before) && i < len(after); i++ {
		if before[i] != after[i] {
			t.Errorf("boot object is %q after, was %q", after[i], before[i])
			break
		}
	}
	// the image saved before loads after, and the singletons are the same ones
	singletons("after")
}
