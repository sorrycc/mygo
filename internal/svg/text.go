package svg

import (
	"image"
	"math"
	"strings"
	"unicode/utf8"
)

// Font is the font of a run of text: its families as CSS lists them, its
// size in user units, its weight from 100 to 900, and its slant.
type Font struct {
	Family string
	Size   float64
	Weight int
	Italic bool
}

// TextImage is a run of text set on one line, and the coverage of its
// glyphs.
type TextImage struct {
	// Mask holds a byte of coverage for each of W×H pixels.
	Mask []byte
	W, H int
	// X and Y are where the run's baseline starts in the mask, in pixels.
	X, Y float64
	// Advance is the run's width, Ascent and Descent the extent of its
	// font above and below the baseline, in user units.
	Advance, Ascent, Descent float64
}

// Fonts sets the text of documents, which Draw draws with: without Fonts,
// it leaves text out.
type Fonts interface {
	// Text sets s in font f on one line, its mask drawn at scale pixels
	// per user unit.
	Text(s string, f Font, scale float64) TextImage
}

// textNode is a text element: the chunks of text it draws, each starting
// at a position of its own and anchored there.
type textNode struct {
	chunks []textChunk
}

type textChunk struct {
	// x and y are where the chunk starts, when hasX and hasY; otherwise
	// it starts where the text before it ends.
	x, y       float64
	hasX, hasY bool
	anchor     uint8
	runs       []textRun
}

// textRun is text of one style, shifted by dx and dy from where the text
// before it ends.
type textRun struct {
	s           string
	font        Font
	dx, dy      float64
	baseline    uint8
	fill        paint
	fillOpacity float64
	visible     bool
}

// textChar is a character of a text element, with the style of the
// element it is in and the positions the elements around it give it.
type textChar struct {
	r                        rune
	st                       *style
	x, y, dx, dy             float64
	hasX, hasY, hasD         bool
	setX, setY, setDX, setDY bool
}

// maxTextChars bounds the characters of a document's text elements.
const maxTextChars = 1 << 16

// text compiles a text element of style st, or returns nil when it draws
// nothing.
func (c *compiler) text(e *elem, st *style) *textNode {
	var chars []textChar
	space := true // drops the spaces text starts with
	var collect func(e *elem, st *style)
	collect = func(e *elem, st *style) {
		start := len(chars)
		for _, k := range e.kids {
			switch k.name {
			case "#text":
				for _, r := range k.text {
					if len(chars) >= maxTextChars {
						break
					}
					switch r {
					case '\n', '\r':
						if !st.preserve {
							continue
						}
						r = ' '
					case '\t':
						r = ' '
					}
					if r == ' ' && !st.preserve {
						if space {
							continue
						}
						space = true
					} else {
						space = false
					}
					chars = append(chars, textChar{r: r, st: st})
				}
			case "tspan", "a", "textPath":
				if c.budget <= 0 {
					continue
				}
				c.budget--
				kst := c.style(k, st)
				if !kst.display {
					continue
				}
				collect(k, &kst)
			}
		}
		c.positions(e, chars[start:])
	}
	collect(e, st)
	if n := len(chars); n > 0 && chars[n-1].r == ' ' && !chars[n-1].st.preserve {
		chars = chars[:n-1]
	}
	if len(chars) == 0 {
		return nil
	}
	t := &textNode{}
	var b strings.Builder
	var run *textRun
	end := func() {
		if run != nil {
			run.s = b.String()
			b.Reset()
		}
	}
	for i := range chars {
		ch := &chars[i]
		if i == 0 || ch.hasX || ch.hasY {
			end()
			t.chunks = append(t.chunks, textChunk{x: ch.x, y: ch.y, hasX: ch.hasX, hasY: ch.hasY, anchor: ch.st.anchor})
			run = nil
		}
		k := &t.chunks[len(t.chunks)-1]
		if run == nil || ch.st != chars[i-1].st || ch.hasD {
			end()
			s := ch.st
			bl := s.baseline
			if s.align != baselineAuto {
				bl = s.align
			}
			k.runs = append(k.runs, textRun{
				font: Font{Family: s.fontFamily, Size: s.fontSize, Weight: s.fontWeight, Italic: s.italic},
				dx:   ch.dx, dy: ch.dy, baseline: bl,
				fill: c.paint(s.fill, s), fillOpacity: s.fillOpacity, visible: s.visible,
			})
			run = &k.runs[len(k.runs)-1]
		}
		b.WriteRune(ch.r)
	}
	end()
	return t
}

