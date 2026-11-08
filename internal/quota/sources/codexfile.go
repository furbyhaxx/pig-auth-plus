package sources

import (
	"encoding/json"
	"os"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// CodexFileAt reads the pi-quotas credential file read-only. It returns nil when the file is
// absent, malformed or its access token has expired: refreshing it belongs to the tool that
// wrote it, and PiG's own `openai-codex` login is the supported path.
func CodexFileAt(path string) CodexFile {
	return func(now time.Time) *quota.Auth {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var c struct {
			Type      string  `json:"type"`
			Access    string  `json:"access"`
			Refresh   string  `json:"refresh"`
			Expires   float64 `json:"expires"`
			AccountID string  `json:"accountId"`
		}
		if json.Unmarshal(raw, &c) != nil || c.Type != "oauth" || c.Access == "" || c.Refresh == "" || c.Expires <= float64(now.UnixMilli()) {
			return nil
		}
		return &quota.Auth{Token: c.Access, AccountID: c.AccountID}
	}
}
