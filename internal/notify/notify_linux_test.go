package notify

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/rajbahakdanyu/scraper/internal/dbustest"
)

func TestSendWithoutSessionBusIsNoop(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing"))
	if err := Send("done"); err != nil {
		t.Fatalf("Send without a session bus = %v, want nil", err)
	}
}

type fakeNotifications struct{ bodies chan string }

func (f fakeNotifications) Notify(app string, replaces uint32, icon, title, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	f.bodies <- body
	return 1, nil
}

func TestSendDeliversTheSummaryOverTheSessionBus(t *testing.T) {
	address := dbustest.Start(t)
	server := fakeNotifications{bodies: make(chan string, 1)}
	dbustest.Serve(t, address, notificationsName, notificationsPath, notificationsName, server)

	const summary = `3 downloaded, 1 failed "quoted" $(id) — ✓`
	if err := Send(summary); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-server.bodies:
		if got != summary {
			t.Fatalf("notification body = %q, want %q", got, summary)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification never reached the service")
	}
}

func TestSendWithoutNotificationDaemonIsNoop(t *testing.T) {
	dbustest.Start(t)
	if err := Send("done"); err != nil {
		t.Fatalf("Send with no notification service = %v, want nil", err)
	}
}
