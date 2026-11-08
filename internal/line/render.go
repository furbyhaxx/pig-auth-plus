package line

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// Theme paints text. Implementations map Style onto the host theme; Stress paints the
// foreground by quota stress (success to warning to error).
type Theme interface {
	Paint(text string, s Style) string
}

func tok(t string) Style                       { return Style{Fg: &Color{Token: t}} }
func paintTok(th Theme, text, t string) string { return th.Paint(text, tok(t)) }

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normalize(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

func scopeKey(s string) string {
	switch n := normalize(s); n {
	case "gemini-models":
		return "gemini"
	case "gemini-argon-wtu", "gemini-4-argon":
		return "gemini-4-argon"
	default:
		return n
	}
}

// ImportantWindows returns the marked windows, or the first two when none is marked.
func ImportantWindows(s *quota.Snapshot) []quota.Window {
	var out []quota.Window
	for _, w := range s.Windows {
		if w.Important {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		out = s.Windows[:min(2, len(s.Windows))]
	}
	return out
}

func namedWindow(s *quota.Snapshot, key string) *quota.Window {
	period, scope, _ := strings.Cut(key, "-")
	var match []*quota.Window
	for i := range s.Windows {
		w := &s.Windows[i]
		if w.Period != "" {
			if normalize(w.Period) == period && scopeKey(w.Scope) == scope {
				match = append(match, w)
			}
			continue
		}
		if (normalize(w.ID) == key || normalize(w.Short) == key) && (w.Scope == "" || scopeKey(w.Scope) == scope) {
			match = append(match, w)
		}
	}
	if len(match) == 1 {
		return match[0]
	}
	var exact []*quota.Window
	for _, w := range match {
		if normalize(w.ID) == key {
			exact = append(exact, w)
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	return nil
}

type value struct {
	text      string
	available bool
}

type ctx struct {
	th    Theme
	cfg   Config
	nowMs int64
	short bool
}

func (c ctx) paint(text string, custom *Style, def Style) string {
	st := def
	if custom != nil {
		st = merge(def, *custom)
	}
	return c.th.Paint(Plain(text), st)
}

func merge(base, over Style) Style {
	if over.Fg != nil {
		base.Fg, base.Stress = over.Fg, nil
	}
	if over.Bg != nil {
		base.Bg = over.Bg
	}
	base.Bold = base.Bold || over.Bold
	base.Italic = base.Italic || over.Italic
	base.Underline = base.Underline || over.Underline
	base.Strikethrough = base.Strikethrough || over.Strikethrough
	return base
}

func stressStyle(used float64) Style { u := clamp01(used); return Style{Stress: &u, Bold: u >= 0.85} }

// fields renders one provider's token.
func (c ctx) fields(v quota.View, alias string, fields []Field, inUse bool, custom *Style) value {
	snap := v.Snapshot
	now := c.nowMs
	hasData := snap != nil && (snap.Credits != nil || len(snap.Windows) > 0)
	failedEmpty := !hasData && ((snap != nil && snap.Error != "") || (v.Backoff != nil && v.Backoff.Until > now))
	muted := tok("muted")
	paint := func(text string) string { return c.paint(text, custom, muted) }
	stress := func(text string, used *float64) string {
		if used == nil {
			return paint(text)
		}
		return c.paint(text, custom, stressStyle(*used))
	}
	missing := func() value {
		word := "…"
		switch {
		case snap != nil && snap.NeedsAuth:
			word = "login"
		case failedEmpty:
			word = "err"
		case snap != nil:
			word = "?"
		}
		return value{paint(word), false}
	}
	var primary *quota.Window
	if snap != nil && len(snap.Windows) > 0 {
		w := ImportantWindows(snap)[0]
		primary = &w
	}
	one := func(f Field) value {
		switch f {
		case FieldInUse:
			return value{"", inUse}
		case FieldID, FieldName, FieldShort:
			name := alias
			switch {
			case f == FieldName:
				name = v.Source.Label
			case f == FieldShort || c.short:
				name = v.Source.Short
			}
			return value{paint(name), true}
		}
		if snap == nil || snap.NeedsAuth || failedEmpty {
			if f == FieldResetsAvailable || f == FieldR {
				return value{}
			}
			return missing()
		}
		var text string
		ok := false
		if m := windowField.FindStringSubmatch(string(f)); m != nil {
			if w := namedWindow(snap, m[1]); w != nil && w.Used != nil {
				u := clamp01(*w.Used)
				if m[2] == "remaining" {
					u = 1 - u
				}
				text, ok = stress(fmt.Sprintf("%d%%", int(math.Round(u*100))), w.Used), true
			}
			if !ok {
				return missing()
			}
			return value{text, true}
		}
		switch f {
		case FieldTier:
			if snap.Tier != "" {
				text, ok = paint(snap.Tier), true
			}
		case FieldCredits, FieldCreditsRemaining:
			if snap.Credits != nil && snap.Credits.Remaining != nil {
				r := *snap.Credits.Remaining
				s := CreditStress(r, c.cfg)
				text, ok = stress(fmt.Sprintf("%.2f%s", r, snap.Credits.Currency), &s), true
			}
		case FieldResetsAvailable, FieldR:
			if snap.ResetCredits == nil || *snap.ResetCredits <= 0 {
				return value{}
			}
			label := fmt.Sprintf("%d", *snap.ResetCredits)
			if f == FieldR {
				label = fmt.Sprintf("(r %d)", *snap.ResetCredits)
			}
			text, ok = c.paint(label, custom, tok("accent")), true
		case FieldPct:
			if primary != nil {
				text, ok = stress(FormatPercent(primary.Used, c.cfg), primary.Used), true
			}
		case FieldWins:
			if len(snap.Windows) > 0 {
				var items []string
				for _, w := range ImportantWindows(snap) {
					p := stress(FormatPercent(w.Used, c.cfg), w.Used)
					if !c.short {
						p = paint(w.Short) + " " + p
					}
					items = append(items, p)
				}
				sep := " "
				if c.short {
					sep = "/"
				}
				text, ok = strings.Join(items, sep), true
			}
		case FieldRst:
			if primary != nil {
				if cd := ResetCountdown(primary.ResetAtMs, now); cd != "" {
					text, ok = paint("↻ "+cd), true
				}
			}
		case FieldBar:
			if primary != nil && primary.Used != nil {
				used := clamp01(*primary.Used)
				filled := int(math.Round(used * 6))
				var b strings.Builder
				b.WriteString(paint("▕"))
				for i := 0; i < 6; i++ {
					if i < filled {
						s := clamp01(float64(i)/5*0.45 + used*0.55)
						b.WriteString(c.paint("█", custom, stressStyle(s)))
					} else {
						b.WriteString(paint("░"))
					}
				}
				b.WriteString(paint("▏"))
				text, ok = b.String(), true
			}
		}
		if !ok {
			return missing()
		}
		return value{text, true}
	}
	vals := make([]value, len(fields))
	for i, f := range fields {
		vals[i] = one(f)
	}
	isID := func(f Field) bool { return f == FieldID || f == FieldName || f == FieldShort }
	var ids, other, all []string
	idCount, allAvailable := 0, true
	for i, f := range fields {
		if isID(f) {
			idCount++
			if vals[i].text != "" {
				ids = append(ids, vals[i].text)
			}
		} else if vals[i].text != "" {
			other = append(other, vals[i].text)
		}
		if vals[i].text != "" {
			all = append(all, vals[i].text)
		}
		allAvailable = allAvailable && vals[i].available
	}
	text := strings.Join(all, " ")
	if idCount > 0 && len(other) > 0 {
		text = strings.Join(ids, " ") + paint(": ") + strings.Join(other, " ")
	}
	return value{text, allAvailable}
}

// Render returns preferred-break segments for the views. inUse lists PiG provider ids the
// session uses right now; row packing is the caller's (see Pack).
func Render(th Theme, t *Template, views []quota.View, cfg Config, nowMs int64, short bool, inUse []string) []string {
	enabled := map[string]quota.View{}
	for _, v := range views {
		enabled[v.Source.ID] = v
	}
	hidden := map[string]bool{}
	if cfg.HideSignedOut {
		for _, v := range views {
			if v.Snapshot != nil && v.Snapshot.NeedsAuth {
				hidden[v.Source.ID] = true
			}
		}
	}
	active := map[string]bool{}
	for _, id := range inUse {
		active[id] = true
	}
	relevant := func(v quota.View) bool {
		if active[v.Source.ID] {
			return true
		}
		if s := v.Snapshot; s != nil {
			for _, w := range s.Windows {
				if w.Used != nil && *w.Used >= 0.9 {
					return true
				}
			}
			if s.Credits != nil && s.Credits.Remaining != nil && CreditStress(*s.Credits.Remaining, cfg) >= 0.9 {
				return true
			}
		}
		return false
	}
	var remainder []quota.View
	for _, v := range views {
		if !t.Explicit[v.Source.ID] {
			remainder = append(remainder, v)
		}
	}
	type result struct {
		segments  []string
		dropped   []bool
		available bool
	}
	var render func(parts []Part, short bool) result
	render = func(parts []Part, short bool) result {
		r := result{segments: []string{""}, dropped: []bool{false}, available: true}
		appendText := func(s string) { r.segments[len(r.segments)-1] += s }
		newSegment := func() { r.segments = append(r.segments, ""); r.dropped = append(r.dropped, false) }
		auto := func(v quota.View, short bool, style *Style) value {
			f := []Field{FieldName}
			if short {
				f[0] = FieldShort
			}
			if v.Source.Credits {
				f = append(f, FieldCredits)
			} else {
				if v.Snapshot != nil && len(v.Snapshot.Windows) == 1 {
					f = append(f, FieldPct)
				} else {
					f = append(f, FieldWins)
				}
				if v.Snapshot != nil && v.Snapshot.ResetCredits != nil && *v.Snapshot.ResetCredits > 0 {
					f = append(f, FieldR)
				}
			}
			return ctx{th, cfg, nowMs, short}.fields(v, v.Source.ID, f, relevant(v), style)
		}
		for _, p := range parts {
			switch p.Kind {
			case KindText:
				appendText(paintTok(th, p.Text, "dim"))
			case KindSep:
				newSegment()
			case KindGroup:
				child := render(p.Parts, short)
				if !child.available {
					continue
				}
				appendText(child.segments[0])
				r.dropped[len(r.dropped)-1] = r.dropped[len(r.dropped)-1] || child.dropped[0]
				for i := 1; i < len(child.segments); i++ {
					r.segments = append(r.segments, child.segments[i])
					r.dropped = append(r.dropped, child.dropped[i])
				}
			case KindProvider:
				v, ok := enabled[p.ID]
				if !ok {
					r.available = false
					continue
				}
				if hidden[p.ID] {
					r.dropped[len(r.dropped)-1] = true
					continue
				}
				var res value
				if p.Fields != nil {
					res = ctx{th, cfg, nowMs, short}.fields(v, p.Alias, p.Fields, relevant(v), p.Style)
				} else {
					res = auto(v, short, p.Style)
				}
				appendText(res.text)
				r.available = r.available && res.available
			case KindAll, KindInUse:
				var cands, sel []quota.View
				for _, v := range remainder {
					if !hidden[v.Source.ID] {
						cands = append(cands, v)
					}
				}
				for _, v := range cands {
					if p.Kind == KindAll || relevant(v) {
						sel = append(sel, v)
					}
				}
				for i, v := range sel {
					if i > 0 {
						newSegment()
					}
					res := auto(v, short || p.Short, nil)
					appendText(res.text)
					r.available = r.available && res.available
				}
				if omitted := len(cands) - len(sel); p.Kind == KindInUse && omitted > 0 {
					if len(sel) > 0 {
						newSegment()
					}
					appendText(paintTok(th, fmt.Sprintf("+%d", omitted), "dim"))
				}
			}
		}
		return r
	}
	res := render(t.Parts, short)
	var out []string
	for i, s := range res.segments {
		if s != "" && !res.dropped[i] {
			out = append(out, s)
		}
	}
	return out
}
