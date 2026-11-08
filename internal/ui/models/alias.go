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

type aliasField struct {
	label    string
	optional bool
	number   bool
}

var aliasFields = []aliasField{
	{label: "id"},
	{label: "name", optional: true},
	{label: "context window", optional: true, number: true},
	{label: "max output", optional: true, number: true},
}

// aliasForm edits one alias; values stay text until saved.
type aliasForm struct {
	orig    Alias
	isNew   bool
	values  [4]string
	sel     int
	input   []rune
	editing bool
	leave   bool
	err     string
	// taken reports whether id is already a model of the alias's provider.
	taken func(id string) bool
}

func newAliasForm(a Alias, isNew bool, taken func(string) bool) *aliasForm {
	f := &aliasForm{orig: a, isNew: isNew, taken: taken}
	f.values[0], f.values[1] = a.ID, a.Name
	if a.ContextWindow > 0 {
		f.values[2] = strconv.FormatInt(a.ContextWindow, 10)
	}
	if a.MaxTokens > 0 {
		f.values[3] = strconv.FormatInt(a.MaxTokens, 10)
	}
	return f
}

func (f *aliasForm) dirty() bool {
	if f.editing {
		return true
	}
	o := newAliasForm(f.orig, f.isNew, nil)
	return o.values != f.values
}

// draft validates the form; the error is shown beside the form and nothing is saved.
func (f *aliasForm) draft() (Alias, error) {
	a := Alias{Provider: f.orig.Provider, Source: f.orig.Source, Name: strings.TrimSpace(f.values[1])}
	a.ID = strings.TrimSpace(f.values[0])
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
	for i, dst := range []*int64{&a.ContextWindow, &a.MaxTokens} {
		text := strings.TrimSpace(f.values[2+i])
		if text == "" {
			continue
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n <= 0 {
			return a, fmt.Errorf("%s must be a positive whole number or empty", aliasFields[2+i].label)
		}
		*dst = n
	}
	return a, nil
}

func (f *aliasForm) lines(th line.Theme, width int) []string {
	title := "New alias of "
	if !f.isNew {
		title = "Alias of "
	}
	out := []string{fit(" "+th.Paint(title+line.Plain(f.orig.Provider+"/"+f.orig.Source), line.Style{Bold: true}), width)}
	for i, fld := range aliasFields {
		marker := "  "
		if i == f.sel {
			marker = paint(th, "→ ", "accent")
		}
		v := f.values[i]
		if v == "" && fld.optional {
			v = paint(th, "inherited from the source", "muted")
		} else {
			v = line.Plain(v)
		}
		out = append(out, fit(marker+fmt.Sprintf("%-16s", fld.label)+v, width))
	}
	if f.editing {
		out = append(out, fit(" "+paint(th, "edit ", "accent")+string(f.input)+"▏", width))
	}
	if f.err != "" {
		out = append(out, fit(paint(th, " ▲ "+line.Plain(f.err), "warning"), width))
	}
	if f.leave {
		out = append(out, fit(paint(th, " ▲ Esc again discards this alias; ctrl+s saves it", "warning"), width))
	}
	out = append(out, fit(paint(th, " Requests use the source model; the alias only renames it and may adjust displayed limits.", "muted"), width))
	return out
}

// key handles a key; save receives the validated alias. It reports whether the form closed.
func (f *aliasForm) key(data string, save func(Alias)) (closed bool) {
	kb := tui.GetKeybindings()
	if f.editing {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			f.editing = false
		case kb.Matches(data, "tui.select.confirm"):
			f.values[f.sel], f.editing = strings.TrimSpace(string(f.input)), false
		case tui.MatchesKey(data, "backspace"):
			if n := len(f.input); n > 0 {
				f.input = f.input[:n-1]
			}
		case tui.MatchesKey(data, "ctrl+u"):
			f.input = nil
		default:
			for _, r := range data {
				if unicode.IsPrint(r) {
					f.input = append(f.input, r)
				}
			}
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
		f.sel = min(f.sel+1, len(aliasFields)-1)
	case kb.Matches(data, "tui.select.up"):
		f.sel = max(f.sel-1, 0)
	case kb.Matches(data, "tui.select.confirm"):
		f.editing, f.input = true, []rune(f.values[f.sel])
	case tui.MatchesKey(data, "backspace"), tui.MatchesKey(data, "delete"):
		if aliasFields[f.sel].optional {
			f.values[f.sel] = ""
		}
	default:
		return false
	}
	return false
}

func (f *aliasForm) hints() []string {
	if f.editing {
		return []string{sdk.KeyHint("tui.select.confirm", "stage"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{sdk.KeyHint("tui.select.confirm", "edit"), sdk.RawKeyHint("backspace", "inherit"), sdk.RawKeyHint("ctrl+s", "save")}
}

const aliasHelp = ` Model alias
 An alias is a second entry for a model under the same provider, listed as <provider>/<id>.
 Requests still go to the source model, so credentials, pricing and thinking levels are the
 source's. Name and limits are optional; empty means inherited from the source.
 Enter edit a field · Backspace make an optional field inherit · Ctrl+S save
 Saving asks whether the alias is kept for this session or in auth-plus.toml.
 Esc leaves; unsaved changes ask first.`
