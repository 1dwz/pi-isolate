package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---------- profile 名校验 ----------

func TestValidateProfileName(t *testing.T) {
	ok := []string{"python", "android", "go", "my.profile", "a_b-c", "P1"}
	for _, n := range ok {
		if err := ValidateProfileName(n); err != nil {
			t.Errorf("名字 %q 应该合法，却报错: %v", n, err)
		}
	}
	bad := []string{"", "default", "DEFAULT", "bad name", "-lead", ".hidden", "a/b", "a\\b", "中文"}
	for _, n := range bad {
		if err := ValidateProfileName(n); err == nil {
			t.Errorf("名字 %q 应该非法，却通过了", n)
		}
	}
}

// ---------- 按键解析 ----------

func TestKeyReader(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\x1b[A", "up"},
		{"\x1b[B", "down"},
		{"\x1b[C", "right"},
		{"\x1b[D", "left"},
		{"\x1b[H", "home"},
		{"\x1b[F", "end"},
		{"\x1b[5~", "pgup"},
		{"\x1b[6~", "pgdn"},
		{"\r", "enter"},
		{"\n", "enter"},
		{"\x03", "ctrl-c"},
		{"\x7f", "backspace"},
		{"q", "q"},
		{"Q", "q"},
		{"s", "s"},
		{"2", "2"},
		{" ", "space"},
		{"\x1b", "esc"},
	}
	for _, c := range cases {
		kr := newKeyReaderFrom(strings.NewReader(c.in))
		if got := kr.read(); got != c.want {
			t.Errorf("输入 %q：期望 %q，得到 %q", c.in, c.want, got)
		}
	}
}

func TestKeyReaderEOF(t *testing.T) {
	kr := newKeyReaderFrom(strings.NewReader(""))
	if got := kr.read(); got != "eof" {
		t.Errorf("空输入期望 eof，得到 %q", got)
	}
}

// ---------- 环境变量注入 ----------

func TestSetUnsetEnv(t *testing.T) {
	env := []string{"PATH=C:\\x", "PI_CODING_AGENT_DIR=old", "FOO=bar"}
	env = setEnv(env, envAgentDir, "new")
	if n := countKey(env, envAgentDir); n != 1 {
		t.Fatalf("期望恰好 1 个 PI_CODING_AGENT_DIR，实际 %d：%v", n, env)
	}
	if got := lookupEnv(env, envAgentDir); got != "new" {
		t.Errorf("期望 new，得到 %q", got)
	}
	env = unsetEnv(env, envAgentDir)
	if n := countKey(env, envAgentDir); n != 0 {
		t.Errorf("unset 后期望 0 个，实际 %d", n)
	}
	if len(env) != 2 {
		t.Errorf("unset 不应删掉其它变量，实际 %v", env)
	}
}

func countKey(env []string, key string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), strings.ToUpper(key)+"=") {
			n++
		}
	}
	return n
}

func lookupEnv(env []string, key string) string {
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), strings.ToUpper(key)+"=") {
			return kv[len(key)+1:]
		}
	}
	return ""
}

// ---------- profile 发现 ----------

func TestDiscoverProfiles(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "python", "agent"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "Android", "agent"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "_shared"), 0o755)) // 保留目录，应被跳过
	must(t, os.MkdirAll(filepath.Join(root, ".hidden"), 0o755)) // 隐藏目录，应被跳过
	must(t, os.WriteFile(filepath.Join(root, "note.txt"), []byte("x"), 0o644))

	got, err := discoverProfiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	want := []string{"Android", "python"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("期望 %v，得到 %v", want, names)
	}
	if got[0].AgentDir != filepath.Join(root, "Android", "agent") {
		t.Errorf("AgentDir 不对: %s", got[0].AgentDir)
	}
}

func TestFindProfileGlobal(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{Root: root, GlobalAgentDir: filepath.Join(root, "..", "global", "agent")}
	p, err := findProfile(cfg, "default")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Global {
		t.Error("default 应该是全局项")
	}
	if _, err := findProfile(cfg, "nope"); err == nil {
		t.Error("不存在的 profile 应该报错")
	}
}

// ---------- 同步范围（核心契约） ----------

func setupGlobal(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "global", "agent")
	must(t, os.MkdirAll(dir, 0o755))
	for name, content := range files {
		must(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	return dir
}

// TestSyncOnlyModelsByDefault 断言：默认只同步 models.json，
// settings.json / auth.json 等属于 profile 自有状态，绝不能被全局覆盖。
func TestSyncOnlyModelsByDefault(t *testing.T) {
	globalAgent := setupGlobal(t, map[string]string{
		"models.json":   `{"providers":{"p":{"apiKey":"root"}}}`,
		"settings.json": `{"theme":"dark","packages":["npm:x"]}`,
		"auth.json":     `{"token":"SECRET"}`,
	})
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{Root: root, GlobalAgentDir: globalAgent}

	agentDir := filepath.Join(root, "python", "agent")
	must(t, os.MkdirAll(agentDir, 0o755))
	custom := `{"theme":"light","myOwnSetting":42}`
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(custom), 0o644))

	results := syncToProfile(cfg, Profile{Name: "python", AgentDir: agentDir}, false)

	var actions = map[string]string{}
	for _, r := range results {
		actions[filepath.Base(r.To)] = r.Action
	}
	if actions[modelsFileName] != "written" {
		t.Errorf("models.json 应被写入，实际 %q（全部：%v）", actions[modelsFileName], actions)
	}
	if _, ok := actions["settings.json"]; ok {
		t.Errorf("默认不应同步 settings.json，实际发生了：%v", actions)
	}
	if _, ok := actions["auth.json"]; ok {
		t.Errorf("默认不应同步 auth.json，实际发生了：%v", actions)
	}

	got, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Errorf("profile 的 settings.json 被改动了：%s", got)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "auth.json")); !os.IsNotExist(err) {
		t.Error("auth.json 不应被同步到 profile")
	}
}

