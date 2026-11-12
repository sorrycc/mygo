package ui

import "testing"

func TestTabSize(t *testing.T) {
	var inherited, own float32
	tt := coreNewTester(func(c *context) {
		coreColumn(c).TabSize(4).Font("monospace").AlignItems(Start).Children(func() {
			coreText(c, "a b").Label("space")
			coreText(c, "ab").Label("none")
			coreText(c, "\tb").Label("four")
			coreText(c, "\tb").TabSize(2).Label("two")
			inherited = coreText(c, "x").textParams(0).Style.TabSize
			own = coreText(c, "x").TabSize(8).textParams(0).Style.TabSize
		})
	}, 400, 300)
	if inherited != 4 || own != 8 {
		t.Errorf("tab sizes %v and %v, want 4 and 8", inherited, own)
	}
	width := func(label string) float32 {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %s", label)
		}
		return r.W
	}
	space := width("space") - width("none")
	letter := width("none") / 2
	if d := width("four") - (4*space + letter); d < -0.5 || d > 0.5 {
		t.Errorf("a tab of 4 spaces of %v with a letter of %v is %v wide", space, letter, width("four"))
	}
	if d := width("two") - (2*space + letter); d < -0.5 || d > 0.5 {
		t.Errorf("a tab of 2 spaces of %v with a letter of %v is %v wide", space, letter, width("two"))
	}
}
