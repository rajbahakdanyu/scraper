//go:build linux

// Package dbustest runs a throwaway D-Bus session bus for tests.
package dbustest

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Start launches a private dbus-daemon without service activation, points
// DBUS_SESSION_BUS_ADDRESS at it for the rest of the test, and returns its
// address. The test is skipped when dbus-daemon is not installed.
func Start(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon not installed")
	}
	// Not t.TempDir(): it embeds the test name, and socket paths max out near 108 bytes.
	dir, err := os.MkdirTemp("", "dbus")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	address := "unix:path=" + filepath.Join(dir, "bus")
	config := `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>` + address + `</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*"/>
    <allow receive_sender="*"/>
    <allow own="*"/>
  </policy>
</busconfig>
`
	if err := os.WriteFile(filepath.Join(dir, "bus.conf"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	daemon := exec.Command("dbus-daemon", "--config-file=bus.conf", "--nofork", "--print-address")
	daemon.Dir = dir
	var stderr bytes.Buffer
	daemon.Stderr = &stderr
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
		_ = daemon.Wait() // flushes stderr
		t.Fatalf("dbus-daemon did not start: %v: %s", err, stderr.String())
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	return address
}

// Serve connects to the bus at address, exports srv's methods at path as
// interface iface, and claims the well-known name.
func Serve(t testing.TB, address, name string, path dbus.ObjectPath, iface string, srv any) {
	t.Helper()
	conn, err := dbus.Dial(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Auth(nil); err != nil {
		t.Fatal(err)
	}
	if err := conn.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(srv, path, iface); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: reply %v, err %v", name, reply, err)
	}
}
