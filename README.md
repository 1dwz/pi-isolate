# pi-isolate — π 配置隔离启动器

给 [pi](https://github.com/earendil-works/pi)（`@earendil-works/pi-coding-agent`）提供**多套彼此隔离的配置**，
并把全局的 `models.json`（模型/provider 配置）同步到每一套配置里。

- 在一个终端里运行 `pi-isolate`，会列出可选配置（如 `python` / `android` / `go`）供你挑一个启动 pi。
- 每个配置是**完全独立**的 pi 环境：独立的 `settings.json`、`sessions/`、登录态、扩展、技能、AGENTS.md。
- 切换配置 = 换一个 `PI_CODING_AGENT_DIR`（pi 官方支持的环境变量），因此**不需要改动 pi 本体**。

## 隔离原理

pi 的配置目录默认是 `~/.pi/agent`，官方提供环境变量 `PI_CODING_AGENT_DIR` 覆盖它
（见 pi 文档 `docs/environment-variables.md`）。pi 的 `settings.json`、`models.json`、`auth.json`、
`sessions/`、`extensions/`、`skills/` 等全部由该目录派生，所以只要改这个变量就完成隔离。

启动器做的事就是：选中配置 → 设好 `PI_CODING_AGENT_DIR` → 启动 pi。

## 目录布局

```
C:\ProgramData\pi\                 ← profile 根目录（PI_ISOLATE_ROOT / --root 可改）
├── python\
│   └── agent\                     ← PI_CODING_AGENT_DIR 指向这里
│       ├── models.json            ← 每次启动前从全局同步
│       ├── settings.json          ← 最小配置，profile 内可自由定制
│       └── sessions\              ← 该配置独有的会话历史
├── android\
│   └── agent\
└── go\
    └── agent\
```

`~/.pi/agent`（即 `C:\Users\Administrator\.pi\agent`）保持不动，作为**全局默认配置**与
`models.json` 的来源；菜单里的 `default` 项就是它。

## 安装 / 构建

```powershell
cd pi-isolate
go build -o pi-isolate.exe .
```

零第三方依赖（纯 Go 标准库），Go 1.21+ 即可。

## 用法

```
pi-isolate                          交互选择配置（无参数 + 终端时弹菜单）
pi-isolate <profile> [...pi 参数]    直接用指定配置启动 pi，参数原样透传
pi-isolate list                     列出所有 profile 及其同步状态
pi-isolate new <name>               新建 profile 并从全局同步配置
pi-isolate sync [--check]           把全局配置同步到所有 profile
pi-isolate env <profile>            打印该 profile 的环境变量（shell 集成用）
pi-isolate path <profile>           打印该 profile 的 agent 目录
pi-isolate info                     打印路径解析结果（排障用）
```

示例：

```powershell
pi-isolate python                     # 用 python 配置启动 pi
pi-isolate android -p "分析这个 apk"   # 透传参数给 pi
pi-isolate new rust                   # 新建 rust 配置
pi-isolate sync --check               # 检查是否有配置落后于全局
```

交互菜单按键：`Enter` 启动、`↑↓` 选择、`n` 新建、`s` 同步全局配置、`r` 刷新、`q`/`Esc` 退出。
启动 pi 前会把终端交还给 pi（退出 raw 模式），pi 退出后自动回到菜单。

## 同步范围

默认**只同步 `models.json`** —— 这就是"把模型配置同步到所有配置"的最小集。

`settings.json` / `auth.json` / `trust.json` 等**故意不同步**，它们属于各 profile 自己的可自定义状态；
反复同步会覆盖 profile 内的改动（实测：同步 `settings.json` 会把全局的 `packages` 列表带进去，
导致每个 profile 各自 `npm install` 一份扩展）。

需要追加同步项时，在 exe 同目录写 `pi-isolate.json`：

```json
{
  "root": "C:\\ProgramData\\pi",
  "globalAgentDir": "C:\\Users\\Administrator\\.pi\\agent",
  "syncFiles": ["models.json", "models-store.json"]
}
```

## 全局选项与环境变量

| 选项 | 环境变量 | 说明 |
|---|---|---|
| `--root <dir>` | `PI_ISOLATE_ROOT` | profile 根目录，默认 `%ProgramData%\pi` |
| `--global-agent-dir <dir>` | `PI_ISOLATE_GLOBAL_AGENT_DIR` | 全局 agent 目录（同步源），默认 `~/.pi/agent` |
| `--pi <cli.js>` / `--node <node.exe>` | — | 手动指定 pi 入口与 node |
| `--pi-cmd <cmd\|ps1>` | — | 直接指定 pi 启动器（测试/自定义封装用） |
| `-q, --quiet` | — | 静默同步日志 |
| — | `PI_ISOLATE_CONFIG` | 指定配置文件位置（默认 exe 同目录 `pi-isolate.json`） |
| — | `PI_ISOLATE_NO_SYNC=1` | 启动时跳过自动同步 |

> 全局选项需写在子命令 / profile 名**之前**，其后的参数一律透传给 pi。
> 所以 `pi-isolate python --version` 打印的是 **pi** 的版本，不是启动器的。

### shell 集成（可选）

想在当前会话内临时切到某个配置，而不是启动新进程：

```powershell
Invoke-Expression (pi-isolate env python)   # 之后本会话的 pi 都用 python 配置
Remove-Item Env:PI_CODING_AGENT_DIR         # 切回全局
```

## 实现要点

| 文件 | 职责 |
|---|---|
| `main.go` | CLI 解析与子命令分发 |
| `config.go` | 配置解析（JSON + 环境变量 + 选项三层优先级）与 pi 入口定位 |
| `profile.go` | profile 发现（扫描根目录）、命名校验、内置全局项 |
| `sync.go` | 同步与漂移检测、最小 settings 生成、原子写入 |
| `launch.go` | 环境注入与 pi 子进程启动 |
| `menu.go` | 交互菜单（raw 模式逐键读取 + 降级逐行菜单） |
| `console_windows.go` | 控制台模式切换（`GetConsoleMode`/`SetConsoleMode`） |

几处刻意的设计：

- **启动 pi 优先走 `node <pkg>/dist/bundle/cli.js`**，不经过 `.cmd` 包裹，参数引用最安全
  （`exec.LookPath` 从 PATH 上的 `pi.cmd` 反推包位置，并读 `package.json` 的 `bin` 字段）。
- **原子写入**：同步时先写同目录临时文件再 `os.Rename`，避免 pi 读到半个 JSON。
- **全局项会清除 `PI_CODING_AGENT_DIR`**：否则从隔离会话里选「全局默认」会继承上一个 profile 的目录，隔离失效。
- **启动前补齐骨架**：`sessions/` 目录与缺失的 `settings.json` 会按需补建，但**绝不覆盖**已存在的 `settings.json`。
- **`--check` 只读**：只报告漂移，不写盘。

## 测试

```powershell
go test ./...        # 29 个用例
```

覆盖：profile 命名校验、发现与排序、按键序列解析、启动环境注入（含全局项清除回归）、
同步范围契约（只同步 models.json）、`--check` 只读、最小 settings 契约（不含 packages）、
菜单决策（导航边界 / 意图映射 / 高亮项启动）、控制台降级（非控制台必须降级、真实控制台必须认得）。

测试有效性用**变异测试**验证过（把实现改回错误写法，套件必须变红）：

| 变异 | 被抓住的用例 |
|---|---|
| 启动环境不再注入 `PI_CODING_AGENT_DIR` | `TestBuildLaunchEnvIsolated` |
| 同步范围擅自扩大到 `settings.json` | `TestSyncOnlyModelsByDefault`、`TestNewProfileLayout` |
| `--check` 也写盘 | `TestSyncCheckDoesNotWrite` |
| 覆盖已存在的 `settings.json` | `TestMaybeSyncRepairsMissingSettings`、`TestEnsureSkeletonKeepsExistingSettings` |

## 已知限制

- 模型 provider 的凭据（如 `apiKey`）建议内联在 `models.json` 里，这样每个 profile 开箱即用。
  若使用 OAuth 登录态（`auth.json`），需在每个 profile 内单独 `/login` —— 这是刻意的：
  多个 profile 共用一份可刷新的 token 会互相踩（同时刷新导致提前失效）。
