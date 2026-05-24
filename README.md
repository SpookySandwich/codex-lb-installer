# Codex Load Balancer - Windows Installer

This repository provides an automated Windows installer and headless launcher wrapper for the excellent [Codex Load Balancer](https://github.com/Soju06/codex-lb) project by [Soju06](https://github.com/Soju06).

It packages the backend, frontend dashboard, and a portable Python runtime into a single, zero-dependency setup wizard.

<p align="center">
  <img src="installer_screenshot.png" width="450" alt="Codex Load Balancer Setup Screen">
</p>

---

## 🛠️ Wrapper Features

*   📦 **Zero Dependencies**: Bundles portable Python 3.13.5 and all dependencies. No system Python, Node, Git, or Docker setup required.
*   🤫 **Headless Executable**: Runs headlessly in the background without displaying a terminal command prompt.
*   🌐 **Auto-Launch Dashboard**: Automatically pops up the dashboard (`http://localhost:2455`) in your default web browser on startup.
*   🔒 **Single-Instance Mutex**: Restricts execution to a single background process. Launching the shortcut again simply opens the browser redirect and exits.
*   🧹 **Clean Uninstallation**: Fully registers with Windows Add/Remove programs and offers to purge database settings (`~/.codex-lb`).
*   🤖 **Automated CI Releases**: A scheduled GitHub Actions workflow checks the target repository for new releases daily, compiles the installer, and publishes the release binaries here automatically.

---

## 📥 Quick Start

1. Download the latest `CodexLB_Installer.exe` from the [Releases](https://github.com/SpookySandwich/codex-lb-installer/releases) tab.
2. Run the installer wizard and complete setup.
3. Launch the app from the Desktop or Start Menu shortcut.

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
4. Run the builder script (requires Go and Python with `uv` on PATH):
   ```bash
   python bundle_build.py
   ```
5. Grab your compiled installer in the `dist/` directory!

---

## 📜 Credits & Licensing

*   **Core Application**: All credits for the load balancer, dashboard proxy, and API logic go to the original [Soju06/codex-lb](https://github.com/Soju06/codex-lb) project.
*   **Wrapper License**: MIT License.
