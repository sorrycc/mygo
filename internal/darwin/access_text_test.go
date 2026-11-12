//go:build darwin

package darwin

import (
	"testing"
	"unicode/utf16"

	"github.com/egoist/mygo/internal/platform"
)

// TestNSStringLeadingBOM keeps a leading U+FEFF, which decoding UTF-8
// drops as a byte order mark.
func TestNSStringLeadingBOM(t *testing.T) {
	load()
	withPool(func() {
		for _, s := range []string{"\ufeffab", "a\ufeffb", "\ufeff", "\ufeff😀"} {
			want := len(utf16.Encode([]rune(s)))
			if got := sendInt(nsString(s), "length"); got != want {
				t.Errorf("nsString(%q) length = %d, want %d", s, got, want)
			}
		}
	})
}

// TestAttributedAccessStringLeadingBOM asks for ranges of a value that
// starts with U+FEFF, as VoiceOver does of a file saved with one: adding
// attributes past the string's end raised and aborted the app.
func TestAttributedAccessStringLeadingBOM(t *testing.T) {
	load()
	n := &platform.AccessNode{
		Value: "\ufeffpackage main",
		TextRuns: []platform.AccessTextRun{
			{Start: 0, End: 8, Weight: 700, Underline: true},
			{Start: 8, End: 13, Italic: true, Color: [4]uint8{255, 0, 0, 255}},
		},
	}
	withPool(func() {
		for _, c := range []struct {
			r    nsRange
			want int
		}{
			{nsRange{Location: 0, Length: 13}, 13},
			{nsRange{Location: 0, Length: 1}, 1},
			{nsRange{Location: 1, Length: 12}, 12},
			{nsRange{Location: 0, Length: 100}, 13},
			{nsRange{Location: 13, Length: 1}, 0},
		} {
			as := attributedAccessString(n, c.r)
			if got := sendInt(as, "length"); got != c.want {
				t.Errorf("range %v: length = %d, want %d", c.r, got, c.want)
			}
		}
	})
}
