package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usage = `π 配置隔离启动器 (pi-isolate)  —— 为 pi 提供多套彼此隔离的配置

用法:
  pi-isolate                           交互选择配置（无参数 + TTY 时）
  pi-isolate <profile> [...pi 参数]     直接用指定配置启动 pi，其余参数原样透传
  pi-isolate list                      列出所有 profile 及其同步状态
  pi-isolate new <name>                新建 profile 并从全局同步配置
  pi-isolate sync [--check]            把全局配置同步到所有 profile
  pi-isolate env <profile>             打印该 profile 的环境变量（shell 集成用）
  pi-isolate path <profile>            打印该 profile 的 agent 目录
  pi-isolate info                      打印路径解析结果（排障用）

全局选项（需写在子命令/profile 名之前）:
  --root <dir>              profile 根目录（默认 %ProgramData%\pi）
  --global-agent-dir <dir>  全局 agent 目录（models.json 来源，默认 ~/.pi/agent）
  --pi <cli.js>             pi 入口文件（默认从 PATH 上的 pi 自动推导）
  --pi-cmd <cmd|ps1>        直接指定 pi 启动器（pi.cmd / pi.ps1 / 自定义封装）
  --node <node.exe>         node 可执行文件
  -q, --quiet               静默（不输出同步日志）

同步范围（默认只同步 models.json）:
  每个 profile 的 agent 目录默认只从全局同步 models.json——"把模型配置同步到所有配置"。
  settings.json / auth.json / trust.json 等属于各 profile 自己的可自定义状态，故意不同步
  （反复同步会覆盖 profile 内改动；实测同步 settings.json 会把全局 packages 列表带进去，
   导致每个 profile 各自 npm 安装一份扩展）。
  需要追加同步项时在配置文件里写 "syncFiles"：
  {
    "root": "C:\\ProgramData\\pi",
    "globalAgentDir": "C:\\Users\\Administrator\\.pi\\agent",
    "syncFiles": ["models.json", "models-store.json"]
  }

环境变量:
  PI_ISOLATE_ROOT              同 --root
  PI_ISOLATE_GLOBAL_AGENT_DIR  同 --global-agent-dir
  PI_ISOLATE_CONFIG            指定配置文件（默认 exe 同目录的 pi-isolate.json）
  PI_ISOLATE_NO_SYNC=1         启动时跳过自动同步

配置文件 pi-isolate.json（可选，与 exe 同目录）:
  {
    "root": "C:\\ProgramData\\pi",
    "globalAgentDir": "C:\\Users\\Administrator\\.pi\\agent",
    "piCli": "...\\node_modules\\@earendil-works\\pi-coding-agent\\dist\\bundle\\cli.js",
    "node": "...\\node.exe"
  }

配置文件 pi-isolate.json（可选，与 exe 同目录）:
  {
    "root": "C:\\ProgramData\\pi",
    "globalAgentDir": "C:\\Users\\Administrator\\.pi\\agent",
    "piCli": "...\\node_modules\\@earendil-works\\pi-coding-agent\\dist\\bundle\\cli.js",
    "node": "...\\node.exe"
  }

示例:
  pi-isolate python                    以 python 配置启动 pi
  pi-isolate python -p "分析这个文件"    透传参数给 pi
  pi-isolate new android               新建 android 配置
`

// globalFlags 收集全局选项，同时把位置参数与 pi 透传参数分开。
type globalFlags struct {
	rootOverride   string
	globalOverride string
	piOverride     string
	nodeOverride   string
	piCmdOverride  string
	quiet          bool
	help           bool
	version        bool
	positional     []string
	piArgs         []string
	hasPiArgsSep   bool
}

func parseGlobalFlags(args []string) (*globalFlags, error) {
	gf := &globalFlags{}
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--":
			// 其后的全部参数透传给 pi
			gf.piArgs = append(gf.piArgs, args[i+1:]...)
			gf.hasPiArgsSep = true
			return gf, nil
		case "--root":
			if i+1 >= len(args) {
				return nil, errors.New("--root 需要一个参数")
			}
			gf.rootOverride = args[i+1]
			i += 2
		case "--global-agent-dir":
			if i+1 >= len(args) {
				return nil, errors.New("--global-agent-dir 需要一个参数")
			}
			gf.globalOverride = args[i+1]
			i += 2
		case "--pi":
			if i+1 >= len(args) {
				return nil, errors.New("--pi 需要一个参数")
			}
			gf.piOverride = args[i+1]
			i += 2
		case "--pi-cmd":
			if i+1 >= len(args) {
				return nil, errors.New("--pi-cmd 需要一个参数")
			}
			gf.piCmdOverride = args[i+1]
			i += 2
		case "--node":
			if i+1 >= len(args) {
				return nil, errors.New("--node 需要一个参数")
			}
			gf.nodeOverride = args[i+1]
			i += 2
		case "-q", "--quiet":
			gf.quiet = true
			i++
		case "-h", "--help", "help":
			// 仅当出现在首个位置参数之前才归启动器；否则原样透传给 pi
			gf.help = true
			return gf, nil
		case "-V", "--version":
			gf.version = true
			return gf, nil
		default:
			// 首个非选项参数之后的内容一律交给调用方（子命令或 pi 透传）
			gf.positional = append(gf.positional, args[i:]...)
			return gf, nil
		}
	}
	return gf, nil
}

