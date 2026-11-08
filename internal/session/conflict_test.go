package session

import (
	"strings"
	"testing"
)

func TestConflictNamesRemoveCommand(t *testing.T) {
	cmds := []Command{
		{Name: "auth", Location: "/own"},
		{Name: "quotas", Source: "git:https://github.com/furbyhaxx/pi-auth-plus", Location: "/x/pi-auth-plus"},
	}
	msg, ok := Conflict(cmds, "/own")
	if !ok || !strings.Contains(msg, "pig remove git:https://github.com/furbyhaxx/pi-auth-plus") || !strings.Contains(msg, "pi-auth-plus also") {
		t.Fatalf("got %q %v", msg, ok)
	}
	if _, ok := Conflict(cmds[:1], "/own"); ok {
		t.Fatal("own commands are not a conflict")
	}
}
