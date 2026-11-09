// Package providers is the Providers dashboard tab: credential state per provider and a
// read-only detail screen per provider, never the credential itself.
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
	// Aliases are the other accepted names of the provider.
	Aliases []string
	State   State
	// Source names where the credential comes from, for example "OAuth login" or "ANTHROPIC_API_KEY".
	Source string
	// Expires is the OAuth access-token expiry; zero when unknown or not OAuth.
	Expires time.Time
	// Login is the command that signs this provider in; Logout the command that signs it out.
	Login, Logout string
	// Note is a short extra line such as "quota only".
	Note string
	// Err is a failure reading the state.
	Err string
}

// Extra is what the owner knows about a provider from cached state; it is read on every render,
// so it must not do I/O.
type Extra struct {
	// Plan is the reported plan badge, empty when unreported.
	Plan string
	// Headline is the quota summary shown at the right of the row, empty when none.
	Headline string
	// Fields are the detail rows after the credential rows.
	Fields []Field
}

// Field is one row of the detail screen.
type Field struct {
	Label, Value string
	// Jump names the tab that owns the value; Enter on the row opens it.
	Jump string
}

func paint(th line.Theme, text, token string) string {
	return th.Paint(text, line.Style{Fg: &line.Color{Token: token}})
}

func fit(s string, width int) string { return tui.TruncateToWidth(s, width, "…", false) }

// glyph is the state glyph with its word; color is applied by the caller.
func glyph(s State) (mark, word, token string) {
	switch s {
	case SignedIn:
		return "●", "signed in", "success"
	case Expired:
		return "◐", "token expired", "warning"
	case Unknown:
		return "?", "unknown", "warning"
	}
	return "○", "not signed in", "muted"
}

// dur is a compact duration: "6h 12m", "41m", "9d".
func dur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func expiry(r Row, now time.Time, long bool) string {
	if r.Expires.IsZero() {
		return ""
	}
	if d := r.Expires.Sub(now); d > 0 {
		if long {
			return "expires in " + dur(d)
		}
		return "expires " + dur(d)
	}
	if long {
		return "token expired " + dur(now.Sub(r.Expires)) + " ago"
	}
	return "expired"
}

// candidate is one way to lay a row out, from most to least informative.
type candidate struct{ mid, right string }

// candidates lists the layouts of a row. The state glyph and the label survive in every one.
func candidates(th line.Theme, r Row, ex Extra, now time.Time) []candidate {
	mark, word, token := glyph(r.State)
	g := paint(th, mark, token)
	plain := func(parts ...string) string {
		var out []string
		for _, p := range parts {
			if p != "" {
				out = append(out, line.Plain(p))
			}
		}
		return strings.Join(out, " · ")
	}
	right := paint(th, line.Plain(ex.Headline), "muted")
	switch r.State {
	case SignedIn:
		return []candidate{
			{g + " " + plain(r.Source, ex.Plan, expiry(r, now, true)), right},
			{g + " " + plain(expiry(r, now, false)), right},
			{g + " " + plain(expiry(r, now, false)), ""},
			{g, ""},
		}
	case Expired:
		return []candidate{
			{g + " " + plain(word, "refreshes on use"), right},
			{g + " " + word, right},
			{g, ""},
		}
	case Unknown:
		return []candidate{{g + " " + plain(word, r.Err), ""}, {g + " " + word, ""}, {g, ""}}
	}
	return []candidate{
		{g + " " + plain(word, r.Login), ""},
		{g + " " + plain(r.Login), ""},
		{g, ""},
	}
}

