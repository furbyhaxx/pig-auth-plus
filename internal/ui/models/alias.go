package models

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

// Alias is the editable definition of a model alias. Blank optional fields inherit from the
// source model.
type Alias struct {
	Provider, Source, ID, Name string
	ContextWindow, MaxTokens   int64
}

const (
	aID     = "id"
	aSource = "source"
	aName   = "name"
	aCtx    = "context"
	aMax    = "max"
	aRemove = "remove"
)

// aliasForm is the alias screen; values stay text until saved.
type aliasForm struct {
	orig   Alias
	isNew  bool
	values map[string]string
	scr    screen
	in     edit
	leave  bool
	err    string
	// taken reports whether id is already a model of the alias's provider.
	taken func(id string) bool
	// remove asks the owner to delete this alias.
	remove func()
}

func newAliasForm(a Alias, isNew bool, taken func(string) bool, remove func()) *aliasForm {
	f := &aliasForm{orig: a, isNew: isNew, taken: taken, remove: remove, values: map[string]string{aID: a.ID, aName: a.Name}}
	if a.ContextWindow > 0 {
		f.values[aCtx] = strconv.FormatInt(a.ContextWindow, 10)
	}
	if a.MaxTokens > 0 {
		f.values[aMax] = strconv.FormatInt(a.MaxTokens, 10)
	}
	return f
}

func (f *aliasForm) dirty() bool {
	if f.in.on {
		return true
	}
	o := newAliasForm(f.orig, f.isNew, nil, nil)
	for _, k := range []string{aID, aName, aCtx, aMax} {
		if o.values[k] != f.values[k] {
			return true
		}
	}
	return false
}

// draft validates the form; the error is shown on the screen and nothing is saved.
func (f *aliasForm) draft() (Alias, error) {
	a := Alias{Provider: f.orig.Provider, Source: f.orig.Source, Name: strings.TrimSpace(f.values[aName])}
	a.ID = strings.TrimSpace(f.values[aID])
	switch {
	case a.ID == "":
		return a, fmt.Errorf("the alias needs an id")
	case strings.ContainsFunc(a.ID, unicode.IsSpace):
		return a, fmt.Errorf("the id cannot contain spaces")
	case a.ID == a.Source:
		return a, fmt.Errorf("the id must differ from the source model")
	case (f.isNew || a.ID != f.orig.ID) && f.taken != nil && f.taken(a.ID):
		return a, fmt.Errorf("%s already names a model of %s", a.ID, a.Provider)
	}
	for _, p := range []struct {
		key, label string
		dst        *int64
	}{{aCtx, "context window", &a.ContextWindow}, {aMax, "max tokens", &a.MaxTokens}} {
		text := strings.TrimSpace(f.values[p.key])
		if text == "" {
			continue
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n <= 0 {
			return a, fmt.Errorf("%s must be a positive whole number or empty", p.label)
		}
		*p.dst = n
	}
	return a, nil
}

func (f *aliasForm) rows() []formRow {
	value := func(key string) (string, string, string) {
		v, orig := strings.TrimSpace(f.values[key]), strings.TrimSpace(newAliasForm(f.orig, f.isNew, nil, nil).values[key])
		src, tok := "alias", "muted"
		if v != orig || (f.isNew && v != "") {
			src, tok = "● staged", "warning"
		}
		if v == "" && key != aID {
			return "inherited from the source", "inherited", "dim"
		}
		if key == aCtx || key == aMax {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				v = Commas(n)
			}
		}
		return line.Plain(v), src, tok
	}
	rows := []formRow{}
	for _, r := range []struct{ id, label string }{{aID, "Alias ID"}, {aSource, "Source"}, {aName, "Display name"}, {aCtx, "Context window"}, {aMax, "Max tokens"}} {
		if r.id == aSource {
			rows = append(rows, formRow{id: r.id, label: r.label, value: line.Plain(f.orig.Provider + "/" + f.orig.Source), source: "fixed", token: "dim"})
			continue
		}
		v, src, tok := value(r.id)
		rows = append(rows, formRow{id: r.id, label: r.label, value: v, source: src, token: tok})
	}
	if !f.isNew {
		rows = append(rows, formRow{id: aRemove, label: "Remove alias", action: true})
	}
	return rows
}

func (f *aliasForm) lines(th line.Theme, height, width int) (lines []string, above, below int) {
	editing := noEdit
	if f.in.on {
		editing = string(f.in.text)
	}
	return f.scr.draw(th, f.rows(), editing, height, width)
}

func (f *aliasForm) selected() string {
	rows := f.rows()
	return rows[f.scr.index(rows)].id
}

// key handles a key; save receives the validated alias. It reports whether the form closed.
func (f *aliasForm) key(data string, save func(Alias)) (closed bool) {
	kb := tui.GetKeybindings()
	sel := f.selected()
	if f.in.on {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			f.in.on = false
		case kb.Matches(data, "tui.select.confirm"):
			f.values[sel], f.in.on = f.in.value(), false
		default:
			f.in.typeKey(data)
		}
		return false
	}
	wasLeave := f.leave
	f.leave, f.err = false, ""
	switch {
	case tui.MatchesKey(data, "ctrl+s"):
		a, err := f.draft()
		if err != nil {
			f.err = err.Error()
			return false
		}
		save(a)
	case kb.Matches(data, "tui.select.cancel"):
		if f.dirty() && !wasLeave {
			f.leave = true
			return false
		}
		return true
	case kb.Matches(data, "tui.select.down"):
		f.scr.l.Move(1)
	case kb.Matches(data, "tui.select.up"):
		f.scr.l.Move(-1)
	case kb.Matches(data, "tui.select.confirm"):
		switch sel {
		case aRemove:
			if f.remove != nil {
				f.remove()
			}
		case aSource:
		default:
			f.in.begin(f.values[sel])
		}
	case tui.MatchesKey(data, "backspace"), tui.MatchesKey(data, "delete"):
		if sel == aName || sel == aCtx || sel == aMax {
			f.values[sel] = ""
		}
	case sel == aRemove && data == "d":
		if f.remove != nil {
			f.remove()
		}
	default:
		return false
	}
	return false
}

func (f *aliasForm) description() string {
	switch {
	case f.err != "":
		return "▲ " + f.err
	case f.leave:
		return "Esc again discards this alias; ctrl+s saves it."
	}
	return "An alias is a second selectable id for the same model; requests use the source."
}

func (f *aliasForm) hints() []string {
	if f.in.on {
		return []string{sdk.KeyHint("tui.select.confirm", "stage"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{sdk.RawKeyHint("↑↓", "move"), sdk.KeyHint("tui.select.confirm", "edit"), sdk.RawKeyHint("backspace", "inherit"), sdk.RawKeyHint("ctrl+s", "save")}
}

func (f *aliasForm) status() string {
	if f.dirty() {
		return "alias · ● staged"
	}
	return "alias"
}

const aliasHelp = ` Model alias
 An alias is a second entry for a model under the same provider, listed as <provider>/<id>.
 Requests still go to the source model, so credentials, pricing and thinking levels are the
 source's. Name and limits are optional; empty means inherited from the source.
 Enter edit a field · Backspace make an optional field inherit · Ctrl+S save
 Saving asks whether the alias is kept for this session or in auth-plus.toml.
 Esc leaves; unsaved changes ask first.`
