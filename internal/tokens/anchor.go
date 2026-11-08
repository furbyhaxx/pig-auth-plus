package tokens

// Anchor is the last valid provider usage on the current branch.
type Anchor struct {
	// Index is the position of the usage-bearing message in the projected messages.
	Index  int
	Tokens int
}

// ProjectedEntry pairs a session entry id with the messages it projected into.
type ProjectedEntry struct {
	SourceID string
	Messages int
}

// FindAnchor returns the last assistant usage that no later compaction or context edit invalidated.
// branch holds the entry id and type of every entry on the current branch, root first.
func FindAnchor(messages []map[string]any, entries []ProjectedEntry, branch []BranchEntry) (Anchor, bool) {
	invalidating := -1
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == "compaction" || branch[i].Type == "context_edit" {
			invalidating = i
			break
		}
	}
	for index := len(messages) - 1; index >= 0; index-- {
		m := messages[index]
		if str(m["role"]) != "assistant" {
			continue
		}
		if stop := str(m["stopReason"]); stop == "aborted" || stop == "error" {
			continue
		}
		tokens := usageTokens(asMap(m["usage"]))
		if tokens <= 0 {
			continue
		}
		offset, sourceID := 0, ""
		for _, e := range entries {
			offset += e.Messages
			if index < offset {
				sourceID = e.SourceID
				break
			}
		}
		sourceIndex := -1
		for i, b := range branch {
			if sourceID != "" && b.ID == sourceID {
				sourceIndex = i
				break
			}
		}
		if sourceIndex > invalidating {
			return Anchor{Index: index, Tokens: tokens}, true
		}
		return Anchor{}, false
	}
	return Anchor{}, false
}

// BranchEntry is the id and type of one session entry.
type BranchEntry struct{ ID, Type string }

func usageTokens(u map[string]any) int {
	if u == nil {
		return 0
	}
	if total := num(u["totalTokens"]); total > 0 {
		return total
	}
	return num(u["input"]) + num(u["output"]) + num(u["cacheRead"]) + num(u["cacheWrite"])
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}
