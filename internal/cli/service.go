package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// serviceLabel is the launchd label and the reverse-DNS identity of the
// login service on macOS; Linux derives its unit name from the tail segment.
const serviceLabel = "com.ferriskleier.delta"

const systemdUnitName = "delta.service"

// Seams for tests: the real values shell out and read the live machine.
var (
	serviceGOOS       = runtime.GOOS
	serviceHome       = os.UserHomeDir
	serviceBinary     = os.Executable
	serviceRunCommand = func(name string, args ...string) (string, error) {
		output, err := exec.Command(name, args...).CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
)

const serviceUsage = "usage: delta service install|uninstall|start|stop|status"

func runService(_ context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New(serviceUsage)
	}
	switch serviceGOOS {
	case "darwin", "linux":
	default:
		return fmt.Errorf("delta service supports macOS and Linux; on %s schedule %q at login yourself (e.g. Task Scheduler)", serviceGOOS, "delta serve")
	}
	switch args[0] {
	case "install":
		return serviceInstall(stdout)
	case "uninstall":
		return serviceUninstall(stdout)
	case "start":
		return serviceStart(stdout)
	case "stop":
		return serviceStop(stdout)
	case "status":
		return serviceStatus(stdout)
	default:
		return errors.New(serviceUsage)
	}
}

// serviceStart loads an installed service back into the session. KeepAlive
// then holds it up again until the next stop or uninstall.
func serviceStart(stdout io.Writer) error {
	_, home, err := servicePaths()
	if err != nil {
		return err
	}
	if serviceGOOS == "darwin" {
		plistPath := launchdPlistPath(home)
		if _, err := os.Stat(plistPath); os.IsNotExist(err) {
			return errors.New("service not installed; run delta service install first")
		}
		if output, err := serviceRunCommand("launchctl", "bootstrap", launchdDomain(), plistPath); err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, output)
		}
		fmt.Fprintln(stdout, "service started")
		return nil
	}
	if _, err := os.Stat(systemdUnitPath(home)); os.IsNotExist(err) {
		return errors.New("service not installed; run delta service install first")
	}
	if output, err := serviceRunCommand("systemctl", "--user", "start", systemdUnitName); err != nil {
		return fmt.Errorf("systemctl start: %v: %s", err, output)
	}
	fmt.Fprintln(stdout, "service started")
	return nil
}

// serviceStop unloads the service from the running session without removing
// it: it stays installed and comes back at the next login or delta service
// start. Killing the process instead would just trip KeepAlive's restart.
func serviceStop(stdout io.Writer) error {
	_, _, err := servicePaths()
	if err != nil {
		return err
	}
	if serviceGOOS == "darwin" {
		if output, err := serviceRunCommand("launchctl", "bootout", launchdDomain()+"/"+serviceLabel); err != nil {
			return fmt.Errorf("launchctl bootout: %v: %s", err, output)
		}
		fmt.Fprintln(stdout, "service stopped (starts again at next login or delta service start)")
		return nil
	}
	if output, err := serviceRunCommand("systemctl", "--user", "stop", systemdUnitName); err != nil {
		return fmt.Errorf("systemctl stop: %v: %s", err, output)
	}
	fmt.Fprintln(stdout, "service stopped (starts again at next login or delta service start)")
	return nil
}

// checkServiceBinary refuses paths inside Go's build cache: a `go run`
// binary disappears after the process exits, which would leave the service
// pointing at nothing.
func checkServiceBinary(binary string) error {
	if strings.Contains(binary, string(filepath.Separator)+"go-build") {
		return fmt.Errorf("this delta runs from a temporary go-run build (%s); install a stable binary first (go install ./cmd/delta) and run `delta service install` from that", binary)
	}
	return nil
}

func servicePaths() (binary, home string, err error) {
	binary, err = serviceBinary()
	if err != nil {
		return "", "", fmt.Errorf("locate delta binary: %w", err)
	}
	if err := checkServiceBinary(binary); err != nil {
		return "", "", err
	}
	home, err = serviceHome()
	if err != nil {
		return "", "", fmt.Errorf("locate home directory: %w", err)
	}
	return binary, home, nil
}

func launchdPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
}

func launchdLogDirectory(home string) string {
	return filepath.Join(home, "Library", "Logs", "delta")
}

