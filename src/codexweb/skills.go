package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SkillInfo struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Desc      string `json:"desc"`
	Path      string `json:"path"`
	System    bool   `json:"system"`
	Protected bool   `json:"protected"`
	Files     int    `json:"files"`
	Size      int64  `json:"size"`
	HasSkill  bool   `json:"has_skill_md"`
}

// scanSkillDir 扫一层 skill 目录：每个子目录 = 一个 skill。
func scanSkillDir(root string, system bool) []SkillInfo {
	out := []SkillInfo{}
	ents, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(root, name)
		info := SkillInfo{Name: name, Title: name, Path: full, System: system, Protected: system}
		var files int
		var size int64
		filepath.Walk(full, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi == nil || fi.IsDir() {
				return nil
			}
			files++
			size += fi.Size()
			return nil
		})
		info.Files = files
		info.Size = size
		md := filepath.Join(full, "SKILL.md")
		if b, err := os.ReadFile(md); err == nil {
			info.HasSkill = true
			title, desc := parseSkillMD(string(b))
			if title != "" {
				info.Title = title
			}
			info.Desc = desc
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// parseSkillMD 从 SKILL.md 里抠出标题与一句话描述。
func parseSkillMD(s string) (string, string) {
	lines := strings.Split(s, "\n")
	title := ""
	desc := ""
	for i := 0; i < len(lines) && i < 60; i++ {
		ln := strings.TrimSpace(lines[i])
		low := strings.ToLower(ln)
		if strings.HasPrefix(low, "description:") && desc == "" {
			desc = strings.TrimSpace(strings.Trim(strings.TrimSpace(ln[len("description:"):]), `"'`))
		}
		if strings.HasPrefix(low, "name:") && title == "" {
			title = strings.TrimSpace(strings.Trim(strings.TrimSpace(ln[len("name:"):]), `"'`))
		}
		if title == "" && strings.HasPrefix(ln, "# ") {
			title = strings.TrimSpace(ln[2:])
		}
	}
	// YAML 块标量（description: > / |）本身不是描述正文，交给下面按行兜底
	switch desc {
	case ">", "|", ">-", "|-", ">+", "|+":
		desc = ""
	}
	if desc == "" {
		for i := 0; i < len(lines) && i < 80; i++ {
			ln := strings.TrimSpace(lines[i])
			if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "---") || strings.HasPrefix(ln, ">") ||
				strings.HasPrefix(ln, "|") || strings.HasPrefix(ln, "```") ||
				strings.HasPrefix(ln, "name:") || strings.HasPrefix(ln, "description:") ||
				strings.HasPrefix(ln, "license:") || strings.HasPrefix(ln, "allowed-tools:") ||
				strings.HasPrefix(ln, "version:") || strings.HasPrefix(ln, "author:") {
				continue
			}
			desc = ln
			break
		}
	}
	if len([]rune(desc)) > 140 {
		desc = string([]rune(desc)[:140]) + "…"
	}
	return title, desc
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	user := scanSkillDir(s.skillDir, false)
	sys := scanSkillDir(filepath.Join(s.skillDir, ".system"), true)
	writeJSON(w, 200, map[string]any{
		"root":   s.skillDir,
		"count":  len(user) + len(sys),
		"skills": append(user, sys...),
	})
}

func safeSkillName(name string) bool {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") ||
		strings.Contains(name, "..") || strings.HasPrefix(name, ".") {
		return false
	}
	return true
}

func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/skill/")
	if !safeSkillName(name) {
		writeJSON(w, 400, map[string]any{"error": "非法 skill 名称"})
		return
	}
	target := filepath.Join(s.skillDir, name)
	if _, err := os.Stat(target); err != nil {
		writeJSON(w, 404, map[string]any{"error": "skill 不存在"})
		return
	}
	switch r.Method {
	case "DELETE":
		if err := os.RemoveAll(target); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "removed": name})
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}
