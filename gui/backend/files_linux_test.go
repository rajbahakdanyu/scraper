package backend

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"

	"github.com/rajbahakdanyu/scraper/internal/dbustest"
)

type fakeFileManager struct{ calls chan []string }

func (f fakeFileManager) ShowFolders(uris []string, startup string) *dbus.Error {
	f.calls <- append([]string{"ShowFolders"}, uris...)
	return nil
}

func (f fakeFileManager) ShowItems(uris []string, startup string) *dbus.Error {
	f.calls <- append([]string{"ShowItems"}, uris...)
	return nil
}

func receive[T any](t *testing.T, ch chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("the call never reached the service")
		panic("unreachable")
	}
}

func TestPlatformOpenUsesTheFileManagerService(t *testing.T) {
	address := dbustest.Start(t)
	fm := fakeFileManager{calls: make(chan []string, 1)}
	dbustest.Serve(t, address, fileManagerName, fileManagerPath, fileManagerName, fm)
	dir := filepath.Join(t.TempDir(), "x y#?")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "clip%20.mp4")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}

	if err := platformOpen(dir, false); err != nil {
		t.Fatal(err)
	}
	if got, want := receive(t, fm.calls), []string{"ShowFolders", "file://" + filepath.Dir(dir) + "/x%20y%23%3F"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if err := platformOpen(file, true); err != nil {
		t.Fatal(err)
	}
	if got, want := receive(t, fm.calls), []string{"ShowItems", "file://" + filepath.Dir(dir) + "/x%20y%23%3F/clip%2520.mp4"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

type portalCall struct {
	method string
	stat   unix.Stat_t
}

type fakePortal struct{ calls chan portalCall }

func (p fakePortal) record(method string, fd dbus.UnixFD) (dbus.ObjectPath, *dbus.Error) {
	defer unix.Close(int(fd))
	var st unix.Stat_t
	if err := unix.Fstat(int(fd), &st); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	p.calls <- portalCall{method, st}
	return "/org/freedesktop/portal/desktop/request/1", nil
}

func (p fakePortal) OpenFile(parent string, fd dbus.UnixFD, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.record("OpenFile", fd)
}

func (p fakePortal) OpenDirectory(parent string, fd dbus.UnixFD, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.record("OpenDirectory", fd)
}

func TestPlatformOpenFallsBackToThePortal(t *testing.T) {
	address := dbustest.Start(t)
	portal := fakePortal{calls: make(chan portalCall, 1)}
	dbustest.Serve(t, address, portalName, portalPath, portalOpenURI, portal)
	dir := t.TempDir()
	file := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		path   string
		reveal bool
		method string
	}{{dir, false, "OpenFile"}, {file, true, "OpenDirectory"}} {
		if err := platformOpen(tt.path, tt.reveal); err != nil {
			t.Fatal(err)
		}
		got := receive(t, portal.calls)
		var want unix.Stat_t
		if err := unix.Stat(tt.path, &want); err != nil {
			t.Fatal(err)
		}
		if got.method != tt.method || got.stat.Dev != want.Dev || got.stat.Ino != want.Ino {
			t.Fatalf("portal got %s for inode %d, want %s for %s (inode %d)", got.method, got.stat.Ino, tt.method, tt.path, want.Ino)
		}
	}
}

func TestPlatformOpenWithoutAnyServiceFails(t *testing.T) {
	dbustest.Start(t)
	if err := platformOpen(t.TempDir(), false); err == nil {
		t.Fatal("expected an error when nothing can show the folder")
	}
}
