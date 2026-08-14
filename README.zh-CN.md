# CodexLB

[English](README.md) | **中文**

[![Release](https://img.shields.io/github/v/release/SpookySandwich/codex-lb-installer?include_prereleases)](https://github.com/SpookySandwich/codex-lb-installer/releases)
[![Downloads](https://img.shields.io/github/downloads/SpookySandwich/codex-lb-installer/total)](https://github.com/SpookySandwich/codex-lb-installer/releases)

[Codex LB](https://github.com/Soju06/codex-lb) 的 Windows 安装包。

<p align="center">
  <img src="install.png" width="46%" alt="CodexLB 安装界面">
  <img src="tray.png" width="46%" alt="CodexLB 托盘菜单">
</p>

## 安装

安装包在 [Releases](https://github.com/SpookySandwich/codex-lb-installer/releases) 页面。下载 `CodexLB_Installer.exe`，运行即可。

装完后桌面和开始菜单会有快捷方式，不需要了可以在 Windows 设置里卸载。

## 功能

这个仓库做的是安装和更新。装完之后 CodexLB 在托盘里跑，并在浏览器里打开仪表盘。

托盘里可以打开仪表盘、开机启动、检查更新。通道有正式版、测试版和尝鲜版；打开自动更新后，下次启动会自己装上新版本。

## 构建

自己打包需要 Go、Inno Setup 6、带 `uv` 的 Python、Node 和 Pillow。先把 [codex-lb](https://github.com/Soju06/codex-lb) clone 到仓库根目录的 `codex-lb-src`，在里面把前端编好，再安装 Pillow 和 `rsrc`，最后跑 `bundle_build.py`。打好的安装包在 `dist/`。

```bash
git clone https://github.com/Soju06/codex-lb.git codex-lb-src
cd codex-lb-src/frontend && npm install && npm run build && cd ../..
pip install Pillow==12.3.0
go install github.com/akavel/rsrc@v0.10.2
python bundle_build.py
```

## 许可

MIT。负载均衡本身是 [Soju06/codex-lb](https://github.com/Soju06/codex-lb)。
