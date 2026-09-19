package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ---------- 键读取 ----------

type keyReader struct {
	ch chan byte
}

func newKeyReader() *keyReader { return newKeyReaderFrom(os.Stdin) }

// newKeyReaderFrom 允许注入输入源，便于对按键解析做单元测试。
func newKeyReaderFrom(r io.Reader) *keyReader {
	kr := &keyReader{ch: make(chan byte, 64)}
	go func() {
		defer close(kr.ch)
		buf := make([]byte, 1)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				kr.ch <- buf[0]
			}
			if err != nil {
				return
			}
		}
	}()
	return kr
}

func (k *keyReader) get(timeout time.Duration) (byte, bool) {
	if timeout <= 0 {
		b, ok := <-k.ch
		return b, ok
	}
	select {
	case b, ok := <-k.ch:
		return b, ok
	case <-time.After(timeout):
		return 0, false
	}
}

// read 返回抽象按键名（方向键已从 ESC 序列解析出来）。
func (k *keyReader) read() string {
	b, ok := k.get(0)
	if !ok {
		return "eof"
	}
	switch b {
	case 0x1b: // ESC，或方向键/功能键的引导字节
		n1, ok1 := k.get(60 * time.Millisecond)
		if !ok1 {
			return "esc"
		}
		if n1 != '[' && n1 != 'O' {
			return "esc"
		}
		n2, ok2 := k.get(60 * time.Millisecond)
		if !ok2 {
			return "esc"
		}
		switch n2 {
		case 'A':
			return "up"
		case 'B':
			return "down"
		case 'C':
			return "right"
		case 'D':
			return "left"
		case 'H':
			return "home"
		case 'F':
			return "end"
		case '5', '6':
			k.get(60 * time.Millisecond) // 吃掉结尾的 '~'
			if n2 == '5' {
				return "pgup"
			}
			return "pgdn"
		}
		return "unknown"
	case '\r', '\n':
		return "enter"
	case 0x03:
		return "ctrl-c"
	case 0x7f, 0x08:
		return "backspace"
	case ' ':
		return "space"
	case '\t':
		return "tab"
	}
	return strings.ToLower(string(b))
}

// ---------- 菜单 ----------

// menuIntent 表示按键处理后的意图；把「决策」与「IO 副作用」分开，
// 这样交互逻辑可以在单元测试里被完整驱动。
type menuIntent int

const (
	intentNone menuIntent = iota
	intentQuit
	intentLaunch
	intentNew
	intentSync
	intentRefresh
)

type menu struct {
	cfg      *Config
	keys     *keyReader
	cursor   int
	printed  int
	status   string
	drift    string
	profiles []Profile
}

// selected 返回当前高亮的 profile。
func (m *menu) selected() Profile {
	if m.cursor < 0 || m.cursor >= len(m.profiles) {
		return Profile{}
	}
	return m.profiles[m.cursor]
}

// handleKey 是纯决策函数：根据按键更新菜单状态并返回意图。
// 除"新建"需要先向用户索要名字外，一切副作用都交给调用方执行。
func (m *menu) handleKey(k string) menuIntent {
	switch k {
	case "q", "esc", "ctrl-c", "eof":
		return intentQuit
	case "up", "left":
		if m.cursor > 0 {
			m.cursor--
		}
		m.status = ""
	case "down", "right":
		if m.cursor < len(m.profiles)-1 {
			m.cursor++
		}
		m.status = ""
	case "home", "pgup":
		m.cursor = 0
		m.status = ""
	case "end", "pgdn":
		m.cursor = len(m.profiles) - 1
		m.status = ""
	case "enter":
		return intentLaunch
	case "n":
		return intentNew
	case "s":
		return intentSync
	case "r":
		return intentRefresh
	}
	return intentNone
}

func (m *menu) clear() {
	if m.printed <= 0 {
		return
	}
	fmt.Fprintf(os.Stdout, "\x1b[%dA", m.printed)
	for i := 0; i < m.printed; i++ {
		fmt.Fprint(os.Stdout, "\r\x1b[2K\n")
	}
	fmt.Fprintf(os.Stdout, "\x1b[%dA", m.printed)
	m.printed = 0
}

