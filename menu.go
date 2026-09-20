package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------- 按键读取 ----------

type keyReader struct {
	ch    chan byte
	extra []byte // 已从流里读出、尚未消费的字节（用于回车后丢弃同批的换行）
}

func newKeyReader() *keyReader { return newKeyReaderFrom(os.Stdin) }

// newKeyReaderFrom 允许注入输入源，便于对按键解析做单元测试。
func newKeyReaderFrom(r io.Reader) *keyReader {
	kr := &keyReader{ch: make(chan byte, 256)}
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
	if len(k.extra) > 0 {
		b := k.extra[0]
		k.extra = k.extra[1:]
		return b, true
	}
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

func (k *keyReader) putBack(b byte) { k.extra = append(k.extra, b) }

// drain 丢掉接下来 delay 时间内到达的所有字节。
// 用途：Windows 的 Enter 会交付 "\r\n" 两个字节，若不丢弃会让第二次回车空转。
func (k *keyReader) drain(delay time.Duration) {
	n := 0
	for {
		if _, ok := k.get(delay); !ok {
			return
		}
		if n++; n > 64 {
			return
		}
	}
}

// 抽象按键名
const (
	keyUnknown = "unknown"
	keyEOF     = "eof"
)

// ---- Windows 控制台在「未开启 VT 输入」时交付的扫描码 ----
// 用 WriteConsoleInput 实测确认：方向键是 EXTENDED 前缀(0x00/0xE0) + 扫描码。
const (
	scanUp    = 0x48
	scanDown  = 0x50
	scanLeft  = 0x4B
	scanRight = 0x4D
	scanHome  = 0x47
	scanEnd   = 0x4F
	scanPgUp  = 0x49
	scanPgDn  = 0x51
	scanIns   = 0x52
	scanDel   = 0x53
)

// read 解析一个按键。同时兼容三种交付格式：
//  1. VT 序列          "\x1b[A"（开启 VT 输入后）
//  2. Windows 扫描码   0x00/0xE0 + 0x48（未开启 VT 输入）
//  3. 单字节           字母 / 回车 / Ctrl+C ...
func (k *keyReader) read() string {
	b, ok := k.get(0)
	if !ok {
		return keyEOF
	}
	switch b {
	case 0x00, 0xE0: // Windows 扩展键前缀 -> 紧跟一个扫描码
		sc, ok := k.get(120 * time.Millisecond)
		if !ok {
			return keyUnknown
		}
		return scanToKey(sc)
	case 0x1b: // ESC，或 CSI/SS3 序列
		return k.readEscape()
	case '\r':
		// 吃掉 Enter 的配套 \n，避免下一次 read 立刻返回一个多余的 "enter"
		k.drain(5 * time.Millisecond)
		return "enter"
	case '\n':
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

func scanToKey(sc byte) string {
	switch sc {
	case scanUp:
		return "up"
	case scanDown:
		return "down"
	case scanLeft:
		return "left"
	case scanRight:
		return "right"
	case scanHome:
		return "home"
	case scanEnd:
		return "end"
	case scanPgUp:
		return "pgup"
	case scanPgDn:
		return "pgdn"
	case scanDel:
		return "delete"
	case scanIns:
		return "insert"
	}
	return keyUnknown
}

// readEscape 解析 ESC 引导的序列；单独的 ESC 键也走这里（靠超时区分）。
func (k *keyReader) readEscape() string {
	b1, ok := k.get(120 * time.Millisecond)
	if !ok {
		return "esc" // 超时 = 用户真的按了 Esc
	}
	if b1 != '[' && b1 != 'O' {
		k.putBack(b1)
		return "esc"
	}
	b2, ok := k.get(120 * time.Millisecond)
	if !ok {
		return "esc"
	}
	switch b2 {
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
	case 'Z':
		return "shift-tab"
	}
	if b2 >= '0' && b2 <= '9' {
		// CSI <num> [;<num>] <final>，如 "\x1b[5~" / "\x1b[1;5A"
		num := []byte{b2}
		var final byte
		for i := 0; i < 8; i++ {
			c, ok := k.get(60 * time.Millisecond)
			if !ok {
				return keyUnknown
			}
			if (c >= '0' && c <= '9') || c == ';' {
				num = append(num, c)
				continue
			}
			final = c
			break
		}
		if final == 0 {
			return keyUnknown
		}
		// 修饰键前缀（1;5 等）不影响方向键语义，取末位数字判断
		code := strings.TrimSpace(string(num))
		if i := strings.IndexByte(code, ';'); i >= 0 {
			code = code[i+1:]
		}
		switch code {
		case "5":
			return "pgup"
		case "6":
			return "pgdn"
		case "1", "7":
			return "home"
		case "4", "8":
			return "end"
		case "2":
			return "insert"
		case "3":
			return "delete"
		}
		switch final {
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
		}
		return keyUnknown
	}
	return keyUnknown
}

// ---------- 渲染行 ----------

// renderLine 是一行待绘制内容：纯文本 + 可选的反色高亮区段。
type renderLine struct {
	text string
	// hl 非 nil 时，对 text 的 [hlStart, hlStart+len(hl)) 显示单元区间套反色。
	hl      string
	hlStart int
}

func plainLine(s string) renderLine { return renderLine{text: s} }

func hlLine(text, hl string, start int) renderLine {
	return renderLine{text: text, hl: hl, hlStart: start}
}

// ---------- 终端宽度与截断 ----------

func (m *menu) size() (int, int) {
	if m.width > 0 && m.height > 0 {
		return m.width, m.height
	}
	if w, h, ok := terminalSize(); ok && w > 0 && h > 0 {
		if w > 160 {
			w = 160 // 超宽窗口没必要铺满，避免提示语被拉得太散
		}
		return w, h
	}
	return 80, 24
}

// displayWidth 计算字符串的终端显示宽度（East Asian Wide/Fullwidth 记 2）。
func displayWidth(s string) int {
	return widthOf(stripANSI(s))
}

// stripANSI 去掉文本里的 ANSI 转义序列，便于按显示宽度裁剪。
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var out strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) && !(runes[i] >= 0x40 && runes[i] <= 0x7e) {
				i++
			}
			continue
		}
		out.WriteRune(runes[i])
	}
	return out.String()
}