// TestSyncFilesConfigExtendsScope 断言 syncFiles 能显式扩大同步范围。
func TestSyncFilesConfigExtendsScope(t *testing.T) {
	globalAgent := setupGlobal(t, map[string]string{
		"models.json":       `{"a":1}`,
		"models-store.json": `{"cached":true}`,
	})
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{
		Root: root, GlobalAgentDir: globalAgent,
		SyncFiles: []string{"models.json", "models-store.json"},
	}
	agentDir := filepath.Join(root, "go", "agent")
	must(t, os.MkdirAll(agentDir, 0o755))

	results := syncToProfile(cfg, Profile{Name: "go", AgentDir: agentDir}, false)
	actions := map[string]string{}
	for _, r := range results {
		actions[filepath.Base(r.To)] = r.Action
	}
	for _, want := range []string{"models.json", "models-store.json"} {
		if actions[want] != "written" {
			t.Errorf("%s 应被写入，实际 %q", want, actions[want])
		}
	}
}

// TestSyncCheckDoesNotWrite 断言 --check 只报告不落盘。
func TestSyncCheckDoesNotWrite(t *testing.T) {
	globalAgent := setupGlobal(t, map[string]string{"models.json": `{"v":2}`})
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{Root: root, GlobalAgentDir: globalAgent}
	agentDir := filepath.Join(root, "python", "agent")
	must(t, os.MkdirAll(agentDir, 0o755))
	must(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"v":1}`), 0o644))

	results := syncToProfile(cfg, Profile{Name: "python", AgentDir: agentDir}, true)
	bad := checkDrift(results)
	if len(bad) != 1 || bad[0].Action != "stale" {
		t.Fatalf("期望 1 个 stale，实际 %v", bad)
	}
	got, _ := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if string(got) != `{"v":1}` {
		t.Errorf("--check 不应写入，实际内容变成了 %s", got)
	}
}

// TestSyncUnchangedDetected 断言内容一致时报告 unchanged（幂等）。
func TestSyncUnchangedDetected(t *testing.T) {
	same := `{"v":7}`
	globalAgent := setupGlobal(t, map[string]string{"models.json": same})
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{Root: root, GlobalAgentDir: globalAgent}
	agentDir := filepath.Join(root, "python", "agent")
	must(t, os.MkdirAll(agentDir, 0o755))
	must(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(same), 0o644))

	results := syncToProfile(cfg, Profile{Name: "python", AgentDir: agentDir}, false)
	if len(results) != 1 || results[0].Action != "unchanged" {
		t.Fatalf("期望 unchanged，实际 %v", results)
	}
}

// TestGlobalProfileNotSynced 断言全局项不会被同步（它本身就是源）。
func TestGlobalProfileNotSynced(t *testing.T) {
	globalAgent := setupGlobal(t, map[string]string{"models.json": `{"v":1}`})
	cfg := &Config{Root: filepath.Join(t.TempDir(), "pi"), GlobalAgentDir: globalAgent}
	p := globalProfile(cfg)
	if got := syncToProfile(cfg, p, false); len(got) != 0 {
		t.Errorf("全局项不应产生同步动作，实际 %v", got)
	}
}

// ---------- new / 最小 settings ----------

func TestNewProfileLayout(t *testing.T) {
	globalAgent := setupGlobal(t, map[string]string{
		"models.json":   `{"providers":{"my-provider":{"apiKey":"root"}}}`,
		"settings.json": `{"theme":"light","defaultModel":"m1","defaultProvider":"p1","packages":["npm:pi-mcp-adapter"]}`,
	})
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{Root: root, GlobalAgentDir: globalAgent}

	var logs []string
	p, err := newProfile(cfg, "python", func(f string, a ...any) {
		logs = append(logs, sprintf(f, a...))
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.AgentDir != filepath.Join(root, "python", "agent") {
		t.Errorf("AgentDir 应为 <root>\\python\\agent，实际 %s", p.AgentDir)
	}
	for _, rel := range []string{modelsFileName, "settings.json", "sessions"} {
		if _, err := os.Stat(filepath.Join(p.AgentDir, rel)); err != nil {
			t.Errorf("缺少 %s：%v", rel, err)
		}
	}
	// 最小 settings：带启动模型与 shellPath，但绝不含 packages
	data, err := os.ReadFile(p.SettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"defaultModel", "defaultProvider", "shellPath", "theme"} {
		if !strings.Contains(s, want) {
			t.Errorf("最小 settings 缺少 %s：%s", want, s)
		}
	}
	if strings.Contains(s, "packages") {
		t.Errorf("最小 settings 不应包含 packages（会让每个 profile 各装一份扩展）：%s", s)
	}
	// models.json 应是从全局同步来的
	if got, _ := os.ReadFile(p.ModelsPath()); !strings.Contains(string(got), "my-provider") {
		t.Errorf("models.json 未同步：%s", got)
	}
	if err := ValidateProfileName("python"); err != nil {
		t.Fatal(err)
	}
	if _, err := newProfile(cfg, "python", func(string, ...any) {}); err == nil {
		t.Error("重复创建同名 profile 应该报错")
	}
}

// TestNewProfileMissingGlobalModels 断言全局 models.json 缺失时明确报错。
func TestNewProfileMissingGlobalModels(t *testing.T) {
	globalAgent := filepath.Join(t.TempDir(), "global", "agent")
	must(t, os.MkdirAll(globalAgent, 0o755))
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{Root: root, GlobalAgentDir: globalAgent}
	_, err := newProfile(cfg, "go", func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "全局模型配置不存在") {
		t.Fatalf("期望明确的全局缺失错误，实际 %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "go")); !os.IsNotExist(statErr) {
		t.Error("失败时不应留下半成品目录")
	}
}

// ---------- 回退菜单的选择解析 ----------

func TestSelectByToken(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "python", "agent"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "go", "agent"), 0o755))
	cfg := &Config{Root: root, GlobalAgentDir: filepath.Join(root, "g")}
	profiles, err := listProfiles(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Fatalf("期望 3 项（default + 2），实际 %d", len(profiles))
	}
	for _, c := range []struct{ tok, want string }{
		{"1", "default"}, {"2", "go"}, {"3", "python"},
		{"python", "python"}, {"DEFAULT", "default"}, {"go", "go"},
	} {
		p, err := selectByToken(cfg, profiles, c.tok)
		if err != nil {
			t.Errorf("token %q 报错: %v", c.tok, err)
			continue
		}
		if p.Name != c.want {
			t.Errorf("token %q 期望 %q，得到 %q", c.tok, c.want, p.Name)
		}
	}
	for _, bad := range []string{"9", "nope", "0"} {
		if _, err := selectByToken(cfg, profiles, bad); err == nil {
			t.Errorf("token %q 应该报错", bad)
		}
	}
}

// ---------- 菜单渲染 ----------

func TestMenuRenderLines(t *testing.T) {
	cfg := &Config{Root: `C:\ProgramData\pi`, GlobalAgentDir: `C:\Users\x\.pi\agent`}
	m := &menu{
		cfg: cfg, drift: "全部 2 个 profile 的 models.json 与全局一致",
		profiles: []Profile{
			{Name: "default", AgentDir: cfg.GlobalAgentDir, Global: true},
			{Name: "python", AgentDir: `C:\ProgramData\pi\python\agent`},
		},
		cursor: 1, width: 100, height: 30, useANSI: false,
	}
	out := strings.Join(m.render(), "\n")
	for _, want := range []string{"default", "python", "(全局默认)", "Enter 启动", "python\\agent"} {
		if !strings.Contains(out, want) {
			t.Errorf("菜单缺少 %q：\n%s", want, out)
		}
	}
}

// ---------- 菜单交互逻辑（决策层） ----------

func testMenu(t *testing.T) *menu {
	t.Helper()
	root := t.TempDir()
	for _, n := range []string{"android", "go", "python"} {
		must(t, os.MkdirAll(filepath.Join(root, n, "agent"), 0o755))
	}
	cfg := &Config{Root: root, GlobalAgentDir: filepath.Join(t.TempDir(), "g", "agent")}
	profiles, err := listProfiles(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &menu{cfg: cfg, profiles: profiles, drift: "测试"}
}

// TestMenuQuitKeys 断言退出类按键都返回 intentQuit。
func TestMenuQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl-c", "eof"} {
		m := testMenu(t)
		if got := m.handleKey(k); got != intentQuit {
			t.Errorf("按键 %q 期望 intentQuit，得到 %v", k, got)
		}
	}
}

// TestMenuNavigation 断言方向键移动光标且不越界。
func TestMenuNavigation(t *testing.T) {
	m := testMenu(t)
	// profiles: default, android, go, python
	if len(m.profiles) != 4 {
		t.Fatalf("期望 4 项，实际 %d", len(m.profiles))
	}
	if m.selected().Name != "default" {
		t.Fatalf("初始应选中 default，实际 %s", m.selected().Name)
	}
	if got := m.handleKey("up"); got != intentNone {
		t.Error("上边界按上键不应产生动作")
	}
	if m.cursor != 0 {
		t.Errorf("上边界光标应停在 0，实际 %d", m.cursor)
	}
	m.handleKey("down")
	m.handleKey("down")
	if m.selected().Name != "go" {
		t.Errorf("下移两次应选中 go，实际 %s", m.selected().Name)
	}
	m.handleKey("end")
	if m.selected().Name != "python" {
		t.Errorf("End 应选中最后一项 python，实际 %s", m.selected().Name)
	}
	m.handleKey("down")
	if m.cursor != len(m.profiles)-1 {
		t.Errorf("下边界按↓不应越界，实际 %d", m.cursor)
	}
	m.handleKey("home")
	if m.selected().Name != "default" {
		t.Errorf("Home 应回到第一项 default，实际 %s", m.selected().Name)
	}
}

// TestMenuIntents 断言功能键映射到正确意图。
func TestMenuIntents(t *testing.T) {
	cases := []struct {
		key  string
		want menuIntent
	}{
		{"enter", intentLaunch},
		{"n", intentNew},
		{"s", intentSync},
		{"r", intentRefresh},
		{"x", intentNone},
		{"1", intentNone},
	}
	for _, c := range cases {
		m := testMenu(t)
		if got := m.handleKey(c.key); got != c.want {
			t.Errorf("按键 %q 期望 %v，得到 %v", c.key, c.want, got)
		}
	}
}

// TestMenuLaunchUsesHighlightedProfile 是核心断言：
// 用真实 keyReader 喂入按键序列，确认「↓ ↓ Enter」最终启动的是 go 而非第一项。
func TestMenuLaunchUsesHighlightedProfile(t *testing.T) {
	m := testMenu(t)
	m.keys = newKeyReaderFrom(strings.NewReader("\x1b[B\x1b[B\r"))

	var launched Profile
	step := 0
	for step < 10 {
		step++
		switch m.handleKey(m.keys.read()) {
		case intentLaunch:
			launched = m.selected()
			step = 100
		}
	}
	if launched.Name != "go" {
		t.Fatalf("期望启动 go，实际 %q", launched.Name)
	}
	if launched.Global {
		t.Error("go 不应是全局项")
	}
	if launched.AgentDir != filepath.Join(m.cfg.Root, "go", "agent") {
		t.Errorf("启动目录不对: %s", launched.AgentDir)
	}
}

// TestMenuDownThenEnterFromTop 断言直接回车启动第一项（即全局默认）。
func TestMenuDownThenEnterFromTop(t *testing.T) {
	m := testMenu(t)
	m.keys = newKeyReaderFrom(strings.NewReader("\r"))
	if got := m.handleKey(m.keys.read()); got != intentLaunch {
		t.Fatalf("期望 intentLaunch，得到 %v", got)
	}
	if p := m.selected(); !p.Global || p.Name != "default" {
		t.Errorf("默认应选中全局项，实际 %+v", p)
	}
}

// TestMenuRefreshKeepsSelection 断言刷新后选中项跟随同名 profile。
func TestMenuRefreshKeepsSelection(t *testing.T) {
	m := testMenu(t)
	m.handleKey("down")
	m.handleKey("down")
	if m.selected().Name != "go" {
		t.Fatalf("前置条件失败：%s", m.selected().Name)
	}
	// 新增一个排在最前的 profile，顺序会变；刷新后仍应选中 go
	must(t, os.MkdirAll(filepath.Join(m.cfg.Root, "aaa", "agent"), 0o755))
	m.refresh()
	if m.selected().Name != "go" {
		t.Errorf("刷新后应仍选中 go，实际 %s", m.selected().Name)
	}
}

// TestRenderRespectsWidth 断言所有渲染行都不会超出终端宽度——
// 超出会被终端自动换行，从而彻底打乱整屏布局。
func TestRenderRespectsWidth(t *testing.T) {
	for _, w := range []int{20, 30, 40, 60, 80, 120} {
		cfg := &Config{
			Root:           `C:\ProgramData\pi`,
			GlobalAgentDir: `C:\Users\Administrator\.pi\agent`,
		}
		m := &menu{
			cfg: cfg, drift: "全部 2 个 profile 的 models.json 与全局一致（中文测试）",
			profiles: []Profile{
				{Name: "default", AgentDir: cfg.GlobalAgentDir, Global: true},
				{Name: "android-reverse-engineering", AgentDir: `C:\ProgramData\pi\android-reverse-engineering\agent`},
			},
			cursor: 1, width: w, height: 40, useANSI: false,
		}
		for i, line := range m.render() {
			if got := displayWidth(line); got > w {
				t.Errorf("宽 %d 时第 %d 行宽度 %d 超出，会触发自动换行：%q", w, i, got, line)
			}
		}
	}
}

// TestRenderFitsHeight 断言渲染行数不超过终端高度（否则内容会滚动、布局错位）。
func TestRenderFitsHeight(t *testing.T) {
	var profiles []Profile
	for i := 0; i < 40; i++ {
		profiles = append(profiles, Profile{Name: fmt.Sprintf("p%02d", i), AgentDir: fmt.Sprintf(`C:\pi\p%02d\agent`, i)})
	}
	for _, h := range []int{10, 15, 24, 30} {
		m := &menu{
			cfg:      &Config{Root: `C:\pi`, GlobalAgentDir: `C:\g\agent`},
			drift:    "测试",
			profiles: profiles, cursor: 39, width: 80, height: h, useANSI: false,
		}
		got := m.render()
		if len(got) > h {
			t.Errorf("高 %d 时渲染了 %d 行，超出会导致滚动", h, len(got))
		}
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, "p39") {
			t.Errorf("高 %d 时选中项 p39 不在可视区内", h)
		}
	}
}

// TestRuneWidth 断言中文/ASCII 的宽度计算，乱算会让整屏错位。
func TestRuneWidth(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"abc", 3},
		{"配置文件", 8},
		{"a中b", 4},
		{"→", 1},
		{"✓", 1},
		{"（中文）", 8},
		{"", 0},
	}
	for _, c := range cases {
		if got := displayWidth(c.s); got != c.want {
			t.Errorf("displayWidth(%q) = %d，期望 %d", c.s, got, c.want)
		}
	}
}

// TestTruncateToWidth 断言截断后不超过目标宽度。
func TestTruncateToWidth(t *testing.T) {
	for _, w := range []int{1, 2, 3, 5, 10} {
		got := truncateToWidth("这是一个很长的中文说明文字", w)
		if displayWidth(got) > w {
			t.Errorf("宽度 %d 截断后变成 %d：%q", w, displayWidth(got), got)
		}
	}
	if got := truncateToWidth("abc", 5); got != "abc" {
		t.Errorf("未超长不应改动，得到 %q", got)
	}
}

// TestStripANSI 断言颜色序列不会计入显示宽度。
func TestStripANSI(t *testing.T) {
	colored := cyan("内容") + dim(" 后缀")
	if got := displayWidth(colored); got != displayWidth("内容 后缀") {
		t.Errorf("ANSI 未被剥离：宽度 %d，期望 %d", got, displayWidth("内容 后缀"))
	}
}

// ---------- 交互循环：导航必须重绘（本次线上 bug 的回归测试） ----------

// TestNavigationRedraws 是本 bug 的回归测试：
// 方向键返回 intentNone，若调用方不重绘，用户看到的就是「按上下毫无反应」。
// 这里直接驱动真实的 handleIntent，并用注入的 writer 断言**确实产生了输出**，
// 同时断言光标真的移动了（避免「重绘了但内容没变」也当作通过）。
func TestNavigationRedraws(t *testing.T) {
	m := testMenu(t)
	var buf bytes.Buffer
	m.out = &buf
	m.width, m.height = 80, 24

	// 首屏由 runMenu 负责绘制，这里手动对齐
	m.draw()
	before := buf.Len()
	if before == 0 {
		t.Fatal("draw 未产生任何输出")
	}

	// 导航一次：状态改变 + 必须重绘
	if intent := m.handleKey("down"); intent != intentNone {
		t.Fatalf("↓ 应返回 intentNone，实际 %v", intent)
	}
	if m.cursor != 1 {
		t.Fatalf("↓ 后光标应为 1，实际 %d", m.cursor)
	}
	buf.Reset()
	if done := m.handleIntent(intentNone); done {
		t.Fatal("intentNone 不应结束循环")
	}
	if buf.Len() == 0 {
		t.Fatal("按↓后未重绘 —— 这正是「上下没反应」那个 bug")
	}
	drawn := stripANSI(buf.String())
	if !strings.Contains(drawn, "› 2) android") {
		t.Errorf("重绘内容未把光标移到第 2 项：\n%s", drawn)
	}

	// 再按↑：回到第 1 项，并再次重绘
	m.handleKey("up")
	buf.Reset()
	m.handleIntent(intentNone)
	if buf.Len() == 0 {
		t.Fatal("按↑后未重绘")
	}
	if !strings.Contains(stripANSI(buf.String()), "› 1) default") {
		t.Errorf("重绘内容未把光标移回第 1 项：\n%s", stripANSI(buf.String()))
	}
}

// TestAllNavigationKeysRedraw 断言每一个导航键都会重绘（穷举，不遗漏）。
func TestAllNavigationKeysRedraw(t *testing.T) {
	for _, k := range []string{"up", "down", "left", "right", "home", "end", "pgup", "pgdn"} {
		m := testMenu(t)
		var buf bytes.Buffer
		m.out = &buf
		m.width, m.height = 80, 24
		m.cursor = 2

		intent := m.handleKey(k)
		if intent != intentNone {
			t.Errorf("键 %q 应返回 intentNone，实际 %v", k, intent)
			continue
		}
		buf.Reset()
		if done := m.handleIntent(intent); done {
			t.Errorf("键 %q 不应结束循环", k)
		}
		if buf.Len() == 0 {
			t.Errorf("键 %q 未触发重绘", k)
		}
	}
}

// TestIntentQuitEndsLoop 断言退出类按键真的结束循环且**不**重绘。
func TestIntentQuitEndsLoop(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl-c", "eof"} {
		m := testMenu(t)
		var buf bytes.Buffer
		m.out = &buf
		m.width, m.height = 80, 24
		intent := m.handleKey(k)
		if intent != intentQuit {
			t.Errorf("键 %q 应为 intentQuit，实际 %v", k, intent)
			continue
		}
		if done := m.handleIntent(intent); !done {
			t.Errorf("键 %q 应结束循环", k)
		}
		if buf.Len() != 0 {
			t.Errorf("键 %q 不应重绘，却写了 %d 字节", k, buf.Len())
		}
	}
}

// TestFullKeySequenceLaunchesHighlighted 端到端驱动按键序列：
// ↓ ↓ Enter 必须启动排在第三位的 profile，且启动前把终端交还（先 restore 再 launch）。
func TestFullKeySequenceLaunchesHighlighted(t *testing.T) {
	m := testMenu(t)
	var buf bytes.Buffer
	m.out = &buf
	m.width, m.height = 80, 24
	m.keys = newKeyReaderFrom(strings.NewReader("\x1b[B\x1b[B\r"))

	var order []string
	var launched Profile
	m.restoreFn = func() { order = append(order, "restore") }
	m.launchFn = func(p Profile) error {
		order = append(order, "launch:"+p.Name)
		launched = p
		return nil
	}
	m.reenterFn = func() (func(), bool, bool) {
		order = append(order, "reenter")
		return func() {}, true, true
	}

	for i := 0; i < 12; i++ {
		if m.handleIntent(m.handleKey(m.keys.read())) {
			break
		}
	}

	if launched.Name != "go" {
		t.Fatalf("期望启动 go（第 3 项），实际 %q", launched.Name)
	}
	var seq []string
	for _, s := range order {
		if s == "restore" || strings.HasPrefix(s, "launch:") || s == "reenter" {
			seq = append(seq, s)
		}
	}
	want := "restore,launch:go,reenter"
	if strings.Join(seq, ",") != want {
		t.Errorf("终端交还顺序应为 %q，实际 %q", want, strings.Join(seq, ","))
	}
}

// TestLaunchFailureKeepsMenuUsable 断言 pi 启动失败后菜单仍可用（不吞错误、不退出）。
func TestLaunchFailureKeepsMenuUsable(t *testing.T) {
	m := testMenu(t)
	var buf bytes.Buffer
	m.out = &buf
	m.width, m.height = 80, 24
	m.restoreFn = func() {}
	m.launchFn = func(Profile) error { return fmt.Errorf("模拟启动失败") }
	m.reenterFn = func() (func(), bool, bool) { return func() {}, true, true }

	if done := m.handleIntent(intentLaunch); done {
		t.Fatal("启动失败不应结束菜单循环")
	}
	if !strings.Contains(m.status, "模拟启动失败") {
		t.Errorf("状态行应显示启动错误，实际 %q", m.status)
	}
}

// TestReenterFailureEndsLoop 断言 pi 退出后终端已不可用时就结束循环，不硬撑。
func TestReenterFailureEndsLoop(t *testing.T) {
	m := testMenu(t)
	var buf bytes.Buffer
	m.out = &buf
	m.width, m.height = 80, 24
	m.restoreFn = func() {}
	m.launchFn = func(Profile) error { return nil }
	m.reenterFn = func() (func(), bool, bool) { return func() {}, false, false }
	if done := m.handleIntent(intentLaunch); !done {
		t.Error("终端不可用时应结束循环")
	}
}

// TestDrawClearsTrailingContent 断言重绘会清掉上一次的残留并回到原点，
// 否则旧字符会留在屏幕上（状态行从长变成短时最明显）。
func TestDrawClearsTrailingContent(t *testing.T) {
	m := testMenu(t)
	var buf bytes.Buffer
	m.out = &buf
	m.width, m.height = 80, 24
	m.status = "这一行很长很长很长很长很长很长很长很长"
	m.draw()
	buf.Reset()
	m.status = "短"
	m.draw()
	out := buf.String()
	if !strings.HasPrefix(out, ansiHome) {
		t.Error("重绘应先回到原点（CSI H）")
	}
	if !strings.Contains(out, ansiClearLine) {
		t.Error("重绘应逐行清行（CSI 2K）")
	}
	if !strings.HasSuffix(out, ansiClearToEnd) {
		t.Error("重绘末尾应清到屏幕结束（CSI J）")
	}
	if strings.Contains(out, "很长很长") {
		t.Error("旧状态行内容不应残留")
	}
}

// TestNewMenuWiresRestoreFn 钉住一个已修过的真实缺陷：
// 菜单必须持有归还终端的函数，否则 handleIntent 的交还被跳过，
// pi 会在 raw 模式下启动（输入模式未恢复）。用构造函数强制这个不变式。
func TestNewMenuWiresRestoreFn(t *testing.T) {
	cfg := &Config{Root: t.TempDir(), GlobalAgentDir: t.TempDir()}
	called := 0
	m := newMenu(cfg, nil, "d", func() { called++ })
	if m.restoreFn == nil {
		t.Fatal("newMenu 未挂上 restoreFn —— 这正是那个真实 bug")
	}
	m.restoreFn()
	if called != 1 {
		t.Errorf("restoreFn 未被正确保存，调用次数 %d", called)
	}
	// nil 也要能构造（不应 panic）
	n := newMenu(cfg, nil, "d", nil)
	if n.restoreFn == nil {
		t.Error("传入 nil 时应退化为 no-op 而不是留下 nil")
	}
	n.restoreFn()
}

// TestLaunchRestoresTerminalBeforeSpawn 是本 bug 的第二个回归测试：
// 必须**在启动 pi 之前**把终端还回去（否则 pi 在 raw 模式下启动，输入可能失效）。
// 除断言顺序外，还断言 restore 确实被调用过至少一次（曾因 menu 未挂 restoreFn
// 而整段被跳过，且没有任何测试发现）。
func TestLaunchRestoresTerminalBeforeSpawn(t *testing.T) {
	m := testMenu(t)
	var buf bytes.Buffer
	m.out = &buf
	m.width, m.height = 80, 24

	restoreCalls := 0
	m.restoreFn = func() { restoreCalls++ }
	restoredBeforeLaunch := false
	m.launchFn = func(Profile) error {
		restoredBeforeLaunch = restoreCalls > 0
		return nil
	}
	m.reenterFn = func() (func(), bool, bool) {
		return func() {}, true, true
	}

	m.cursor = 1
	if done := m.handleIntent(intentLaunch); done {
		t.Fatal("不应结束循环")
	}
	if restoreCalls == 0 {
		t.Fatal("启动 pi 前未归还终端控制台模式")
	}
	if !restoredBeforeLaunch {
		t.Fatal("终端交还发生在 launch 之后，pi 会在 raw 模式下启动")
	}
	// 交还 alt screen + 显示光标也必须在 launch 之前发生
	pre := buf.String()
	if !strings.Contains(pre, ansiLeaveAltScreen) {
		t.Error("启动前未退出备用屏")
	}
	if !strings.Contains(pre, ansiShowCursor) {
		t.Error("启动前未恢复光标显示")
	}
	// 回来后必须重新进入备用屏
	if !strings.Contains(pre, ansiEnterAltScreen) {
		t.Error("pi 退出后未重新进入备用屏")
	}
}

// TestHandleIntentReentersRawAfterLaunch 断言 pi 退出后 restoreFn 被换成新的还原函数，
// 否则后续退出时还原的是已经过期的控制台模式。
func TestHandleIntentReentersRawAfterLaunch(t *testing.T) {
	m := testMenu(t)
	var buf bytes.Buffer
	m.out = &buf
	m.width, m.height = 80, 24
	m.restoreFn = func() {}
	m.launchFn = func(Profile) error { return nil }
	newRestore := func() {}
	m.reenterFn = func() (func(), bool, bool) { return newRestore, true, true }

	m.handleIntent(intentLaunch)
	if m.restoreFn == nil {
		t.Fatal("reenter 后 restoreFn 不应为 nil")
	}
	// 比较函数地址，确认确实被替换（而不是仍指向旧的 no-op）
	if funcAddr(m.restoreFn) == funcAddr(func() {}) {
		t.Error("restoreFn 未被替换为新的还原函数")
	}
}

// --- 通过渲染层验证：菜单行里含光标标记，且高亮区段用反色包裹 ---

func TestHighlightUsesInverse(t *testing.T) {
	m := testMenu(t)
	m.width, m.height = 80, 24
	m.cursor = 0
	lines := m.render()
	var sel string
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "›") {
			sel = l
		}
	}
	if sel == "" {
		t.Fatal("未找到选中行")
	}
	if !strings.Contains(sel, "\x1b[7m") {
		t.Errorf("选中项未使用反色高亮：%q", sel)
	}
	if !strings.Contains(sel, "\x1b[0m") {
		t.Errorf("高亮未正确复位：%q", sel)
	}
}

// ---------- 路径展开 ---------- ----------

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("无法取 home 目录")
	}
	if got := expandPath("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("~ 展开错误：%s", got)
	}
	if got := expandPath("~"); got != home {
		t.Errorf("单独的 ~ 展开错误：%s", got)
	}
	abs := filepath.Join(t.TempDir(), "a", "b")
	if got := expandPath(abs + string(filepath.Separator) + "."); got != abs {
		t.Errorf("路径清理错误：%s != %s", got, abs)
	}
}

func TestHashBytes(t *testing.T) {
	if hashBytes([]byte("a")) == hashBytes([]byte("b")) {
		t.Error("不同内容不应得到相同哈希")
	}
	if hashBytes([]byte("a")) != hashBytes([]byte("a")) {
		t.Error("相同内容应得到相同哈希")
	}
}

// ---------- 启动环境（配置隔离的核心契约） ----------

// TestBuildLaunchEnvIsolated 断言隔离 profile 会显式注入 PI_CODING_AGENT_DIR。
func TestBuildLaunchEnvIsolated(t *testing.T) {
	os.Setenv(envAgentDir, `C:\stale\should\be\overwritten`)
	defer os.Unsetenv(envAgentDir)

	p := Profile{Name: "python", AgentDir: `C:\ProgramData\pi\python\agent`}
	env := buildLaunchEnv(p)

	if n := countKey(env, envAgentDir); n != 1 {
		t.Fatalf("期望恰好 1 个 %s，实际 %d", envAgentDir, n)
	}
	if got := lookupEnv(env, envAgentDir); got != p.AgentDir {
		t.Errorf("期望 %s，得到 %s", p.AgentDir, got)
	}
}

// TestBuildLaunchEnvGlobalClears 是关键回归断言：
// 从一个已设 PI_CODING_AGENT_DIR 的会话里选择「全局默认」，必须把该变量清掉，
// 否则 pi 仍会读到上一个 profile 的目录，隔离失效。
func TestBuildLaunchEnvGlobalClears(t *testing.T) {
	os.Setenv(envAgentDir, `C:\ProgramData\pi\python\agent`)
	defer os.Unsetenv(envAgentDir)

	env := buildLaunchEnv(Profile{Name: "default", Global: true, AgentDir: `C:\Users\x\.pi\agent`})
	if n := countKey(env, envAgentDir); n != 0 {
		t.Errorf("全局项必须清除 %s，实际仍有 %d 个：%v", envAgentDir, n, env)
	}
	// 其它环境变量必须保留
	os.Setenv("PI_ISOLATE_KEEP_ME", "1")
	defer os.Unsetenv("PI_ISOLATE_KEEP_ME")
	env = buildLaunchEnv(Profile{Global: true})
	if lookupEnv(env, "PI_ISOLATE_KEEP_ME") != "1" {
		t.Error("清理时误删了其它环境变量")
	}
}

// ---------- 启动骨架补齐 ----------

// TestMaybeSyncRepairsMissingSettings 断言启动路径会补齐被删掉的最小 settings.json，
// 同时不覆盖已存在的自定义 settings.json（保住 profile 的个性化）。
func TestMaybeSyncRepairsMissingSettings(t *testing.T) {
	globalAgent := setupGlobal(t, map[string]string{
		"models.json":   `{"providers":{"p":{"apiKey":"root"}}}`,
		"settings.json": `{"theme":"light","defaultModel":"m1","defaultProvider":"p1"}`,
	})
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{Root: root, GlobalAgentDir: globalAgent}
	p, err := newProfile(cfg, "python", func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}

	// 1) 用户自定义后，maybeSync 绝不能覆盖
	custom := []byte(`{"theme":"light","totallyMine":123}`)
	must(t, os.WriteFile(p.SettingsPath(), custom, 0o644))
	maybeSync(cfg, p, true)
	got, _ := os.ReadFile(p.SettingsPath())
	if string(got) != string(custom) {
		t.Errorf("maybeSync 覆盖了自定义 settings.json：%s", got)
	}

	// 2) 删掉 settings.json / sessions 后，maybeSync 应补齐
	must(t, os.Remove(p.SettingsPath()))
	must(t, os.RemoveAll(p.SessionsDir()))
	maybeSync(cfg, p, true)
	if !fileExists(p.SettingsPath()) {
		t.Error("maybeSync 未补齐缺失的 settings.json")
	}
	if !dirExists(p.SessionsDir()) {
		t.Error("maybeSync 未补齐 sessions 目录")
	}
	data, err := os.ReadFile(p.SettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "defaultProvider") {
		t.Errorf("补齐的最小 settings 缺少启动模型：%s", data)
	}

	// 3) 全局项不做任何事
	maybeSync(cfg, globalProfile(cfg), true)
}

// TestNewProfileDoesNotOverwriteExistingSettings 断言 new 不覆盖已存在的 settings.json。
// （该路径现在与新目录双保险：ensureAgentSkeleton 已有 exists 检查。）
func TestEnsureSkeletonKeepsExistingSettings(t *testing.T) {
	globalAgent := setupGlobal(t, map[string]string{"models.json": `{"a":1}`})
	root := filepath.Join(t.TempDir(), "pi")
	cfg := &Config{Root: root, GlobalAgentDir: globalAgent}
	agentDir := filepath.Join(root, "go", "agent")
	must(t, os.MkdirAll(agentDir, 0o755))
	custom := `{"mine":true}`
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(custom), 0o644))

	p := Profile{Name: "go", AgentDir: agentDir}
	must(t, ensureAgentSkeleton(cfg, p, func(string, ...any) {}))
	got, _ := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if string(got) != custom {
		t.Errorf("已存在的 settings.json 被覆盖：%s", got)
	}
}

// TestEnterRawTerminalDegradesSafely 断言：当 stdin 不是控制台时，
// enterRawTerminal 不能假称成功（否则菜单会等永远等不到的按键，把用户锁死），
// 而应返回 rawOK=false 让调用方降级到逐行菜单。
func TestEnterRawTerminalDegradesSafely(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { r.Close(); w.Close() }()

	orig := os.Stdin
	os.Stdin = r // 管道不是控制台，正是需要降级的场景
	defer func() { os.Stdin = orig }()

	restore, rawOK, vtOK := enterRawTerminal()
	defer restore()
	if rawOK || vtOK {
		t.Errorf("非控制台 stdin 下应降级，得到 rawOK=%v vtOK=%v", rawOK, vtOK)
	}
	if isInteractive() {
		t.Error("非控制台 stdin 下 isInteractive() 应为 false")
	}
}

// TestIsInteractiveAcceptsConsoleFile 反向验证：把 stdin 换成真正的控制台
// （CONIN$）时，isInteractive 必须为 true，否则真终端里将永远进不了菜单。
func TestIsInteractiveAcceptsConsoleFile(t *testing.T) {
	con, err := os.OpenFile("CONIN$", os.O_RDONLY, 0)
	if err != nil {
		t.Skip("无控制台可打开（无 GUI 会话）：" + err.Error())
	}
	defer con.Close()
	fi, err := con.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		t.Skip("CONIN$ 在当前会话不是字符设备")
	}

	orig := os.Stdin
	os.Stdin = con
	defer func() { os.Stdin = orig }()
	if !isInteractive() {
		t.Error("stdin 指向真实控制台时 isInteractive() 应为 true")
	}
}

// ---------- helpers ----------

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sprintf(f string, a ...any) string {
	return fmt.Sprintf(f, a...)
}

// funcAddr 取出函数的代码地址，用于判断一个函数变量是否被替换过。
func funcAddr(f func()) uintptr {
	return reflect.ValueOf(f).Pointer()
}
