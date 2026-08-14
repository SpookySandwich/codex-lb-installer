# CodexLB

[Codex LB](https://github.com/Soju06/codex-lb) 的 Windows 安装包。

[English](README.md)

## 安装

从 [Releases](https://github.com/SpookySandwich/codex-lb-installer/releases) 下载最新的 `CodexLB_Installer.exe`，双击运行即可。

<p align="center">
  <img src="install.png" width="520" alt="CodexLB 安装界面">
</p>

- 不用装 Python、Node、Git、Docker
- 桌面和开始菜单会生成快捷方式
- 可在 Windows 设置里卸载

## 功能

装完之后会在系统托盘里跑，浏览器打开仪表盘。

<p align="center">
  <img src="tray.png" width="420" alt="CodexLB 托盘菜单">
</p>

- 多个 ChatGPT 账户一起用
- 仪表盘看用量、剩余额度和费用
- 可以发 API Key，按额度限制
- 兼容 Codex CLI、OpenCode 和其他 OpenAI 客户端
- 托盘里可以打开仪表盘、开机启动、自动更新
- 更新通道：正式版、测试版、尝鲜版

## 构建

需要 Go、Inno Setup 6、带 `uv` 的 Python、Node，以及 Pillow。

```bash
git clone https://github.com/Soju06/codex-lb.git codex-lb-src
cd codex-lb-src/frontend && npm install && npm run build && cd ../..
pip install Pillow==12.3.0
go install github.com/akavel/rsrc@v0.10.2
python bundle_build.py
```

安装包在 `dist/`。

## 许可

MIT。负载均衡本体见 [Soju06/codex-lb](https://github.com/Soju06/codex-lb)。