// truncateToWidth 把纯文本按显示宽度裁剪到 w，超出时在末尾放省略号。
func truncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if widthOf(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	limit := w - 1
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > limit {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

func widthOf(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth 返回一个字符占用的终端列数。
// 覆盖 CJK（4E00-9FFF）、全角标点/假名（3000-30FF）、宽符号（FF00-FF60）、
// 以及常见 emoji 与杂项符号块，这些在终端里都占两列。
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32:
		return 0
	case r < 0x1100:
		return 1
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK 部首 / 假名标点
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF, // CJK 统一表意
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3, // Hangul 音节
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60, // 全角
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// ---------- 菜单状态 ----------

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
	status   string
	drift    string
	profiles []Profile

	width, height int
	useANSI       bool

	// out 默认 os.Stdout；测试里替换为 buffer 即可断言「是否真的重绘了」。
	out io.Writer
	// 启动 pi 需要接管/交还终端，测试中替换为桩函数。
	launchFn  func(Profile) error
	restoreFn func()
	reenterFn func() (func(), bool, bool)
}

func (m *menu) writer() io.Writer {
	if m.out == nil {
		return os.Stdout
	}
	return m.out
}

// newMenu 构造交互菜单。restore 是「归还终端控制台模式」的函数，
// **必须**在构造时传入并挂到 m.restoreFn 上：
// 曾因 runMenu 里漏了这一步，使 handleIntent 的终端交还被整段跳过，
// pi 于是在 raw 模式下启动（输入模式未恢复）。用构造函数把它变成强制约束。
func newMenu(cfg *Config, profiles []Profile, drift string, restore func()) *menu {
	if restore == nil {
		restore = func() {}
	}
	m := &menu{
		cfg:       cfg,
		keys:      newKeyReader(),
		drift:     drift,
		profiles:  profiles,
		useANSI:   true,
		restoreFn: restore,
	}
	if w, h, ok := terminalSize(); ok {
		m.width, m.height = w, h
	}
	if m.width > 160 {
		m.width = 160
	}
	return m
}

func (m *menu) wf(format string, a ...any) { fmt.Fprintf(m.writer(), format, a...) }

func (m *menu) ws(s string) { _, _ = io.WriteString(m.writer(), s) }

// selected 返回当前高亮的 profile。
func (m *menu) selected() Profile {
	if m.cursor < 0 || m.cursor >= len(m.profiles) {
		return Profile{}
	}
	return m.profiles[m.cursor]
}

// handleKey 是纯决策函数：根据按键更新菜单状态并返回意图。
// 除「新建」需要先向用户索要名字外，一切副作用都交给调用方执行。
//
// 注意：导航类按键返回 intentNone，但**状态已改变**——调用方必须重绘，
// 否则表现为「按方向键没反应」（曾因此踩坑）。
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

// refresh 重新扫描 profile，更新漂移摘要，并尽量保持选中项不变。
func (m *menu) refresh() {
	prev := ""
	if p := m.selected(); p.Name != "" {
		prev = p.Name
	}
	if ps, err := listProfiles(m.cfg); err == nil {
		if len(ps) > 0 {
			m.profiles = ps
		}
	}
	m.drift = driftSummary(m.cfg)
	m.cursor = 0
	for i, p := range m.profiles {
		if strings.EqualFold(p.Name, prev) {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.profiles) {
		m.cursor = len(m.profiles) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// ---------- 绘制 ----------

// body 生成「固定内容」部分（表头 + 列表 + 页脚），
// 与状态行分开是为了在视口高度变化时能精确截断。
func (m *menu) body() []renderLine {
	w, _ := m.size()
	var lines []renderLine
	lines = append(lines, renderLine{text: bold("π 配置隔离启动器") + dim("  "+appVersion)})
	lines = append(lines, plainLine(dim(truncateToWidth("profile 根目录    "+m.cfg.Root, w-1))))
	lines = append(lines, plainLine(dim(truncateToWidth("全局 models 来源 "+m.cfg.GlobalAgentDir, w-1))))
	lines = append(lines, plainLine(dim(truncateToWidth("同步状态         "+m.drift, w-1))))
	lines = append(lines, plainLine(""))

	// 列表（可能被视口裁剪）
	start, end := m.viewport()
	for i := start; i < end; i++ {
		p := m.profiles[i]
		marker := "  "
		if i == m.cursor {
			marker = "› "
		}
		label := fmt.Sprintf("%d) %s", i+1, p.Name)
		if p.Global {
			label += "  (全局默认)"
		}
		raw := marker + label
		if i == m.cursor {
			// 高亮区段就是整行内容（跳过光标提示符本身）
			lines = append(lines, hlLine(raw, label, widthOf(marker)))
			dir := "→ " + p.AgentDir
			lines = append(lines, plainLine(dim("     "+truncateToWidth(dir, w-8))))
		} else {
			lines = append(lines, plainLine(raw))
		}
	}
	if start > 0 || end < len(m.profiles) {
		lines = append(lines, plainLine(dim(fmt.Sprintf("  (%d-%d / %d)", start+1, end, len(m.profiles)))))
	}
	lines = append(lines, plainLine(""))
	lines = append(lines, plainLine(dim("↑↓ 选择   Enter 启动   n 新建   s 同步全局配置   r 刷新   q 退出")))
	return lines
}

// viewport 计算当前应显示的 profile 区间，保证选中项可见。
func (m *menu) viewport() (start, end int) {
	_, h := m.size()
	// 预留：表头 5 行 + 空行 + 页脚 2 行 + 状态 1 行 + 选中项说明 1 行
	budget := h - 10
	if budget < 3 {
		budget = 3
	}
	if len(m.profiles) <= budget {
		return 0, len(m.profiles)
	}
	start = 0
	if m.cursor >= budget {
		start = m.cursor - budget + 1
	}
	end = start + budget
	if end > len(m.profiles) {
		end = len(m.profiles)
		start = end - budget
	}
	return start, end
}

// render 生成完整屏幕内容（每行已按宽度裁剪，可直接绘制）。
func (m *menu) render() []string {
	w, h := m.size()
	if w < 20 {
		w = 20
	}
	lines := m.body()
	// 状态行永远在最底部；内容超长时优先截断中间，保住表头与页脚
	maxBody := h - 1
	if maxBody < 1 {
		maxBody = 1
	}
	if len(lines) > maxBody {
		lines = lines[:maxBody]
	}

	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		out = append(out, clip(l, w))
	}
	if m.status != "" {
		out = append(out, green(clip(renderLine{text: m.status}, w)))
	} else {
		out = append(out, "")
	}
	return out
}

// clip 按显示宽度裁剪一行（保住高亮区段），确保不会因自动换行打乱布局。
func clip(l renderLine, w int) string {
	raw := l.text
	vis := widthOf(stripANSI(raw))
	if l.hl == "" || l.hlStart < 0 {
		t := truncateToWidth(stripANSI(raw), w)
		_ = vis
		return t
	}
	plain := stripANSI(raw)
	hlW := widthOf(l.hl)
	_ = vis
	// 高亮区段太靠右而被裁掉时退化为普通行
	if l.hlStart+hlW > w || widthOf(plain) > w {
		if len(plain) >= l.hlStart && l.hlStart+len(l.hl) <= len(plain) {
			// 按 rune 重建：文本可能被截断，直接用裁剪后的普通文本
			cutEnd := truncateToWidth(string([]rune(plain)), l.hlStart)
			_ = cutEnd
		}
		return truncateToWidth(plain, w)
	}
	head := takeWidth(plain, l.hlStart)
	tail := dropWidth(plain, l.hlStart+hlW)
	return head + inverse(l.hl) + tail
}

// takeWidth 取前 w 个显示列对应的文本。
func takeWidth(s string, w int) string {
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > w {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

// dropWidth 丢掉前 w 个显示列对应的文本。
func dropWidth(s string, w int) string {
	used := 0
	runes := []rune(s)
	for i, r := range runes {
		if used >= w {
			return string(runes[i:])
		}
		used += runeWidth(r)
	}
	return ""
}

// ---------- 交互循环 ----------

// runMenu 交互循环：选择 → 启动 pi → pi 退出后回到菜单。
func runMenu(cfg *Config, profiles []Profile, drift string) error {
	restore, rawOK, vtOK := enterRawTerminal()
	if !rawOK || !vtOK {
		restore()
		return runMenuFallback(cfg, profiles, drift)
	}

	// 全程使用备用屏缓冲：不破坏用户原来的 shell 回滚历史，
	// 退出时恢复成进入前的样子（这是 TUI 的标准做法）。
	fmt.Fprint(os.Stdout, ansiEnterAltScreen, ansiHideCursor)

	m := newMenu(cfg, profiles, drift, restore)
	// leave 必须读 m.restoreFn（而非捕获局部变量）：
	// 每次启动 pi 后都会重新进入 raw 模式并替换该函数。
	leave := func() {
		fmt.Fprint(os.Stdout, ansiShowCursor, ansiLeaveAltScreen)
		if m.restoreFn != nil {
			m.restoreFn()
		}
	}
	defer leave()
	m.draw()

	for {
		// 键盘先到：按键路径不查窗口尺寸（实测 GetConsoleScreenBufferInfo 在
		// 这种会话里会失败，绝不能让它挡住按键处理）。
		intent := m.handleKey(m.keys.read())

		// 尺寸变化（用户拖动了终端窗口）才去查询并刷新视口。
		if w, h, ok := terminalSize(); ok && (w != m.width || h != m.height) {
			if w > 160 {
				w = 160
			}
			m.width, m.height = w, h
			m.draw()
		}

		if m.handleIntent(intent) {
			return nil
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

// draw 整屏重绘：光标归位后逐行覆盖并清到行尾。
// 不做「回退 N 行」式的增量刷新——一旦有滚动就会错位。
func (m *menu) draw() {
	lines := m.render()
	var b strings.Builder
	b.WriteString(ansiHome)
	for i, l := range lines {
		b.WriteString(ansiClearLine)
		b.WriteString(l)
		if i < len(lines)-1 {
			b.WriteString("\r\n")
		}
	}
	// 清掉本次内容之后的残留
	b.WriteString(ansiClearToEnd)
	m.ws(b.String())
}

// handleIntent 执行一个意图并重绘。
// 独立的理由（曾是真实 bug）：导航类按键返回 intentNone 但状态已变，
// 若调用方漏掉「intentNone 也要重绘」，现象就是「按方向键毫无反应」。
// 把它单独抽出来并配单测，就能钉住这个回归。
func (m *menu) handleIntent(intent menuIntent) (done bool) {
	switch intent {
	case intentQuit:
		return true

	case intentNone:
		// 导航（↑↓/Home/End/PgUp/PgDn）走这里：必须重绘。
		m.draw()

	case intentLaunch:
		p := m.selected()
		m.ws(ansiClearScreen + ansiHome)
		m.wf("\x1b[36m▶\x1b[0m \x1b[1m%s\x1b[0m\n\x1b[2m%s\x1b[0m\n\n",
			p.Name, "PI_CODING_AGENT_DIR="+p.AgentDir)

		// 交还终端：pi 需要常规控制台模式，否则它的 TUI 收不到输入。
		m.ws(ansiShowCursor)
		m.ws(ansiLeaveAltScreen)
		if m.restoreFn != nil {
			m.restoreFn()
		}

		var err error
		if m.launchFn != nil {
			err = m.launchFn(p)
		} else {
			err = launch(m.cfg, p, nil)
		}

		// pi 退出后重新接管终端并回到菜单。
		var ok bool
		reenter := m.reenterFn
		if reenter == nil {
			reenter = reenterRaw
		}
		if m.restoreFn, ok, _ = reenter(); !ok {
			return true // 终端已不可用，直接结束
		}
		m.keys = newKeyReader()
		m.ws(ansiEnterAltScreen + ansiHideCursor + ansiClearScreen + ansiHome)
		m.refresh()
		if err != nil {
			m.status = "pi 退出：" + err.Error()
		} else {
			m.status = "pi 已退出（" + timestamp() + "）—— 选择下一个配置"
		}
		m.draw()

	case intentNew:
		name, ok := m.promptLine("新建 profile 名称 > ")
		switch {
		case !ok:
			m.status = "已取消新建"
		case name == "":
			m.status = "名称为空，已取消"
		default:
			var msgs []string
			log := func(f string, a ...any) { msgs = append(msgs, fmt.Sprintf(f, a...)) }
			if _, err := newProfile(m.cfg, name, log); err != nil {
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
		results, err := syncAll(m.cfg, false, log)
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
	return false
}

// promptLine 在 raw 模式下读一行文本（自己负责回显与擦除）。
func (m *menu) promptLine(label string) (string, bool) {
	w, _ := m.size()
	var buf []byte
	render := func() {
		txt := label + string(buf)
		m.ws("\r" + ansiClearLine + truncateToWidth(txt, w-1))
	}
	render()
	for {
		k := m.keys.read()
		switch {
		case k == "enter":
			m.ws(ansiClearLine)
			return strings.TrimSpace(string(buf)), true
		case k == "esc" || k == "ctrl-c" || k == "eof":
			m.ws(ansiClearLine)
			return "", false
		case k == "backspace":
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		case k == "space":
			buf = append(buf, ' ')
		case k == "shift-tab", k == "tab":
			// 忽略
		case len(k) == 1 && k[0] >= 32 && k[0] < 127:
			buf = append(buf, k[0])
		}
		render()
	}
}

// ---------- 降级：逐行菜单 ----------

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
			if len(profiles) > 0 {
				launchAndResume(profiles[0])
			}
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
	if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100000 {
		return n
	}
	return 0
}
