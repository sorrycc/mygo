//go:build linux && (amd64 || arm64)

package linux

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRestoreDataDirs checks the default data folders come back only when
// XDG_DATA_DIRS leaves out every folder with the MIME database, which GTK
// needs to show a file chooser at all.
func TestRestoreDataDirs(t *testing.T) {
	withMIME := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withMIME, "mime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(withMIME, "mime", "mime.cache"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{withMIME, withMIME},
		{empty + ":" + withMIME, empty + ":" + withMIME},
		{empty, empty + ":/usr/local/share:/usr/share"},
		{"/snap/x/current/usr/share:/usr/local/share:/usr/share" + ":" + empty, "/snap/x/current/usr/share:/usr/local/share:/usr/share" + ":" + empty},
	} {
		t.Setenv("XDG_DATA_DIRS", c.in)
		restoreDataDirs()
		if got := os.Getenv("XDG_DATA_DIRS"); got != c.want {
			t.Errorf("XDG_DATA_DIRS %q became %q, want %q", c.in, got, c.want)
		}
	}
}
