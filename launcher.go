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
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows/registry"
)

//go:embed codex_lb_icon.ico
var iconICO []byte

var (
	shell32                = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW      = shell32.NewProc("ShellExecuteW")
	user32                 = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW        = user32.NewProc("MessageBoxW")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW       = kernel32.NewProc("CreateMutexW")
	procCloseHandle        = kernel32.NewProc("CloseHandle")
	procGetUserDefaultLCID = kernel32.NewProc("GetUserDefaultLCID")
)

// Tray menu strings - auto-detected based on system locale
type trayStrings struct {
	OpenDashboard   string
	StartOnLogon    string
	AutoUpdate      string
	CheckUpdates    string
	Quit            string
	Tooltip         string
	UpdateAvailable string
	UpToDate        string
	InstallConfirm  string
	DownloadFailed  string
	CheckFailed     string
}

var lang = trayStrings{
	OpenDashboard:   "Open Dashboard",
	StartOnLogon:    "Start on Windows Logon",
	AutoUpdate:      "Auto Update",
	CheckUpdates:    "Check for Updates",
	Quit:            "Quit",
	Tooltip:         "CodexLB",
	UpdateAvailable: "Update Available",
	UpToDate:        "CodexLB is up to date (%s).",
	InstallConfirm:  "Current version: %s\nNew version: %s\n\nInstall now?",
	DownloadFailed:  "Failed to download update.",
	CheckFailed:     "Failed to check for updates.",
}

const (
	MB_OK           = 0x00000000
	MB_YESNO        = 0x00000004
	MB_ICONINFO     = 0x00000040
	MB_ICONQUESTION = 0x00000020
	MB_ICONERROR    = 0x00000010
	IDYES           = 6
)

func showMessageBox(title, message string, flags uint32) int {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	messagePtr, _ := syscall.UTF16PtrFromString(message)
	ret, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(messagePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(flags),
	)
	return int(ret)
}

func isChineseLocale() bool {
	lcid, _, _ := procGetUserDefaultLCID.Call()
	// Chinese Simplified: 0x0804, Chinese Traditional: 0x0404
	// LCID & 0xFF == 0x04 means Chinese
	return (lcid & 0x3FF) == 0x004
}

func init() {
	if isChineseLocale() {
		lang = trayStrings{
			OpenDashboard:   "\u6253\u5f00\u4eea\u8868\u76d8",
			StartOnLogon:    "\u5f00\u673a\u81ea\u52a8\u542f\u52a8",
			AutoUpdate:      "\u81ea\u52a8\u66f4\u65b0",
			CheckUpdates:    "\u68c0\u67e5\u66f4\u65b0",
			Quit:            "\u9000\u51fa",
			Tooltip:         "CodexLB",
			UpdateAvailable: "\u53d1\u73b0\u65b0\u7248\u672c",
			UpToDate:        "CodexLB \u5df2\u662f\u6700\u65b0\u7248\u672c (%s)\u3002",
			InstallConfirm:  "\u5f53\u524d\u7248\u672c: %s\n\u65b0\u7248\u672c: %s\n\n\u662f\u5426\u7acb\u5373\u5b89\u88c5\uff1f",
			DownloadFailed:  "\u4e0b\u8f7d\u66f4\u65b0\u5931\u8d25\u3002",
			CheckFailed:     "\u68c0\u67e5\u66f4\u65b0\u5931\u8d25\u3002",
		}
	}
}

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

type installMode int

const (
	installVisible installMode = iota
	installSilent
)

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

// versionCore removes tag prefixes such as "v" while preserving semver suffixes.
func versionCore(v string) string {
	v = strings.TrimSpace(v)
	for i := 0; i < len(v); i++ {
		if v[i] >= '0' && v[i] <= '9' {
			return v[i:]
		}
	}
	return v
}

func displayVersion(v string) string {
	v = versionCore(v)
	if v == "" {
		return "unknown"
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
	valid      bool
}

func parseSemanticVersion(v string) semanticVersion {
	v = versionCore(v)
	if build := strings.IndexByte(v, '+'); build >= 0 {
		v = v[:build]
	}

	var prerelease []string
	if pre := strings.IndexByte(v, '-'); pre >= 0 {
		prerelease = strings.Split(v[pre+1:], ".")
		v = v[:pre]
	}

	parts := strings.Split(v, ".")
	if len(parts) == 0 {
		return semanticVersion{}
	}

	nums := [3]int{}
	for i := 0; i < len(parts) && i < len(nums); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return semanticVersion{}
		}
		nums[i] = n
	}

	return semanticVersion{
		major:      nums[0],
		minor:      nums[1],
		patch:      nums[2],
		prerelease: prerelease,
		valid:      true,
	}
}

// parseVersion extracts major, minor, patch from a version string like "v1.19.0" or "1.19.0-beta.1".
func parseVersion(v string) (int, int, int) {
	parsed := parseSemanticVersion(v)
	if !parsed.valid {
		return 0, 0, 0
	}
	return parsed.major, parsed.minor, parsed.patch
}

// isNewerVersion returns true if version a is newer than version b.
func isNewerVersion(a, b string) bool {
	av := parseSemanticVersion(a)
	bv := parseSemanticVersion(b)
	if !av.valid {
		return false
	}
	if !bv.valid {
		return true
	}
	if av.major != bv.major {
		return av.major > bv.major
	}
	if av.minor != bv.minor {
		return av.minor > bv.minor
	}
	if av.patch != bv.patch {
		return av.patch > bv.patch
	}
	return comparePrerelease(av.prerelease, bv.prerelease) > 0
}

