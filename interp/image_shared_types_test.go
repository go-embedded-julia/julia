package interp

import (
	"bytes"
	"errors"
	"maps"
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
