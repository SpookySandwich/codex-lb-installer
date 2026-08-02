package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
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
	OpenDashboard    string
	StartOnLogon     string
	AutoUpdate       string
	ChannelMenu      string
	ChannelRelease   string
	ChannelBeta      string
	ChannelEdge      string
	CheckUpdates     string
	StatusIdle       string
	StatusChecking   string
	StatusInstalling string
	DowntimeNotice   string
	ChannelSwitched  string
	AutoUpdateOn     string
	Quit             string
	Tooltip          string
	UpdateAvailable  string
	UpToDate         string
	InstallConfirm   string
	UpdateFailed     string
	UpdateBusy       string
	CheckFailed      string
	RuntimeMissing   string
}

var lang = trayStrings{
	OpenDashboard:    "Open Dashboard",
	StartOnLogon:     "Start on Windows Logon",
	AutoUpdate:       "Auto Update",
	ChannelMenu:      "Update Channel",
	ChannelRelease:   "Release (stable)",
	ChannelBeta:      "Beta (pre-releases)",
	ChannelEdge:      "Edge (main-branch builds)",
	CheckUpdates:     "Check for Updates",
	StatusIdle:       "Status: running (%s channel)",
	StatusChecking:   "Status: checking for updates…",
	StatusInstalling: "Status: installing — codex-lb is restarting…",
	DowntimeNotice:   "codex-lb will stop for a few seconds while it updates, then restart automatically.",
	ChannelSwitched:  "Update channel set to %s.\n\nNothing is being updated now — the new channel applies the next time you check for updates or restart CodexLB.",
	AutoUpdateOn:     "Auto update is on.\n\nNothing is being updated now — CodexLB checks once each time it starts.\n\n" + "When an update is found it installs automatically and codex-lb restarts, so expect a few seconds without service.",
	Quit:             "Quit",
	Tooltip:          "CodexLB",
	UpdateAvailable:  "Update Available",
	UpToDate:         "CodexLB is up to date (%s).",
	InstallConfirm:   "Current version: %s\nNew version: %s\n\nInstall now?",
	UpdateFailed:     "The update did not complete. CodexLB is still available.\n\n%s",
	UpdateBusy:       "Another update check or installation is already in progress.",
	CheckFailed:      "Failed to check for updates.\n\n%s",
	RuntimeMissing:   "The bundled Python runtime is missing. Reinstall CodexLB to repair this installation.",
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
			OpenDashboard:    "\u6253\u5f00\u4eea\u8868\u76d8",
			StartOnLogon:     "\u5f00\u673a\u81ea\u52a8\u542f\u52a8",
			AutoUpdate:       "\u81ea\u52a8\u66f4\u65b0",
			ChannelMenu:      "\u66f4\u65b0\u901a\u9053",
			ChannelRelease:   "\u6b63\u5f0f\u7248\uff08\u7a33\u5b9a\uff09",
			ChannelBeta:      "\u6d4b\u8bd5\u7248\uff08\u9884\u53d1\u5e03\uff09",
			ChannelEdge:      "\u5c1d\u9c9c\u7248\uff08\u4e3b\u5206\u652f\u6784\u5efa\uff09",
			CheckUpdates:     "\u68c0\u67e5\u66f4\u65b0",
			StatusIdle:       "\u72b6\u6001\uff1a\u8fd0\u884c\u4e2d\uff08%s \u901a\u9053\uff09",
			StatusChecking:   "\u72b6\u6001\uff1a\u6b63\u5728\u68c0\u67e5\u66f4\u65b0\u2026",
			StatusInstalling: "\u72b6\u6001\uff1a\u6b63\u5728\u5b89\u88c5 \u2014 codex-lb \u6b63\u5728\u91cd\u542f\u2026",
			DowntimeNotice:   "\u66f4\u65b0\u65f6 codex-lb \u4f1a\u4e2d\u65ad\u51e0\u79d2\u949f\uff0c\u968f\u540e\u81ea\u52a8\u91cd\u542f\u3002",
			ChannelSwitched:  "\u66f4\u65b0\u901a\u9053\u5df2\u8bbe\u7f6e\u4e3a %s\u3002\n\n\u5f53\u524d\u4e0d\u4f1a\u8fdb\u884c\u4efb\u4f55\u66f4\u65b0 \u2014 \u65b0\u901a\u9053\u5c06\u5728\u4e0b\u6b21\u68c0\u67e5\u66f4\u65b0\u6216\u91cd\u542f CodexLB \u65f6\u751f\u6548\u3002",
			AutoUpdateOn:     "\u81ea\u52a8\u66f4\u65b0\u5df2\u5f00\u542f\u3002\n\n\u5f53\u524d\u4e0d\u4f1a\u8fdb\u884c\u4efb\u4f55\u66f4\u65b0 \u2014 CodexLB \u53ea\u5728\u6bcf\u6b21\u542f\u52a8\u65f6\u68c0\u67e5\u4e00\u6b21\u3002\n\n\u53d1\u73b0\u66f4\u65b0\u65f6\u4f1a\u81ea\u52a8\u5b89\u88c5\u5e76\u91cd\u542f codex-lb\uff0c\u5c4a\u65f6\u4f1a\u6709\u51e0\u79d2\u949f\u65e0\u6cd5\u63d0\u4f9b\u670d\u52a1\u3002",
			Quit:             "\u9000\u51fa",
			Tooltip:          "CodexLB",
			UpdateAvailable:  "\u53d1\u73b0\u65b0\u7248\u672c",
			UpToDate:         "CodexLB \u5df2\u662f\u6700\u65b0\u7248\u672c (%s)\u3002",
			InstallConfirm:   "\u5f53\u524d\u7248\u672c: %s\n\u65b0\u7248\u672c: %s\n\n\u662f\u5426\u7acb\u5373\u5b89\u88c5\uff1f",
			UpdateFailed:     "\u66f4\u65b0\u672a\u80fd\u5b8c\u6210\uff0cCodexLB \u4ecd\u53ef\u7ee7\u7eed\u4f7f\u7528\u3002\n\n%s",
			UpdateBusy:       "\u53e6\u4e00\u4e2a\u66f4\u65b0\u68c0\u67e5\u6216\u5b89\u88c5\u6b63\u5728\u8fdb\u884c\u4e2d\u3002",
			CheckFailed:      "\u68c0\u67e5\u66f4\u65b0\u5931\u8d25\u3002\n\n%s",
			RuntimeMissing:   "\u7f3a\u5c11\u5185\u7f6e Python \u8fd0\u884c\u65f6\u3002\u8bf7\u91cd\u65b0\u5b89\u88c5 CodexLB \u4ee5\u4fee\u590d\u5f53\u524d\u5b89\u88c5\u3002",
		}
	}
}

