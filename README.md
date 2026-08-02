# CodexLB - Windows Installer

<p align="right">
  <a href="README.zh-CN.md">🇨🇳 中文</a>
</p>

This repository provides an automated Windows installer and headless launcher wrapper for the excellent [Codex Load Balancer](https://github.com/Soju06/codex-lb) project by [Soju06](https://github.com/Soju06).

It packages the backend, frontend dashboard, and a portable Python runtime into a single, zero-dependency setup wizard.

<p align="center">
  <img src="installer_screenshot.png" width="450" alt="CodexLB Setup Screen">
</p>

---

## 🛠️ Wrapper Features

*   📦 **Zero Dependencies**: Bundles portable Python 3.13.5 and all dependencies. No system Python, Node, Git, or Docker setup required.
*   🤫 **Headless Executable**: Runs headlessly in the background without displaying a terminal command prompt.
*   🖥️ **System Tray Icon**: Runs in the system tray with Open Dashboard, Auto Update, and Quit options.
*   🌐 **Auto-Launch Dashboard**: Automatically pops up the dashboard (`http://localhost:2455`) in your default web browser on startup.
*   🔒 **Single-Instance Mutex**: Restricts execution to a single background process. Launching the shortcut again simply opens the browser redirect and exits.
*   🔄 **Verified Updates**: Follows stable releases by default, with an opt-in rolling edge channel. Every installer is size-bounded and SHA-256 verified against GitHub's release metadata before it can run.
*   🧹 **Clean Uninstallation**: Fully registers with Windows Add/Remove programs with a checkbox option to purge database settings (`~/.codex-lb`).
*   🌏 **Multi-Language**: Tray menu auto-detects Windows system language and uses Chinese (Simplified) when applicable.
*   🤖 **Automated CI Releases**: A scheduled GitHub Actions workflow checks the target repository every six hours, tests the launcher, compiles the installer, and publishes release binaries here automatically.

---

## 📥 Quick Start

1. Download the latest `CodexLB_Installer.exe` from the [Releases](https://github.com/SpookySandwich/codex-lb-installer/releases) tab.
2. Run the installer wizard and complete setup.
3. Launch the app from the Desktop or Start Menu shortcut.

---

## 🔄 Auto Update

CodexLB includes a built-in auto-update mechanism via the system tray:

- **Manual**: Right-click the tray icon → **Check for Updates** to check the active channel and choose whether to install.
- **Automatic**: Enable **Auto Update** to check on startup and install a newer build silently.
- **Channels**: Stable is the default and skips prereleases. **Edge Channel** follows the tested rolling build of upstream `main`; switching back explicitly returns to the newest stable build.

Update work is single-flight, so automatic and repeated manual checks cannot start competing installers. The updater accepts only the canonical release asset over HTTPS, enforces the published byte size, verifies GitHub's SHA-256 digest while streaming to a temporary `.part` file, and promotes it only after validation. Python/application payloads are content-addressed and installed side by side, with the previous payload retained instead of overlaying a live environment. CodexLB continues serving while the download and installer wizard run; Setup stops it only when installation is ready to commit, and a failed commit attempts to restore the prior launcher and payload.

---

## ⚙️ Local Build Instructions

If you want to compile the installer package locally:

1. Clone this repository:
   ```bash
   git clone https://github.com/SpookySandwich/codex-lb-installer.git
   cd codex-lb-installer
   ```
2. Clone the target repository:
   ```bash
   git clone https://github.com/Soju06/codex-lb.git codex-lb-src
   ```
3. Build the React frontend assets in `codex-lb-src/frontend`:
   ```bash
   cd codex-lb-src/frontend
   npm install && npm run build
   cd ../..
   ```
4. Run the builder script (requires Go, Inno Setup 6, Python with `uv` on PATH, and Pillow):
   ```bash
   pip install Pillow==12.3.0
   go install github.com/akavel/rsrc@v0.10.2
   python bundle_build.py
   ```
5. Grab your compiled installer in the `dist/` directory!

---

## 📜 Credits & Licensing

*   **Core Application**: All credits for the load balancer, dashboard proxy, and API logic go to the original [Soju06/codex-lb](https://github.com/Soju06/codex-lb) project.
*   **Wrapper License**: MIT License.
