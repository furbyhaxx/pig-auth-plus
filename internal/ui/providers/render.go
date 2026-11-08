// Package providers is the Providers dashboard tab: credential state per provider, never the
// credential itself.
package providers

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

// State is a provider's credential state.
type State int

// Credential states.
const (
	SignedOut State = iota
	SignedIn
	// Expired is an OAuth login whose access token has lapsed and could not be refreshed yet.
	Expired
	// Unknown means the host could not report the state.
	Unknown
)

// Row is one provider as the tab shows it.
type Row struct {
	ID    string
	Label string
	State State
	// Source names where the credential comes from, for example "OAuth" or "ANTHROPIC_API_KEY".
	Source string
	// Expires is the OAuth access-token expiry; zero when unknown or not OAuth.
	Expires time.Time
	// Login is the command that signs this provider in.
	Login string
	// Note is a short extra line such as "quota only".
	Note string
	// Err is a failure reading the state.
	Err string
}

func paint(th line.Theme, text, token string) string {
	return th.Paint(text, line.Style{Fg: &line.Color{Token: token}})
}

func fit(s string, width int) string { return tui.TruncateToWidth(s, width, "…", false) }

// Lines renders every row at width; each line fits. The row's identity survives every width and
// its state is a glyph plus a word.
func Lines(th line.Theme, rows []Row, selected string, width int, now time.Time) []string {
	var out []string
	for _, r := range rows {
		marker := "  "
		if r.ID == selected {
			marker = paint(th, "→ ", "accent")
		}
		head := marker + th.Paint(line.Plain(r.Label), line.Style{Bold: true}) + paint(th, " ("+r.ID+")", "muted")
		status := statusText(th, r, now)
		below := ""
		if tui.VisibleWidth(head)+2+tui.VisibleWidth(status) <= width {
			head += strings.Repeat(" ", width-tui.VisibleWidth(head)-tui.VisibleWidth(status)) + status
		} else {
			below = fit("    "+status, width)
		}
		out = append(out, fit(head, width))
		if below != "" {
			out = append(out, below)
		}
		for _, detail := range details(r, now) {
			out = append(out, fit(paint(th, "    "+detail, "muted"), width))
		}
		if r.State != SignedIn && r.Login != "" {
			out = append(out, fit(paint(th, "    sign in: "+r.Login, "muted"), width))
		}
	}
	return out
}

func statusText(th line.Theme, r Row, now time.Time) string {
	switch r.State {
	case SignedIn:
		return paint(th, "✓ signed in", "success")
	case Expired:
		return paint(th, "▲ token expired", "warning")
	case Unknown:
		return paint(th, "? unknown", "warning")
	}
	return paint(th, "○ signed out", "warning")
}

func details(r Row, now time.Time) []string {
	var out []string
	var parts []string
	if r.Source != "" && r.State != SignedOut {
		parts = append(parts, line.Plain(r.Source))
	}
	if !r.Expires.IsZero() {
		parts = append(parts, expiry(r.Expires, now))
	}
	if len(parts) > 0 {
		out = append(out, strings.Join(parts, " · "))
	}
	if r.Note != "" {
		out = append(out, line.Plain(r.Note))
	}
	if r.Err != "" {
		out = append(out, "error: "+line.Plain(r.Err))
	}
	return out
}

func expiry(at, now time.Time) string {
	d := at.Sub(now)
	if d <= 0 {
		return "token expired " + ago(-d) + " ago, refreshed on use"
	}
	return "token valid " + ago(d)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

type plainTheme struct{}

func (plainTheme) Paint(text string, _ line.Style) string { return text }

// PlainText renders the rows without styling for headless output.
func PlainText(rows []Row, width int, now time.Time) string {
	return strings.Join(Lines(plainTheme{}, rows, "", width, now), "\n")
}
