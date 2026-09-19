package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GlobalProfileName 是内置的「全局默认」选项标识（不隔离，直接用全局 agent 目录）。
const GlobalProfileName = "default"

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Profile 是一个隔离配置（agent 配置目录 = <root>\<name>\agent）。
type Profile struct {
	Name     string // profile 名（目录名）
	Base     string // <root>\<name>
	AgentDir string // <root>\<name>\agent —— 传给 PI_CODING_AGENT_DIR
	Global   bool   // 是否为内置「全局默认」项
}

func (p Profile) ModelsPath() string   { return filepath.Join(p.AgentDir, "models.json") }
func (p Profile) SettingsPath() string { return filepath.Join(p.AgentDir, "settings.json") }
func (p Profile) SessionsDir() string  { return filepath.Join(p.AgentDir, "sessions") }

// ValidateProfileName 校验 profile 名是否可安全用作目录名。
func ValidateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile 名不能为空")
	}
	if strings.EqualFold(name, GlobalProfileName) {
		return fmt.Errorf("%q 是内置的全局默认项名称，请换一个", name)
	}
	if !profileNameRe.MatchString(name) {
		return fmt.Errorf("非法 profile 名 %q：只允许字母/数字/._-，且以字母或数字开头", name)
	}
	return nil
}

// globalProfile 返回内置的全局默认项。
func globalProfile(cfg *Config) Profile {
	return Profile{
		Name:     GlobalProfileName,
		Base:     filepath.Dir(cfg.GlobalAgentDir),
		AgentDir: cfg.GlobalAgentDir,
		Global:   true,
	}
}

// discoverProfiles 扫描 root 下的目录，返回按名称排序的 profile 列表（不含全局项）。
func discoverProfiles(root string) ([]Profile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Profile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue // 隐藏目录 / 保留目录（如 _shared）
		}
		if err := ValidateProfileName(name); err != nil {
			continue
		}
		base := filepath.Join(root, name)
		out = append(out, Profile{
			Name:     name,
			Base:     base,
			AgentDir: filepath.Join(base, "agent"),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// listProfiles 返回「全局默认项 + 已发现的 profile」。
func listProfiles(cfg *Config) ([]Profile, error) {
	found, err := discoverProfiles(cfg.Root)
	if err != nil {
		return nil, err
	}
	return append([]Profile{globalProfile(cfg)}, found...), nil
}

// findProfile 按名称查找 profile（支持 default 全局项）。
func findProfile(cfg *Config, name string) (Profile, error) {
	if strings.EqualFold(name, GlobalProfileName) {
		return globalProfile(cfg), nil
	}
	profiles, err := discoverProfiles(cfg.Root)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range profiles {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("未找到 profile %q（根目录：%s）", name, cfg.Root)
}