// renderLines 是纯函数：由当前状态生成菜单文本，便于单元测试。
func (m *menu) renderLines() []string {
	var lines []string
	head := bold("π 配置隔离启动器") + dim("  "+appVersion+"   ")
	head += dim("根目录 " + m.cfg.Root)
	lines = append(lines, head)
	lines = append(lines, dim("全局 models.json 源："+m.cfg.GlobalAgentDir))
	lines = append(lines, dim("同步状态："+m.drift))
	lines = append(lines, "")
	for i, p := range m.profiles {
		marker := "  "
		if i == m.cursor {
			marker = cyan("› ")
		}
		line := marker + dim(fmt.Sprintf("%d)", i+1)) + " " + p.Name
		if p.Global {
			line += dim("  (全局默认)")
		}
		if i == m.cursor {
			line = bold(line)
		}
		lines = append(lines, line)
		if i == m.cursor {
			lines = append(lines, dim("       → "+p.AgentDir))
		}
	}
	lines = append(lines, "")
	lines = append(lines, dim("Enter 启动   ↑↓ 选择   n 新建   s 同步全局配置   r 刷新   q/Esc 退出"))
	if m.status != "" {
		lines = append(lines, green(m.status))
	}
	return lines
}

func (m *menu) draw() {
	lines := m.renderLines()
	if m.printed > 0 {
		fmt.Fprintf(os.Stdout, "\x1b[%dA", m.printed)
	}
	max := len(lines)
	if m.printed > max {
		max = m.printed
	}
	for i := 0; i < max; i++ {
		fmt.Fprint(os.Stdout, "\r\x1b[2K")
		if i < len(lines) {
			fmt.Fprint(os.Stdout, lines[i])
		}
		fmt.Fprint(os.Stdout, "\n")
	}
	if max > len(lines) {
		fmt.Fprintf(os.Stdout, "\x1b[%dA", max-len(lines))
	}
	m.printed = len(lines)
}

// promptLine 在 raw 模式下读一行文本（自己负责回显与擦除）。
func (m *menu) promptLine(label string) (string, bool) {
	m.clear()
	var buf []byte
	render := func() {
		fmt.Fprint(os.Stdout, "\r\x1b[2K"+label+string(buf))
	}
	render()
	m.printed = 1
	for {
		k := m.keys.read()
		switch {
		case k == "enter":
			fmt.Fprint(os.Stdout, "\r\x1b[2K"+label+string(buf)+"\n")
			m.printed = 0
			return strings.TrimSpace(string(buf)), true
		case k == "esc" || k == "ctrl-c" || k == "eof":
			fmt.Fprint(os.Stdout, "\r\x1b[2K\n")
			m.printed = 0
			return "", false
		case k == "backspace":
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		case len(k) == 1 && k[0] >= 32 && k[0] < 127:
			buf = append(buf, k[0])
		}
		render()
	}
}

// refresh 重新扫描 profile，更新漂移摘要，并尽量保持选中项不变。
func (m *menu) refresh() {
	prev := ""
	if m.cursor >= 0 && m.cursor < len(m.profiles) {
		prev = m.profiles[m.cursor].Name
	}
	if ps, err := listProfiles(m.cfg); err == nil {
		m.profiles = ps
	}
	m.drift = driftSummary(m.cfg)
	m.cursor = 0
	for i, p := range m.profiles {
		if strings.EqualFold(p.Name, prev) {
			m.cursor = i
			break
		}
	}
}

// runMenu 交互循环：选择 → 启动 pi → pi 退出后回到菜单。
func runMenu(cfg *Config, profiles []Profile, drift string) error {
	restore, rawOK, vtOK := enterRawTerminal()
	// 用闭包持有 restoreFn：启动 pi 前还原终端，pi 退出后重新进入 raw 模式。
	if !rawOK || !vtOK {
		restore()
		return runMenuFallback(cfg, profiles, drift)
	}
	showCursor := func() { fmt.Fprint(os.Stdout, "\x1b[?25h") }
	hideCursor := func() { fmt.Fprint(os.Stdout, "\x1b[?25l") }
	hideCursor()
	defer func() {
		restore()
		showCursor()
	}()

	m := &menu{cfg: cfg, keys: newKeyReader(), drift: drift, profiles: profiles}
	m.draw()

	for {
		switch m.handleKey(m.keys.read()) {
		case intentQuit:
			m.clear()
			return nil

		case intentLaunch:
			p := m.selected()
			m.clear()
			showCursor()
			fmt.Fprintf(os.Stdout, "%s %s\n%s\n\n", cyan("▶"), bold(p.Name),
				dim("PI_CODING_AGENT_DIR="+p.AgentDir))

			// 交还终端控制权给 pi，否则 pi 会在 raw 模式下启动、输入全废。
			restore()
			err := launch(cfg, p, nil)

			// pi 退出后重新进入 raw 模式；失败就直接结束（不再画菜单）。
			var ok bool
			restore, ok, _ = reenterRaw()
			if !ok {
				return err
			}
			hideCursor()
			m.keys = newKeyReader()
			m.refresh()
			if err != nil {
				m.status = "pi 退出：" + err.Error()
			} else {
				m.status = "pi 已退出（" + timestamp() + "），选择下一个配置"
			}
			m.draw()

		case intentNew:
			name, ok := m.promptLine("新建 profile 名称 > ")
			if !ok {
				m.status = "已取消新建"
			} else {
				var msgs []string
				log := func(f string, a ...any) { msgs = append(msgs, fmt.Sprintf(f, a...)) }
				if _, err := newProfile(cfg, name, log); err != nil {
					m.status = "✗ 新建失败：" + err.Error()
				} else {
					m.status = "✓ 已创建 " + name + " —— " + strings.Join(msgs, "；")
				}
			}
			m.refresh()
			m.draw()

		case intentSync:
			var msgs []string
			log := func(f string, a ...any) { msgs = append(msgs, fmt.Sprintf(f, a...)) }
			results, err := syncAll(cfg, false, log)
			switch {
			case err != nil:
				m.status = "✗ 同步失败：" + err.Error()
			default:
				wrote := 0
				for _, r := range results {
					if r.Action == "written" {
						wrote++
					}
				}
				if wrote == 0 {
					m.status = "✓ 同步完成：所有 profile 均已是最新"
				} else {
					m.status = fmt.Sprintf("✓ 同步完成：更新 %d 个文件", wrote)
				}
			}
			m.refresh()
			m.draw()

		case intentRefresh:
			m.status = "已刷新（" + timestamp() + "）"
			m.refresh()
			m.draw()
		}
	}
}

