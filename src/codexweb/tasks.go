package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Task struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prompt     string `json:"prompt"`
	Schedule   string `json:"schedule"`
	Enabled    bool   `json:"enabled"`
	ThreadID   string `json:"thread_id,omitempty"`
	LastRun    int64  `json:"last_run"`
	LastStatus string `json:"last_status"`
	LastOutput string `json:"last_output"`
	NextRun    int64  `json:"next_run"`
	Created    int64  `json:"created"`
}

// TaskRunLog 是定时任务单次执行归档，写入 web/tasklogs/<id>/<ts>.json。
type TaskRunLog struct {
	ID        string `json:"id"`
	TS        int64  `json:"ts"`
	Status    string `json:"status"`
	Output    string `json:"output"`
	Error     string `json:"error"`
	Prompt    string `json:"prompt"`
	Model     string `json:"model"`
	ThreadID  string `json:"thread_id,omitempty"`
	EventN    int    `json:"event_n"`
	Duration  int64  `json:"duration_ms"`
}

var (
	taskMu   sync.Mutex
	taskBusy = map[string]bool{}
)

const maxTaskLogs = 30

func (s *Server) taskLogDir(id string) string {
	return filepath.Join(s.home, "web", "tasklogs", id)
}

func (s *Server) taskLogPath(id string, ts int64) string {
	return filepath.Join(s.taskLogDir(id), fmt.Sprintf("%d.json", ts))
}

