package ui

import "testing"

// A text area's TextRanges set the font of their runes: a larger size makes
// its line taller, italics and a family lay it out apart, and hidden runes
// take no room while the caret still steps over them.
func TestTextRangesShapeAreas(t *testing.T) {
	s := "# Title\nplain **bold** end\nnext"
	var ranges []TextRange
	var el *node
	tt := coreNewTester(func(c *context) {
		el = coreTextAreaBase(c, &s).Fill().TextRanges(ranges...)
	}, 400, 300)
	ed := el.st.editor
	plain := ed.area.paraLayout(ed, 0).Height
	plainW := ed.area.paraLayout(ed, 1).Lines[0].Width
	ranges = []TextRange{
		{Start: 0, End: 7, Size: 28},
		{Start: 0, End: 2, Hidden: true},
		{Start: 14, End: 16, Hidden: true},
		{Start: 20, End: 22, Hidden: true},
		{Start: 16, End: 20, Weight: 700, Italic: true},
	}
	tt.Frame()
	if h := ed.area.paraLayout(ed, 0).Height; h <= plain*1.5 {
		t.Errorf("the heading's line is %v high, plain %v", h, plain)
	}
	l := ed.area.paraLayout(ed, 1)
	if w := l.Lines[0].Width; w >= plainW {
		t.Errorf("with its marks hidden the line is %v wide, %v shown", w, plainW)
	}
	// The hidden marks keep their caret positions, at the same x.
	x0, _, _ := l.Caret(6)
	x1, _, _ := l.Caret(8)
	if x1-x0 > 0.01 {
		t.Errorf("the hidden ** is %v wide", x1-x0)
	}
	// Ranges merge by attribute: the hidden mark keeps the heading's size.
	if !ed.apart {
		ed.sortRanges()
	}
	for _, r := range ed.ranges {
		if r.Start == 0 && (!r.Hidden || r.Size != 28) {
			t.Errorf("the heading's mark merged into %+v", r)
		}
	}
}

// DrawObjects paints a text area's inline objects at their runes, and not
// its hidden runes, which are objects too narrow to see.
func TestTextAreaDrawsObjects(t *testing.T) {
	s := "one\nan [x] box"
	var got []int
	var boxes []Rect
	tt := coreNewTester(func(c *context) {
		coreTextAreaBase(c, &s).Fill().
			TextRanges(TextRange{Start: 7, End: 8, ObjectWidth: 20, ObjectHeight: 14}, TextRange{Start: 8, End: 10, Hidden: true}).
			DrawObjects(func(p *Painter, at int, r Rect) { got, boxes = append(got, at), append(boxes, r) })
	}, 300, 200)
	got, boxes = nil, nil
	tt.Frame()
	if len(got) == 0 || got[0] != 7 {
		t.Fatalf("objects painted at %v, want rune 7", got)
	}
	for _, at := range got {
		if at != 7 {
			t.Errorf("a hidden rune %d painted as an object", at)
		}
	}
	if boxes[0].W != 20 || boxes[0].H != 14 {
		t.Errorf("the object's box is %v", boxes[0])
	}
}

// TextBlocks show runs of paragraphs as boxes of their height, painted by
// DrawBlocks, and the paragraphs after them move to follow.
func TestTextBlocks(t *testing.T) {
	s := "a\n|x|\n|-|\n|y|\nb"
	blocks := []TextBlock{{First: 1, Last: 3, Height: 120}}
	var painted []TextBlock
	var at Rect
	var el *node
	tt := coreNewTester(func(c *context) {
		el = coreTextAreaBase(c, &s).Fill().TextBlocks(blocks...).
			DrawBlocks(func(p *Painter, b TextBlock, r Rect) { painted, at = append(painted, b), r })
	}, 300, 400)
	painted = nil
	tt.Frame()
	ed := el.st.editor
	a := ed.area
	if len(painted) != 1 || painted[0].First != 1 || at.H != 120 {
		t.Fatalf("blocks painted %v at %v", painted, at)
	}
	line := float64(a.paraLayout(ed, 0).Height)
	if top := a.hs.top(4); top < 120+line-0.5 || top > 120+line+0.5 {
		t.Errorf("the line after the block starts at %v, want %v", top, 120+line)
	}
	for p := 2; p <= 3; p++ {
		if h := a.paraLayout(ed, p).Height; h != 0 {
			t.Errorf("paragraph %d of the block is %v high", p, h)
		}
	}
	// Without the block, the lines show again.
	blocks = nil
	tt.Frame()
	if h := a.paraLayout(ed, 2).Height; h == 0 {
		t.Error("the block's paragraph stayed collapsed")
	}
	// Overlapping blocks keep the first given.
	ed.blocks = ed.blocks[:0]
	el.TextBlocks(TextBlock{First: 1, Last: 2, Height: 10}, TextBlock{First: 2, Last: 3, Height: 10}, TextBlock{First: 0, Last: 0})
	if len(ed.blocks) != 2 || ed.blocks[0].First != 0 || ed.blocks[1].First != 1 {
		t.Errorf("blocks %v", ed.blocks)
	}
}

// TextLines.Caret places the caret before a rune as the area lays out its
// fonts: after larger runes, further along.
func TestTextLinesCaret(t *testing.T) {
	s := "abc\nabc"
	var lines TextLines
	tt := coreNewTester(func(c *context) {
		coreTextAreaBase(c, &s).Fill().TrackLines(&lines).TextRanges(TextRange{Start: 4, End: 7, Size: 40})
	}, 300, 300)
	tt.Frame()
	x0, y0, h0, ok := lines.Caret(3)
	x1, y1, h1, _ := lines.Caret(7)
	if !ok || x1 <= x0 || h1 <= h0 || y1 < y0+h0-0.5 {
		t.Errorf("carets at %v,%v %v and %v,%v %v", x0, y0, h0, x1, y1, h1)
	}
}

// A block shown, taken away and shown again leaves the tops of the lines
// after it where their heights put them: a collapsed line, 0 high, is
// measured, not unknown.
func TestTextBlocksComeAndGo(t *testing.T) {
	s := "a\nb\nc\nd\ne"
	blocks := []TextBlock{{First: 1, Last: 3, Height: 50}}
	var el *node
	tt := coreNewTester(func(c *context) {
		el = coreTextAreaBase(c, &s).Fill().TextBlocks(blocks...)
	}, 300, 400)
	ed := el.st.editor
	a := ed.area
	line := a.paraLayout(ed, 0).Height
	for range 3 {
		blocks = nil
		tt.Frame()
		if top := a.hs.top(4); top < 4*float64(line)-0.5 || top > 4*float64(line)+0.5 {
			t.Fatalf("without the block, the last line is at %v, want %v", top, 4*line)
		}
		blocks = []TextBlock{{First: 1, Last: 3, Height: 50}}
		tt.Frame()
		if top := a.hs.top(4); top < 50+float64(line)-0.5 || top > 50+float64(line)+0.5 {
			t.Fatalf("with the block, the last line is at %v, want %v", top, 50+line)
		}
	}
}
