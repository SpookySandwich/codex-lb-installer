# CodexLB

Windows installer for [Codex LB](https://github.com/Soju06/codex-lb).

[中文](README.zh-CN.md)

## Install

Download the latest `CodexLB_Installer.exe` from [Releases](https://github.com/SpookySandwich/codex-lb-installer/releases) and run it.

<p align="center">
  <img src="install.png" width="520" alt="CodexLB installer">
</p>

- No Python, Node, Git, or Docker
- Desktop and Start Menu shortcuts
- Uninstall from Windows Settings

## Features

After install, CodexLB sits in the system tray and opens the dashboard in your browser.

<p align="center">
  <img src="tray.png" width="420" alt="CodexLB tray menu">
</p>

- Pool multiple ChatGPT accounts
- Usage, remaining quota, and cost on one dashboard
- API keys with per-key limits
- Works with Codex CLI, OpenCode, and other OpenAI-compatible clients
- Tray menu: open dashboard, start on logon, auto-update
- Update channels: Release, Beta, Edge

## Build

Needs Go, Inno Setup 6, Python with `uv` on PATH, Node, and Pillow.

```bash
git clone https://github.com/Soju06/codex-lb.git codex-lb-src
cd codex-lb-src/frontend && npm install && npm run build && cd ../..
pip install Pillow==12.3.0
go install github.com/akavel/rsrc@v0.10.2
python bundle_build.py
```

Installer is in `dist/`.

## License

MIT. The load balancer itself is [Soju06/codex-lb](https://github.com/Soju06/codex-lb).
