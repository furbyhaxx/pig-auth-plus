package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/session"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	uiquotas "github.com/furbyhaxx/pig-auth-plus/internal/ui/quotas"
)

func testDashboard(t *testing.T) (*dashboard, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth-plus.toml")
	st := &state{settings: config.NewManager(path), group: session.NewGroup()}
	t.Cleanup(st.group.Close)
	tab := uiquotas.New(func() []quota.View { return nil }, func() uiquotas.Display { return uiquotas.Display{} }, uiquotas.Actions{}, "")
	d := &dashboard{st: st, quotas: tab}
	d.frame = frame.New(func() frame.Env { return frame.Env{Width: 80, Rows: 24} }, "quotas", tab)
	return d, path
}

// answer waits for the dialog titled title and selects option.
func answer(t *testing.T, d *dashboard, title, option string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if d.frame.ModalTitle() == title {
			for i, o := range d.frame.ModalOptions() {
				if o != option {
					continue
				}
				for range i {
					_, _ = d.frame.HandleInput("\x1b[B")
				}
				_, _ = d.frame.HandleInput("\r")
				return
			}
			t.Fatalf("dialog %q has no option %q", title, option)
		}
	}
	t.Fatalf("dialog %q never opened", title)
}

func TestPlacementIsSavedOnlyAfterTheScopeIsConfirmed(t *testing.T) {
	d, path := testDashboard(t)
	done := make(chan struct{})
	go func() { d.choosePlacement(context.Background()); close(done) }()
	answer(t, d, "Quota line placement", "Hidden (refresh continues)")
	time.Sleep(20 * time.Millisecond)
	if got := d.st.settings.Snapshot().LinePlacement; got != "below-editor-first" {
		t.Fatalf("placement changed before the scope was confirmed: %q", got)
	}
	answer(t, d, "Save placement to", "This session only")
	<-done
	if got := d.st.settings.Snapshot().LinePlacement; got != "off" {
		t.Fatalf("session placement = %q", got)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a session save wrote the user file")
	}
}

func TestPlacementSavedToUserFileKeepsComments(t *testing.T) {
	d, path := testDashboard(t)
	if err := os.WriteFile(path, []byte("# mine\n[quotas]\nrefreshIntervalSec = 90 # keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.st.settings.Load(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { d.choosePlacement(context.Background()); close(done) }()
	answer(t, d, "Quota line placement", "Above the editor")
	answer(t, d, "Save placement to", "User file (auth-plus.toml)")
	<-done
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# mine") || !strings.Contains(string(raw), "# keep") || !strings.Contains(string(raw), `linePlacement = "above-editor"`) {
		t.Fatalf("file:\n%s", raw)
	}
}

func TestDismissingTheChooserSavesNothing(t *testing.T) {
	d, path := testDashboard(t)
	done := make(chan struct{})
	go func() { d.choosePlacement(context.Background()); close(done) }()
	for d.frame.ModalTitle() == "" {
		time.Sleep(time.Millisecond)
	}
	_, _ = d.frame.HandleInput("\x1b")
	<-done
	if _, err := os.Stat(path); err == nil || d.st.settings.Snapshot().LinePlacement != "below-editor-first" {
		t.Fatal("dismissal changed settings")
	}
}
