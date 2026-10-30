package terminal

import (
	"regexp"
	"strings"
	"unicode"
)

// cjkStop is the punctuation of Chinese and Japanese text, which ends a
// URL or path as a space does: the ideographic space and the CJK symbols,
// fullwidth punctuation, curly quotes and the ellipsis. CJK letters do
// not, so paths such as ~/文档/笔记.md stay whole.
const cjkStop = `\x{3000}-\x{303F}\x{FF01}-\x{FF0F}\x{FF1A}-\x{FF20}\x{FF3B}-\x{FF40}\x{FF5B}-\x{FF65}\x{2018}-\x{201F}\x{2026}`

// urlPattern finds the URLs programs print as text, which a Command+click
// opens as it does hyperlinks.
var urlPattern = regexp.MustCompile(`(?:https?|ftp|file)://[^\s"'<>` + "`" + `\x00-\x1f` + cjkStop + `]+|mailto:[^\s"'<>` + "`" + cjkStop + `]+`)

// pathPattern finds the file paths programs print as text: absolute, from
// ~, ./ or ../, with a slash, or a file name with an extension; each may end in
// :line or :line:col.
var pathPattern = regexp.MustCompile(`(?:(?:~|\.{1,2})?/[^\s"'<>` + "`" + `\x00-\x1f:` + cjkStop + `]+|[\w.@+-]+(?:/[\w.@+-]+)+/?)(?::\d+(?::\d+)?)?|[\w@+-][\w.@+-]*\.\w+(?::\d+(?::\d+)?)?`)

// urlAt returns the URL of text, a row whose runes are in columns cols, at
// column col, and the columns it spans, x0 up to x1, or "".
func urlAt(text []rune, cols []int, col int) (url string, x0, x1 int) {
	return matchAt(urlPattern, text, cols, col)
}

// pathAt returns the file path of text at column col, as urlAt, or "".
func pathAt(text []rune, cols []int, col int) (path string, x0, x1 int) {
	p, x0, x1 := matchAt(pathPattern, text, cols, col)
	// Do not turn dotted numbers such as version 1.2.3 into file links.
	if !strings.ContainsAny(p, "/:") && strings.IndexFunc(p, unicode.IsLetter) < 0 {
		return "", 0, 0
	}
	return p, x0, x1
}

func matchAt(re *regexp.Regexp, text []rune, cols []int, col int) (string, int, int) {
	s := string(text)
	for _, m := range re.FindAllStringIndex(s, -1) {
		u := trimURL(s[m[0]:m[1]])
		if u == "" {
			continue
		}
		start := len([]rune(s[:m[0]]))
		end := start + len([]rune(u))
		if start < len(cols) && cols[start] <= col && col <= cols[end-1] {
			return u, cols[start], cols[end-1] + 1
		}
	}
	return "", 0, 0
}

// trimURL trims what likely ends the sentence around a URL rather than the
// URL: punctuation, and closing brackets that the URL did not open.
func trimURL(u string) string {
	for len(u) > 0 {
		switch last := u[len(u)-1]; last {
		case '.', ',', ';', ':', '!', '?':
			u = u[:len(u)-1]
			continue
		case ')', ']', '}':
			open := map[byte]string{')': "(", ']': "[", '}': "{"}[last]
			if strings.Count(u, open) < strings.Count(u, string(last)) {
				u = u[:len(u)-1]
				continue
			}
		}
		return u
	}
	return u
}
