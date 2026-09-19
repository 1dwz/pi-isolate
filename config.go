package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	appName     = "pi-isolate"
	appVersion  = "1.0.0"
	envAgentDir = "PI_CODING_AGENT_DIR" // pi 官方：覆盖 agent 配置目录（默认 ~/.pi/agent）
)

// Config 是启动器的全部路径配置。
// 优先级：内置默认 < JSON 配置文件 < 环境变量 / 命令行选项。
type Config struct {
	Root           string   `json:"root,omitempty"`           // profile 根目录（默认 %ProgramData%\pi）
	GlobalAgentDir string   `json:"globalAgentDir,omitempty"` // 全局 agent 目录（models.json 的来源，默认 ~/.pi/agent）
	PiCli          string   `json:"piCli,omitempty"`          // pi CLI 入口（dist/bundle/cli.js）
	Node           string   `json:"node,omitempty"`           // node 可执行文件
	PiCmd          string   `json:"piCmd,omitempty"`          // 兜底：pi.cmd / pi.ps1
	SyncFiles      []string `json:"syncFiles,omitempty"`      // 额外同步项（默认仅 models.json）

	configPath string
}

// expandPath 展开 ~ 前缀并清理路径。
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			rest := strings.TrimLeft(p[1:], `/\`)
			if rest == "" {
				p = home
			} else {
				p = filepath.Join(home, rest)
			}
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func defaultRoot() string {
	if v := os.Getenv("PI_ISOLATE_ROOT"); v != "" {
		return expandPath(v)
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "pi")
	}
	if pd := os.Getenv("ALLUSERSPROFILE"); pd != "" {
		return filepath.Join(pd, "pi")
	}
	return `C:\ProgramData\pi`
}

// defaultGlobalAgentDir 返回「全局」agent 目录，即各类 profile 的 models.json 来源。
func defaultGlobalAgentDir() string {
	if v := os.Getenv("PI_ISOLATE_GLOBAL_AGENT_DIR"); v != "" {
		return expandPath(v)
	}
	// 若环境里已经设了 pi 官方的目录变量，那它就是全局目录（否则会自我覆盖）。
	if v := os.Getenv(envAgentDir); v != "" {
		return expandPath(v)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return filepath.Join(home, ".pi", "agent")
}

func loadConfig() (*Config, error) {
	cfg := &Config{}

	// 1) 配置文件：PI_ISOLATE_CONFIG 显式指定，否则取 exe 同目录的 pi-isolate.json
	path := os.Getenv("PI_ISOLATE_CONFIG")
	if path == "" {
		if exe, err := os.Executable(); err == nil {
			cand := filepath.Join(filepath.Dir(exe), appName+".json")
			if fileExists(cand) {
				path = cand
			}
		}
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件失败 %s: %w", path, err)
		}
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件失败 %s: %w", path, err)
		}
		cfg.configPath = path
	}

	// 2) 默认值 / 环境变量
	if cfg.Root == "" {
		cfg.Root = defaultRoot()
	}
	cfg.Root = expandPath(cfg.Root)
	if cfg.GlobalAgentDir == "" {
		cfg.GlobalAgentDir = defaultGlobalAgentDir()
	}
	cfg.GlobalAgentDir = expandPath(cfg.GlobalAgentDir)

	if err := cfg.resolvePi(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolvePi 定位 pi 的 node 入口，避免走 cmd.exe 包裹（参数引用更安全）。
func (c *Config) resolvePi() error {
	if c.PiCli != "" {
		c.PiCli = expandPath(c.PiCli)
	}
	if c.Node != "" {
		c.Node = expandPath(c.Node)
	}
	if c.PiCmd != "" {
		c.PiCmd = expandPath(c.PiCmd)
	}

	if c.PiCli != "" && c.Node != "" {
		return nil
	}
	if c.PiCli != "" && c.Node == "" {
		if n := lookPath("node.exe", "node"); n != "" {
			c.Node = n
			return nil
		}
	}

	for _, name := range []string{"pi.cmd", "pi.exe", "pi.ps1", "pi"} {
		launcher := lookPath(name)
		if launcher == "" {
			continue
		}
		dir := filepath.Dir(launcher)
		pkgDir := filepath.Join(dir, "node_modules", "@earendil-works", "pi-coding-agent")
		binRel := filepath.Join("dist", "bundle", "cli.js")
		if b, err := os.ReadFile(filepath.Join(pkgDir, "package.json")); err == nil {
			var pkg struct {
				Bin map[string]string `json:"bin"`
			}
			if json.Unmarshal(b, &pkg) == nil && pkg.Bin["pi"] != "" {
				binRel = filepath.FromSlash(pkg.Bin["pi"])
			}
		}
		cli := filepath.Join(pkgDir, binRel)
		node := filepath.Join(dir, "node.exe")
		if !fileExists(node) {
			node = lookPath("node.exe", "node")
		}
		if fileExists(cli) && node != "" && fileExists(node) {
			c.PiCli, c.Node, c.PiCmd = cli, node, launcher
			return nil
		}
		if c.PiCmd == "" {
			c.PiCmd = launcher
		}
	}
	if c.PiCmd == "" {
		return errors.New("未找到 pi：请把 pi 加入 PATH，或在 " + appName + ".json 里配置 piCli + node / piCmd")
	}
	return nil
}

// lookPath 依次尝试多个可执行名，返回第一个存在的完整路径。
func lookPath(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil && p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			return p
		}
	}
	return ""
}
