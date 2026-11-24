//go:build !dev

package fbhttp

// global headers to append to every response
var globalHeaders = map[string]string{
	"Cache-Control":          "no-cache, no-store, must-revalidate",
	"X-Content-Type-Options": "nosniff",
	"X-Frame-Options":        "DENY",
	"Referrer-Policy":        "strict-origin-when-cross-origin",
	"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
}
