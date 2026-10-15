package notify

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// envMessage carries the notification text to darwinScript. Passing it
// through the environment keeps it out of the script source, so no message
// can be parsed as code.
const envMessage = "SCRAPER_NOTIFICATION"

// darwinMessage reads envMessage as UTF-8 text (AppleScript's
// "system attribute" decodes it as MacRoman and mangles non-ASCII).
const darwinMessage = `$.NSProcessInfo.processInfo.environment.objectForKey("` + envMessage + `").js`

// darwinScript is fixed JavaScript for Automation.
const darwinScript = `ObjC.import("Foundation");` +
	`var app = Application.currentApplication();` +
	`app.includeStandardAdditions = true;` +
	`app.displayNotification(` + darwinMessage + `, {withTitle: "scraper"});`

func send(summary string) error {
	if out, err := notifyCommand(summary).CombinedOutput(); err != nil {
		return fmt.Errorf("notification: osascript failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func notifyCommand(summary string) *exec.Cmd {
	cmd := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", darwinScript)
	cmd.Env = append(os.Environ(), envMessage+"="+summary)
	return cmd
}
