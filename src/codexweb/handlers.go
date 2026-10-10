package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var upClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 180 * time.Second,
		ForceAttemptHTTP2:     true,
	},
}

var (
	verMu   sync.Mutex
	verVal  string
	verTime time.Time
)

func (s *Server) codexVersion() string {
	verMu.Lock()
	defer verMu.Unlock()
	if verVal != "" && time.Since(verTime) < 5*time.Minute {
		return verVal
	}
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.codexBin, "--version").Output()
	if err != nil {
		verVal = "unknown"
	} else {
		verVal = strings.TrimSpace(string(out))
	}
	verTime = time.Now()
	return verVal
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	gw := s.snapshot()
	hint := ""
	if len(gw.APIKey) > 12 {
		hint = gw.APIKey[:6] + "..." + gw.APIKey[len(gw.APIKey)-4:]
	} else if gw.APIKey != "" {
		hint = "***"
	}
	writeJSON(w, 200, map[string]any{
		"base_url":      gw.BaseURL,
		"model":         gw.Model,
		"upstream_wire": gw.UpstreamWire,
		"workspace":     gw.Workspace,
		"key_hint":      hint,
		"has_key":       gw.APIKey != "",
		"configured":    gw.BaseURL != "" && gw.APIKey != "" && gw.Model != "",
		"listen":        gw.Listen,
		"token":         gw.Token,
		"codex_version": s.codexVersion(),
		"threads":       s.listThreads(),
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL      *string `json:"base_url"`
		APIKey       *string `json:"api_key"`
		Model        *string `json:"model"`
		UpstreamWire *string `json:"upstream_wire"`
		Workspace    *string `json:"workspace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	s.mu.Lock()
	if in.BaseURL != nil && strings.TrimSpace(*in.BaseURL) != "" {
		s.gw.BaseURL = strings.TrimSpace(*in.BaseURL)
	}
	if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		s.gw.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if in.Model != nil && strings.TrimSpace(*in.Model) != "" {
		s.gw.Model = strings.TrimSpace(*in.Model)
	}
	if in.UpstreamWire != nil && *in.UpstreamWire != "" {
		s.gw.UpstreamWire = *in.UpstreamWire
	}
	if in.Workspace != nil && strings.TrimSpace(*in.Workspace) != "" {
		s.gw.Workspace = strings.TrimSpace(*in.Workspace)
	}
	s.mu.Unlock()
	if err := s.saveGateway(); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := s.syncCodexConfig(); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "model": s.snapshot().Model})
}

func (s *Server) fetchModels() ([]string, error) {
	gw := s.snapshot()
	url := strings.TrimRight(gw.BaseURL, "/") + "/models"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gw.APIKey)
	resp, err := upClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ids, err := s.fetchModels()
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"models": ids})
}

func (s *Server) handleThreads(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		writeJSON(w, 200, map[string]any{"threads": s.listThreads()})
	case "POST":
		var in struct {
			Title string `json:"title"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		gw := s.snapshot()
		t := &Thread{
			ID:      fmt.Sprintf("%d-%s", time.Now().Unix(), randHex(4)),
			Title:   firstNonEmpty(in.Title, "新对话"),
			Created: nowMS(),
			Cwd:     gw.Workspace,
			Model:   gw.Model,
		}
		if err := s.saveThread(t); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, t)
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}

func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/thread/")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, 400, map[string]any{"error": "bad id"})
		return
	}
	switch r.Method {
	case "GET":
		t, err := s.loadThread(id)
		if err != nil {
			writeJSON(w, 404, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, 200, t)
	case "DELETE":
		os.Remove(s.threadPath(id))
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	var in struct {
		ThreadID string `json:"thread_id"`
		Prompt   string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if strings.TrimSpace(in.Prompt) == "" {
		writeJSON(w, 400, map[string]any{"error": "empty prompt"})
		return
	}
	if gw0 := s.snapshot(); gw0.BaseURL == "" || gw0.APIKey == "" || gw0.Model == "" {
		writeJSON(w, 400, map[string]any{"error": "还没配置上游：点左下角「设置」，填好 接口地址 / API Key / 模型 再保存"})
		return
	}
	t, err := s.loadThread(in.ThreadID)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "thread not found"})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]any{"error": "no flusher"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}

	gw := s.snapshot()
	if t.Model == "" {
		t.Model = gw.Model
	}
	if t.Title == "新对话" || t.Title == "" {
		t.Title = truncate(strings.TrimSpace(in.Prompt), 40)
	}
	userMsg := Msg{Role: "user", Text: in.Prompt, TS: nowMS()}
	t.Messages = append(t.Messages, userMsg)
	s.saveThread(t)
	send(map[string]any{"type": "user_saved", "ts": userMsg.TS})

	events := []map[string]any{}
	emit := func(ev map[string]any) {
		events = append(events, ev)
		send(ev)
	}

	ctx := r.Context()
	err = s.runCodexImpl(ctx, t, in.Prompt, emit)

	assistantText := ""
	for _, ev := range events {
		if ty, _ := ev["type"].(string); ty == "item.completed" || ty == "item.updated" {
			if it, ok := ev["item"].(map[string]any); ok {
				if it["type"] == "agent_message" {
					if tx, ok := it["text"].(string); ok && tx != "" {
						assistantText = tx
					}
				}
			}
		}
	}
	t.Messages = append(t.Messages, Msg{Role: "assistant", Text: assistantText, Events: events, TS: nowMS()})
	s.saveThread(t)

	if err != nil {
		send(map[string]any{"type": "done", "error": err.Error(), "session_id": t.SessionID, "title": t.Title})
		return
	}
	send(map[string]any{"type": "done", "session_id": t.SessionID, "title": t.Title, "model": t.Model})
}

// ---------- LLM 网关 ----------

func (s *Server) handleUpstreamModels(w http.ResponseWriter, r *http.Request) {
	gw := s.snapshot()
	req, err := http.NewRequestWithContext(r.Context(), "GET", strings.TrimRight(gw.BaseURL, "/")+"/models", nil)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+gw.APIKey)
	resp, err := upClient.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Server) proxyUpstream(w http.ResponseWriter, r *http.Request, path string, body []byte) {
	gw := s.snapshot()
	req, err := http.NewRequestWithContext(r.Context(), "POST", strings.TrimRight(gw.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gw.APIKey)
	if a := r.Header.Get("Accept"); a != "" {
		req.Header.Set("Accept", a)
	}
	resp, err := upClient.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": map[string]any{"message": err.Error(), "type": "upstream_error"}})
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	s.proxyUpstream(w, r, "/chat/completions", body)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	gw := s.snapshot()
	if strings.EqualFold(gw.UpstreamWire, "responses") {
		s.proxyUpstream(w, r, "/responses", body)
		return
	}
	if err := s.translateResponses(w, r, body); err != nil {
		log.Printf("translate: %v", err)
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
