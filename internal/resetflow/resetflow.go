// Package resetflow runs the interactive reset-credit flow: list, pick, ask, spend once, report.
package resetflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// Choices offered in the approval dialog; Keep is first so a default selection spends nothing.
const (
	Keep  = "Keep the credit"
	Spend = "Spend 1 credit now (cannot be undone)"
)

// Engine is the quota engine as the flow uses it.
type Engine interface {
	Sources() []*quota.Source
	Registry() *quota.Registry
	ResetSupport(id string) bool
	ListResets(ctx context.Context, id string) (quota.ResetList, error)
	ConsumeReset(ctx context.Context, id, creditID string, by quota.Requester) (quota.ResetOutcome, error)
	View() []quota.View
}

// Dialogs asks the user; Interactive is false when no dialog can be shown, so the flow only lists.
type Dialogs interface {
	Interactive() bool
	// Select returns the chosen option; ok is false when the dialog was dismissed.
	Select(title string, options []string) (choice string, ok bool, err error)
}

// Result is what the command prints. Warn marks outcomes that did not spend.
type Result struct {
	Text string
	Warn bool
}

// DefaultProvider is the first enabled reset-capable provider.
func DefaultProvider(e Engine) (string, bool) {
	for _, s := range e.Sources() {
		if s.Resets != nil {
			return s.ID, true
		}
	}
	return "", false
}

// Pick chooses the redeemable credit that expires soonest, keeping the longest-lived one; with
// creditID it returns exactly that credit or nothing.
func Pick(l quota.ResetList, creditID string) (quota.ResetCredit, bool) {
	var ok []quota.ResetCredit
	for _, c := range l.Credits {
		if c.Status == "available" && (c.SupportedByPlan == nil || *c.SupportedByPlan) {
			ok = append(ok, c)
		}
	}
	if creditID != "" {
		for _, c := range ok {
			if c.ID == creditID {
				return c, true
			}
		}
		return quota.ResetCredit{}, false
	}
	if len(ok) == 0 {
		return quota.ResetCredit{}, false
	}
	sort.SliceStable(ok, func(i, j int) bool { return expiry(ok[i]) < expiry(ok[j]) })
	return ok[0], true
}

func expiry(c quota.ResetCredit) int64 {
	if c.ExpiresAtMs == nil {
		return 1<<62 - 1
	}
	return *c.ExpiresAtMs
}

// Plain removes terminal control sequences and control characters from provider text.
func Plain(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 0x1b {
			i++
			if i < len(rs) && rs[i] == '[' {
				for i+1 < len(rs) && (rs[i+1] < 0x40 || rs[i+1] > 0x7e) {
					i++
				}
				i++
			} else if i < len(rs) && rs[i] == ']' {
				for i+1 < len(rs) && rs[i+1] != 0x07 && rs[i+1] != 0x1b {
					i++
				}
				i++
			}
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

var zurich = func() *time.Location {
	if l, err := time.LoadLocation("Europe/Zurich"); err == nil {
		return l
	}
	return time.UTC
}()

// Date formats an expiry as de-CH 24-hour time in Zurich.
func Date(ms *int64) string {
	if ms == nil {
		return "-"
	}
	return time.UnixMilli(*ms).In(zurich).Format("02.01.2006 15:04")
}

// Describe describes a credit without terminal controls.
func Describe(c quota.ResetCredit) string {
	title := Plain(c.Title)
	if title == "" {
		title = Plain(c.Type)
	}
	exp := ""
	if c.ExpiresAtMs != nil {
		exp = ", expires " + Date(c.ExpiresAtMs)
	}
	return fmt.Sprintf("%s (%s%s)", title, Plain(c.Status), exp)
}

// List renders the listing under the provider label.
func List(e Engine, l quota.ResetList) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d reset credit%s available", label(e, l.Provider), l.Available, plural(l.Available))
	for _, c := range l.Credits {
		b.WriteString("\n  " + Describe(c))
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func label(e Engine, id string) string {
	if s := e.Registry().ByID(id); s != nil {
		return Plain(s.Label)
	}
	return Plain(id)
}

// Describe the provider's verdict; only ResetDone confirms a spend.
func outcomeText(e Engine, o quota.ResetOutcome) Result {
	n := label(e, o.Provider)
	switch o.Code {
	case quota.ResetDone:
		return Result{Text: fmt.Sprintf("%s: reset applied; %d window%s reset, one credit spent.", n, o.WindowsReset, plural(o.WindowsReset))}
	case quota.NothingToReset:
		return Result{Text: n + ": nothing to reset; no credit was spent.", Warn: true}
	case quota.NoCredit:
		return Result{Text: n + ": no reset credit available; nothing was spent.", Warn: true}
	default:
		return Result{Text: n + ": that credit was already redeemed.", Warn: true}
	}
}

func windowsText(e Engine, id string) string {
	for _, v := range e.View() {
		if v.Source.ID != id || v.Snapshot == nil {
			continue
		}
		var parts []string
		for _, w := range v.Snapshot.Windows {
			if w.Used == nil {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %.0f%%", Plain(firstNonEmpty(w.Short, w.ID)), *w.Used*100))
		}
		if len(parts) > 0 {
			return "Current usage: " + strings.Join(parts, ", ")
		}
	}
	return ""
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// Run lists the credits, asks, and spends one only on the explicit second choice. provider may
// be a PiG id or alias; empty selects the default reset-capable provider.
func Run(ctx context.Context, e Engine, d Dialogs, provider string, by quota.Requester) (Result, error) {
	id := ""
	if provider == "" {
		var ok bool
		if id, ok = DefaultProvider(e); !ok {
			return Result{Text: "No enabled provider supports reset credits.", Warn: true}, nil
		}
	} else if s := e.Registry().Resolve(provider); s != nil {
		id = s.ID
	} else {
		return Result{}, fmt.Errorf("unknown provider %q", Plain(provider))
	}
	list, err := e.ListResets(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if !d.Interactive() {
		return Result{Text: List(e, list) + "\nSpending a credit needs the interactive TUI; nothing was spent."}, nil
	}
	credit, ok := Pick(list, "")
	if !ok {
		return Result{Text: List(e, list) + "\nNo redeemable credit; nothing was spent.", Warn: true}, nil
	}
	title := fmt.Sprintf("%s\nCredit to use: %s", List(e, list), Describe(credit))
	if w := windowsText(e, id); w != "" {
		title += "\n" + w
	}
	choice, answered, err := d.Select(title, []string{Keep, Spend})
	if err != nil {
		return Result{}, err
	}
	if !answered || choice != Spend {
		return Result{Text: label(e, id) + ": kept the credit; nothing was spent."}, nil
	}
	out, err := e.ConsumeReset(ctx, id, credit.ID, by)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			err = fmt.Errorf("%w; the credit may already be spent: run /quotas reset %s to list again", context.Cause(ctx), id)
		}
		return Result{}, err
	}
	return outcomeText(e, out), nil
}

var _ Engine = (*quota.Engine)(nil)
