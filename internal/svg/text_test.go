package svg

import (
	"image"
	"image/color"
	"math"
	"testing"
	"unicode/utf8"
)

// boxFonts sets every character as a box half as wide as the font's size,
// from 0.8 of its size above the baseline to 0.2 below it, and records
// what it sets.
type boxFonts struct {
	runs  []string
	fonts []Font
}

func (f *boxFonts) Text(s string, ft Font, scale float64) TextImage {
	f.runs, f.fonts = append(f.runs, s), append(f.fonts, ft)
	adv := float64(utf8.RuneCountInString(s)) * ft.Size / 2
	w, h := int(math.Round(adv*scale)), int(math.Round(ft.Size*scale))
	mask := make([]byte, w*h)
	for i := range mask {
		mask[i] = 255
	}
	return TextImage{Mask: mask, W: w, H: h, Y: 0.8 * ft.Size * scale, Advance: adv, Ascent: 0.8 * ft.Size, Descent: 0.2 * ft.Size}
}

func drawText(t *testing.T, src string, w, h int) (*image.RGBA, *boxFonts) {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	f := &boxFonts{}
	d.Draw(img, red, false, f)
	return img, f
}

// inked returns the box of the pixels drawn.
func inked(img *image.RGBA) image.Rectangle {
	var r image.Rectangle
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).A > 127 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

func TestTextIsDrawn(t *testing.T) {
	src := `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="100">
  <text x="16" y="55" font-size="20" fill="blue">中文 Hello</text>
</svg>`
	img, f := drawText(t, src, 320, 100)
	if len(f.runs) != 1 || f.runs[0] != "中文 Hello" {
		t.Fatalf("runs %q", f.runs)
	}
	if got, want := inked(img), image.Rect(16, 39, 96, 59); got != want {
		t.Errorf("text covers %v, want %v", got, want)
	}
	if c := at(img, 50, 50); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("text is %v", c)
	}
	// Without fonts, text is left out.
	d, _ := Parse([]byte(src))
	img = image.NewRGBA(image.Rect(0, 0, 320, 100))
	d.Draw(img, red, false, nil)
	if r := inked(img); !r.Empty() {
		t.Errorf("text without fonts covers %v", r)
	}
}

func TestTextAnchors(t *testing.T) {
	for _, tc := range []struct {
		anchor string
		x0     int
	}{{"start", 100}, {"middle", 90}, {"end", 80}} {
		img, _ := drawText(t, `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="50">
  <text x="100" y="30" font-size="10" text-anchor="`+tc.anchor+`">abc<tspan>d</tspan></text>
</svg>`, 200, 50)
		if got := inked(img); got.Min.X != tc.x0 || got.Dx() != 20 {
			t.Errorf("%s: text covers %v, want from x %d, 20 wide", tc.anchor, got, tc.x0)
		}
	}
}

func TestTextSpansInheritAndMove(t *testing.T) {
	src := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100">
  <style>.big { font-size: 20px }</style>
  <text x="10" y="50" font-family="Inter, 'Noto Sans SC'" font-weight="bold" fill="#00f">a<tspan class="big" dy="-10">b</tspan><tspan x="100" fill="lime" font-style="italic">c</tspan></text>
</svg>`
	img, f := drawText(t, src, 200, 100)
	if len(f.runs) != 3 || f.runs[0] != "a" || f.runs[1] != "b" || f.runs[2] != "c" {
		t.Fatalf("runs %q", f.runs)
	}
	want := []Font{
		{Family: "Inter, 'Noto Sans SC'", Size: 16, Weight: 700},
		{Family: "Inter, 'Noto Sans SC'", Size: 20, Weight: 700},
		{Family: "Inter, 'Noto Sans SC'", Size: 16, Weight: 700, Italic: true},
	}
	for i, w := range want {
		if f.fonts[i] != w {
			t.Errorf("run %d font %+v, want %+v", i, f.fonts[i], w)
		}
	}
	// b follows a, 10 higher; c starts at x 100 on b's baseline.
	if c := at(img, 22, 30); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("b is %v", c)
	}
	if c := at(img, 104, 35); c != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("c is %v", c)
	}
}

func TestTextTransforms(t *testing.T) {
	img, _ := drawText(t, `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100">
  <g transform="translate(50 20) scale(2)"><text y="10" font-size="10">ab</text></g>
</svg>`, 200, 100)
	if got, want := inked(img), image.Rect(50, 24, 70, 44); got != want {
		t.Errorf("scaled text covers %v, want %v", got, want)
	}
	img, _ = drawText(t, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
  <text transform="translate(50 50) rotate(90)" font-size="10">abcd</text>
</svg>`, 100, 100)
	// Turned a quarter, the text runs down from (50, 50), its ascent right
	// of x 50.
	if got := inked(img); got != image.Rect(48, 50, 58, 70) {
		t.Errorf("turned text covers %v", got)
	}
}

func TestTextSpaces(t *testing.T) {
	_, f := drawText(t, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
  <text y="20">
    a   b	<tspan> c </tspan>
  </text>
  <text y="40" xml:space="preserve"> d  e </text>
  <text y="60"> </text>
</svg>`, 100, 100)
	if len(f.runs) != 3 || f.runs[0] != "a b " || f.runs[1] != "c" || f.runs[2] != " d  e " {
		t.Errorf("runs %q", f.runs)
	}
}

func TestTextBaselines(t *testing.T) {
	for _, tc := range []struct {
		baseline string
		y0       int
	}{{"auto", 42}, {"central", 45}, {"text-before-edge", 50}, {"text-after-edge", 40}} {
		img, _ := drawText(t, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
  <text y="50" font-size="10" dominant-baseline="`+tc.baseline+`">a</text>
</svg>`, 100, 100)
		if got := inked(img); got.Min.Y != tc.y0 {
			t.Errorf("%s: text covers %v, want from y %d", tc.baseline, got, tc.y0)
		}
	}
}

func TestFontShorthandAndSizes(t *testing.T) {
	_, f := drawText(t, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
  <text y="20" style="font: italic bold 12px/1.5 'DejaVu Sans', sans-serif">a<tspan font-size="2em">b</tspan><tspan font-size="50%">c</tspan><tspan font-size="large">d</tspan></text>
</svg>`, 100, 100)
	want := []Font{
		{Family: "'DejaVu Sans', sans-serif", Size: 12, Weight: 700, Italic: true},
		{Family: "'DejaVu Sans', sans-serif", Size: 24, Weight: 700, Italic: true},
		{Family: "'DejaVu Sans', sans-serif", Size: 6, Weight: 700, Italic: true},
		{Family: "'DejaVu Sans', sans-serif", Size: 18, Weight: 700, Italic: true},
	}
	if len(f.fonts) != len(want) {
		t.Fatalf("fonts %+v", f.fonts)
	}
	for i, w := range want {
		if f.fonts[i] != w {
			t.Errorf("run %d font %+v, want %+v", i, f.fonts[i], w)
		}
	}
}
