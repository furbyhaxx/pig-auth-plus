package claude

import (
	"fmt"
	"strings"
)

const piPlaceholder = "__pi_deferred_placeholder__"

// wireToolName maps a host tool name to Claude Code's PascalCase; native MCP names are kept.
func wireToolName(name string) string {
	if name == piPlaceholder {
		return "DeferredToolPlaceholder"
	}
	if strings.HasPrefix(name, "mcp__") {
		return name
	}
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// ToolNameCollisionError reports two current tools that share a wire name.
type ToolNameCollisionError struct{ First, Second string }

func (e *ToolNameCollisionError) Error() string {
	return fmt.Sprintf("claude: tools %q and %q share a wire name; rename one of them", e.First, e.Second)
}

// restorer maps wire names back to request names. Current tools must be wire-unique; names
// from earlier transcript entries only fill free slots, so a tool the host no longer offers
// never blocks the session.
type restorer map[string]string

func newRestorer(current, earlier []string) (restorer, error) {
	r := restorer{}
	for _, name := range append([]string{piPlaceholder}, current...) {
		w := wireToolName(name)
		if prev, ok := r[w]; ok && prev != name {
			return nil, &ToolNameCollisionError{First: prev, Second: name}
		}
		r[w] = name
	}
	for _, name := range earlier {
		w := wireToolName(name)
		if _, ok := r[w]; !ok {
			r[w] = name
		}
	}
	return r, nil
}

func (r restorer) restore(wire string) string {
	if name, ok := r[wire]; ok {
		return name
	}
	return wire
}
