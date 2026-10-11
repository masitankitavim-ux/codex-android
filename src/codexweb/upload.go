package main

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxUploadBytes = 64 << 20 // 单次上传上限 64 MB

// handleUpload 把网页控制台上传的文件保存到 codex 工作目录，
// 让 codex 在后续会话里可以直接读/处理这些文件。
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	ws := s.snapshot().Workspace
	if strings.TrimSpace(ws) == "" {
		writeJSON(w, 500, map[string]any{"error": "workspace 未配置"})
		return
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		writeJSON(w, 500, map[string]any{"error": "创建工作目录失败：" + err.Error()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSON(w, 400, map[string]any{"error": "解析上传失败：" + err.Error()})
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeJSON(w, 400, map[string]any{"error": "没有收到文件（表单字段名应为 file）"})
		return
	}
	saved := make([]map[string]any, 0, len(files))
	for _, fh := range files {
		name := safeUploadName(fh.Filename)
		if name == "" {
			continue
		}
		dst, err := availableUploadPath(ws, name)
		if err != nil {
			continue
		}
		n, err := writeUploadFile(dst, fh)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": fmt.Sprintf("保存 %s 失败：%v", name, err)})
			return
		}
		saved = append(saved, map[string]any{
			"name": filepath.Base(dst),
			"path": dst,
			"size": n,
		})
	}
	if len(saved) == 0 {
		writeJSON(w, 400, map[string]any{"error": "没有可保存的文件（文件名非法）"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "workspace": ws, "files": saved})
}

// safeUploadName 只保留文件名，去掉任何目录成分，防路径穿越。
func safeUploadName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// availableUploadPath 若同名文件已存在，则自动加 -1/-2 后缀，避免覆盖。
func availableUploadPath(dir, name string) (string, error) {
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return dst, nil
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("无法为 %s 生成唯一文件名", name)
}

func writeUploadFile(dst string, fh *multipart.FileHeader) (int64, error) {
	src, err := fh.Open()
	if err != nil {
		return 0, err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}