// rowLine lays one provider out at width: cursor, label column, state, and the headline quota
// right-aligned. Secondary text is dropped before the label or the state glyph.
func rowLine(th line.Theme, r Row, ex Extra, labelW int, selected bool, width int, now time.Time) string {
	marker := "  "
	label := line.Plain(r.Label)
	if selected {
		marker = paint(th, "→ ", "accent")
		label = th.Paint(label, line.Style{Bold: true})
	}
	labelW = min(labelW, max(width/3, 6))
	head := marker + fit(label, labelW)
	head += strings.Repeat(" ", max(labelW-tui.VisibleWidth(fit(label, labelW)), 0)) + " "
	var last string
	for _, c := range candidates(th, r, ex, now) {
		l := head + c.mid
		if c.right != "" {
			gap := width - tui.VisibleWidth(l) - tui.VisibleWidth(c.right)
			if gap < 2 {
				continue
			}
			l += strings.Repeat(" ", gap) + c.right
		}
		if tui.VisibleWidth(l) <= width {
			return l
		}
		last = l
	}
	return fit(last, width)
}

type plainTheme struct{}

func (plainTheme) Paint(text string, _ line.Style) string { return text }

// PlainText renders the rows without styling for headless output: one block per provider.
func PlainText(rows []Row, extra func(id string) Extra, width int, now time.Time) string {
	var out []string
	for _, r := range rows {
		ex := Extra{}
		if extra != nil {
			ex = extra(r.ID)
		}
		out = append(out, fit(rowLine(plainTheme{}, r, ex, labelWidth(rows), false, width, now), width))
		if r.Note != "" {
			out = append(out, fit("    "+line.Plain(r.Note), width))
		}
		if r.Err != "" {
			out = append(out, fit("    error: "+line.Plain(r.Err), width))
		}
	}
	return strings.Join(out, "\n")
}

func labelWidth(rows []Row) int {
	w := 0
	for _, r := range rows {
		w = max(w, tui.VisibleWidth(line.Plain(r.Label)))
	}
	return w
}

// credentialValue is the Credential detail row.
func credentialValue(r Row, now time.Time) string {
	mark, word, _ := glyph(r.State)
	parts := []string{mark + " " + word}
	if r.Source != "" && r.State != SignedOut {
		parts = append(parts, r.Source)
	}
	if e := expiry(r, now, true); e != "" {
		parts = append(parts, e)
	}
	if r.Err != "" {
		parts = append(parts, "error: "+r.Err)
	}
	return line.Plain(strings.Join(parts, " · "))
}

// detailFields are the detail rows of one provider: the credential rows first, then the
// owner's.
func detailFields(r Row, ex Extra, now time.Time) []Field {
	name := r.ID
	if len(r.Aliases) > 0 {
		name += " · alias " + strings.Join(r.Aliases, ", ")
	}
	fields := []Field{
		{Label: "Provider", Value: line.Plain(r.Label) + " (" + name + ")"},
		{Label: "Credential", Value: credentialValue(r, now)},
	}
	if ex.Plan != "" {
		fields = append(fields, Field{Label: "Plan", Value: ex.Plan})
	}
	if r.Login != "" || r.Logout != "" {
		fields = append(fields, Field{Label: "Sign in/out", Value: strings.TrimSpace(r.Login + "  " + r.Logout)})
	}
	if r.Note != "" {
		fields = append(fields, Field{Label: "Note", Value: r.Note})
	}
	return append(fields, ex.Fields...)
}

// fieldLine lays one detail row out: cursor, fixed label column, value, jump hint.
func fieldLine(th line.Theme, f Field, labelW int, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = paint(th, "→ ", "accent")
	}
	l := marker + paint(th, fit(f.Label, labelW), "muted")
	l += strings.Repeat(" ", max(labelW-tui.VisibleWidth(fit(f.Label, labelW)), 0)) + "  "
	value := line.Plain(f.Value)
	hint := ""
	if f.Jump != "" {
		hint = "→ open in " + strings.ToUpper(f.Jump[:1]) + f.Jump[1:]
	}
	room := width - tui.VisibleWidth(l)
	if hint != "" && room-tui.VisibleWidth(value) >= tui.VisibleWidth(hint)+2 {
		gap := room - tui.VisibleWidth(value) - tui.VisibleWidth(hint)
		return l + value + strings.Repeat(" ", gap) + paint(th, hint, "accent")
	}
	return fit(l+value, width)
}
