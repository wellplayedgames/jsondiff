package jsondiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

func TestShiftedArrayCases(t *testing.T) {
	runCasesFromFile(t, "testdata/tests/options/shifts.json", ShiftedRemovals(), ShiftedInserts())
}

func loadShiftCases(t *testing.T) []testcase {
	t.Helper()

	b, err := os.ReadFile("testdata/tests/options/shifts.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []testcase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func comparePatch(t *testing.T, before, after interface{}, opts ...Option) Patch {
	t.Helper()

	afterBytes, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	d := &Differ{targetBytes: afterBytes}
	d.WithOpts(opts...).Compare(before, after)

	return d.Patch()
}

func patchString(t *testing.T, p Patch) string {
	t.Helper()

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertApplies checks that applying the patch to before produces after.
func assertApplies(t *testing.T, p Patch, before, after interface{}) {
	t.Helper()

	src, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.apply(src, false)
	if err != nil {
		t.Fatalf("failed to apply patch %s: %s", p, err)
	}
	if got := unmarshalMarshal(t, b); !bytes.Equal(got, want) {
		t.Errorf("patch %s does not produce the expected changes\ngot:  %s\nwant: %s", p, got, want)
	}
}

func TestShiftedRemovalsOnly(t *testing.T) {
	for _, tc := range loadShiftCases(t) {
		t.Run(testNameReplacer.Replace(tc.Name), func(t *testing.T) {
			got := comparePatch(t, tc.Before, tc.After, ShiftedRemovals())
			assertApplies(t, got, tc.Before, tc.After)

			// Without ShiftedInserts, a change that only inserts keeps
			// the positional comparison's output.
			insertsAtIndex, removes := false, false
			for _, op := range comparePatch(t, tc.Before, tc.After, ShiftedRemovals(), ShiftedInserts()) {
				switch {
				case op.Type == OperationAdd && op.Path[len(op.Path)-1] != '-':
					insertsAtIndex = true
				case op.Type == OperationRemove:
					removes = true
				}
			}
			if insertsAtIndex && !removes {
				want := comparePatch(t, tc.Before, tc.After)
				if g, w := patchString(t, got), patchString(t, want); g != w {
					t.Errorf("got %s, want the positional patch %s", g, w)
				}
			}
			for _, op := range got {
				if op.Type == OperationAdd && op.Path[len(op.Path)-1] != '-' {
					t.Errorf("unexpected add at an index: %s", op)
				}
			}
		})
	}
}

func TestShiftedArraysWithOptions(t *testing.T) {
	shifts := []Option{ShiftedRemovals(), ShiftedInserts()}
	withShifts := func(opts ...Option) []Option {
		return append(append([]Option{}, shifts...), opts...)
	}
	for _, tc := range loadShiftCases(t) {
		t.Run(testNameReplacer.Replace(tc.Name), func(t *testing.T) {
			shifted := comparePatch(t, tc.Before, tc.After, shifts...)

			t.Run("Invertible", func(t *testing.T) {
				got := comparePatch(t, tc.Before, tc.After, withShifts(Invertible())...)
				assertApplies(t, got, tc.Before, tc.After)

				// Each remove and replace is preceded by a test.
				var ops Patch
				for i, op := range got {
					if op.Type == OperationTest {
						continue
					}
					if op.Type == OperationRemove || op.Type == OperationReplace {
						if i == 0 || got[i-1].Type != OperationTest || got[i-1].Path != op.Path {
							t.Errorf("op #%d %s is not preceded by a test", i, op)
						}
					}
					ops = append(ops, op)
				}
				if g, w := patchString(t, ops), patchString(t, shifted); g != w {
					t.Errorf("without tests got %s, want %s", g, w)
				}
			})
			t.Run("Rationalize", func(t *testing.T) {
				got := comparePatch(t, tc.Before, tc.After, withShifts(Rationalize())...)
				assertApplies(t, got, tc.Before, tc.After)

				want := comparePatch(t, tc.Before, tc.After, Rationalize())
				if g, w := got.jsonLength(), want.jsonLength(); g > w {
					t.Errorf("patch is longer than without shifts: %d > %d bytes", g, w)
				}
			})
			t.Run("Equivalent", func(t *testing.T) {
				// Equivalence only applies to arrays of equal length,
				// which are never shifted.
				got := comparePatch(t, tc.Before, tc.After, withShifts(Equivalent())...)
				assertApplies(t, got, tc.Before, tc.After)
				if g, w := patchString(t, got), patchString(t, shifted); g != w {
					t.Errorf("got %s, want %s", g, w)
				}
			})
			// The shift options have no effect alongside these.
			for _, opt := range []struct {
				name string
				opt  Option
			}{
				{"LCS", LCS()},
				{"Factorize", Factorize()},
				{"Ignores", Ignores("/nothing")},
			} {
				t.Run(opt.name, func(t *testing.T) {
					got := comparePatch(t, tc.Before, tc.After, withShifts(opt.opt)...)
					want := comparePatch(t, tc.Before, tc.After, opt.opt)
					if g, w := patchString(t, got), patchString(t, want); g != w {
						t.Errorf("got %s, want %s", g, w)
					}
				})
			}
		})
	}
}

// TestShiftedArraysRandom applies the patches of random array edits, of the
// kinds that shift detection targets and those it must leave alone.
func TestShiftedArraysRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	randomArray := func(n int) []interface{} {
		a := make([]interface{}, n)
		for i := range a {
			// Few distinct values, so that runs of equal elements
			// and false prefix/suffix matches are common.
			switch rng.Intn(3) {
			case 0:
				a[i] = fmt.Sprint(rng.Intn(3))
			case 1:
				a[i] = map[string]interface{}{"id": fmt.Sprint(rng.Intn(3))}
			default:
				a[i] = []interface{}{fmt.Sprint(rng.Intn(2))}
			}
		}
		return a
	}
	edit := func(src []interface{}) []interface{} {
		tgt := append([]interface{}{}, src...)
		for n := 1 + rng.Intn(2); n > 0; n-- {
			i := rng.Intn(len(tgt) + 1)
			switch rng.Intn(3) {
			case 0: // insert a block
				block := randomArray(1 + rng.Intn(3))
				tgt = append(tgt[:i], append(block, tgt[i:]...)...)
			case 1: // remove a block
				if i < len(tgt) {
					j := min(len(tgt), i+1+rng.Intn(3))
					tgt = append(tgt[:i], tgt[j:]...)
				}
			default: // change an element
				if i < len(tgt) {
					tgt[i] = randomArray(1)[0]
				}
			}
		}
		return tgt
	}
	optionSets := map[string][]Option{
		"shifts":             {ShiftedRemovals(), ShiftedInserts()},
		"removals":           {ShiftedRemovals()},
		"shifts+rationalize": {ShiftedRemovals(), ShiftedInserts(), Rationalize()},
		"shifts+invertible":  {ShiftedRemovals(), ShiftedInserts(), Invertible()},
		// Not Equivalent: its digest-based check misses changes on its
		// own, e.g. ["0", "2"] -> ["0", ["0"]] produces an empty patch.
	}
	for i := 0; i < 2000; i++ {
		src := randomArray(rng.Intn(8))
		tgt := edit(src)
		before := map[string]interface{}{"a": src}
		after := map[string]interface{}{"a": tgt}

		for name, opts := range optionSets {
			got := comparePatch(t, before, after, opts...)
			assertApplies(t, got, before, after)
			if t.Failed() {
				t.Fatalf("%s: before %v, after %v", name, src, tgt)
			}
		}
	}
}