func main() {
	if err := run(); err != nil {
		var ec *ExitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.Code)
		}
		fmt.Fprintln(os.Stderr, red("错误: ")+err.Error())
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]

	gf, err := parseGlobalFlags(args)
	if err != nil {
		return err
	}
	// --help / --version 只认首个参数位置上的（见 parseGlobalFlags），
	// 这样 `pi-isolate python --version` 会把 --version 透传给 pi。
	if gf.help {
		fmt.Print(usage)
		return nil
	}
	if gf.version {
		fmt.Printf("%s %s\n", appName, appVersion)
		return nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if gf.rootOverride != "" {
		cfg.Root = expandPath(gf.rootOverride)
	}
	if gf.globalOverride != "" {
		cfg.GlobalAgentDir = expandPath(gf.globalOverride)
	}
	if gf.piOverride != "" {
		cfg.PiCli = expandPath(gf.piOverride)
	}
	if gf.nodeOverride != "" {
		cfg.Node = expandPath(gf.nodeOverride)
	}
	if gf.piCmdOverride != "" {
		cfg.PiCmd = expandPath(gf.piCmdOverride)
		if !strings.EqualFold(filepath.Ext(cfg.PiCmd), ".ps1") {
			// 非 .ps1 的启动器优先于 node 直启（便于测试与自定义封装）
			cfg.PiCli = ""
		}
	}
	if cfg.PiCli == "" && cfg.PiCmd == "" {
		return errors.New("未找到 pi：请把 pi 加入 PATH，或用 --pi/--node 指定入口")
	}

	log := newLogger(gf.quiet, false)

	if len(gf.positional) == 0 {
		if gf.hasPiArgsSep {
			// `pi-isolate -- <pi 参数>`：用全局默认配置启动
			return launch(cfg, globalProfile(cfg), gf.piArgs)
		}
		return cmdInteractive(cfg, gf.quiet)
	}

	cmd := strings.ToLower(gf.positional[0])
	rest := gf.positional[1:]

	switch cmd {
	case "list", "ls":
		return cmdList(cfg)

	case "new", "create":
		if len(rest) == 0 {
			return errors.New("用法: pi-isolate new <name>")
		}
		p, err := newProfile(cfg, rest[0], log)
		if err != nil {
			return err
		}
		fmt.Printf("%s 已创建：%s\n", green("✓"), p.AgentDir)
		fmt.Printf("  启动：%s\n", bold(appName+" "+p.Name))
		return nil

	case "sync":
		check := false
		for _, a := range rest {
			if a == "--check" || a == "--dry-run" {
				check = true
			}
		}
		results, err := syncAll(cfg, check, log)
		if err != nil {
			return err
		}
		bad := checkDrift(results)
		if check {
			if len(bad) > 0 {
				return fmt.Errorf("%d 个文件与全局不一致：%s", len(bad), summarizeDrift(bad))
			}
			fmt.Println(green("✓") + " 所有 profile 与全局一致")
		} else if len(bad) > 0 {
			return fmt.Errorf("%d 个文件同步失败：%s", len(bad), summarizeDrift(bad))
		}
		return nil

	case "env":
		if len(rest) == 0 {
			return errors.New("用法: pi-isolate env <profile>")
		}
		p, err := findProfile(cfg, rest[0])
		if err != nil {
			return err
		}
		if p.Global {
			fmt.Printf("# 全局默认：不设置 %s，pi 将回落到 %s\n", envAgentDir, p.AgentDir)
			fmt.Printf("Remove-Item Env:%s -ErrorAction SilentlyContinue\n", envAgentDir)
			return nil
		}
		fmt.Printf("$env:%s = '%s'\n", envAgentDir, p.AgentDir)
		return nil

	case "path", "dir":
		if len(rest) == 0 {
			return errors.New("用法: pi-isolate path <profile>")
		}
		p, err := findProfile(cfg, rest[0])
		if err != nil {
			return err
		}
		fmt.Println(p.AgentDir)
		return nil

	case "info":
		return cmdInfo(cfg)

	case "version":
		fmt.Printf("%s %s\n", appName, appVersion)
		return nil
	}

	// 其余情况：当作 profile 名，其后全部参数透传给 pi
	if p, err := findProfile(cfg, gf.positional[0]); err == nil {
		piArgs := append([]string{}, rest...)
		if gf.hasPiArgsSep {
			piArgs = append(piArgs, gf.piArgs...)
		}
		maybeSync(cfg, p, gf.quiet)
		return launch(cfg, p, piArgs)
	}

	if strings.HasPrefix(gf.positional[0], "-") {
		// 没写 profile 但带了 pi 自己的选项（如 pi-isolate -c）：用全局默认配置启动
		return launch(cfg, globalProfile(cfg), append([]string{}, gf.positional...))
	}

	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("未知命令或 profile：%q", gf.positional[0])
}

