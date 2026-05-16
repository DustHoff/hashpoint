//go:build windows

package feedback

// NewDefaultTokenStore returns the production wincred-backed store.
// Wired from main on Windows; non-Windows builds use the in-memory
// fallback in default_store_other.go so CI / dev environments still
// compile and exercise the feedback flow end-to-end.
func NewDefaultTokenStore() TokenStore { return NewWinCredTokenStore() }
