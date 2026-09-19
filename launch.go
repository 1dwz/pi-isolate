package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
)

// ExitCodeError 携带子进程退出码，供 main 原样透出。
type ExitCodeError struct{ Code int }

func (e *ExitCodeError) Error() string { return fmt.Sprintf("pi exited with code %d", e.Code) }

// buildLaunchEnv 构造 pi 子进程的环境变量。
// 这是配置隔离的契约点，单独抽成纯函数以便测试：
//   - 隔离 profile：显式设置 PI_CODING_AGENT_DIR 指向该 profile 的 agent 目录；
//   - 全局默认项：删除该变量，让 pi 回落到 ~/.pi/agent（否则从隔离会话里
//     再选「全局默认」会继承上一个 profile 的目录，隔离失效）。
func buildLaunchEnv(p Profile) []string {
	env := os.Environ()
	if p.Global {
		return unsetEnv(env, envAgentDir)
	}
	return setEnv(env, envAgentDir, p.AgentDir)
}

// launch 以指定 profile 启动 pi。
// 子进程直接继承当前终端，所以 pi 的 TUI 正常工作；
// 关键点是把 PI_CODING_AGENT_DIR 指到 profile 的 agent 目录，实现配置隔离。
func launch(cfg *Config, p Profile, piArgs []string) error {
	cmd, err := piCommand(cfg, piArgs)
	if err != nil {
		return err
	}
	cmd.Env = buildLaunchEnv(p)
	cmd.Dir = mustGetwd()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 pi 失败: %w", err)
	}

	// 把 Ctrl+C 转给子进程后继续等待（不自行退出，退出码以 pi 为准）。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			_ = cmd.Process.Signal(os.Interrupt)
		case <-done:
		}
	}()

	err = cmd.Wait()
	close(done)
	signal.Stop(sigCh)

	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitCodeError{Code: ee.ExitCode()}
	}
	return err
}

// piCommand 构造 pi 的调用：
//
//	优先 node <pkg>/dist/bundle/cli.js <args> —— 不经 cmd.exe/ps1 包裹，参数引用最安全；
//	退化到 pi.cmd / pi.ps1（用户显式配置时）。
func piCommand(cfg *Config, piArgs []string) (*exec.Cmd, error) {
	if cfg.PiCli != "" && cfg.Node != "" && fileExists(cfg.PiCli) && fileExists(cfg.Node) {
		return exec.Command(cfg.Node, append([]string{cfg.PiCli}, piArgs...)...), nil
	}
	if cfg.PiCmd != "" && fileExists(cfg.PiCmd) {
		if strings.EqualFold(filepath.Ext(cfg.PiCmd), ".ps1") {
			exe := lookPath("pwsh.exe", "powershell.exe")
			if exe == "" {
				return nil, fmt.Errorf("需要 pwsh/powershell 来运行 %s", cfg.PiCmd)
			}
			args := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", cfg.PiCmd}, piArgs...)
			return exec.Command(exe, args...), nil
		}
		return exec.Command(cfg.PiCmd, piArgs...), nil
	}
	if cfg.PiCli != "" && fileExists(cfg.PiCli) {
		node := lookPath("node.exe", "node")
		if node == "" {
			return nil, fmt.Errorf("找到 pi 入口 %s 但缺少 node 可执行文件", cfg.PiCli)
		}
		return exec.Command(node, append([]string{cfg.PiCli}, piArgs...)...), nil
	}
	return nil, fmt.Errorf("无法定位 pi 可执行入口，请检查 %s.json", appName)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func setEnv(env []string, key, val string) []string {
	prefix := strings.ToUpper(key) + "="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), prefix) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+val)
}

func unsetEnv(env []string, key string) []string {
	prefix := strings.ToUpper(key) + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// isInteractive 判断当前是否适合弹交互菜单。
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func isWindows() bool { return runtime.GOOS == "windows" }
