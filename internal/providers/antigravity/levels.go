package antigravity

import (
	"fmt"
	"strconv"
	"strings"
)

// Thinking is how one catalog level reaches generationConfig.thinkingConfig.
type Thinking struct {
	// Kind is "level", "adaptive" (Claude) or "budget".
	Kind   string
	Effort string // LOW, MEDIUM or HIGH for level and adaptive
	Budget int    // for budget; -1 is dynamic
}

// Level is one catalog entry such as gemini-4-argon-low.
type Level struct {
	// ID is sent as the envelope model.
	ID string
	// Enum is the catalog's model enum, sent as labels.model_enum.
	Enum     string
	Thinking Thinking
}

// Encode stores a level in a model's thinkingLevelMap, so a request needs no live catalog:
// "gemini-4-argon-low|MODEL_PLACEHOLDER_M419|level:LOW".
func (l Level) Encode() string {
	t := l.Thinking.Kind + ":" + l.Thinking.Effort
	if l.Thinking.Kind == "budget" {
		t = "budget:" + strconv.Itoa(l.Thinking.Budget)
	}
	return l.ID + "|" + l.Enum + "|" + t
}

// DecodeLevel parses what Encode produced.
func DecodeLevel(value string) (Level, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return Level{}, false
	}
	kind, arg, ok := strings.Cut(parts[2], ":")
	if !ok || arg == "" {
		return Level{}, false
	}
	l := Level{ID: parts[0], Enum: parts[1], Thinking: Thinking{Kind: kind}}
	switch kind {
	case "budget":
		n, err := strconv.Atoi(arg)
		if err != nil || n < -1 {
			return Level{}, false
		}
		l.Thinking.Budget = n
	case "level", "adaptive":
		if arg != "LOW" && arg != "MEDIUM" && arg != "HIGH" {
			return Level{}, false
		}
		l.Thinking.Effort = arg
	default:
		return Level{}, false
	}
	return l, true
}

// Config is the CLI's thinkingConfig; adaptive keeps a zero budget.
func (t Thinking) Config() map[string]any {
	switch t.Kind {
	case "adaptive":
		return map[string]any{"includeThoughts": true, "thinkingBudget": 0, "thinkingLevel": t.Effort}
	case "budget":
		return map[string]any{"includeThoughts": true, "thinkingBudget": t.Budget}
	default:
		return map[string]any{"includeThoughts": true, "thinkingLevel": t.Effort}
	}
}

// piLevels are PiG's thinking levels from cheapest to most expensive.
var piLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// PickLevel returns the requested level, else the next offered one above it, else the highest
// offered below it. No requested level (compaction, titles) takes the cheapest.
func PickLevel(levelMap map[string]any, requested string) (Level, error) {
	offered := make([]*Level, len(piLevels))
	for i, name := range piLevels {
		if s, ok := levelMap[name].(string); ok {
			if l, ok := DecodeLevel(s); ok {
				offered[i] = &l
			}
		}
	}
	start := 0
	for i, name := range piLevels {
		if name == requested {
			start = i
		}
	}
	for i := start; i < len(offered); i++ {
		if offered[i] != nil {
			return *offered[i], nil
		}
	}
	for i := start - 1; i >= 0; i-- {
		if offered[i] != nil {
			return *offered[i], nil
		}
	}
	return Level{}, fmt.Errorf("antigravity: model has no usable thinking level; refresh models with /model")
}
