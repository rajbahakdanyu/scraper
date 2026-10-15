package backend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const (
	fileManagerName = "org.freedesktop.FileManager1"
	fileManagerPath = "/org/freedesktop/FileManager1"
	portalName      = "org.freedesktop.portal.Desktop"
	portalPath      = "/org/freedesktop/portal/desktop"
	portalOpenURI   = "org.freedesktop.portal.OpenURI"
	busCallDeadline = 5 * time.Second
)

// platformOpen asks the desktop over D-Bus rather than running xdg-open:
// the org.freedesktop.FileManager1 service that Nautilus, Dolphin, Nemo,
// Caja and others implement, or, without one, the XDG desktop portal.
func platformOpen(abs string, reveal bool) error {
	// godbus's Hello has no timeout; cancelling the conn's context closes it.
	ctx, cancel := context.WithTimeout(context.Background(), busCallDeadline)
	defer cancel()
	conn, err := dbus.SessionBusPrivateNoAutoStartup(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("no desktop session bus: %w", err)
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return err
	}
	if err := conn.Hello(); err != nil {
		return err
	}

	method := fileManagerName + ".ShowFolders"
	if reveal {
		method = fileManagerName + ".ShowItems"
	}
	uri := (&url.URL{Scheme: "file", Path: abs}).String()
	err = conn.Object(fileManagerName, fileManagerPath).CallWithContext(ctx, method, 0, []string{uri}, "").Err
	if !isServiceUnknown(err) {
		return err
	}
	return portalOpen(ctx, conn, abs, reveal)
}

// portalOpen hands the portal an O_PATH descriptor, as it requires for
// local files: OpenFile shows a folder in the default file manager,
// OpenDirectory shows the folder containing an item.
func portalOpen(ctx context.Context, conn *dbus.Conn, abs string, reveal bool) error {
	fd, err := unix.Open(abs, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if fd < 0 || fd > math.MaxInt32 {
		return fmt.Errorf("file descriptor %d does not fit D-Bus's int32", fd)
	}
	method := portalOpenURI + ".OpenFile"
	if reveal {
		method = portalOpenURI + ".OpenDirectory"
	}
	return conn.Object(portalName, portalPath).CallWithContext(ctx, method, 0, "", dbus.UnixFD(fd), map[string]dbus.Variant{}).Err
}

func isServiceUnknown(err error) bool {
	var dbusErr dbus.Error
	return errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown"
}