const (
	ERROR_ALREADY_EXISTS = 183
	SW_SHOWNORMAL        = 1
	registryRunKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	registryValueName    = "CodexLB"
	// Settings live under their own key. They used to be written into the Run
	// key, where Windows treats every value as a command to execute at logon —
	// so "CodexLBAutoUpdate"=1 made Windows try to run "1" on every sign-in.
	registrySettingsKey = `Software\CodexLB`
	registryAutoUpdate  = "AutoUpdate"
	registryChannel     = "Channel"
	// Legacy Run-key value names, read once and then removed.
	legacyRegistryAutoUpdate  = "CodexLBAutoUpdate"
	legacyRegistryEdgeChannel = "CodexLBEdgeChannel"
)

// openSettingsKey opens (creating if absent) the dedicated settings key.
func openSettingsKey(access uint32) (registry.Key, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, registrySettingsKey, access)
	return k, err
}

// migrateLegacySettings moves settings out of the Run key, where earlier
// versions stored them. It is idempotent and safe to call on every start.
func migrateLegacySettings() {
	run, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer run.Close()

	if val, _, err := run.GetIntegerValue(legacyRegistryAutoUpdate); err == nil {
		_ = setAutoUpdate(val == 1)
		_ = run.DeleteValue(legacyRegistryAutoUpdate)
	}
	if val, _, err := run.GetIntegerValue(legacyRegistryEdgeChannel); err == nil {
		if val == 1 {
			// Only adopt edge if a newer explicit choice has not been made.
			if current, err := readChannelSetting(); err != nil || current == "" {
				_ = setChannelSetting(string(edgeChannel))
			}
		}
		_ = run.DeleteValue(legacyRegistryEdgeChannel)
	}
}

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
	k, err := openSettingsKey(registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue(registryAutoUpdate)
	return err == nil && val == 1
}

