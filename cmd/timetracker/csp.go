package main

import "net/http"

// cspPolicy is the Content-Security-Policy served for the embedded UI. The
// built frontend loads only its own bundled module script and stylesheet
// (script-src 'self'), so a strict policy holds; style-src additionally
// allows 'unsafe-inline' for React's inline style attributes. The Wails
// runtime and window.go bindings are injected natively by the webview host
// and are not governed by this page-level CSP.
const cspPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'; " +
	"object-src 'none'"

// cspMiddleware wraps the Wails AssetServer so every served document carries
// the Content-Security-Policy header. It is a defence-in-depth backstop:
// even if an unvalidated attacker-controlled URL or string reached the DOM,
// the policy blocks inline/javascript: script execution and cross-origin
// loads in the privileged renderer. Its signature matches
// assetserver.Middleware.
func cspMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", cspPolicy)
		next.ServeHTTP(w, r)
	})
}
