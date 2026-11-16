package interp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// cachedShows counts, by the way each type shows, the types cached under core's
// names: a type that lost a parameter shows as Type{#null}.
func cachedShows() map[string]int {
	out := map[string]int{}
	for _, o := range core.StaticObjects() {
		if tn, ok := o.(*core.TypeName); ok {
			for _, dt := range tn.CachedTypes() {
				out[core.Show(dt)]++
			}
		}
	}
	return out
}

// showsAdded is what b has more of than a.
func showsAdded(a, b map[string]int) map[string]int {
	out := map[string]int{}
	for k, n := range b {
		if d := n - a[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

// A refused image writes nothing to the types a VM's creation put in the
// shared caches (issue #105). The VM a load makes for itself holds them as its
// boot objects, they are in the caches of core's names before the image is
// read, and every other VM of the process reads them: a load that decoded the
// image's records over them, then failed, left Type{Expr} and Ptr{Int32}
// without their parameter, and the next VM made a second one.
//
// The image is the real one, refused at each of its objects in turn (two bytes
// after the record): whatever has been decoded when the load stops, the caches
// hold what one VM's creation puts there, and nothing else.
func TestImageLoadRefusedLeavesCachedTypesIntact(t *testing.T) {
	var img bytes.Buffer
	if err := New().SaveImage(&img, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	vmGrowth() // what a VM's creation makes only the first time is made
	before := cachedShows()
	New()
	made := showsAdded(before, cachedShows())
	if len(made) == 0 {
		t.Fatal("a VM's creation adds no type to the caches: the test proves nothing")
	}

	bad := 0
	for k, o := range craftOfImage(t, img.Bytes()).objs {
		if o.tier == tierStatic {
			continue // a static object has no record
		}
		c := craftOfImage(t, img.Bytes())
		c.objs[k].rec = append(c.objs[k].rec, 1, 2)
		before := cachedShows()
		_, err := LoadImage(sealCraft(c), LoadOptions{})
		if !errors.Is(err, ErrImageCorrupt) {
			t.Fatalf("object %d (%s): %v", k, o.typ, err)
		}
		if got := showsAdded(before, cachedShows()); !maps.Equal(got, made) {
			if bad++; bad <= 5 {
				t.Errorf("object %d (%s) refused: the caches gained %v, a VM's creation adds %v", k, o.typ, got, made)
			}
		}
	}
	if bad > 5 {
		t.Errorf("and %d more objects", bad-5)
	}
}

// A boot type the image says another thing of than the VM holds (what
// evaluating Base did to it after boot) is given that at commit: the record of
// a boot type with its first flag flipped loads, and the type holds the flag
// the record says. The type is put back (the other tests of the process read
// it).
func TestImageLoadGivesSharedTypesWhatDiffers(t *testing.T) {
	var img bytes.Buffer
	vm := New()
	if err := vm.SaveImage(&img, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	flags := func(dt *core.DataType) (out []bool) {
		v := reflect.ValueOf(dt).Elem()
		for f := 0; f < v.NumField(); f++ {
			if v.Field(f).Kind() == reflect.Bool && v.Field(f).CanSet() {
				out = append(out, v.Field(f).Bool())
			}
		}
		return out
	}
	flips := 0
	for i, o := range craftOfImage(t, img.Bytes()).objs {
		if o.tier != tierBoot || o.typ != "*core.DataType" || len(o.rec) == 0 || flips == 3 {
			continue
		}
		// Name, Super, Parameters, hash, then the flags, one byte each
		off := 0
		for f := 0; f < 4; f++ {
			_, n := binary.Uvarint(o.rec[off:])
			off += n
		}
		c := craftOfImage(t, img.Bytes())
		c.objs[i].rec = bytes.Clone(o.rec)
		c.objs[i].rec[off] ^= 1
		dt := vm.bootList[o.idx].Interface().(*core.DataType)
		was := flags(dt)
		restore := snapshotFlags(dt)
		loaded, err := LoadImage(sealCraft(c), LoadOptions{})
		if err != nil {
			restore()
			t.Fatalf("boot type %d with a flag flipped: %v", i, err)
		}
		got := loaded.bootList[o.idx].Interface().(*core.DataType)
		if slices.Equal(flags(got), was) {
			restore()
			t.Errorf("boot type %d: the flag the record says is not the type's", i)
		}
		restore()
		flips++
	}
	if flips == 0 {
		t.Fatal("no boot type with a record: the test proves nothing")
	}
}

// snapshotFlags puts the exported fields of dt back as they are now.
func snapshotFlags(dt *core.DataType) func() {
	v := reflect.ValueOf(dt).Elem()
	saved := make([]reflect.Value, v.NumField())
	for f := range saved {
		if v.Field(f).CanSet() {
			saved[f] = reflect.ValueOf(v.Field(f).Interface())
		}
	}
	return func() {
		for f, x := range saved {
			if x.IsValid() {
				v.Field(f).Set(x)
			}
		}
	}
}

// sameField tells a slice that is nil from one that is empty, and one of
// other elements or another length from the same.
func TestSameField(t *testing.T) {
	var nilS, empty, one, two = []any(nil), []any{}, []any{1}, []any{2}
	for _, c := range []struct {
		a, b any
		same bool
	}{
		{nilS, nilS, true}, {nilS, empty, false}, {one, one, true},
		{one, two, false}, {one, []any{1, 2}, false}, {1, 1, true}, {1, 2, false},
	} {
		if got := sameField(reflect.ValueOf(c.a), reflect.ValueOf(c.b)); got != c.same {
			t.Errorf("sameField(%v, %v) = %v", c.a, c.b, got)
		}
	}
}
