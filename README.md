# toolbox

[![ci](https://github.com/haikuotianko/toolbox/actions/workflows/ci.yml/badge.svg)](https://github.com/haikuotianko/toolbox/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/haikuotianko/toolbox)](https://github.com/haikuotianko/toolbox/releases)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

一个可移植的 **MCP 工具箱**：把 ffmpeg / ripgrep / fd / jq / yt-dlp 这类命令行工具，按当前系统下载、安装、登记成 AI 可以直接调用的工具，并有一个能看**实时控制台**的管理页。

MCP 优先的原因：同一份服务可以同时给 **DeepSeek Harness**、**opencode**、**Claude Code**、**Codex** 用，不用为每个宿主重写一遍工具层。

```
AI ──MCP──▶ toolbox ──▶ 下载/安装/运行 CLI 工具
                 │
                 └─<root>/runs/*.log ──▶ 管理页（实时控制台、进度、速度、取消）
```

## 快速开始

**方式一：下载二进制**（Windows / macOS / Linux，无需 Go）

从 [Releases](https://github.com/haikuotianko/toolbox/releases) 下载对应压缩包，解压后把 `toolbox`（Windows 是 `toolbox.exe`）放到 PATH 里。

**方式二：从源码装**（需要 Go ≥ 1.25）

```bash
go install github.com/haikuotianko/toolbox@latest
```

**方式三：DSH 插件**（带管理页，自动准备二进制）

```bash
dsh plugin --profile <profile> add dsh-toolboxes
```

> 插件在首次使用时从 GitHub Releases 下载匹配平台的二进制，校验 `SHA256SUMS` 后缓存到 `<toolbox root>/bin/`。想用自己的构建，设 `TOOLBOX_BIN` 指向它即可。
>
> npm 上的包名是 **`dsh-toolboxes`**：`dsh-toolbox` 已被另一个用途完全不同的 DSH 插件占用。磁盘上的工具箱目录（`~/.dsh/dsh-toolbox`）和 `TOOLBOX_ROOT` 默认值没有跟着改，所以老安装不会丢数据。

## 接到你的 AI 客户端

```bash
toolbox -mcp                          # stdio MCP，客户端直接拉起
toolbox -http 127.0.0.1:8770          # MCP(streamable HTTP) + JSON API + 管理页
toolbox -ui                           # 只启动管理页并打开浏览器
```

**Claude Code / Claude Desktop**（`claude_desktop_config.json`）：

```json
{ "mcpServers": { "toolbox": { "command": "toolbox", "args": ["-mcp"] } } }
```

**opencode**（`opencode.json`）：

```json
{ "mcp": { "toolbox": { "type": "local", "command": ["toolbox", "-mcp"], "enabled": true } } }
```

**DeepSeek Harness**：管理器页面顶部有「接入 AI」一节，会按本机真实路径生成可直接粘贴的配置；源码安装也可以用 `toolbox -setup -patch <profile>/node_modules/<包名>/cordis.patch.yml` 一次写好（从 npm 装时包名是 `dsh-toolboxes`，从本地目录 link 时是你自己的目录名）。

## 管理页

```bash
toolbox -ui
```

- **工具**：已安装工具、可安装清单、每个工具的多下载源与实时测速、启用/停用/卸载、导入本机已有程序。
- **命令运行**：所有正在跑的命令（**包括 AI 通过 MCP 发起的**）都在这里，带工具、参数、耗时和**实时控制台**。`\r` 形式的进度行按终端语义原地刷新，所以 yt-dlp / curl / aria2 / ffmpeg 的百分比、速度、ETA 直接可读；「取消」会连子进程一起结束。
- **脚本**：`toolbox_save_script` 归档的通用脚本，可以在页面填参数运行。

运行记录写在 `<root>/runs/<id>.json` + `<id>.log`，所以**不同进程**（AI 的 stdio 服务 / 管理页的 HTTP 服务）看到的是同一份状态。

## 内置工具

| 工具 | 作用 |
|---|---|
| `toolbox_list` | 已装工具（含一行用法）+ 当前系统可装条目及其下载源 |
| `toolbox_install` | 按目录安装；多源顺序尝试，失败自动切下一个；可传 `url` 自定义 |
| `toolbox_run` | 运行已装工具，返回 exitCode / stdout / stderr（默认 120s 超时，上限 600s） |
| `toolbox_remove` | 删除一个工具 |
| `toolbox_runtimes` | 本机真正可执行的运行时（node / python / go / rustc / powershell…）及版本 |
| `toolbox_save_script` | 把通用脚本归档进工具箱，之后可用 `toolbox_run` 调 |

目录默认 `$TOOLBOX_ROOT`，否则 `~/.toolbox`。布局：

```
<root>/
  catalog.json            可选：覆盖/新增目录条目
  tools/<id>/tool.json    安装清单（命令、版本、来源）
  tools/<id>/bin/         可执行文件
  tools/<id>/README.md    用法说明
  runs/<id>.json          每条命令的运行记录
  runs/<id>.log           该命令的实时输出（管理页控制台读它）
  .downloads/             断点续传的临时文件
```

## 自己加工具

内置目录编译进二进制（`internal/catalog/catalog.json`）。用户在 `<root>/catalog.json` 写同名 `id` 即**整条覆盖**，写新 `id` 即追加：

```json
{
  "version": 1,
  "tools": [
    {
      "id": "imagemagick",
      "title": "ImageMagick",
      "description": "图片批量转换、裁剪、加水印。",
      "usage": "toolbox_run { tool: \"imagemagick\", args: [\"magick\",\"in.png\",\"-resize\",\"50%\",\"out.png\"] }",
      "binaries": ["magick"],
      "sources": [
        { "os": "windows", "arch": "amd64", "url": "https://…/ImageMagick.zip", "archive": "zip",
          "sha256": "可选的十六进制摘要" },
        { "os": "darwin", "arch": "arm64", "url": "https://…/imagemagick.tar.gz", "archive": "tar.gz" }
      ]
    }
  ]
}
```

- `os` 用 GOOS 值（`windows` / `darwin` / `linux`），`arch` 用 GOARCH（`amd64` / `arm64`），留空表示不限。
- 同一系统内 `sources` 的**顺序就是重试顺序**；多个源时先各测 512KB 再按速度排序。
- 只有单文件（如 `jq.exe`）时标 `"single": true`，下载完直接落到 `bin/`。
- `sha256` 可选但建议给：填了就会在解包/执行**之前**校验，不匹配直接拒绝并删掉下载文件。滚动更新的 "latest" 链接不要填。
- 支持的包格式：`.zip`、`.tar.gz`、`.tgz`、裸可执行文件。`tar.xz` 明确不支持（会给出错误而不是静默失败）。

## JSON API

`-http` / `-ui` 模式下同时提供（管理页就是调它们）：

```
GET  /api/state      { root, platform, installed[], available[] }
GET  /api/runs       { runs[], running }        最近命令（含 AI 发起的，跨进程）
GET  /api/runlog?id=<id>&tail=24000             一条命令 + 控制台输出尾部
POST /api/runs/cancel { id }                    结束某条正在运行的命令
POST /api/runs/clear                            清掉已结束的记录
POST /api/install    { tool, url?, archive? }
POST /api/remove     { tool }
POST /api/run        { tool, args[], cwd?, timeout_ms? }
POST /api/upload?tool=<id>&name=<file>          请求体是文件二进制
GET  /api/scripts                               已归档脚本
```

MCP 端点在同一端口的 `/mcp`（streamable HTTP）。

## 安全模型

请先读这一节再决定装什么：

- **按设计，工具箱会下载并执行程序**，而且**不经过 DSH 的文件沙箱**——装进来的程序以你的用户身份运行，和在终端里直接跑等价。
- 下载默认**不校验**任何东西：目录条目没写 `sha256` 时，信任建立在 URL 上。企业/多用户环境建议给目录条目补 `sha256`，或自建镜像。
- 内置目录里个别源走了 `gh-proxy.com` 这类第三方 GitHub 镜像（为了国内直连）。介意的话在 `<root>/catalog.json` 里覆盖成官方地址。
- 管理页只监听回环地址，并且要求同源/本机 cookie；它没有远程访问能力，别把它暴露到公网。

安全问题的报告方式见 [SECURITY.md](SECURITY.md)。

## 开发

```bash
go build -o build/bin/toolbox.exe .     # 构建
go vet ./... && go test ./...           # 检查与测试
node --check dsh-toolbox/client.js      # 管理页语法
node _scripts/ui-smoke.mjs              # 管理页组件冒烟测试（无需浏览器）
```

跨平台构建/发版由 GitHub Actions 完成，见 [`.github/workflows/release.yml`](.github/workflows/release.yml)。发版流程：

1. `dsh-toolbox/package.json` 的 `version` 改成 `X.Y.Z`
2. `git tag vX.Y.Z && git push origin vX.Y.Z`
3. CI 构建 6 个平台、生成 `SHA256SUMS`、发 Release，并（配好 `NPM_TOKEN` 时）发布 npm 包

版本号只有一个来源：二进制用 `-ldflags "-X main.version=$TAG"` 注入，插件用 `package.json` 的版本去下载对应 Release。

## 已知限制

- `tar.xz` 不支持（Linux 的 ffmpeg 官方包是 xz，需要换 zip/tar.gz 源或自己补解压）。
- 下载走系统代理设置；企业代理需要自己配 `HTTPS_PROXY`。
- Windows 上下载的可执行文件没有签名，首次运行可能被 SmartScreen 拦一次。
- 管理页目前只有中文文案。

## 许可证

[MIT](LICENSE)