// positions gives the characters of element e the positions its x, y, dx
// and dy attributes list, one for each, where an element within e did
// not give them one.
func (c *compiler) positions(e *elem, chars []textChar) {
	if e.name == "textPath" {
		return
	}
	list := func(name string, base float64) []float64 {
		v, ok := e.attrs[name]
		if !ok {
			return nil
		}
		var out []float64
		for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
			l, ok := parseLength(f, base)
			if !ok {
				return nil
			}
			out = append(out, l)
		}
		return out
	}
	xs, ys := list("x", c.vw), list("y", c.vh)
	dxs, dys := list("dx", c.vw), list("dy", c.vh)
	for i := range chars {
		ch := &chars[i]
		if i < len(xs) && !ch.setX {
			ch.x, ch.hasX, ch.setX = xs[i], true, true
		}
		if i < len(ys) && !ch.setY {
			ch.y, ch.hasY, ch.setY = ys[i], true, true
		}
		if i < len(dxs) && !ch.setDX {
			ch.dx, ch.setDX = dxs[i], true
			ch.hasD = ch.hasD || ch.dx != 0
		}
		if i < len(dys) && !ch.setDY {
			ch.dy, ch.setDY = dys[i], true
			ch.hasD = ch.hasD || ch.dy != 0
		}
	}
}

// estimate returns a box around the text, before Fonts sets it: as wide as
// its characters at half its size each, and as high as its size.
func (t *textNode) estimate() (box, bool) {
	var b box
	ok := false
	var x, y float64
	for _, k := range t.chunks {
		if k.hasX {
			x = k.x
		}
		if k.hasY {
			y = k.y
		}
		for _, r := range k.runs {
			x, y = x+r.dx, y+r.dy
			w := float64(utf8.RuneCountInString(r.s)) * r.font.Size / 2
			rb := box{x, y - r.font.Size*0.8, w, r.font.Size}
			if ok {
				b = b.union(rb)
			} else {
				b, ok = rb, true
			}
			x += w
		}
	}
	return b, ok
}

// placedRun is a run of text set, at (x, y) in user space.
type placedRun struct {
	run  *textRun
	img  TextImage
	x, y float64
}

// maxGlyphPixels bounds the size text is rasterized at: larger text is
// rasterized at this size and stretched.
const maxGlyphPixels = 512

// text draws a text node, whose user space maps to pixels by m.
func (r *renderer) text(t *textNode, m matrix, dst *layer, alpha float64) {
	scale := m.stretch()
	if r.fonts == nil || !(scale > 0) {
		return
	}
	placed := r.placed[:0]
	var x, y float64
	var bbox box
	hasBox := false
	for ci := range t.chunks {
		k := &t.chunks[ci]
		if k.hasX {
			x = k.x
		}
		if k.hasY {
			y = k.y
		}
		start := len(placed)
		x0 := x
		for ri := range k.runs {
			run := &k.runs[ri]
			x, y = x+run.dx, y+run.dy
			size := run.font.Size
			if size <= 0 || run.s == "" {
				continue
			}
			rs := min(scale, maxGlyphPixels/size)
			img := r.fonts.Text(run.s, run.font, rs)
			placed = append(placed, placedRun{run: run, img: img, x: x, y: y + shift(run.baseline, img)})
			x += img.Advance
		}
		var dx float64
		switch k.anchor {
		case anchorMiddle:
			dx = (x - x0) / 2
		case anchorEnd:
			dx = x - x0
		}
		for i := start; i < len(placed); i++ {
			p := &placed[i]
			p.x -= dx
			rb := box{p.x, p.y - p.img.Ascent, p.img.Advance, p.img.Ascent + p.img.Descent}
			if hasBox {
				bbox = bbox.union(rb)
			} else {
				bbox, hasBox = rb, true
			}
		}
		x -= dx
	}
	for i := range placed {
		p := &placed[i]
		if !p.run.visible || p.run.fill.kind == paintNone {
			continue
		}
		if rect, ok := r.coverText(p, m); ok {
			r.paint(dst, rect, p.run.fill, alpha*p.run.fillOpacity, m, bbox)
		}
	}
	clear(placed)
	r.placed = placed[:0]
}

