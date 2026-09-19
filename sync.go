package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SyncFamily 描述一类从全局 agent 目录同步到各 profile 的文件。
//
// 默认只同步 models.json —— 这是「同步模型配置到所有配置」这一需求的最小集。
// 其余内容（settings.json / trust.json / auth.json …）刻意不同步：
// 它们属于各 profile 自己的可自定义状态，反复同步会覆盖 profile 内的改动
// （实测：同步 settings.json 会把全局的 packages 列表带进去，导致每个 profile
// 各自 npm 安装一份扩展）。需要更多同步项时用配置文件的 syncFiles 显式追加。
type SyncFamily struct {
	Desc   string
	Glob   string
	Needed bool // 缺失时视为异常（只有 models.json 属于此类）
}

const modelsFileName = "models.json"

// syncFamilies 由配置推导同步项清单。
func syncFamilies(cfg *Config) []SyncFamily {
	patterns := cfg.SyncFiles
	if len(patterns) == 0 {
		patterns = []string{modelsFileName}
	}
	out := make([]SyncFamily, 0, len(patterns))
	seen := map[string]bool{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		base := filepath.Base(p)
		desc := "附加配置"
		if strings.EqualFold(base, modelsFileName) {
			desc = "模型配置"
		}
		out = append(out, SyncFamily{Desc: desc, Glob: p, Needed: strings.EqualFold(base, modelsFileName)})
	}
	if len(out) == 0 {
		out = append(out, SyncFamily{Desc: "模型配置", Glob: modelsFileName, Needed: true})
	}
	return out
}

