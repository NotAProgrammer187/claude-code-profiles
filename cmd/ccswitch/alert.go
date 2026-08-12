package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// alertUsage tells the user an account has crossed the red threshold, in a way
// that works when the panel isn't in view: the terminal bell always — most
// terminals sound or flash, and taskbars badge the window — plus a desktop
// notification where the platform has one built in. Failures are silent; an
// alert is a courtesy, never worth taking the panel down for.
func alertUsage(profile, window string, pct int) {
	// Stderr, not stdout: bubbletea owns stdout while the panel is up, and a
	// BEL has no glyph so nothing visible can be disturbed either way.
	fmt.Fprint(os.Stderr, "\a")
	notify("ccswitch", fmt.Sprintf("%s: %s window at %d%%", profile, window, pct))
}

// notify posts a desktop notification with whatever the platform ships:
// a WinRT toast on Windows, Notification Center via osascript on macOS,
// notify-send on Linux. Nothing to install; a platform (or desktop) without
// the mechanism just gets the bell.
func notify(title, body string) {
	switch runtime.GOOS {
	case "windows":
		toast(title, body)
	case "darwin":
		// AppleScript strings escape only backslash and double-quote.
		esc := func(s string) string {
			s = strings.ReplaceAll(s, `\`, `\\`)
			return strings.ReplaceAll(s, `"`, `\"`)
		}
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, esc(body), esc(title))
		startDetached(exec.Command("osascript", "-e", script))
	case "linux":
		startDetached(exec.Command("notify-send", "--app-name=ccswitch", title, body))
	}
}

// startDetached fires a command and forgets it: the panel must never wait on a
// notification, and whether it actually appeared is not ours to check.
func startDetached(cmd *exec.Cmd) {
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// powershellAppID is the AppUserModelID toasts are posted under. Toasts from
// an ID Windows has never seen are dropped silently, so borrow the one every
// machine has registered: PowerShell's own.
const powershellAppID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

// toast posts a Windows notification via PowerShell's WinRT bridge — nothing
// to install or configure.
func toast(title, body string) {
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	script := fmt.Sprintf(`
$null = [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime]
$xml = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$texts = $xml.GetElementsByTagName('text')
$null = $texts.Item(0).AppendChild($xml.CreateTextNode('%s'))
$null = $texts.Item(1).AppendChild($xml.CreateTextNode('%s'))
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('%s').Show([Windows.UI.Notifications.ToastNotification]::new($xml))
`, esc(title), esc(body), esc(powershellAppID))

	startDetached(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script))
}
