package models

import (
	"sort"
	"strings"
	"sync"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Tab implements frame.Tab over a model list the owner supplies; it reads no host state while
// rendering.
type Tab struct {
	models func() []Model
	failed func() string

	mu     sync.Mutex
	query  string
	typing bool
	sel    string // key of the selected model
	top    int
	detail *Model
	dtop   int
	dlast  int
	height int
}

var _ frame.Tab = (*Tab)(nil)

// New builds the tab; models returns the cached catalog, nil while it is loading, and failed
// the reason the catalog could not be read, when it could not.
func New(models func() []Model, failed func() string) *Tab {
	return &Tab{models: models, failed: failed}
}

// ID implements frame.Tab.
func (t *Tab) ID() string { return "models" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Models" }

// Dirty implements frame.Tab; browsing stages nothing.
func (t *Tab) Dirty() bool { return false }

// Discard implements frame.Tab.
func (t *Tab) Discard() {}

// shown is the catalog filtered by the query, ordered by provider then id.
func (t *Tab) shown() []Model {
	all := t.models()
	out := make([]Model, 0, len(all))
	for _, m := range all {
		if match(m, t.query) {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func indexOf(shown []Model, key string) int {
	for i, m := range shown {
		if m.Key() == key {
			return i
		}
	}
	return 0
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	th := uitheme.Theme{T: env.Theme}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.height = env.Rows
	if t.detail != nil {
		lines := detailLines(th, *t.detail, env.Width)
		t.dlast = len(lines)
		t.dtop = max(0, min(t.dtop, len(lines)-env.Rows))
		end := min(t.dtop+env.Rows, len(lines))
		return lines[t.dtop:end:end]
	}
	all := t.models()
	if len(all) == 0 {
		if msg := t.failed(); msg != "" {
			return []string{fit(paint(th, " ▲ the model catalog could not be read: "+line.Plain(msg), "warning"), env.Width)}
		}
		return []string{"  reading the model catalog…"}
	}
	shown := t.shown()
	foot := []string{}
	q := " " + paint(th, "search ", "dim") + t.query
	if t.typing {
		q += "▏"
	}
	if t.query != "" || t.typing {
		foot = append(foot, fit(q, env.Width))
	}
	if len(shown) == 0 {
		return append([]string{"  no model matches"}, foot...)
	}
	sel := indexOf(shown, t.sel)
	t.sel = shown[sel].Key()
	lines, at := listLines(th, shown, sel, env.Width)
	room := max(env.Rows-len(foot), 3)
	if at[sel] < t.top {
		t.top = at[sel]
		if sel == 0 || shown[sel-1].Provider != shown[sel].Provider {
			t.top-- // keep the provider heading with its first row
		}
	} else if at[sel] >= t.top+room {
		t.top = at[sel] - room + 1
	}
	t.top = max(0, min(t.top, len(lines)-room))
	end := min(t.top+room, len(lines))
	return append(append([]string(nil), lines[t.top:end]...), foot...)
}

// Selected is the model under the cursor, when the list is showing.
func (t *Tab) Selected() (Model, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.detail != nil {
		return *t.detail, true
	}
	shown := t.shown()
	if len(shown) == 0 {
		return Model{}, false
	}
	return shown[indexOf(shown, t.sel)], true
}

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.typing {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			t.query, t.typing = "", false
		case kb.Matches(data, "tui.select.confirm"):
			t.typing = false
		case tui.MatchesKey(data, "backspace"):
			if r := []rune(t.query); len(r) > 0 {
				t.query = string(r[:len(r)-1])
			}
		default:
			for _, r := range data {
				if unicode.IsPrint(r) {
					t.query += string(r)
				}
			}
		}
		return true
	}
	if t.detail != nil {
		switch {
		case kb.Matches(data, "tui.select.cancel"), tui.MatchesKey(data, "left"):
			t.detail, t.dtop = nil, 0
		case kb.Matches(data, "tui.select.down"):
			t.dtop = max(0, min(t.dtop+1, t.dlast-t.height))
		case kb.Matches(data, "tui.select.up"):
			t.dtop = max(0, t.dtop-1)
		default:
			return false
		}
		return true
	}
	shown := t.shown()
	i := indexOf(shown, t.sel)
	switch {
	case data == "/":
		t.typing = true
	case kb.Matches(data, "tui.select.down"), data == "j":
		if len(shown) > 0 {
			t.sel = shown[min(i+1, len(shown)-1)].Key()
		}
	case kb.Matches(data, "tui.select.up"), data == "k":
		if len(shown) > 0 {
			t.sel = shown[max(i-1, 0)].Key()
		}
	case kb.Matches(data, "tui.select.pageDown"):
		if len(shown) > 0 {
			t.sel = shown[min(i+max(t.height-2, 1), len(shown)-1)].Key()
		}
	case kb.Matches(data, "tui.select.pageUp"):
		if len(shown) > 0 {
			t.sel = shown[max(i-max(t.height-2, 1), 0)].Key()
		}
	case kb.Matches(data, "tui.select.confirm"), tui.MatchesKey(data, "right"):
		if len(shown) > 0 {
			m := shown[i]
			t.detail, t.dtop = &m, 0
		}
	case kb.Matches(data, "tui.select.cancel") && t.query != "":
		t.query = ""
	default:
		return false
	}
	return true
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.typing:
		return []string{sdk.KeyHint("tui.select.confirm", "keep filter"), sdk.KeyHint("tui.select.cancel", "clear")}
	case t.detail != nil:
		return []string{sdk.KeyHint("tui.select.cancel", "back")}
	}
	return []string{sdk.RawKeyHint("/", "search"), sdk.KeyHint("tui.select.confirm", "detail")}
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string { return strings.Split(helpText, "\n") }

const helpText = ` Models
 Every model PiG can list, grouped by provider, with its context window on the right.
 /  search provider, id and name (all words must match)     Enter  open the detail
 Up/Down or j/k  move     PageUp/PageDown  page     Esc  clear the search, then back
 alias and override tags mark models changed by pig-auth-plus.`