// writeTaskLog 写一条运行归档，并保留最近 maxTaskLogs 条。
func (s *Server) writeTaskLog(l TaskRunLog) error {
	if strings.TrimSpace(l.ID) == "" {
		return nil
	}
	dir := s.taskLogDir(l.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.taskLogPath(l.ID, l.TS), append(b, '\n'), 0o600); err != nil {
		return err
	}
	// 环形保留：只删最旧的，超过上限按时间戳清理。
	ents, _ := os.ReadDir(dir)
	var files []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e.Name())
		}
	}
	if len(files) > maxTaskLogs {
		// 名字是 Unix 毫秒时间戳，字典序即时间序。
		sortStrings(files)
		for _, name := range files[:len(files)-maxTaskLogs] {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	return nil
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// readTaskLogs 返回按时间倒序的运行归档。
func (s *Server) readTaskLogs(id string, limit int) []TaskRunLog {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	dir := s.taskLogDir(id)
	ents, _ := os.ReadDir(dir)
	names := []string{}
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sortStrings(names)
	// 倒序（最新在前）
	out := make([]TaskRunLog, 0, minInt(len(names), limit))
	for i := len(names) - 1; i >= 0 && len(out) < limit; i-- {
		b, err := os.ReadFile(filepath.Join(dir, names[i]))
		if err != nil {
			continue
		}
		var l TaskRunLog
		if json.Unmarshal(stripBOM(b), &l) == nil {
			out = append(out, l)
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Server) loadTasks() []Task {
	taskMu.Lock()
	defer taskMu.Unlock()
	return s.loadTasksLocked()
}

func (s *Server) loadTasksLocked() []Task {
	b, err := os.ReadFile(s.taskPath)
	if err != nil {
		return []Task{}
	}
	var ts []Task
	if json.Unmarshal(stripBOM(b), &ts) != nil {
		return []Task{}
	}
	return ts
}

func (s *Server) saveTasksLocked(ts []Task) error {
	if err := os.MkdirAll(filepath.Dir(s.taskPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.taskPath, append(b, '\n'), 0o600)
}

func (s *Server) saveTasks(ts []Task) error {
	taskMu.Lock()
	defer taskMu.Unlock()
	return s.saveTasksLocked(ts)
}

// ---------- 计划解析 ----------

func parseDur(s string) (time.Duration, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, false
	}
	unit := s[len(s)-1]
	num := s[:len(s)-1]
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return 0, false
	}
	switch unit {
	case 's':
		return time.Duration(n) * time.Second, true
	case 'm':
		return time.Duration(n) * time.Minute, true
	case 'h':
		return time.Duration(n) * time.Hour, true
	case 'd':
		return time.Duration(n) * 24 * time.Hour, true
	}
	return 0, false
}

// nextRun 按 spec 算出 from 之后的下一次执行时间。
// 支持： every 30m / every 2h / 30m（默认分钟）/ hourly / daily 09:00 /
//        cron 分 时 日 月 周（* 与 */N 与固定数字）
func nextRun(spec string, from time.Time) (time.Time, bool) {
	sp := strings.TrimSpace(strings.ToLower(spec))
	if sp == "" {
		return time.Time{}, false
	}
	if strings.HasPrefix(sp, "every ") {
		if d, ok := parseDur(strings.TrimSpace(sp[6:])); ok {
			return from.Add(d), true
		}
		return time.Time{}, false
	}
	if sp == "hourly" {
		return from.Add(time.Hour), true
	}
	if strings.HasPrefix(sp, "daily ") {
		hm := strings.TrimSpace(sp[6:])
		parts := strings.Split(hm, ":")
		if len(parts) != 2 {
			return time.Time{}, false
		}
		h, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		m, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if e1 != nil || e2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return time.Time{}, false
		}
		next := time.Date(from.Year(), from.Month(), from.Day(), h, m, 0, 0, from.Location())
		if !next.After(from) {
			next = next.Add(24 * time.Hour)
		}
		return next, true
	}
	cron := sp
	if strings.HasPrefix(sp, "cron ") {
		cron = strings.TrimSpace(sp[5:])
	}
	f := strings.Fields(cron)
	if len(f) == 5 {
		return nextCronLite(f, from)
	}
	if d, ok := parseDur(sp); ok {
		return from.Add(d), true
	}
	return time.Time{}, false
}

func cronMatch(field string, v int) bool {
	field = strings.TrimSpace(field)
	if field == "*" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "*/") {
			if n, err := strconv.Atoi(part[2:]); err == nil && n > 0 && v%n == 0 {
				return true
			}
			continue
		}
		if strings.Contains(part, "-") {
			ab := strings.SplitN(part, "-", 2)
			a, e1 := strconv.Atoi(ab[0])
			b, e2 := strconv.Atoi(ab[1])
			if e1 == nil && e2 == nil && v >= a && v <= b {
				return true
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n == v {
			return true
		}
	}
	return false
}

// nextCronLite 按分钟粒度向后找最近一个匹配时刻（最多找 366 天）。
func nextCronLite(f []string, from time.Time) (time.Time, bool) {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.AddDate(1, 0, 0)
	for t.Before(limit) {
		if cronMatch(f[0], t.Minute()) && cronMatch(f[1], t.Hour()) &&
			cronMatch(f[2], t.Day()) && cronMatch(f[3], int(t.Month())) && cronMatch(f[4], int(t.Weekday())) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}

// ---------- HTTP ----------

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		ts := s.loadTasks()
		writeJSON(w, 200, map[string]any{"tasks": ts})
	case "POST":
		var in struct {
			Name     string `json:"name"`
			Prompt   string `json:"prompt"`
			Schedule string `json:"schedule"`
			Enabled  *bool  `json:"enabled"`
			ThreadID string `json:"thread_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if strings.TrimSpace(in.Prompt) == "" {
			writeJSON(w, 400, map[string]any{"error": "任务内容不能为空"})
			return
		}
		nr, ok := nextRun(in.Schedule, time.Now())
		if !ok {
			writeJSON(w, 400, map[string]any{"error": "时间计划格式不对，例如：every 30m / daily 09:00 / cron 0 9 * * *"})
			return
		}
		en := true
		if in.Enabled != nil {
			en = *in.Enabled
		}
		t := Task{
			ID:       fmt.Sprintf("t%d-%s", time.Now().Unix(), randHex(3)),
			Name:     firstNonEmpty(strings.TrimSpace(in.Name), truncate(strings.TrimSpace(in.Prompt), 24)),
			Prompt:   in.Prompt,
			Schedule: strings.TrimSpace(in.Schedule),
			Enabled:  en,
			ThreadID: in.ThreadID,
			NextRun:  nr.UnixMilli(),
			Created:  nowMS(),
		}
		ts := s.loadTasks()
		ts = append(ts, t)
		if err := s.saveTasks(ts); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, t)
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/task/")
	if rest == "" || strings.Contains(rest, "..") {
		writeJSON(w, 400, map[string]any{"error": "bad id"})
		return
	}
	// 运行日志归档：GET /api/task/<id>/logs 与 GET /api/task/<id>/logs/<ts>
	if r.Method == "GET" {
		if strings.HasSuffix(rest, "/logs") {
			id := strings.TrimSuffix(rest, "/logs")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, 400, map[string]any{"error": "bad id"})
				return
			}
			writeJSON(w, 200, map[string]any{"logs": s.readTaskLogs(id, 50)})
			return
		}
		if strings.HasSuffix(rest, "/logs/export") {
			id := strings.TrimSuffix(rest, "/logs/export")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, 400, map[string]any{"error": "bad id"})
				return
			}
			logs := s.readTaskLogs(id, 200)
			b, err := json.MarshalIndent(logs, "", "  ")
			if err != nil {
				writeJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=task-%s-logs.json", id))
			w.WriteHeader(200)
			w.Write(append(b, '\n'))
			return
		}
		if i := strings.LastIndex(rest, "/logs/"); i > 0 {
			id := rest[:i]
			tsStr := rest[i+len("/logs/"):]
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, 400, map[string]any{"error": "bad id"})
				return
			}
			ts, err := strconv.ParseInt(tsStr, 10, 64)
			if err != nil {
				writeJSON(w, 400, map[string]any{"error": "bad ts"})
				return
			}
			b, err := os.ReadFile(s.taskLogPath(id, ts))
			if err != nil {
				writeJSON(w, 404, map[string]any{"error": "log not found"})
				return
			}
			var l TaskRunLog
			if json.Unmarshal(stripBOM(b), &l) != nil {
				writeJSON(w, 500, map[string]any{"error": "bad log"})
				return
			}
			writeJSON(w, 200, l)
			return
		}
	}
	// 一键重跑：POST /api/task/<id>/rerun {prompt?}
	if strings.HasSuffix(rest, "/rerun") {
		id := strings.TrimSuffix(rest, "/rerun")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, 400, map[string]any{"error": "bad id"})
			return
		}
		if r.Method != "POST" {
			writeJSON(w, 405, map[string]any{"error": "method not allowed"})
			return
		}
		t, ok := s.findTask(id)
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "task not found"})
			return
		}
		var in struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		prompt := strings.TrimSpace(in.Prompt)
		if prompt == "" {
			prompt = t.Prompt
		}
		go s.runTaskWithPrompt(id, prompt)
		writeJSON(w, 200, map[string]any{"ok": true, "started": id})
		return
	}
	runNow := false
	id := rest
	if strings.HasSuffix(rest, "/run") {
		id = strings.TrimSuffix(rest, "/run")
		runNow = true
	}
	if strings.Contains(id, "/") {
		writeJSON(w, 400, map[string]any{"error": "bad id"})
		return
	}
	if runNow && r.Method == "POST" {
		if _, ok := s.findTask(id); !ok {
			writeJSON(w, 404, map[string]any{"error": "任务不存在"})
			return
		}
		go s.runTask(id)
		writeJSON(w, 200, map[string]any{"ok": true, "started": id})
		return
	}

	ts := s.loadTasks()
	idx := -1
	for i := range ts {
		if ts[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, 404, map[string]any{"error": "任务不存在"})
		return
	}
	switch r.Method {
	case "GET":
		writeJSON(w, 200, ts[idx])
	case "PATCH":
		var in struct {
			Name     *string `json:"name"`
			Prompt   *string `json:"prompt"`
			Schedule *string `json:"schedule"`
			Enabled  *bool   `json:"enabled"`
			ThreadID *string `json:"thread_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if in.Name != nil {
			ts[idx].Name = strings.TrimSpace(*in.Name)
		}
		if in.Prompt != nil && strings.TrimSpace(*in.Prompt) != "" {
			ts[idx].Prompt = *in.Prompt
		}
		if in.Schedule != nil {
			if nr, ok := nextRun(*in.Schedule, time.Now()); ok {
				ts[idx].Schedule = strings.TrimSpace(*in.Schedule)
				ts[idx].NextRun = nr.UnixMilli()
			}
		}
		if in.Enabled != nil {
			ts[idx].Enabled = *in.Enabled
			if *in.Enabled && ts[idx].NextRun == 0 {
				if nr, ok := nextRun(ts[idx].Schedule, time.Now()); ok {
					ts[idx].NextRun = nr.UnixMilli()
				}
			}
		}
		if in.ThreadID != nil {
			ts[idx].ThreadID = *in.ThreadID
		}
		s.saveTasks(ts)
		writeJSON(w, 200, ts[idx])
	case "DELETE":
		ts = append(ts[:idx], ts[idx+1:]...)
		s.saveTasks(ts)
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
	}
}

func (s *Server) findTask(id string) (Task, bool) {
	for _, t := range s.loadTasks() {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// ---------- 调度 ----------

func (s *Server) taskLoop() {
	for {
		time.Sleep(15 * time.Second)
		ts := s.loadTasks()
		changed := false
		now := time.Now()
		for i := range ts {
			if !ts[i].Enabled || strings.TrimSpace(ts[i].Schedule) == "" {
				continue
			}
			if ts[i].NextRun == 0 {
				if nr, ok := nextRun(ts[i].Schedule, now); ok {
					ts[i].NextRun = nr.UnixMilli()
					changed = true
				}
				continue
			}
			if ts[i].NextRun <= now.UnixMilli() {
				if nr, ok := nextRun(ts[i].Schedule, now); ok {
					ts[i].NextRun = nr.UnixMilli()
				} else {
					ts[i].NextRun = now.Add(time.Hour).UnixMilli()
				}
				ts[i].LastRun = now.UnixMilli()
				changed = true
				go s.runTask(ts[i].ID)
			}
		}
		if changed {
			s.saveTasks(ts)
		}
	}
}

// runTask 真正执行一次定时任务：把 prompt 交给 codex，结果追加进会话。
func (s *Server) runTask(id string) { s.runTaskWithPrompt(id, "") }

func (s *Server) runTaskWithPrompt(id, promptOverride string) {
	taskMu.Lock()
	if taskBusy[id] {
		taskMu.Unlock()
		return
	}
	taskBusy[id] = true
	taskMu.Unlock()
	defer func() { taskMu.Lock(); delete(taskBusy, id); taskMu.Unlock() }()

	t, ok := s.findTask(id)
	if !ok {
		return
	}
	prompt := t.Prompt
	if strings.TrimSpace(promptOverride) != "" {
		prompt = promptOverride
	}
	status := "ok"
	output := ""
	started := time.Now()
	var events []map[string]any
	threadID := ""
	defer func() {
		ts := s.loadTasks()
		for i := range ts {
			if ts[i].ID == id {
				ts[i].LastStatus = status
				ts[i].LastOutput = truncate(output, 400)
				ts[i].LastRun = nowMS()
				break
			}
		}
		s.saveTasks(ts)
		errText := ""
		if status != "ok" {
			errText = status
		}
		_ = s.writeTaskLog(TaskRunLog{
			ID:       id,
			TS:       started.UnixMilli(),
			Status:   status,
			Output:   output,
			Error:    errText,
			Prompt:   prompt,
			Model:    s.snapshot().Model,
			ThreadID: threadID,
			EventN:   len(events),
			Duration: time.Since(started).Milliseconds(),
		})
	}()

	var th *Thread
	if t.ThreadID != "" {
		if load, err := s.loadThread(t.ThreadID); err == nil {
			th = load
		}
	}
	if th == nil {
		th = s.newThread("[定时] " + t.Name)
		s.saveThread(th)
	}
	threadID = th.ID

	th.Messages = append(th.Messages, Msg{Role: "user", Text: "[定时任务] " + prompt, TS: nowMS()})
	th.Model = s.snapshot().Model
	s.saveThread(th)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	events = []map[string]any{}
	idx := map[string]int{}
	emit := func(ev map[string]any) {
		ty, _ := ev["type"].(string)
		it, ok := ev["item"].(map[string]any)
		if !ok {
			events = append(events, ev)
			return
		}
		if ty == "item.started" {
			return
		}
		evid, _ := it["id"].(string)
		if evid == "" {
			events = append(events, ev)
			return
		}
		if i, seen := idx[evid]; seen {
			events[i] = ev
		} else {
			idx[evid] = len(events)
			events = append(events, ev)
		}
	}

	err := s.runCodexImpl(ctx, th, prompt, nil, emit)
	text := ""
	for _, ev := range events {
		if it, ok := ev["item"].(map[string]any); ok {
			if it["type"] == "agent_message" {
				if tx, ok := it["text"].(string); ok && tx != "" {
					text = tx
				}
			}
		}
	}
	if err != nil {
		events = append(events, map[string]any{
			"type": "item.completed",
			"item": map[string]any{"id": "turn-error", "type": "error", "message": err.Error()},
		})
	}
	th.Messages = append(th.Messages, Msg{Role: "assistant", Text: text, Events: events, TS: nowMS()})
	s.saveThread(th)
	output = text
	if err != nil {
		status = "error: " + err.Error()
	}
}
