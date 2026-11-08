package session

import (
	"fmt"
	"slices"
	"strings"
)

// Command describes a registered slash command and where it came from.
type Command struct {
	Name    string
	Source  string // package source string, e.g. git:https://…
	BaseDir string
}

// OwnedCommands are the commands this extension registers.
var OwnedCommands = []string{"auth", "quotas", "quota", "tokens", "token"}

// Conflict reports another extension that registers one of our commands; ok is false when
// there is none. ownBase is this extension's package directory.
func Conflict(commands []Command, ownBase string) (message string, ok bool) {
	for _, c := range commands {
		if !slices.Contains(OwnedCommands, c.Name) || (ownBase != "" && c.BaseDir == ownBase) {
			continue
		}
		what := "another extension"
		if strings.Contains(c.BaseDir, "pi-auth-plus") || strings.Contains(c.Source, "pi-auth-plus") {
			what = "pi-auth-plus"
		}
		fix := "stop loading it"
		if c.Source != "" {
			fix = "run `pig remove " + c.Source + "`"
		}
		return fmt.Sprintf("pig-auth-plus is disabled: %s also registers /%s; %s and restart", what, c.Name, fix), true
	}
	return "", false
}
