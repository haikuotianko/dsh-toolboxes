# 贡献指南

## 开发环境

- Go ≥ 1.25（`go.mod` 里就地声明，`go` 会自动切换工具链）
- Node ≥ 20，只用于管理页的语法检查与冒烟测试

```bash
go build -o build/bin/toolbox.exe .
go vet ./... && go test ./...
node --check dsh-toolbox/client.js
node _scripts/ui-smoke.mjs
```

## 代码结构

```
main.go                命令行入口（-mcp / -http / -ui / -setup）
setup.go               生成 DSH 的 cordis.patch.yml
ui.go                  内嵌管理页（-ui）
internal/catalog       工具目录：内置 + 用户 catalog.json
internal/install       下载/解包/校验/运行
internal/store         安装清单的磁盘布局
internal/runlog        运行记录：跨进程的运行状态与实时日志
internal/mcpserver     MCP 工具与服务方法
internal/httpapi       JSON API
dsh-toolbox/           DSH 插件（页面 + 宿主端代理）
web/index.html         不依赖宿主的内嵌管理页
```

## 提 PR 之前

- `go vet ./...`、`go test ./...` 必须通过；改了 `dsh-toolbox/client.js` 还要跑 `node _scripts/ui-smoke.mjs`。
- 新增目录条目请带上官方下载地址；能给 `sha256` 更好。
- 不要提交构建产物（`build/`、`dist/`、`*.exe`）或任何本机绝对路径（`C:\Users\...`、`/Users/...`）。
- 面向模型的工具描述保持在 `internal/mcpserver/server.go` 里，一句话说清「什么时候该用它」。

## 加一个工具

1. 编辑 `internal/catalog/catalog.json`，按 README 的格式加一条。
2. `go build` 后运行 `toolbox -list`（或直接打开管理页）确认条目可见、下载源能被测速。
3. 只改目录、不改代码时，也可以让用户在 `<root>/catalog.json` 里自己加，不必发版。
