package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxFileEditBytes = 8 << 20  // 编辑器保存单文件上限 8MB
	maxSearchBytes   = 2 << 20  // 搜索跳过超过 2MB 的文件
	maxSearchHits    = 200
)

type fileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Dir   bool   `json:"is_dir"`
	MTime int64  `json:"mtime"`
}

// resolvePath 把相对路径解析到 workspace 内，防止目录穿越。
func (s *Server) resolvePath(rel string) (string, error) {
	root := filepath.Clean(s.snapshot().Workspace)
	if root == "" {
		return "", fmt.Errorf("workspace 未配置")
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return root, nil
	}
	if strings.Contains(rel, "..") {
		return "", fmt.Errorf("非法路径")
	}
	abs := filepath.Join(root, rel)
	if !strings.HasPrefix(filepath.Clean(abs), root+string(os.PathSeparator)) && filepath.Clean(abs) != root {
		return "", fmt.Errorf("越界路径")
	}
	return filepath.Clean(abs), nil
}

func relOf(root, abs string) string {
	r := strings.TrimPrefix(filepath.Clean(abs), filepath.Clean(root)+string(os.PathSeparator))
	if r == filepath.Clean(abs) {
		return ""
	}
	return strings.ReplaceAll(r, string(os.PathSeparator), "/")
}

// GET /api/files?path=  列出目录
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	dir, err := s.resolvePath(rel)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		writeJSON(w, 404, map[string]any{"error": "目录不存在"})
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	out := make([]fileEntry, 0, len(ents))
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fileEntry{
			Name:  e.Name(),
			Path:  relOf(filepath.Clean(s.snapshot().Workspace), filepath.Join(dir, e.Name())),
			Size:  info.Size(),
			Dir:   e.IsDir(),
			MTime: info.ModTime().UnixMilli(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	root := filepath.Clean(s.snapshot().Workspace)
	writeJSON(w, 200, map[string]any{
		"root":   root,
		"path":   relOf(root, dir),
		"parent": relOf(root, filepath.Dir(dir)),
		"entries": out,
	})
}

// GET /api/file?path=           读取（?raw=1 纯文本 / ?download=1 附件下载）
// DELETE /api/file?path=[&dir=1] 删除文件或目录
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	abs, err := s.resolvePath(rel)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	switch r.Method {
	case "GET":
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			writeJSON(w, 404, map[string]any{"error": "文件不存在"})
			return
		}
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(abs)))
			w.Header().Set("Content-Type", "application/octet-stream")
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		http.ServeFile(w, r, abs)
	case "DELETE":
		if r.URL.Query().Get("dir") == "1" {
			err = os.RemoveAll(abs)
		} else {
			err = os.Remove(abs)
		}
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}

// POST /api/file  {path, content}  保存/新建文件
func (s *Server) handleFileSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	abs, err := s.resolvePath(in.Path)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if len(in.Content) > maxFileEditBytes {
		writeJSON(w, 400, map[string]any{"error": "文件过大（上限 8MB）"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := os.WriteFile(abs, []byte(in.Content), 0o644); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": in.Path})
}

// POST /api/mkdir {path}
func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	abs, err := s.resolvePath(in.Path)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/rename {from,to}
func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	from, err := s.resolvePath(in.From)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	to, err := s.resolvePath(in.To)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if err := os.Rename(from, to); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// GET /api/search?q=  在工作目录内做大小写不敏感的文本搜索
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 400, map[string]any{"error": "缺少关键词"})
		return
	}
	q = strings.ToLower(q)
	root := filepath.Clean(s.snapshot().Workspace)
	hits := []map[string]any{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(hits) >= maxSearchHits {
			if len(hits) >= maxSearchHits {
				return fmt.Errorf("stop")
			}
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxSearchBytes {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(b) {
			return nil
		}
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), q) {
				if len(line) > 400 {
					line = line[:400] + "..."
				}
				hits = append(hits, map[string]any{
					"path": relOf(root, path),
					"line": i + 1,
					"text": strings.TrimSpace(line),
				})
				if len(hits) >= maxSearchHits {
					break
				}
			}
		}
		return nil
	})
	writeJSON(w, 200, map[string]any{"hits": hits, "q": q})
}
