//go:build windows

package server

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Direct Wi-Fi on Windows 10 and 11: a Wi-Fi Direct "legacy" network (WinRT's
// WiFiDirectAdvertisementPublisher), which needs no administrator rights or internet and leaves the PC
// on its own Wi-Fi. Go can't call WinRT, so a hidden PowerShell runs it for as long as Ferry wants it,
// and stops it when its input closes: when Ferry turns it off, or quits in any way.
func init() {
	hotspotAvailable = func() bool { _, err := exec.LookPath("powershell"); return err == nil }
	// Windows counts the new network as public, and its firewall may only have let Ferry in on private ones.
	hotspotWarning = func() string {
		return "If phones can't reach Ferry on it, allow Ferry on public networks in Windows Security › Firewall."
	}
	startHotspot = startWindowsHotspot
}

const windowsHotspotScript = `
$ErrorActionPreference = 'Stop'
[Windows.Devices.WiFiDirect.WiFiDirectAdvertisementPublisher, Windows.Devices.WiFiDirect, ContentType=WindowsRuntime] | Out-Null
[Windows.Security.Credentials.PasswordCredential, Windows.Security.Credentials, ContentType=WindowsRuntime] | Out-Null
$pub = [Windows.Devices.WiFiDirect.WiFiDirectAdvertisementPublisher]::new()
$pub.Advertisement.IsAutonomousGroupOwnerEnabled = $true
$pub.Advertisement.LegacySettings.IsEnabled = $true
$pub.Advertisement.LegacySettings.Ssid = '%s'
$cred = [Windows.Security.Credentials.PasswordCredential]::new()
$cred.Password = '%s'
$pub.Advertisement.LegacySettings.Passphrase = $cred
$pub.Start()
$deadline = (Get-Date).AddSeconds(10)
while ($pub.Status.ToString() -ne 'Started') {
    if ($pub.Status.ToString() -eq 'Aborted' -or (Get-Date) -gt $deadline) { [Console]::Out.WriteLine('FAILED|' + $pub.Status); exit 1 }
    Start-Sleep -Milliseconds 150
}
[Console]::Out.WriteLine('READY|' + $pub.Advertisement.LegacySettings.Ssid)
[Console]::Out.Flush()
$null = [Console]::In.ReadToEnd()
$pub.Stop()
`

func startWindowsHotspot(ssid, password string) (hotspotRun, error) {
	// ssid is letters, digits and dashes, and password letters and digits, so the quotes hold.
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf(windowsHotspotScript, ssid, password))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	keep, err := cmd.StdinPipe()
	if err != nil {
		return hotspotRun{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return hotspotRun{}, err
	}
	if err := cmd.Start(); err != nil {
		return hotspotRun{}, err
	}
	ended := make(chan struct{})
	go func() { cmd.Wait(); close(ended) }()

	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "READY|") || strings.HasPrefix(line, "FAILED|") {
				ready <- line
				return
			}
		}
		close(ready)
	}()
	stop := func() {
		keep.Close()
		select {
		case <-ended:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
		}
	}
	select {
	case line, ok := <-ready:
		if !ok || !strings.HasPrefix(line, "READY|") {
			stop()
			return hotspotRun{}, fmt.Errorf("this PC's Wi-Fi can't make a network of its own (is Wi-Fi on?)")
		}
	case <-time.After(15 * time.Second):
		stop()
		return hotspotRun{}, fmt.Errorf("Windows took too long")
	}

	// Its address (Windows gives the Wi-Fi Direct adapter 192.168.137.1).
	ip := ""
	for i := 0; i < 30 && ip == ""; i++ {
		for _, a := range lanIPs() {
			if isHotspotAddr(a) {
				ip = a.String()
			}
		}
		if ip == "" {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if ip == "" {
		ip = "192.168.137.1"
	}
	return hotspotRun{ssid: ssid, password: password, ip: ip, stop: stop, ended: ended}, nil
}
