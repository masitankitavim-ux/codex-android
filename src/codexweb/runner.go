package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// benignNotice 识别 codex 输出里「不是错误、但会以 error 事件下发」的提示信息。
// 这类提示如果原样转发，前端会渲染成红色失败卡片，误导用户。
func benignNotice(ev map[string]any) string {
	it, ok := ev["item"].(map[string]any)
	if !ok {
		return ""
	}
	if t, _ := it["type"].(string); t != "error" {
		return ""
	}
	msg, _ := it["message"].(string)
	for _, p := range []string{
		"not found. Defaulting to fallback metadata", // 自定义模型名没有内置元数据
		"is ignored.",                  // 未知配置项
		"Shell snapshot not supported", // 非 bash/zsh 的 shell
	} {
		if strings.Contains(msg, p) {
			return msg
		}
	}
	return ""
}

// runCodexImpl 启动 `codex exec --json`，把 JSONL 事件实时转发出去。
func (s *Server) runCodexImpl(ctx context.Context, t *Thread, prompt string, emit func(map[string]any)) error {
	gw := s.snapshot()
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	if t.SessionID != "" {
		args = append(args, "resume", t.SessionID)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, s.codexBin, args...)
	cmd.Dir = firstNonEmpty(t.Cwd, gw.Workspace)
	cmd.Env = append(os.Environ(),
		"CODEX_HOME="+s.home,
		"HOME="+s.home,
		"TMPDIR="+s.home+"/tmp",
	)
	cmd.Stdin = strings.NewReader("")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ty, _ := ev["type"].(string); ty == "thread.started" {
			if id, ok := ev["thread_id"].(string); ok && id != "" {
				t.SessionID = id
			}
		}
		if benign := benignNotice(ev); benign != "" {
			log.Printf("codex notice (suppressed): %s", benign)
			continue
		}
		emit(ev)
	}
	if werr := cmd.Wait(); werr != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = werr.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