// shift returns how far down a baseline moves the text's alphabetic
// baseline, which y places.
func shift(b uint8, img TextImage) float64 {
	switch b {
	case baselineCentral:
		return (img.Ascent - img.Descent) / 2
	case baselineHanging:
		return img.Ascent * 0.8
	case baselineMathematical:
		return img.Ascent / 2
	case baselineTop:
		return img.Ascent
	case baselineBottom:
		return -img.Descent
	}
	return 0
}

// coverText puts the coverage of a run's mask, placed in user space and
// mapped to pixels by m, into r.cov, returning the rectangle of the canvas
// it covers.
func (r *renderer) coverText(p *placedRun, m matrix) (image.Rectangle, bool) {
	img := &p.img
	if img.W <= 0 || img.H <= 0 || len(img.Mask) < img.W*img.H {
		return image.Rectangle{}, false
	}
	rs := math.Min(m.stretch(), maxGlyphPixels/p.run.font.Size)
	// The mask's pixels to the canvas's.
	mm := m.mul(translate(p.x, p.y)).mul(scaling(1/rs, 1/rs)).mul(translate(-img.X, -img.Y))
	inv, ok := mm.invert()
	if !ok {
		return image.Rectangle{}, false
	}
	b := box{0, 0, float64(img.W), float64(img.H)}.transform(mm)
	const far = 1 << 24
	if !(b.x > -far && b.y > -far && b.x+b.w < far && b.y+b.h < far) {
		return image.Rectangle{}, false
	}
	rect := image.Rect(int(math.Floor(b.x)), int(math.Floor(b.y)), int(math.Ceil(b.x+b.w))+1, int(math.Ceil(b.y+b.h))+1).
		Intersect(image.Rect(0, 0, r.w, r.h))
	if rect.Empty() {
		return image.Rectangle{}, false
	}
	w, h := rect.Dx(), rect.Dy()
	if cap(r.cov) < w*h {
		r.cov = make([]byte, w*h)
	}
	r.cov = r.cov[:w*h]
	clear(r.cov)
	const eps = 1e-6
	if math.Abs(mm[0]-1) < eps && math.Abs(mm[3]-1) < eps && math.Abs(mm[1]) < eps && math.Abs(mm[2]) < eps {
		// Not turned nor stretched: the mask's pixels, on whole pixels.
		ox, oy := int(math.Round(mm[4])), int(math.Round(mm[5]))
		for y := range h {
			my := rect.Min.Y + y - oy
			if my < 0 || my >= img.H {
				continue
			}
			for x := range w {
				if mx := rect.Min.X + x - ox; mx >= 0 && mx < img.W {
					r.cov[y*w+x] = img.Mask[my*img.W+mx]
				}
			}
		}
		return rect, true
	}
	// Turned or stretched: the mask sampled bilinearly at pixel centers.
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= img.W || y >= img.H {
			return 0
		}
		return float64(img.Mask[y*img.W+x])
	}
	for y := range h {
		for x := range w {
			q := inv.apply(point{float64(rect.Min.X+x) + 0.5, float64(rect.Min.Y+y) + 0.5})
			qx, qy := q.x-0.5, q.y-0.5
			fx, fy := math.Floor(qx), math.Floor(qy)
			ix, iy := int(fx), int(fy)
			if ix < -1 || iy < -1 || ix >= img.W || iy >= img.H {
				continue
			}
			tx, ty := qx-fx, qy-fy
			v := (at(ix, iy)*(1-tx)+at(ix+1, iy)*tx)*(1-ty) + (at(ix, iy+1)*(1-tx)+at(ix+1, iy+1)*tx)*ty
			r.cov[y*w+x] = uint8(math.Min(255, v+0.5))
		}
	}
	return rect, true
}
