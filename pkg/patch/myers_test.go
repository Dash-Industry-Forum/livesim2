package patch

import (
	"reflect"
	t "testing"

	"github.com/beevik/etree"
)

type TestCase struct {
	l1  []*etree.Element
	l2  []*etree.Element
	exp []Op
}

func applyDiff(ops []Op, old, new []*etree.Element) []*etree.Element {
	out := make([]*etree.Element, 0, len(old))
	i := 0
	for _, op := range ops {
		for {
			if op.OldPos > i {
				out = append(out, old[i])
				i++
			} else {
				break
			}
		}
		if op.OldPos == i {
			switch op.OpType {
			case OpDelete:
				i++
			case OpInsert:
				out = append(out, new[op.NewPos])
			}
		}
	}
	for {
		if i < len(old) {
			out = append(out, old[i])
			i++
		} else {
			break
		}
	}
	return out
}

func TestMyersDiffStr(t *t.T) {
	el1 := etree.NewElement("S")
	el2 := etree.NewElement("S")
	A := etree.NewElement("A")
	B := etree.NewElement("B")
	C := etree.NewElement("C")
	el2.CreateAttr("t", "17")
	start1 := etree.NewElement("S")
	start1.CreateAttr("t", "82158745344000")
	start1.CreateAttr("d", "96256")
	start1.CreateAttr("r", "2")
	rep1 := etree.NewElement("S")
	rep1.CreateAttr("d", "95232")
	rep2 := etree.NewElement("S")
	rep2.CreateAttr("d", "96256")
	rep2.CreateAttr("r", "2")
	start2 := etree.NewElement("S")
	start2.CreateAttr("t", "82158745632768")
	start2.CreateAttr("d", "95232")
	end2 := etree.NewElement("S")
	end2.CreateAttr("d", "96256")
	end2.CreateAttr("r", "1")

	testCases := []TestCase{
		{[]*etree.Element{}, []*etree.Element{}, []Op{}},
		{[]*etree.Element{}, []*etree.Element{el1}, []Op{{OpInsert, 0, 0, el1}}},
		{[]*etree.Element{el1}, []*etree.Element{}, []Op{{OpDelete, 0, -1, el1}}},
		{[]*etree.Element{el1, el2}, []*etree.Element{el1}, []Op{{OpDelete, 1, -1, el2}}},
		{[]*etree.Element{start1, rep1}, []*etree.Element{start1}, []Op{{OpDelete, 1, -1, rep1}}},
		{[]*etree.Element{A, B, C, A, B, B, A}, []*etree.Element{C, B, A, B, A, C},
			[]Op{{OpDelete, 0, -1, A}, {OpInsert, 1, 0, C}, {OpDelete, 2, -1, C}, {OpDelete, 5, -1, B}, {OpInsert, 7, 5, C}}},
		// A, B, C, A, B, B, A -> B, C, A, B, B, A ->
		{[]*etree.Element{start1, rep1, rep2, rep1},
			[]*etree.Element{start2, rep2, rep1, end2},
			[]Op{{OpDelete, 0, -1, start1}, {OpDelete, 1, -1, rep1}, {OpInsert, 2, 0, start2}, {OpInsert, 4, 3, end2}},
		},
		{[]*etree.Element{start1, rep1, rep2, rep1, rep2, rep1, rep2, rep1, rep2,
			rep1, rep2, rep1, rep2, rep1, rep2},
			[]*etree.Element{start2, rep2, rep1, rep2, rep1, rep2, rep1, rep2, rep1,
				rep2, rep1, rep2, rep1, rep2, rep1},
			[]Op{{OpDelete, 0, -1, start1}, {OpDelete, 1, -1, rep1}, {OpInsert, 2, 0, start2}, {OpInsert, 15, 14, rep1}},
		},
	}
	for i, c := range testCases {
		act := MyersDiff(c.l1, c.l2, equalLeafs)
		if !reflect.DeepEqual(c.exp, act) {
			t.Errorf("Failed diff, case %d expected %v actual %v\n", i, c.exp, act)
		}
		out := applyDiff(act, c.l1, c.l2)
		if !reflect.DeepEqual(c.l2, out) {
			t.Errorf("Failed apply edits, case %d expected %v actual %v\n", i, c.l2, out)
		}
	}
}
