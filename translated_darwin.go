package mygo

import "syscall"

// translated reports whether Rosetta translates the app, an Intel build
// on Apple Silicon: sysctl.proc_translated is 1 then, 0 for native apps
// on Apple Silicon and missing on Intel Macs.
func translated() bool {
	s, err := syscall.Sysctl("sysctl.proc_translated")
	return err == nil && len(s) > 0 && s[0] == 1
}