func setAutoUpdate(enabled bool) error {
	k, err := openSettingsKey(registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enabled {
		return k.SetDWordValue(registryAutoUpdate, 1)
	}
	if err := k.DeleteValue(registryAutoUpdate); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// readChannelSetting returns the raw stored channel, or "" when unset.
func readChannelSetting() (string, error) {
	k, err := openSettingsKey(registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	val, _, err := k.GetStringValue(registryChannel)
	if err != nil {
		return "", err
	}
	return val, nil
}

func setChannelSetting(channel string) error {
	k, err := openSettingsKey(registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(registryChannel, channel)
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
	_ = cleanupStaleUpdateArtifacts(os.TempDir(), time.Now(), staleUpdateArtifactAge)
	migrateLegacySettings()

	// Locate the bundled Python relative to this executable.
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	installDir := filepath.Dir(execPath)

	pythonExe, err := findBundledPython(installDir)
	if err != nil {
		showMessageBox("CodexLB", lang.RuntimeMissing, MB_OK|MB_ICONERROR)
		os.Exit(1)
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
			if !updates.tryBegin() {
				return
			}
			defer updates.finish()
			updates.setPhase(phaseChecking)
			channel := activeUpdateChannel()
			cand, err := checkForUpdates(context.Background(), channel)
			if err != nil {
				recordUpdateError("automatic check", err)
				return
			}
			if cand == nil {
				return
			}
			// A build without an embedded sha cannot tell whether an edge
			// build is new; require the user-driven check for that first hop.
			if channel == edgeChannel && buildSHA == "" {
				return
			}
			// Same-version rebuilds differ only in wrapper identity, so an
			// unattended install could silently swap the payload for one built
			// from different sources. Leave those to an explicit user check.
			if cand.sameVersionRebuild {
				return
			}
			updates.setPhase(phaseInstalling)
			if err := downloadAndRunInstaller(context.Background(), *cand, installSilent); err != nil {
				recordUpdateError("automatic install", err)
				return
			}
		}()
	}

	// Run the system tray. This call blocks until systray.Quit() is called.
	systray.Run(
		func() {
			// onReady — set up tray icon and menu.
			systray.SetIcon(iconICO)
			systray.SetTitle("CodexLB")
			systray.SetTooltip(lang.Tooltip)

			// Non-clickable status line: says at a glance whether an update is
			// running and therefore whether codex-lb is about to be interrupted.
			mStatus := systray.AddMenuItem("", "Current updater activity")
			mStatus.Disable()
			systray.AddSeparator()

			mOpen := systray.AddMenuItem(lang.OpenDashboard, "Open the web dashboard")
			mAutostart := systray.AddMenuItemCheckbox(lang.StartOnLogon, "Start CodexLB automatically on startup", isAutostartEnabled())
			mAutoUpdate := systray.AddMenuItemCheckbox(lang.AutoUpdate, "Automatically check and install updates on startup", isAutoUpdateEnabled())

			// Update channel picker — mutually exclusive, so the three entries
			// behave as radio buttons inside their own submenu.
			mChannel := systray.AddMenuItem(lang.ChannelMenu, "Choose which builds this installation follows")
			active := activeUpdateChannel()
			mChanRelease := mChannel.AddSubMenuItemCheckbox(lang.ChannelRelease, "Final releases only", active == stableChannel)
			mChanBeta := mChannel.AddSubMenuItemCheckbox(lang.ChannelBeta, "Newest release including pre-releases", active == betaChannel)
			mChanEdge := mChannel.AddSubMenuItemCheckbox(lang.ChannelEdge, "Rolling builds of upstream main", active == edgeChannel)

			channelLabel := func(ch updateChannel) string {
				switch ch {
				case betaChannel:
					return lang.ChannelBeta
				case edgeChannel:
					return lang.ChannelEdge
				default:
					return lang.ChannelRelease
				}
			}

			// Assigned once the status renderer exists below; only ever called
			// from menu clicks, which cannot happen before then.
			var refreshStatus func()

			selectChannel := func(choice updateChannel) {
				// Never let a channel change race an in-flight update.
				if updates.currentPhase() != phaseIdle {
					showMessageBox("CodexLB", lang.UpdateBusy, MB_OK|MB_ICONINFO)
					return
				}
				if err := setChannelSetting(string(choice)); err != nil {
					return
				}
				for item, ch := range map[*systray.MenuItem]updateChannel{
					mChanRelease: stableChannel,
					mChanBeta:    betaChannel,
					mChanEdge:    edgeChannel,
				} {
					if ch == choice {
						item.Check()
					} else {
						item.Uncheck()
					}
				}
				// Refresh the idle status line with the new channel name and
				// make explicit that switching did not start an update.
				refreshStatus()
				showMessageBox("CodexLB", fmt.Sprintf(lang.ChannelSwitched, channelLabel(choice)), MB_OK|MB_ICONINFO)
			}

			mUpdate := systray.AddMenuItem(lang.CheckUpdates, "Check for a new version on the active channel and install if available")
			systray.AddSeparator()
			mQuit := systray.AddMenuItem(lang.Quit, "Stop CodexLB")

			// Reflect updater activity in the status line, the tooltip, and by
			// locking the controls that must not change mid-update.
			applyPhase := func(phase updatePhase) {
				var text string
				busy := true
				switch phase {
				case phaseChecking:
					text = lang.StatusChecking
				case phaseInstalling:
					text = lang.StatusInstalling
				default:
					text = fmt.Sprintf(lang.StatusIdle, channelLabel(activeUpdateChannel()))
					busy = false
				}
				mStatus.SetTitle(text)
				systray.SetTooltip(lang.Tooltip + " — " + text)
				for _, item := range []*systray.MenuItem{mUpdate, mChanRelease, mChanBeta, mChanEdge, mAutoUpdate} {
					if busy {
						item.Disable()
					} else {
						item.Enable()
					}
				}
			}
			refreshStatus = func() { applyPhase(updates.currentPhase()) }
			updates.observe(applyPhase)

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
							if !updates.tryBegin() {
								showMessageBox("CodexLB", lang.UpdateBusy, MB_OK|MB_ICONINFO)
								return
							}
							defer updates.finish()
							updates.setPhase(phaseChecking)
							channel := activeUpdateChannel()
							cand, err := checkForUpdates(context.Background(), channel)
							if err != nil {
								recordUpdateError("manual check", err)
								showMessageBox("CodexLB", fmt.Sprintf(lang.CheckFailed, err), MB_OK|MB_ICONERROR)
								return
							}
							if cand == nil {
								showMessageBox("CodexLB", fmt.Sprintf(lang.UpToDate, currentDisplay(channel)), MB_OK|MB_ICONINFO)
								return
							}
							// Ask user to confirm installation
							msg := installConfirmMessage(cand.newVersion, channel)
							ret := showMessageBox(lang.UpdateAvailable, msg, MB_YESNO|MB_ICONQUESTION)
							if ret != IDYES {
								return
							}
							updates.setPhase(phaseInstalling)
							if err := downloadAndRunInstaller(context.Background(), *cand, installVisible); err != nil {
								recordUpdateError("manual install", err)
								showMessageBox("CodexLB", fmt.Sprintf(lang.UpdateFailed, err), MB_OK|MB_ICONERROR)
								return
							}
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
							// Turning this on does not update anything now; say
							// so, and say what will happen when it does.
							showMessageBox("CodexLB", lang.AutoUpdateOn, MB_OK|MB_ICONINFO)
						}
					case <-mChanRelease.ClickedCh:
						selectChannel(stableChannel)
					case <-mChanBeta.ClickedCh:
						selectChannel(betaChannel)
					case <-mChanEdge.ClickedCh:
						selectChannel(edgeChannel)
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