// reenterRaw 在 pi 退出后重新进入 raw 模式，并返回新的还原函数。
func reenterRaw() (func(), bool, bool) {
	restore, rawOK, vtOK := enterRawTerminal()
	if !rawOK || !vtOK {
		restore()
		return func() {}, false, false
	}
	return restore, true, true
}

// runMenuFallback 在 raw 模式 / ANSI 不可用时使用逐行输入（序号或名称均可）。
func runMenuFallback(cfg *Config, profiles []Profile, drift string) error {
	reader := bufio.NewReader(os.Stdin)
	launchAndResume := func(p Profile) {
		if err := launch(cfg, p, nil); err != nil {
			fmt.Println(red(err.Error()))
		}
		profiles, _ = listProfiles(cfg)
		drift = driftSummary(cfg)
	}
	for {
		fmt.Println()
		fmt.Println(bold("π 配置隔离启动器") + dim("  "+appVersion))
		fmt.Println(dim("profile 根目录 ：" + cfg.Root))
		fmt.Println(dim("全局 models.json 源：" + cfg.GlobalAgentDir))
		fmt.Println(dim("同步状态：" + drift))
		fmt.Println()
		for i, p := range profiles {
			suffix := ""
			if p.Global {
				suffix = "  (全局默认)"
			}
			fmt.Printf("  %2d) %s%s\n", i+1, p.Name, suffix)
		}
		fmt.Println()
		fmt.Println("  n) 新建 profile      s) 同步全局配置      r) 刷新      q) 退出")
		fmt.Print("\n选择 > ")

		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil && line == "" {
			fmt.Println()
			return nil // 输入结束（管道/EOF）
		}
		switch strings.ToLower(line) {
		case "q", "quit", "exit":
			return nil
		case "r", "refresh":
			profiles, _ = listProfiles(cfg)
			drift = driftSummary(cfg)
		case "s", "sync":
			if _, err := syncAll(cfg, false, newLogger(false, false)); err != nil {
				fmt.Println(red("同步失败：" + err.Error()))
			}
			profiles, _ = listProfiles(cfg)
			drift = driftSummary(cfg)
		case "n", "new":
			fmt.Print("新建 profile 名称 > ")
			name, rerr := reader.ReadString('\n')
			name = strings.TrimSpace(name)
			if rerr == nil && name != "" {
				if _, err := newProfile(cfg, name, newLogger(false, false)); err != nil {
					fmt.Println(red("新建失败：" + err.Error()))
				}
			}
			profiles, _ = listProfiles(cfg)
			drift = driftSummary(cfg)
		case "":
			launchAndResume(profiles[0])
		default:
			p, err := selectByToken(cfg, profiles, line)
			if err != nil {
				fmt.Println(red(err.Error()))
				continue
			}
			launchAndResume(p)
		}
	}
}

// selectByToken 支持用序号或名字选择 profile。
func selectByToken(cfg *Config, profiles []Profile, token string) (Profile, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Profile{}, fmt.Errorf("请输入序号或 profile 名称")
	}
	if n := atoiSafe(token); n > 0 {
		if n > len(profiles) {
			return Profile{}, fmt.Errorf("序号 %d 超范围：可选 1-%d", n, len(profiles))
		}
		return profiles[n-1], nil
	}
	p, err := findProfile(cfg, token)
	if err != nil {
		return Profile{}, fmt.Errorf("无效选择 %q：请输入序号或 profile 名称", token)
	}
	return p, nil
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 100000 {
			return 0
		}
	}
	return n
}
