// Package list is the selection, filter and scroll state shared by the dashboard's list
// screens. A screen hands it the entries to show; it returns the window to draw and tracks the
// cursor by entry ID, so a refresh or a filter change never moves the selection to another row.
package list

import (
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// Entry is one selectable row.
type Entry struct {
	// ID is stable across refreshes; the cursor follows it.
	ID string
	// Search is the plain text the filter matches against.
	Search string
	// Header marks a section title: it is drawn but never selected, and a filter hides it.
	Header bool
}

// List holds the cursor, scroll offset and filter of one list screen. The zero value is ready
// to use and safe for concurrent use.
type List struct {
	mu    sync.Mutex
	sel   string
	top   int
	query string
	ids   []string
	skip  []bool
	rows  int
}

// Window is the part of the filtered entries to draw.
type Window struct {
	// Entries are the visible entries, at most the requested rows.
	Entries []Entry
	// Selected is the index in Entries of the cursor row, or -1 when nothing is selectable.
	Selected int
	// Above and Below count the filtered entries outside the window.
	Above, Below int
	// Total is the number of entries that match the filter.
	Total int
}

// Filter returns the entries matching query, best match first; a blank query keeps them as is.
func Filter(entries []Entry, query string) []Entry {
	if strings.TrimSpace(query) == "" {
		return entries
	}
	items := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if !e.Header {
			items = append(items, e)
		}
	}
	return tui.FuzzyFilter(items, query, func(e Entry) string { return e.Search })
}

// SetFilter sets the filter text and returns the cursor to the top of the new results.
func (l *List) SetFilter(query string) {
	l.mu.Lock()
	l.query, l.top, l.sel = query, 0, ""
	l.mu.Unlock()
}

// Query is the current filter text.
func (l *List) Query() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.query
}

// Selected is the ID under the cursor, empty when the list is empty.
func (l *List) Selected() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sel
}

// Select moves the cursor to id when the last window held it.
func (l *List) Select(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, have := range l.ids {
		if have == id && !l.skip[i] {
			l.sel = id
			return
		}
	}
}

// Window applies the filter, keeps the cursor on its entry (or the first one), scrolls just
// enough to show it and returns rows entries.
func (l *List) Window(entries []Entry, rows int) Window {
	l.mu.Lock()
	defer l.mu.Unlock()
	rows = max(rows, 1)
	shown := Filter(entries, l.query)
	l.ids, l.skip = l.ids[:0], l.skip[:0]
	idx, first := -1, -1
	for i, e := range shown {
		l.ids, l.skip = append(l.ids, e.ID), append(l.skip, e.Header)
		if first < 0 && !e.Header {
			first = i
		}
		if e.ID == l.sel && !e.Header {
			idx = i
		}
	}
	l.rows = rows
	if idx < 0 {
		idx, l.sel = first, ""
		if first >= 0 {
			l.sel = shown[first].ID
		}
	}
	l.top = clamp(l.top, 0, max(len(shown)-rows, 0))
	// A section title stays with its first row when the cursor scrolls back to it.
	if idx > 0 && l.skip[idx-1] && idx == l.top {
		l.top--
	}
	// Keep the cursor inside the window: scrolling may leave it behind, moving pulls it back.
	switch {
	case idx >= 0 && idx < l.top:
		l.top = idx
	case idx >= l.top+rows:
		l.top = idx - rows + 1
	}
	end := min(l.top+rows, len(shown))
	w := Window{Entries: shown[l.top:end:end], Selected: -1, Above: l.top, Below: len(shown) - end, Total: len(shown)}
	if idx >= 0 {
		w.Selected = idx - l.top
	}
	return w
}

// Move moves the cursor by delta entries, stopping at the ends.
func (l *List) Move(delta int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.ids) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	i := l.index()
	for n := abs(delta); n > 0; n-- {
		j := i + step
		for j >= 0 && j < len(l.ids) && l.skip[j] {
			j += step
		}
		if j < 0 || j >= len(l.ids) {
			break
		}
		i = j
	}
	l.sel = l.ids[i]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Page moves the cursor by one window less one row.
func (l *List) Page(dir int) { l.Move(dir * max(l.pageRows()-1, 1)) }

func (l *List) pageRows() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rows
}

func (l *List) index() int {
	for i, id := range l.ids {
		if id == l.sel && !l.skip[i] {
			return i
		}
	}
	return 0
}

// Scroll moves the window by delta lines without moving the cursor; the next Window pulls the
// cursor into view only when it was moved.
func (l *List) Scroll(delta int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.top = clamp(l.top+delta, 0, max(len(l.ids)-l.rows, 0))
	// A wheel that scrolled the cursor out of sight takes it along, so Window need not scroll back.
	idx := l.index()
	switch {
	case idx < l.top:
		l.sel = l.firstSelectable(l.top, 1)
	case idx >= l.top+l.rows && l.top+l.rows-1 < len(l.ids):
		l.sel = l.firstSelectable(l.top+l.rows-1, -1)
	}
}

// firstSelectable is the ID of the first non-header entry from i in direction step, or the
// entry at i when every one that way is a header.
func (l *List) firstSelectable(i, step int) string {
	for j := i; j >= 0 && j < len(l.ids); j += step {
		if !l.skip[j] {
			return l.ids[j]
		}
	}
	return l.ids[i]
}

// Click selects the entry on body row and reports whether it was already selected, which a
// caller treats as a request to open it. A row past the last entry is ignored.
func (l *List) Click(row int) (id string, again bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.top + row
	if row < 0 || row >= l.rows || i >= len(l.ids) || l.skip[i] {
		return "", false
	}
	again = l.ids[i] == l.sel
	l.sel = l.ids[i]
	return l.sel, again
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }
