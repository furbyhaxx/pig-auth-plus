package models

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/modelsfile"
)

type fieldKind int

const (
	kindText fieldKind = iota
	kindInt
	kindPrice
	kindBool
	kindInput
)

// ovField is one overridable field of a model.
type ovField struct {
	path  []string
	label string
	kind  fieldKind
}

func (f ovField) key() string { return strings.Join(f.path, ".") }

var ovFields = []ovField{
	{[]string{"name"}, "name", kindText},
	{[]string{"contextWindow"}, "context window", kindInt},
	{[]string{"maxTokens"}, "max output", kindInt},
	{[]string{"reasoning"}, "reasoning", kindBool},
	{[]string{"input"}, "input types", kindInput},
	{[]string{"cost", "input"}, "price in /M", kindPrice},
	{[]string{"cost", "output"}, "price out /M", kindPrice},
	{[]string{"cost", "cacheRead"}, "price cache read", kindPrice},
	{[]string{"cost", "cacheWrite"}, "price cache write", kindPrice},
}

// staged is one pending change: a new value, or removal of the override.
type staged struct {
	value  any
	remove bool
}

// override edits the models.json override of one model; changes stay staged until saved.
type override struct {
	model   Model
	sel     int
	changes map[string]staged
	input   []rune
	editing bool
	leave   bool
}

func newOverride(m Model) *override {
	return &override{model: m, changes: map[string]staged{}}
}

func (o *override) overridden(f ovField) bool { return slices.Contains(o.model.Overridden, f.key()) }

func (o *override) dirty() bool { return len(o.changes) > 0 || o.editing }

// current is the value shown for f: staged, else the model's effective value.
func (o *override) current(f ovField) any {
	if c, ok := o.changes[f.key()]; ok && !c.remove {
		return c.value
	}
	m := o.model
	switch f.key() {
	case "name":
		return m.Label()
	case "contextWindow":
		return int64(m.ContextWindow)
	case "maxTokens":
		return int64(m.MaxTokens)
	case "reasoning":
		return m.Reasoning
	case "input":
		return slices.Clone(m.Input)
	case "cost.input":
		return m.Cost.Input
	case "cost.output":
		return m.Cost.Output
	case "cost.cacheRead":
		return m.Cost.CacheRead
	case "cost.cacheWrite":
		return m.Cost.CacheWrite
	}
	return nil
}

