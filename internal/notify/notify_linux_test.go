package notify

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestSendWithoutSessionBusIsNoop(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing"))
	if err := Send("done"); err != nil {
		t.Fatalf("Send without a session bus = %v, want nil", err)
	}
}

// privateBus starts a throwaway dbus-daemon with no service activation and
// points DBUS_SESSION_BUS_ADDRESS at it.
func privateBus(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon not installed")
	}
	dir := t.TempDir()
	address := "unix:path=" + filepath.Join(dir, "bus")
	config := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(config, []byte(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>`+address+`</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*"/>
    <allow own="*"/>
  </policy>
</busconfig>
`), 0600); err != nil {
		t.Fatal(err)
	}
	daemon := exec.Command("dbus-daemon", "--config-file="+config, "--nofork", "--print-address")
	stdout, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
	})
	// The daemon prints its address once it is listening.
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatalf("dbus-daemon did not start: %v", err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	return address
}

type fakeNotifications struct{ bodies chan string }

func (f fakeNotifications) Notify(app string, replaces uint32, icon, title, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	f.bodies <- body
	return 1, nil
}

func TestSendDeliversTheSummaryOverTheSessionBus(t *testing.T) {
	address := privateBus(t)
	conn, err := dbus.Dial(address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		t.Fatal(err)
	}
	if err := conn.Hello(); err != nil {
		t.Fatal(err)
	}
	server := fakeNotifications{bodies: make(chan string, 1)}
	if err := conn.Export(server, notificationsPath, notificationsName); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(notificationsName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: reply %v, err %v", notificationsName, reply, err)
	}

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
	privateBus(t)
	if err := Send("done"); err != nil {
		t.Fatalf("Send with no notification service = %v, want nil", err)
	}
}