func launchdPlist(binary, logDirectory string) string {
	logPath := filepath.Join(logDirectory, "serve.log")
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + serviceLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + binary + `</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + logPath + `</string>
	<key>StandardErrorPath</key>
	<string>` + logPath + `</string>
</dict>
</plist>
`
}

func systemdUnitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", systemdUnitName)
}

func systemdUnit(binary string) string {
	return `[Unit]
Description=DELTA journal server

[Service]
ExecStart=` + binary + ` serve
Restart=on-failure

[Install]
WantedBy=default.target
`
}

func launchdDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func serviceInstall(stdout io.Writer) error {
	binary, home, err := servicePaths()
	if err != nil {
		return err
	}
	if serviceGOOS == "darwin" {
		logDirectory := launchdLogDirectory(home)
		if err := os.MkdirAll(logDirectory, 0o755); err != nil {
			return err
		}
		plistPath := launchdPlistPath(home)
		if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(plistPath, []byte(launchdPlist(binary, logDirectory)), 0o644); err != nil {
			return err
		}
		// A previous registration would make bootstrap fail; unloading an
		// unknown service is harmless, so the bootout result is ignored.
		_, _ = serviceRunCommand("launchctl", "bootout", launchdDomain()+"/"+serviceLabel)
		if output, err := serviceRunCommand("launchctl", "bootstrap", launchdDomain(), plistPath); err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, output)
		}
		fmt.Fprintf(stdout, "installed %s\ndelta serve now runs at login; logs: %s\n", plistPath, filepath.Join(logDirectory, "serve.log"))
		return nil
	}

	unitPath := systemdUnitPath(home)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(systemdUnit(binary)), 0o644); err != nil {
		return err
	}
	if output, err := serviceRunCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v: %s", err, output)
	}
	if output, err := serviceRunCommand("systemctl", "--user", "enable", "--now", systemdUnitName); err != nil {
		return fmt.Errorf("systemctl enable: %v: %s", err, output)
	}
	fmt.Fprintf(stdout, "installed %s\ndelta serve now runs at login; logs: journalctl --user -u %s\n", unitPath, systemdUnitName)
	return nil
}

func serviceUninstall(stdout io.Writer) error {
	_, home, err := servicePaths()
	if err != nil {
		return err
	}
	if serviceGOOS == "darwin" {
		_, _ = serviceRunCommand("launchctl", "bootout", launchdDomain()+"/"+serviceLabel)
		plistPath := launchdPlistPath(home)
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Fprintln(stdout, "service removed; delta serve no longer starts at login")
		return nil
	}

	_, _ = serviceRunCommand("systemctl", "--user", "disable", "--now", systemdUnitName)
	if err := os.Remove(systemdUnitPath(home)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if output, err := serviceRunCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v: %s", err, output)
	}
	fmt.Fprintln(stdout, "service removed; delta serve no longer starts at login")
	return nil
}

func serviceStatus(stdout io.Writer) error {
	_, home, err := servicePaths()
	if err != nil {
		return err
	}
	if serviceGOOS == "darwin" {
		if _, err := os.Stat(launchdPlistPath(home)); os.IsNotExist(err) {
			fmt.Fprintln(stdout, "not installed")
			return nil
		}
		output, err := serviceRunCommand("launchctl", "print", launchdDomain()+"/"+serviceLabel)
		if err != nil {
			fmt.Fprintln(stdout, "installed but stopped (delta service start)")
			return nil
		}
		state := "loaded"
		seen := map[string]bool{}
		for _, line := range strings.Split(output, "\n") {
			trimmed := strings.TrimSpace(line)
			for _, prefix := range []string{"state =", "pid ="} {
				if strings.HasPrefix(trimmed, prefix) && !seen[prefix] {
					seen[prefix] = true
					state = state + " · " + trimmed
				}
			}
		}
		fmt.Fprintln(stdout, state)
		return nil
	}

	if _, err := os.Stat(systemdUnitPath(home)); os.IsNotExist(err) {
		fmt.Fprintln(stdout, "not installed")
		return nil
	}
	output, _ := serviceRunCommand("systemctl", "--user", "is-active", systemdUnitName)
	fmt.Fprintln(stdout, "installed · "+output)
	return nil
}
