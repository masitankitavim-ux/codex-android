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

// benignPatterns 是 codex 会当成 error 事件上报、但其实只是提示的信息。
// 命中这些的行不是真失败，前端用浅色（黄色）提醒，不要染成红色失败。
var benignPatterns = []string{
	"not found. Defaulting to fallback metadata", // 自定义模型没有完整元数据
	"is ignored.",                                // 未知配置项
	"Shell snapshot not supported",               // 安卓没有 bash/zsh
	"not supported in this environment",          // seccomp/landlock 不可用
	"could not create PATH aliases",              // 只读文件系统，PATH 别名建不了
	"Read-only file system",
	"Failed to load skill",
	"failed to load skill",
	"missing yaml frontmatter",                       // 单个 skill 解析失败不影响本次对话
	"failed to parse",
	"no such file or directory",
	"command not found",
}

// isBenign 判断一条 error 文案是不是「非真失败」。
func isBenign(msg string) bool {
	m := strings.ToLower(msg)
	for _, p := range benignPatterns {
		if strings.Contains(m, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// markBenign 给 item 打上 benign 标记（前端据此用浅色提醒）。
func markBenign(it map[string]any) {
	if it == nil {
		return
	}
	t, _ := it["type"].(string)
	if t != "error" {
		return
	}
	msg, _ := it["message"].(string)
	if isBenign(msg) {
		it["benign"] = true
	}
}

// runCodexImpl 运行 `codex exec --json`，把 JSONL 事件实时转成回调。
func (s *Server) runCodexImpl(ctx context.Context, t *Thread, prompt string, images []string, emit func(map[string]any)) error {
	gw := s.snapshot()
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	if gw.SandboxMode != "" && gw.SandboxMode != "danger-full-access" {
		args = append(args, "-s", gw.SandboxMode)
	}
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
		if it, ok := ev["item"].(map[string]any); ok {
			markBenign(it)
		}
		emit(ev)
	}
	if werr := cmd.Wait(); werr != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = werr.Error()
		}
		// 只要不是真失败（例如 ctx 取消），就把 stderr 归一到最后一个非空行，避免糊一大片
		if n := strings.LastIndex(msg, "\n"); n >= 0 {
			msg = strings.TrimSpace(msg[n+1:])
		}
		if msg == "" {
			msg = "codex 进程异常退出"
		}
		log.Printf("codex stderr tail: %s", msg)
		if isBenign(msg) {
			// 非真失败：例如单个 skill 前文错，进程会以非 0 退出，但回复已生成，不能当失败。
			return nil
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
