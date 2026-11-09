package models

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

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
	{[]string{"name"}, "Name", kindText},
	{[]string{"contextWindow"}, "Context window", kindInt},
	{[]string{"maxTokens"}, "Max tokens", kindInt},
	{[]string{"reasoning"}, "Reasoning", kindBool},
	{[]string{"input"}, "Input", kindInput},
	{[]string{"cost", "input"}, "Price in /M", kindPrice},
	{[]string{"cost", "output"}, "Price out /M", kindPrice},
	{[]string{"cost", "cacheRead"}, "Cache read /M", kindPrice},
	{[]string{"cost", "cacheWrite"}, "Cache write /M", kindPrice},
}

const (
	rowAliases  = "\x01aliases"
	rowNewAlias = "\x01new-alias"
)

// staged is one pending change: a new value, or removal of the override.
type staged struct {
	value  any
	remove bool
}

// override is the model detail screen: it edits the models.json override of one model, and
// lists its aliases. Changes stay staged until saved.
type override struct {
	model   Model
	aliases []string
	scr     screen
	changes map[string]staged
	in      edit
	leave   bool
	// newAlias opens the alias form for this model.
	newAlias func()
}

func newOverride(m Model, aliases []string, newAlias func()) *override {
	return &override{model: m, aliases: aliases, changes: map[string]staged{}, newAlias: newAlias}
}

func (o *override) overridden(f ovField) bool { return slices.Contains(o.model.Overridden, f.key()) }

func (o *override) dirty() bool { return len(o.changes) > 0 || o.in.on }

// current is the value shown for f: staged, else the model's effective value.
func (o *override) current(f ovField) any {
	if c, ok := o.changes[f.key()]; ok && !c.remove {
		return c.value
	}
	return effective(o.model, f)
}

func effective(m Model, f ovField) any {
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

func format(v any) string {
	switch x := v.(type) {
	case string:
		return line.Plain(x)
	case int64:
		return Commas(x)
	case float64:
		return "$" + strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return onOff(x)
	case []string:
		return orUnknown(strings.Join(x, ", "))
	}
	return "–"
}

// ops turns the staged changes into models.json operations in field order.
func (o *override) ops() []modelsfile.Op {
	var out []modelsfile.Op
	for _, f := range ovFields {
		if c, ok := o.changes[f.key()]; ok {
			out = append(out, modelsfile.Op{Path: f.path, Value: c.value, Remove: c.remove})
		}
	}
	return out
}

func (o *override) stage(f ovField, v any) { o.changes[f.key()] = staged{value: v} }

// rows are the screen's rows: every overridable field, then the model's aliases.
func (o *override) rows() []formRow {
	rows := make([]formRow, 0, len(ovFields)+2)
	for _, f := range ovFields {
		r := formRow{id: f.key(), label: f.label, value: format(o.current(f)), source: "provider", token: "dim"}
		c, isStaged := o.changes[f.key()]
		switch {
		case isStaged && c.remove:
			r.value, r.source, r.token = format(effective(o.model, f)), "● override removed", "warning"
		case isStaged:
			r.source, r.token = "● staged", "warning"
		case o.overridden(f):
			r.source, r.token = "override", "muted"
		}
		rows = append(rows, r)
	}
	aliases := "none"
	if len(o.aliases) > 0 {
		aliases = strings.Join(o.aliases, ", ")
	}
	return append(rows,
		formRow{id: rowAliases, label: "Aliases", value: aliases},
		formRow{id: rowNewAlias, label: "+ New alias", action: true})
}

func (o *override) lines(th line.Theme, height, width int) (lines []string, above, below int) {
	editing := noEdit
	if o.in.on {
		editing = string(o.in.text)
	}
	return o.scr.draw(th, o.rows(), editing, height, width)
}

func (o *override) selected() (formRow, ovField, bool) {
	rows := o.rows()
	r := rows[o.scr.index(rows)]
	for _, f := range ovFields {
		if f.key() == r.id {
			return r, f, true
		}
	}
	return r, ovField{}, false
}

// key handles a key; save is called with the operations when the user saves. It reports
// whether the screen closed.
func (o *override) key(data string, save func([]modelsfile.Op)) (closed bool) {
	kb := tui.GetKeybindings()
	r, f, isField := o.selected()
	if o.in.on {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			o.in.on = false
		case kb.Matches(data, "tui.select.confirm"):
			o.commit(f)
		default:
			o.in.typeKey(data)
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
		o.scr.l.Move(1)
	case kb.Matches(data, "tui.select.up"):
		o.scr.l.Move(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		o.scr.l.Page(1)
	case kb.Matches(data, "tui.select.pageUp"):
		o.scr.l.Page(-1)
	case isField && (tui.MatchesKey(data, "left") || tui.MatchesKey(data, "right") || data == " "):
		o.toggle(f)
	case kb.Matches(data, "tui.select.confirm"):
		switch {
		case r.id == rowNewAlias && o.newAlias != nil:
			o.newAlias()
		case isField && (f.kind == kindBool || f.kind == kindInput):
			o.toggle(f)
		case isField:
			o.in.begin(format0(o.current(f)))
		}
	case isField && (tui.MatchesKey(data, "backspace") || tui.MatchesKey(data, "delete")):
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

// format0 is the editable text of a value: no grouping or currency symbol.
func format0(v any) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return line.Plain(x)
	}
	return ""
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

// commit validates the typed value and stages it; an invalid value keeps the input open.
func (o *override) commit(f ovField) {
	text := o.in.value()
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
	o.in.on = false
}

func (o *override) description() string {
	switch {
	case o.leave:
		return "Esc again discards the staged changes; ctrl+s saves them."
	case o.in.on:
		return "Type a value; enter stages it, esc cancels. Prices are per million tokens."
	}
	return "Changes how PiG lists, limits and prices the model, not what the provider enforces."
}

func (o *override) hints() []string {
	if o.in.on {
		return []string{sdk.KeyHint("tui.select.confirm", "stage"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{
		sdk.RawKeyHint("↑↓", "move"), sdk.KeyHint("tui.select.confirm", "edit"), sdk.RawKeyHint("←→", "toggle"),
		sdk.RawKeyHint("backspace", "reset"), sdk.RawKeyHint("ctrl+s", "save"),
	}
}

func (o *override) status() string {
	if n := len(o.changes); n > 0 {
		return fmt.Sprintf("● %d staged", n)
	}
	return "listed"
}

const overrideHelp = ` Model detail
 Every field shows its effective value and where it comes from: provider (PiG's catalog),
 override (models.json) or staged (not saved yet). Changes how PiG lists, limits and prices
 the model; it does not change what the provider enforces.
 Enter edit a field · Left/Right toggle · Backspace reset a field to the provider value
 Ctrl+S saves all staged fields in one write to models.json; comments and other settings
 in that file are kept and a malformed file is never overwritten. Esc leaves; staged
 changes ask first.`
