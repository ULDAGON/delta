package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchdPlistContent(t *testing.T) {
	plist := launchdPlist("/usr/local/bin/delta", "/Users/x/Library/Logs/delta")
	for _, want := range []string{
		"<string>" + serviceLabel + "</string>",
		"<string>/usr/local/bin/delta</string>",
		"<string>serve</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"/Users/x/Library/Logs/delta/serve.log",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}
}

func TestSystemdUnitContent(t *testing.T) {
	unit := systemdUnit("/usr/bin/delta")
	for _, want := range []string{
		"ExecStart=/usr/bin/delta serve",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", unit, want)
		}
	}
}

func TestServiceInstallRejectsGoRunBinary(t *testing.T) {
	temporary := filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "delta")
	if err := checkServiceBinary(temporary); err == nil {
		t.Fatal("want an error for a go-run temporary binary")
	}
	if err := checkServiceBinary("/usr/local/bin/delta"); err != nil {
		t.Fatalf("stable path rejected: %v", err)
	}
}

func TestServiceInstallWritesFileAndBootstraps(t *testing.T) {
	home := t.TempDir()
	var calls [][]string
	restore := stubServicePlatform(t, "darwin", home, func(name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		return "", nil
	})
	defer restore()

	var out strings.Builder
	if err := runService(nil, []string{"install"}, &out); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	found := false
	for _, call := range calls {
		if call[0] == "launchctl" && call[1] == "bootstrap" {
			found = true
		}
	}
	if !found {
		t.Fatalf("launchctl bootstrap not invoked; calls: %v", calls)
	}
}

func TestServiceStopAndStartDriveLaunchctl(t *testing.T) {
	home := t.TempDir()
	var calls [][]string
	restore := stubServicePlatform(t, "darwin", home, func(name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		return "", nil
	})
	defer restore()

	var out strings.Builder
	if err := runService(nil, []string{"install"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := runService(nil, []string{"stop"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := runService(nil, []string{"start"}, &out); err != nil {
		t.Fatal(err)
	}
	var bootouts, bootstraps int
	for _, call := range calls {
		if call[0] != "launchctl" {
			continue
		}
		switch call[1] {
		case "bootout":
			bootouts++
		case "bootstrap":
			bootstraps++
		}
	}
	// install does bootout+bootstrap, stop adds a bootout, start a bootstrap.
	if bootouts != 2 || bootstraps != 2 {
		t.Fatalf("bootouts=%d bootstraps=%d, want 2/2; calls: %v", bootouts, bootstraps, calls)
	}
}

func TestServiceStartWithoutInstallExplains(t *testing.T) {
	home := t.TempDir()
	restore := stubServicePlatform(t, "darwin", home, func(name string, args ...string) (string, error) {
		return "", nil
	})
	defer restore()

	var out strings.Builder
	if err := runService(nil, []string{"start"}, &out); err == nil || !strings.Contains(err.Error(), "install") {
		t.Fatalf("want an error pointing at install, got %v", err)
	}
}

func TestServiceUninstallRemovesFile(t *testing.T) {
	home := t.TempDir()
	restore := stubServicePlatform(t, "darwin", home, func(name string, args ...string) (string, error) {
		return "", nil
	})
	defer restore()

	var out strings.Builder
	if err := runService(nil, []string{"install"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := runService(nil, []string{"uninstall"}, &out); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist still present after uninstall: %v", err)
	}
}

// stubServicePlatform pins the service code to a platform, home directory,
// binary path, and command runner for a test.
func stubServicePlatform(t *testing.T, goos, home string, runner func(string, ...string) (string, error)) func() {
	t.Helper()
	oldGOOS, oldHome, oldBinary, oldRunner := serviceGOOS, serviceHome, serviceBinary, serviceRunCommand
	serviceGOOS = goos
	serviceHome = func() (string, error) { return home, nil }
	serviceBinary = func() (string, error) { return "/usr/local/bin/delta", nil }
	serviceRunCommand = runner
	return func() {
		serviceGOOS, serviceHome, serviceBinary, serviceRunCommand = oldGOOS, oldHome, oldBinary, oldRunner
	}
}