func comparePrerelease(a, b []string) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}

	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if cmp := comparePrereleaseIdentifier(a[i], b[i]); cmp != 0 {
			return cmp
		}
	}
	switch {
	case len(a) > len(b):
		return 1
	case len(a) < len(b):
		return -1
	default:
		return 0
	}
}

func comparePrereleaseIdentifier(a, b string) int {
	ai, aErr := strconv.Atoi(a)
	bi, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		switch {
		case ai > bi:
			return 1
		case ai < bi:
			return -1
		default:
			return 0
		}
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func isInstallerAsset(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".exe") && strings.Contains(name, "installer")
}

func isExeAsset(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".exe")
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

	// Find the latest stable release that's actually newer than current version
	var bestTag string
	var bestURL string
	for _, rel := range releases {
		if rel.Prerelease {
			continue
		}
		if !isNewerVersion(rel.TagName, currentVersion) {
			continue
		}
		// This release is newer - check if it's the best we've found
		if bestTag != "" && !isNewerVersion(rel.TagName, bestTag) {
			continue
		}
		// Prefer the named installer executable. Fall back to the only .exe when a
		// release has not followed the naming convention.
		var installerURL string
		var exeURL string
		exeCount := 0
		for _, asset := range rel.Assets {
			if isInstallerAsset(asset.Name) {
				installerURL = asset.BrowserDownloadURL
				break
			}
			if isExeAsset(asset.Name) {
				exeURL = asset.BrowserDownloadURL
				exeCount++
			}
		}
		if installerURL == "" && exeCount == 1 {
			installerURL = exeURL
		}
		if installerURL != "" {
			bestTag = rel.TagName
			bestURL = installerURL
		}
	}
	if bestTag == "" {
		return "", "", nil // Already up to date
	}
	return bestTag, bestURL, nil
}

func downloadInstaller(url string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	file, err := os.CreateTemp(os.TempDir(), "CodexLB_Installer_Update_*.exe")
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	installerPath := file.Name()
	downloaded := false
	defer func() {
		_ = file.Close()
		if !downloaded {
			_ = os.Remove(installerPath)
		}
	}()

	if _, err := file.ReadFrom(resp.Body); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize installer: %w", err)
	}
	downloaded = true
	return installerPath, nil
}

func installerArgs(mode installMode) []string {
	args := []string{
		"/NORESTART",
		"/LOG=" + filepath.Join(os.TempDir(), "CodexLB_Update.log"),
	}
	if mode == installSilent {
		args = append(args, "/VERYSILENT", "/SUPPRESSMSGBOXES")
	}
	return args
}

func launchInstaller(installerPath string, mode installMode) error {
	installer := exec.Command(installerPath, installerArgs(mode)...)
	if mode == installSilent {
		installer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	return installer.Start()
}

// downloadAndLaunchInstaller stages the installer before the launcher exits.
// Exiting first races the backend-death watcher and can abort the update.
func downloadAndLaunchInstaller(url string, mode installMode) error {
	installerPath, err := downloadInstaller(url)
	if err != nil {
		return err
	}
	return launchInstaller(installerPath, mode)
}

func exitForUpdate(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	os.Exit(0)
}

func installConfirmMessage(newTag string) string {
	return fmt.Sprintf(lang.InstallConfirm, displayVersion(currentVersion), displayVersion(newTag))
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
			if err := downloadAndLaunchInstaller(installerURL, installSilent); err != nil {
				return
			}
			exitForUpdate(cmd)
		}()
	}

	// Run the system tray. This call blocks until systray.Quit() is called.
	systray.Run(
		func() {
			// onReady — set up tray icon and menu.
			systray.SetIcon(iconICO)
			systray.SetTitle("CodexLB")
			systray.SetTooltip(lang.Tooltip)

			mOpen := systray.AddMenuItem(lang.OpenDashboard, "Open the web dashboard")
			mAutostart := systray.AddMenuItemCheckbox(lang.StartOnLogon, "Start CodexLB automatically on startup", isAutostartEnabled())
			mAutoUpdate := systray.AddMenuItemCheckbox(lang.AutoUpdate, "Automatically check and install updates on startup", isAutoUpdateEnabled())
			mUpdate := systray.AddMenuItem(lang.CheckUpdates, "Check for new stable version and install if available")
			systray.AddSeparator()
			mQuit := systray.AddMenuItem(lang.Quit, "Stop CodexLB")

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
								showMessageBox("CodexLB", lang.CheckFailed, MB_OK|MB_ICONERROR)
								return
							}
							if newTag == "" {
								showMessageBox("CodexLB", fmt.Sprintf(lang.UpToDate, displayVersion(currentVersion)), MB_OK|MB_ICONINFO)
								return
							}
							// Ask user to confirm installation
							msg := installConfirmMessage(newTag)
							ret := showMessageBox(lang.UpdateAvailable, msg, MB_YESNO|MB_ICONQUESTION)
							if ret != IDYES {
								return
							}
							if err := downloadAndLaunchInstaller(installerURL, installVisible); err != nil {
								showMessageBox("CodexLB", lang.DownloadFailed, MB_OK|MB_ICONERROR)
								return
							}
							exitForUpdate(cmd)
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