// SyncResult 记录单个 profile × 单个文件的同步结果。
type SyncResult struct {
	Profile string
	Desc    string
	From    string
	To      string
	Action  string // written / unchanged / stale / missing
	Err     error
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeFileAtomic 原子写入（先写同目录临时文件再 rename，避免 pi 读到半个文件）。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pi-isolate-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后这里是 no-op
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// minimalSettings 生成新 profile 的最小 settings.json。
// 刻意不含 packages：新 profile 不继承全局扩展，避免每个 profile 各装一份。
func minimalSettings(cfg *Config) map[string]any {
	min := map[string]any{"theme": "dark"}
	if sh := existingShell(); sh != "" {
		min["shellPath"] = sh
	}
	globalSettings := filepath.Join(cfg.GlobalAgentDir, "settings.json")
	if gs, err := readJSONObject(globalSettings); err == nil {
		// models.json 已同步，带上启动模型让新 profile 开箱即用（之后可自行改）
		for _, k := range []string{"defaultProvider", "defaultModel", "defaultThinkingLevel"} {
			if v, ok := gs[k]; ok {
				min[k] = v
			}
		}
	}
	return min
}

// existingShell 返回本机可用的 pwsh 路径（供 pi 的 powershell 工具使用）。
func existingShell() string {
	for _, cand := range []string{"pwsh.exe", `C:\Program Files\PowerShell\7\pwsh.exe`} {
		if p := lookPath(cand); p != "" && fileExists(p) {
			return p
		}
	}
	return ""
}

// ensureAgentSkeleton 创建 sessions 目录，并在 settings.json 缺失时写入最小配置。
// 已存在的 settings.json 一律不覆盖（profile 内自定义优先）。
func ensureAgentSkeleton(cfg *Config, p Profile, log func(string, ...any)) error {
	if err := os.MkdirAll(p.SessionsDir(), 0o755); err != nil {
		return fmt.Errorf("创建 sessions 目录失败: %w", err)
	}
	if fileExists(p.SettingsPath()) {
		return nil
	}
	min := minimalSettings(cfg)
	data, err := json.MarshalIndent(min, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeFileAtomic(p.SettingsPath(), data); err != nil {
		return fmt.Errorf("写入最小 settings.json 失败: %w", err)
	}
	keys := make([]string, 0, len(min))
	for k := range min {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	log("  + %s  写入最小 settings.json（%s）", p.Name, strings.Join(keys, ", "))
	return nil
}

func readJSONObject(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// syncToProfile 把全局 agent 目录中命中的文件同步到单个 profile。
// check=true 时只比对不写入（用于 --check / list 状态显示）。
func syncToProfile(cfg *Config, p Profile, check bool) []SyncResult {
	var results []SyncResult
	if p.Global {
		return results // 全局项本身就是源
	}
	if err := os.MkdirAll(p.AgentDir, 0o755); err != nil {
		return append(results, SyncResult{Profile: p.Name, Action: "missing", Err: err})
	}
	for _, fam := range syncFamilies(cfg) {
		matches, _ := filepath.Glob(filepath.Join(cfg.GlobalAgentDir, fam.Glob))
		sort.Strings(matches)
		if len(matches) == 0 {
			if fam.Needed {
				results = append(results, SyncResult{
					Profile: p.Name, Desc: fam.Desc, Action: "missing",
					From: filepath.Join(cfg.GlobalAgentDir, fam.Glob),
					Err:  fmt.Errorf("全局目录中未找到 %s", fam.Glob),
				})
			}
			continue
		}
		for _, src := range matches {
			res := SyncResult{Profile: p.Name, Desc: fam.Desc, From: src,
				To: filepath.Join(p.AgentDir, filepath.Base(src))}
			srcData, err := os.ReadFile(src)
			if err != nil {
				res.Action, res.Err = "missing", err
				results = append(results, res)
				continue
			}
			dstData, err := os.ReadFile(res.To)
			switch {
			case err == nil && bytes.Equal(dstData, srcData):
				res.Action = "unchanged"
			case check:
				res.Action = "stale" // 只报告不写入
			default:
				if err := writeFileAtomic(res.To, srcData); err != nil {
					res.Action, res.Err = "missing", err
				} else {
					res.Action = "written"
				}
			}
			results = append(results, res)
		}
	}
	return results
}

// syncAll 同步所有已发现的 profile（不含全局项）。
func syncAll(cfg *Config, check bool, log func(string, ...any)) ([]SyncResult, error) {
	models := filepath.Join(cfg.GlobalAgentDir, modelsFileName)
	if !fileExists(models) {
		return nil, fmt.Errorf("全局模型配置不存在：%s", models)
	}
	profiles, err := discoverProfiles(cfg.Root)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("根目录 %s 下没有任何 profile（先用 new 命令创建）", cfg.Root)
	}
	log("全局源：%s", cfg.GlobalAgentDir)
	var all []SyncResult
	for _, p := range profiles {
		res := syncToProfile(cfg, p, check)
		all = append(all, res...)
		for _, r := range res {
			switch r.Action {
			case "written":
				log("  ✓ [%s] %s 已同步（%s）", r.Profile, filepath.Base(r.To), r.Desc)
			case "stale":
				log("  ! [%s] %s 与全局不一致（%s）", r.Profile, filepath.Base(r.To), r.Desc)
			case "unchanged":
				log("  · [%s] %s 已是最新", r.Profile, filepath.Base(r.To))
			case "missing":
				log("  × [%s] %s：%v", r.Profile, r.Desc, r.Err)
			}
		}
	}
	return all, nil
}

// checkDrift 返回需要关注的差异项（stale / missing）。
func checkDrift(results []SyncResult) []SyncResult {
	var bad []SyncResult
	for _, r := range results {
		if r.Action == "stale" || r.Action == "missing" {
			bad = append(bad, r)
		}
	}
	return bad
}

// driftProfiles 返回同步状态不一致的 profile 名（去重、排序）。
func driftProfiles(cfg *Config) []string {
	profiles, err := discoverProfiles(cfg.Root)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, p := range profiles {
		if len(checkDrift(syncToProfile(cfg, p, true))) > 0 {
			set[p.Name] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// driftSummary 给交互菜单用：一句话描述同步状态。
func driftSummary(cfg *Config) string {
	profiles, err := discoverProfiles(cfg.Root)
	if err != nil {
		return "同步状态未知：" + err.Error()
	}
	if len(profiles) == 0 {
		return "尚无 profile"
	}
	bad := driftProfiles(cfg)
	if len(bad) == 0 {
		return fmt.Sprintf("全部 %d 个 profile 的 models.json 与全局一致", len(profiles))
	}
	return fmt.Sprintf("%d/%d 个 profile 的 models.json 需同步：%s",
		len(bad), len(profiles), strings.Join(bad, ", "))
}

// newProfile 创建 profile：建目录、写最小 settings、同步模型配置。
func newProfile(cfg *Config, name string, log func(string, ...any)) (Profile, error) {
	if err := ValidateProfileName(name); err != nil {
		return Profile{}, err
	}
	models := filepath.Join(cfg.GlobalAgentDir, modelsFileName)
	if !fileExists(models) {
		return Profile{}, fmt.Errorf("全局模型配置不存在：%s", models)
	}
	p := Profile{
		Name:     name,
		Base:     filepath.Join(cfg.Root, name),
		AgentDir: filepath.Join(cfg.Root, name, "agent"),
	}
	if dirExists(p.Base) {
		return Profile{}, fmt.Errorf("profile %q 已存在：%s", name, p.Base)
	}
	if err := os.MkdirAll(p.AgentDir, 0o755); err != nil {
		return Profile{}, err
	}
	log("创建 %s", p.AgentDir)
	if err := ensureAgentSkeleton(cfg, p, log); err != nil {
		return Profile{}, err
	}
	for _, r := range syncToProfile(cfg, p, false) {
		switch r.Action {
		case "written":
			log("  ✓ 同步 %s（%s）", filepath.Base(r.To), r.Desc)
		case "missing":
			log("  × 同步 %s 失败：%v", filepath.Base(r.To), r.Err)
		}
	}
	return p, nil
}

func timestamp() string { return time.Now().Format("2006-01-02 15:04:05") }
