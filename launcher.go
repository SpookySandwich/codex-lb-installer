package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

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
)

func createNamedMutex(name string) (uintptr, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	// CreateMutexW(lpMutexAttributes, bInitialOwner, lpName)
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

func main() {
	// Mutex name for single instance detection
	mutexName := "Local\\CodexLBLauncherMutex"
	mutex, err := createNamedMutex(mutexName)

	// Determine the backend port (default 2455)
	port := os.Getenv("PORT")
	if port == "" {
		port = "2455"
	}
	url := "http://127.0.0.1:" + port

	if err != nil {
		// Mutex already exists, another instance is running.
		// Open the browser and exit.
		_ = openURL(url)
		os.Exit(0)
	}
	if mutex != 0 {
		defer procCloseHandle.Call(mutex)
	}

	// Determine installer directory to locate bundled Python relative to launcher
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	installDir := filepath.Dir(execPath)

	pythonExe := filepath.Join(installDir, "python", "python.exe")
	if _, err := os.Stat(pythonExe); os.IsNotExist(err) {
		// Fallback to system python if running in a dev environment
		pythonExe = "python"
	}

	// Prepare command to run the FastAPI app via cli module
	cmd := exec.Command(pythonExe, "-m", "app.cli")
	cmd.Dir = installDir

	// Hide window to run headlessly without showing terminal
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	// Run process in background
	if err := cmd.Start(); err != nil {
		os.Exit(1)
	}

	// Ensure Python child process is killed when the launcher exits
	defer func() {
		_ = cmd.Process.Kill()
	}()

	// Wait for port to become ready
	serverReady := false
	for i := 0; i < 150; i++ { // Try for 15 seconds (150 * 100ms)
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			serverReady = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if serverReady {
		_ = openURL(url)
	}

	// Monitor child process
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Keep running until child process exits or launcher is terminated
	<-done
}
