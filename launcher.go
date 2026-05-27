package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows/registry"
)

//go:embed codex_lb_icon.ico
var iconICO []byte

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW  = kernel32.NewProc("CreateMutexW")
	procCloseHandle   = kernel32.NewProc("CloseHandle")
)

const (
	ERROR_ALREADY_EXISTS = 183
	SW_SHOWNORMAL        = 1
	registryRunKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	registryValueName    = "CodexLB"
	registryAutoUpdate   = "CodexLBAutoUpdate"
	githubAPIURL         = "https://api.github.com/repos/SpookySandwich/codex-lb-installer/releases"
)

// currentVersion is set via ldflags at build time.
var currentVersion string

func createNamedMutex(name string) (uintptr, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	ret, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if ret == 0 {
		return 0, err
	}
	if err != nil && err.(syscall.Errno) == ERROR_ALREADY_EXISTS {
		procCloseHandle.Call(ret)
		return 0, err
	}
	return ret, nil
}

func openURL(url string) error {
	verbPtr, _ := syscall.UTF16PtrFromString("open")
	filePtr, _ := syscall.UTF16PtrFromString(url)
	ret, _, err := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(filePtr)),
		0,
		0,
		SW_SHOWNORMAL,
	)
	if ret <= 32 {
		return err
	}
	return nil
}

func isAutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	_, _, err = k.GetStringValue(registryValueName)
	return err == nil
}

func isAutoUpdateEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue(registryAutoUpdate)
	return err == nil && val == 1
}

func setAutoUpdate(enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enabled {
		return k.SetDWordValue(registryAutoUpdate, 1)
	}
	return k.DeleteValue(registryAutoUpdate)
}

func setAutostart(enabled bool) error {
	if enabled {
		execPath, err := os.Executable()
		if err != nil {
			return err
		}
		k, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		// Wrap in quotes to handle paths with spaces safely
		return k.SetStringValue(registryValueName, `"`+execPath+`"`)
	} else {
		k, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		return k.DeleteValue(registryValueName)
	}
}

// GitHubRelease represents the GitHub API response for a release.
type GitHubRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// checkForUpdates checks GitHub for a newer stable release.
func checkForUpdates() (string, string, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(githubAPIURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var releases []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", "", fmt.Errorf("failed to parse releases: %w", err)
	}

	// Find the latest stable release that's newer than current
	for _, rel := range releases {
		if rel.Prerelease {
			continue
		}
		if rel.TagName == currentVersion {
			return "", "", nil // Already up to date
		}
		// Found a newer version
		var installerURL string
		for _, asset := range rel.Assets {
			if len(asset.Name) > 4 && asset.Name[len(asset.Name)-4:] == ".exe" {
				installerURL = asset.BrowserDownloadURL
				break
			}
		}
		if installerURL == "" {
			return "", "", fmt.Errorf("no installer found in release")
		}
		return rel.TagName, installerURL, nil
	}
	return "", "", nil // No newer release found
}

// downloadAndInstall downloads the installer and launches it silently.
// The caller should exit the process after this returns so the installer
// can replace the locked launcher.exe file.
func downloadAndInstall(url string) error {
	tmpDir := os.TempDir()
	installerPath := filepath.Join(tmpDir, "CodexLB_Installer_Update.exe")

	// Download the installer
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	file, err := os.Create(installerPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if _, err := file.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	file.Close()

	// Launch the installer silently then return.
	// The caller must exit the process so the installer can replace locked files.
	installer := exec.Command(installerPath, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART")
	installer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return installer.Start()
}

func main() {
	mutexName := "Local\\CodexLBLauncherMutex"
	mutex, err := createNamedMutex(mutexName)

	port := os.Getenv("PORT")
	if port == "" {
		port = "2455"
	}
	url := "http://127.0.0.1:" + port

	if err != nil {
		// Another instance is already running — just open the browser and exit.
		_ = openURL(url)
		os.Exit(0)
	}
	if mutex != 0 {
		defer procCloseHandle.Call(mutex)
	}

	// Locate the bundled Python relative to this executable.
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	installDir := filepath.Dir(execPath)

	pythonExe := filepath.Join(installDir, "python", "python.exe")
	if _, err := os.Stat(pythonExe); os.IsNotExist(err) {
		pythonExe = "python"
	}

	// Start the Python backend headlessly.
	cmd := exec.Command(pythonExe, "-m", "app.cli")
	cmd.Dir = installDir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	if err := cmd.Start(); err != nil {
		os.Exit(1)
	}

	// Monitor the Python process in the background.
	dying := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(dying)
	}()

	// Wait for the server to become ready, then open the dashboard.
	go func() {
		for i := 0; i < 150; i++ {
			conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				_ = openURL(url)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// Auto-update check on startup if enabled.
	if isAutoUpdateEnabled() {
		go func() {
			// Wait a moment for the app to fully start
			time.Sleep(10 * time.Second)
			newTag, installerURL, err := checkForUpdates()
			if err != nil || newTag == "" {
				return
			}
			if err := downloadAndInstall(installerURL); err != nil {
				return
			}
			// Kill Python backend, then exit so installer can replace locked files
			_ = cmd.Process.Kill()
			os.Exit(0)
		}()
	}

	// Run the system tray. This call blocks until systray.Quit() is called.
	systray.Run(
		func() {
			// onReady — set up tray icon and menu.
			systray.SetIcon(iconICO)
			systray.SetTitle("CodexLB")
			systray.SetTooltip("CodexLB")

			mOpen := systray.AddMenuItem("Open Dashboard", "Open the web dashboard")
			mAutostart := systray.AddMenuItemCheckbox("Start on Windows Logon", "Start CodexLB automatically on startup", isAutostartEnabled())
			mAutoUpdate := systray.AddMenuItemCheckbox("Auto Update", "Automatically check and install updates on startup", isAutoUpdateEnabled())
			mUpdate := systray.AddMenuItem("Check for Updates", "Check for new stable version and install if available")
			systray.AddSeparator()
			mQuit := systray.AddMenuItem("Quit", "Stop CodexLB")

			// Watch for Python process death.
			go func() {
				select {
				case <-dying:
					systray.Quit()
				}
			}()

			// Handle menu clicks.
			go func() {
				for {
					select {
					case <-mOpen.ClickedCh:
						_ = openURL(url)
					case <-mUpdate.ClickedCh:
						go func() {
							newTag, installerURL, err := checkForUpdates()
							if err != nil {
								return
							}
							if newTag == "" {
								return // Already up to date
							}
							if err := downloadAndInstall(installerURL); err != nil {
								return
							}
							// Kill Python backend, then exit so installer can replace locked files
							_ = cmd.Process.Kill()
							os.Exit(0)
						}()
					case <-mAutostart.ClickedCh:
						if mAutostart.Checked() {
							_ = setAutostart(false)
							mAutostart.Uncheck()
						} else {
							_ = setAutostart(true)
							mAutostart.Check()
						}
					case <-mAutoUpdate.ClickedCh:
						if mAutoUpdate.Checked() {
							_ = setAutoUpdate(false)
							mAutoUpdate.Uncheck()
						} else {
							_ = setAutoUpdate(true)
							mAutoUpdate.Check()
						}
					case <-mQuit.ClickedCh:
						_ = cmd.Process.Kill()
						systray.Quit()
					}
				}
			}()
		},
		func() {
			// onExit — clean shutdown.
			_ = cmd.Process.Kill()
		},
	)
}
