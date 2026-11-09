package frame

// Panel accelerators. Every action carries a modifier: a printable key never acts, it only
// types into the search line or an inline input (the frame enforces this, see [Typist]).
// Enter, Esc, Tab, the arrows and paging keys are navigation, not actions.
const (
	// KeyRefresh refreshes, rereads or recounts the data of the current screen.
	KeyRefresh = "alt+r"
	// KeyRefreshAll refreshes every provider.
	KeyRefreshAll = "alt+a"
	// KeyPosition opens the quota line position chooser.
	KeyPosition = "alt+p"
	// KeyReset spends a reset credit, after asking.
	KeyReset = "alt+x"
	// KeyDelete removes the selected item: a rule, a custom provider, an alias.
	KeyDelete = "alt+d"
	// KeyDefault stages the built-in default (or the inherited value) for the selected field.
	KeyDefault = "alt+u"
	// KeySave stages or saves the draft of the current screen.
	KeySave = "ctrl+s"
)
