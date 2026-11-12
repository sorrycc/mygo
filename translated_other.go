//go:build !darwin

package mygo

// translated reports whether Rosetta translates the app, which only macOS
// does (translated_darwin.go).
func translated() bool { return false }
