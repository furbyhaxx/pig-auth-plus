// Package antigravity is the antigravity provider: Google OAuth, Cloud Code project discovery,
// the agent model catalog and the Cloud Code envelope over PiG's google-generative-ai API.
package antigravity

import (
	"fmt"
	"net/http"
	"runtime"
)

// Verified against agy 1.4.8 (host:/mnt/Projects/furbyhaxx/pi-auth-plus/.project/analyses/antigravity-cli/tracking.md).
const (
	agyVersion    = "1.4.8"
	agyChangeList = "1007749967"
	// BaseURL is Cloud Code PA as the agy CLI uses it.
	BaseURL = "https://daily-cloudcode-pa.googleapis.com"
	// ID is the provider id.
	ID = "antigravity"
)

// userAgent is the CLI's; it reports Go's GOOS and GOARCH, as this process does.
func userAgent() string {
	return fmt.Sprintf("antigravity/cli/%s (aidev_client; os_type=%s; arch=%s; cl=%s; auth_method=consumer)", agyVersion, runtime.GOOS, runtime.GOARCH, agyChangeList)
}

// cliHeaders are the CLI's JSON request headers.
func cliHeaders(access string) http.Header {
	h := http.Header{"User-Agent": {userAgent()}, "Content-Type": {"application/json"}}
	if access != "" {
		h.Set("Authorization", "Bearer "+access)
	}
	return h
}