func format(f ovField, v any) string {
	switch x := v.(type) {
	case string:
		return line.Plain(x)
	case int64:
		return fmt.Sprint(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return yesNo(x)
	case []string:
		return strings.Join(x, ", ")
	}
	return "?"
}

// ops turns the staged changes into models.json operations in field order.
func (o *override) ops() []modelsfile.Op {
	var out []modelsfile.Op
	for _, f := range ovFields {
		c, ok := o.changes[f.key()]
		if !ok {
			continue
		}
		out = append(out, modelsfile.Op{Path: f.path, Value: c.value, Remove: c.remove})
	}
	return out
}

func (o *override) stage(f ovField, v any) {
	o.changes[f.key()] = staged{value: v}
}

func (o *override) lines(th line.Theme, width int) []string {
	head := " " + th.Paint(o.model.Label(), line.Style{Bold: true}) + paint(th, "  "+o.model.Key(), "muted")
	out := []string{fit(head, width)}
	for i, f := range ovFields {
		marker := "  "
		if i == o.sel {
			marker = paint(th, "→ ", "accent")
		}
		c, isStaged := o.changes[f.key()]
		val := format(f, o.current(f))
		tag := ""
		switch {
		case isStaged && c.remove:
			val, tag = format(f, o.baseline(f)), paint(th, "● override removed", "warning")
		case isStaged:
			tag = paint(th, "● staged", "warning")
		case o.overridden(f):
			tag = paint(th, "override", "muted")
		}
		out = append(out, fit(marker+fmt.Sprintf("%-18s", f.label)+val+"  "+tag, width))
	}
	if o.editing {
		out = append(out, fit(" "+paint(th, "edit ", "accent")+string(o.input)+"▏", width))
	}
	if o.leave {
		out = append(out, fit(paint(th, " ▲ Esc again discards the staged changes; ctrl+s saves them", "warning"), width))
	}
	out = append(out, fit(paint(th, " Changes the model as PiG lists and prices it, not what the provider enforces.", "muted"), width))
	return out
}

// baseline is the value without the staged change: where a removal ends up is unknown until
// the file is rewritten, so the editor shows the effective value.
func (o *override) baseline(f ovField) any {
	saved := o.changes
	o.changes = nil
	defer func() { o.changes = saved }()
	return o.current(f)
}

// key handles a key; saved is called with the operations when the user saves.
func (o *override) key(data string, save func([]modelsfile.Op)) (closed bool) {
	kb := tui.GetKeybindings()
	f := ovFields[o.sel]
	if o.editing {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			o.editing = false
		case kb.Matches(data, "tui.select.confirm"):
			o.commit(f)
		case tui.MatchesKey(data, "backspace"):
			if n := len(o.input); n > 0 {
				o.input = o.input[:n-1]
			}
		case tui.MatchesKey(data, "ctrl+u"):
			o.input = nil
		default:
			for _, r := range data {
				if unicode.IsPrint(r) {
					o.input = append(o.input, r)
				}
			}
		}
		return false
	}
	wasLeave := o.leave
	o.leave = false
	switch {
	case tui.MatchesKey(data, "ctrl+s"):
		if ops := o.ops(); len(ops) > 0 {
			save(ops)
		}
	case kb.Matches(data, "tui.select.cancel"):
		if o.dirty() && !wasLeave {
			o.leave = true
			return false
		}
		return true
	case kb.Matches(data, "tui.select.down"):
		o.sel = min(o.sel+1, len(ovFields)-1)
	case kb.Matches(data, "tui.select.up"):
		o.sel = max(o.sel-1, 0)
	case tui.MatchesKey(data, "left"), tui.MatchesKey(data, "right"), data == " ":
		o.toggle(f)
	case kb.Matches(data, "tui.select.confirm"):
		if f.kind == kindBool || f.kind == kindInput {
			o.toggle(f)
		} else {
			o.editing, o.input = true, []rune(format(f, o.current(f)))
		}
	case tui.MatchesKey(data, "backspace"), tui.MatchesKey(data, "delete"):
		if o.overridden(f) {
			o.changes[f.key()] = staged{remove: true}
		} else {
			delete(o.changes, f.key())
		}
	default:
		return false
	}
	return false
}

func (o *override) toggle(f ovField) {
	switch f.kind {
	case kindBool:
		v, _ := o.current(f).(bool)
		o.stage(f, !v)
	case kindInput:
		cur, _ := o.current(f).([]string)
		if slices.Contains(cur, "image") {
			o.stage(f, []string{"text"})
		} else {
			o.stage(f, []string{"text", "image"})
		}
	}
}

// commit validates the typed value and stages it; an invalid value keeps the editor open.
func (o *override) commit(f ovField) {
	text := strings.TrimSpace(string(o.input))
	switch f.kind {
	case kindText:
		if text == "" {
			return
		}
		o.stage(f, text)
	case kindInt:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n <= 0 {
			return
		}
		o.stage(f, n)
	case kindPrice:
		v, err := strconv.ParseFloat(text, 64)
		if err != nil || v < 0 {
			return
		}
		o.stage(f, v)
	}
	o.editing = false
}

func (o *override) hints() []string {
	if o.editing {
		return []string{sdk.KeyHint("tui.select.confirm", "stage"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{
		sdk.RawKeyHint("←/→", "change"), sdk.KeyHint("tui.select.confirm", "edit"),
		sdk.RawKeyHint("backspace", "remove override"), sdk.RawKeyHint("ctrl+s", "save"),
	}
}

const overrideHelp = ` Model override
 Changes how PiG lists, limits and prices one model; it does not change what the provider
 enforces. Overrides are written to models.json (modelOverrides); comments and other
 settings in that file are kept, and a malformed file is never overwritten.
 Left/Right toggle · Enter edit · Backspace stage removal of the override on a field
 Ctrl+S save all staged fields in one write · Esc leaves; staged changes ask first.`