// maybeSync 启动前保证该 profile 的 models.json 与全局一致，
// 并补齐 agent 骨架（sessions 目录、缺失的最小 settings.json）。
func maybeSync(cfg *Config, p Profile, quiet bool) {
	log := newLogger(quiet, false)
	if p.Global {
		return
	}
	// 骨架补齐不依赖同步开关：否则 models.json 没同步、sessions 目录也没建，pi 一样跑不起来。
	if err := ensureAgentSkeleton(cfg, p, log); err != nil {
		log("补齐 [%s] 骨架失败：%v", p.Name, err)
	}
	if os.Getenv("PI_ISOLATE_NO_SYNC") == "1" {
		return
	}
	for _, r := range syncToProfile(cfg, p, false) {
		if r.Action == "written" {
			log("已同步全局配置到 [%s]：%s", p.Name, filepath.Base(r.To))
		}
		if r.Action == "missing" && r.Err != nil {
			log("同步 [%s] %s 失败：%v", p.Name, filepath.Base(r.To), r.Err)
		}
	}
}

// cmdInteractive 无参数启动：扫描 profile 并弹菜单。
func cmdInteractive(cfg *Config, quiet bool) error {
	profiles, err := listProfiles(cfg)
	if err != nil {
		return err
	}
	log := newLogger(quiet, false)

	// 无参数启动时先对齐一次全局配置，避免带着陈旧 models.json 进 pi。
	if os.Getenv("PI_ISOLATE_NO_SYNC") != "1" {
		if _, err := syncAll(cfg, false, log); err != nil {
			log("跳过自动同步：%v", err)
		}
	}
	profiles, _ = listProfiles(cfg)

	if !isInteractive() {
		name := "<profile>"
		if len(profiles) > 1 {
			name = profiles[len(profiles)-1].Name
		}
		fmt.Fprintf(os.Stderr, "非交互环境：请显式指定 profile，例如 %s %s\n", appName, name)
		return cmdList(cfg)
	}
	return runMenu(cfg, profiles, driftSummary(cfg))
}

func cmdList(cfg *Config) error {
	profiles, err := listProfiles(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("profile 根目录 : %s\n", cfg.Root)
	fmt.Printf("全局 agent 目录: %s  (models.json 来源)\n\n", cfg.GlobalAgentDir)
	fmt.Printf("%-4s %-16s %-10s %s\n", "", "NAME", "状态", "AGENT DIR")
	for i, p := range profiles {
		state := ""
		if p.Global {
			state = "全局默认"
		} else if len(checkDrift(syncToProfile(cfg, p, true))) == 0 {
			state = green("已同步")
		} else {
			state = yellow("有漂移")
		}
		fmt.Printf("%-4d %-16s %-10s %s\n", i+1, p.Name, state, dim(p.AgentDir))
	}
	return nil
}

func cmdInfo(cfg *Config) error {
	fmt.Printf("%s %s\n\n", appName, appVersion)
	fmt.Printf("配置文件       : %s\n", orNone(cfg.configPath))
	fmt.Printf("profile 根目录 : %s\n", cfg.Root)
	fmt.Printf("全局 agent 目录: %s\n", cfg.GlobalAgentDir)
	models := filepath.Join(cfg.GlobalAgentDir, "models.json")
	fmt.Printf("  models.json  : %s  %s\n", models, existsMark(models))
	fmt.Printf("pi 入口        : %s\n", orNone(cfg.PiCli))
	fmt.Printf("node           : %s\n", orNone(cfg.Node))
	fmt.Printf("pi 启动器      : %s\n", orNone(cfg.PiCmd))
	profiles, _ := listProfiles(cfg)
	fmt.Printf("profile 数量   : %d（含全局默认项）\n", len(profiles))
	return nil
}

func summarizeDrift(results []SyncResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("[%s]%s(%s)", r.Profile, filepath.Base(r.To), r.Action))
	}
	return strings.Join(parts, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "(未设置)"
	}
	return s
}

func existsMark(p string) string {
	if fileExists(p) {
		return green("存在")
	}
	return red("缺失")
}
