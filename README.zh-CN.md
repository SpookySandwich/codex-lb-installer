# CodexLB - Windows 安装程序

<p align="right">
  <a href="README.md">🇺🇸 English</a>
</p>

本仓库为优秀的 [Codex Load Balancer](https://github.com/Soju06/codex-lb) 项目（作者 [Soju06](https://github.com/Soju06)）提供自动化 Windows 安装程序和无界面启动器封装。

它将后端、前端仪表盘和便携式 Python 运行时打包成一个无需额外依赖的安装向导。

<p align="center">
  <img src="installer_screenshot.png" width="450" alt="CodexLB 安装界面">
</p>

---

## 🛠️ 封装功能

*   📦 **零依赖**：内置便携式 Python 3.13.5 及所有依赖项。无需安装系统 Python、Node、Git 或 Docker。
*   🤫 **无界面运行**：后台静默运行，不显示终端命令窗口。
*   🖥️ **系统托盘图标**：在系统托盘中运行，提供打开仪表盘、自动更新和退出选项。
*   🌐 **自动启动仪表盘**：启动时自动在默认浏览器中打开仪表盘（`http://localhost:2455`）。
*   🔒 **单实例互斥锁**：限制为单个后台进程运行。再次启动快捷方式仅打开浏览器并退出。
*   🔄 **自动更新**：从 GitHub 检查新的稳定版本并静默安装。仅安装稳定版本 — 跳过预发布版本。
*   🧹 **干净卸载**：完全注册到 Windows 添加/删除程序，卸载时提供复选框选项以清除数据库设置（`~/.codex-lb`）。
*   🌏 **多语言支持**：系统托盘菜单自动检测 Windows 系统语言，简体中文系统自动使用中文界面。
*   🤖 **自动化 CI 发布**：GitHub Actions 定时工作流每天检查目标仓库的新版本，自动编译安装程序并在此发布。

---

## 📥 快速开始

1. 从 [Releases](https://github.com/SpookySandwich/codex-lb-installer/releases) 页面下载最新的 `CodexLB_Installer.exe`。
2. 运行安装向导并完成设置。
3. 从桌面或开始菜单快捷方式启动应用。

---

## 🔄 自动更新

CodexLB 通过系统托盘内置了自动更新机制：

- **手动更新**：右键点击托盘图标 → **检查更新**，检查并安装最新的稳定版本。
- **自动更新**：在托盘菜单中启用 **自动更新**，每次启动时自动检查更新。如果发现新的稳定版本，将在后台静默下载和安装。

仅安装**稳定版本** — 自动跳过预发布和 Beta 版本。

---

## ⚙️ 本地构建说明

如果您想在本地编译安装程序：

1. 克隆本仓库：
   ```bash
   git clone https://github.com/SpookySandwich/codex-lb-installer.git
   cd codex-lb-installer
   ```
2. 克隆目标仓库：
   ```bash
   git clone https://github.com/Soju06/codex-lb.git codex-lb-src
   ```
3. 在 `codex-lb-src/frontend` 中构建 React 前端资源：
   ```bash
   cd codex-lb-src/frontend
   npm install && npm run build
   cd ../..
   ```
4. 运行构建脚本（需要 Go、Python 且 PATH 中有 `uv`，以及 Pillow）：
   ```bash
   pip install Pillow
   python bundle_build.py
   ```
5. 在 `dist/` 目录中获取编译好的安装程序！

---

## 📜 致谢与许可

*   **核心应用**：负载均衡器、仪表盘代理和 API 逻辑的所有功劳归于原项目 [Soju06/codex-lb](https://github.com/Soju06/codex-lb)。
*   **封装许可**：MIT License。
