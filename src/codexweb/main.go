package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed ui/index.html
var uiHTML []byte

type Gateway struct {
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Model        string `json:"model"`
	UpstreamWire string `json:"upstream_wire"`
	Token        string `json:"token"`
	Workspace    string `json:"workspace"`
	Listen        string `json:"listen"`
	Theme         string `json:"theme"`
	SandboxMode   string `json:"sandbox_mode"`
	ApprovalPolicy string `json:"approval_policy"`
}

type Server struct {
	home     string
	codexBin string
	gwPath   string
	thDir    string
	taskPath string
	skillDir string
	mu       sync.RWMutex
	gw       Gateway
}

type Msg struct {
	Role   string           `json:"role"`
	Text   string           `json:"text"`
	Images []string         `json:"images,omitempty"`
	Events []map[string]any `json:"events,omitempty"`
	TS     int64            `json:"ts"`
}

type Thread struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
	Messages  []Msg  `json:"messages"`
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// stripBOM 去掉 UTF-8 BOM，避免外部编辑器或脚本写出的 gateway.json 解析失败。
func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

func nowMS() int64 { return time.Now().UnixMilli() }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (s *Server) loadGateway() {
	def := Gateway{
		BaseURL:      "",
		APIKey:       "",
		Model:        "",
		UpstreamWire: "chat",
		Token:        randHex(12),
		Workspace:    filepath.Join(s.home, "workspace"),
		Listen:         "0.0.0.0:8787",
		Theme:          "auto",
		SandboxMode:    "danger-full-access",
		ApprovalPolicy: "never",
	}
	if b, err := os.ReadFile(s.gwPath); err == nil {
		var g Gateway
		if json.Unmarshal(stripBOM(b), &g) == nil {
			if g.BaseURL == "" {
				g.BaseURL = def.BaseURL
			}
			if g.Model == "" {
				g.Model = def.Model
			}
			if g.UpstreamWire == "" {
				g.UpstreamWire = def.UpstreamWire
			}
			if g.Token == "" {
				g.Token = def.Token
			}
			if g.Workspace == "" {
				g.Workspace = def.Workspace
			}
			if g.Listen == "" {
				g.Listen = def.Listen
			}
			if g.Theme == "" {
				g.Theme = def.Theme
			}
			if g.SandboxMode == "" {
				g.SandboxMode = def.SandboxMode
			}
			if g.ApprovalPolicy == "" {
				g.ApprovalPolicy = def.ApprovalPolicy
			}
			s.gw = g
			return
		}
		// 文件存在但解析失败：绝不静默覆盖用户配置，先留证据再回退默认值。
		_ = os.WriteFile(s.gwPath+".bad", b, 0o600)
		log.Printf("gateway.json 解析失败，已备份为 %s.bad；本次使用默认配置，请检查该文件", s.gwPath)
	}
	s.gw = def
}

func (s *Server) saveGateway() error {
	s.mu.RLock()
	b, err := json.MarshalIndent(s.gw, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(s.gwPath, append(b, '\n'), 0o600)
}

func (s *Server) snapshot() Gateway {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gw
}

func (s *Server) port() string {
	_, p, err := splitHostPort(s.snapshot().Listen)
	if err != nil || p == "" {
		return "8787"
	}
	return p
}

func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", fmt.Errorf("no port")
	}
	return addr[:i], addr[i+1:], nil
}

// syncCodexConfig 让 codex 指向本机网关；上游地址/密钥只存在 gateway.json 里。
func (s *Server) syncCodexConfig() error {
	gw := s.snapshot()
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(gw.Workspace, 0o755); err != nil {
		return err
	}
	model := gw.Model
	if model == "" {
		// 还没配置时给个占位，避免 codex 因为空 model 直接起不来；
		// 真正的模型名由用户在网上「设置」里选。
		model = "unset"
	}
	cfg := fmt.Sprintf(`# 由 codexweb 自动生成，请勿手工修改。
# 上游接口 / 密钥 / 模型请改 %s，或用网页界面 / codexctl。
model = %s
model_provider = "codexweb"
model_context_window = 131072
approval_policy = "%s"
sandbox_mode = "%s"

[model_providers.codexweb]
name = "CodexWeb local gateway"
base_url = "http://127.0.0.1:%s/v1"
wire_api = "responses"
requires_openai_auth = true
`, s.gwPath, tomlQuote(model), gw.ApprovalPolicy, gw.SandboxMode, s.port())
	if err := os.WriteFile(filepath.Join(s.home, "config.toml"), []byte(cfg), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.home, "auth.json"), []byte("{\"OPENAI_API_KEY\":\"codexweb-local\"}\n"), 0o600)
}

func tomlQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---------- auth ----------

func (s *Server) authed(r *http.Request) bool {
	gw := s.snapshot()
	tok := r.Header.Get("X-Codexweb-Token")
	if tok == "" {
		tok = r.URL.Query().Get("t")
	}
	if tok == "" {
		if c, err := r.Cookie("codexweb_token"); err == nil {
			tok = c.Value
		}
	}
	if tok == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			tok = strings.TrimPrefix(a, "Bearer ")
		}
	}
	return tok != "" && tok == gw.Token
}

func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// ---------- threads ----------

func (s *Server) threadPath(id string) string {
	return filepath.Join(s.thDir, id+".json")
}

func (s *Server) loadThread(id string) (*Thread, error) {
	b, err := os.ReadFile(s.threadPath(id))
	if err != nil {
		return nil, err
	}
	var t Thread
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Server) saveThread(t *Thread) error {
	t.Updated = nowMS()
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.threadPath(t.ID), b, 0o600)
}

func (s *Server) newThread(title string) *Thread {
	gw := s.snapshot()
	return &Thread{
		ID:      fmt.Sprintf("%d-%s", time.Now().Unix(), randHex(4)),
		Title:   firstNonEmpty(title, "新对话"),
		Created: nowMS(),
		Cwd:     gw.Workspace,
		Model:   gw.Model,
	}
}

func (s *Server) listThreads() []map[string]any {
	out := []map[string]any{}
	ents, _ := os.ReadDir(s.thDir)
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, err := s.loadThread(id)
		if err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id": t.ID, "title": t.Title, "updated": t.Updated,
			"created": t.Created, "count": len(t.Messages),
		})
	}
	return out
}

// ---------- main ----------

func main() {
	home := flag.String("home", envOr("CODEX_HOME", "/data/adb/codex"), "codex home")
	codexBin := flag.String("codex", "/system/bin/codex-bin", "codex binary")
	addr := flag.String("addr", "", "listen address override")
	flag.Parse()

	s := &Server{
		home:     *home,
		codexBin: *codexBin,
		gwPath:   filepath.Join(*home, "gateway.json"),
		thDir:    filepath.Join(*home, "web", "threads"),
		taskPath: filepath.Join(*home, "web", "tasks.json"),
		skillDir: filepath.Join(*home, "skills"),
	}
	if err := os.MkdirAll(s.thDir, 0o755); err != nil {
		log.Fatalf("mkdir threads: %v", err)
	}
	s.loadGateway()
	if err := s.syncCodexConfig(); err != nil {
		log.Printf("syncCodexConfig: %v", err)
	}
	if err := s.saveGateway(); err != nil {
		log.Printf("saveGateway: %v", err)
	}

	go s.taskLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})
	mux.HandleFunc("/api/state", s.guard(s.handleState))
	mux.HandleFunc("/api/settings", s.guard(s.handleSettings))
	mux.HandleFunc("/api/models", s.guard(s.handleModels))
	mux.HandleFunc("/api/threads", s.guard(s.handleThreads))
	mux.HandleFunc("/api/thread/", s.guard(s.handleThread))
	mux.HandleFunc("/api/chat", s.guard(s.handleChat))
	mux.HandleFunc("/api/upload", s.guard(s.handleUpload))
	mux.HandleFunc("/api/files", s.guard(s.handleFiles))
	mux.HandleFunc("/api/file", s.guard(s.handleFile))
	mux.HandleFunc("/api/file/save", s.guard(s.handleFileSave))
	mux.HandleFunc("/api/mkdir", s.guard(s.handleMkdir))
	mux.HandleFunc("/api/rename", s.guard(s.handleRename))
	mux.HandleFunc("/api/search", s.guard(s.handleSearch))
	mux.HandleFunc("/api/skills", s.guard(s.handleSkills))
	mux.HandleFunc("/api/skill/", s.guard(s.handleSkill))
	mux.HandleFunc("/api/tasks", s.guard(s.handleTasks))
	mux.HandleFunc("/api/task/", s.guard(s.handleTask))
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/models", s.handleUpstreamModels)

	listen := s.snapshot().Listen
	if *addr != "" {
		listen = *addr
	}
	log.Printf("codexweb on %s  home=%s  token=%s", listen, s.home, s.snapshot().Token)
	srv := &http.Server{Addr: listen, Handler: mux}
	log.Fatal(srv.ListenAndServe())
}
