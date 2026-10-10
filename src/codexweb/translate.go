package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// 本文件把 Codex 发出的 Responses API 请求翻译成上游的 chat/completions，
// 再把 chat 的 SSE 流翻译回 Responses 事件。目的是让「只支持 chat/completions
// 的中转站」（如 New API 的 deepseek 渠道）也能被新版 Codex 使用。

type respReq struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions"`
	Input           []json.RawMessage `json:"input"`
	Tools           []json.RawMessage `json:"tools"`
	ToolChoice      json.RawMessage   `json:"tool_choice"`
	MaxOutputTokens *int              `json:"max_output_tokens"`
	Temperature     *float64          `json:"temperature"`
	Stream          bool              `json:"stream"`
}

type respItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Name    string          `json:"name"`
	Args    string          `json:"arguments"`
	CallID  string          `json:"call_id"`
	Output  json.RawMessage `json:"output"`
	Input   json.RawMessage `json:"input"`
	ID      string          `json:"id"`
	Action  json.RawMessage `json:"action"`
}

func rawToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		b, _ := json.Marshal(v)
		return string(b)
	}
	return string(raw)
}

func contentParts(raw json.RawMessage) (string, []any) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) != nil {
		return rawToText(raw), nil
	}
	var texts []string
	out := []any{}
	for _, p := range parts {
		ty, _ := p["type"].(string)
		switch ty {
		case "input_text", "output_text", "text", "summary_text", "reasoning_text":
			if tx, ok := p["text"].(string); ok {
				texts = append(texts, tx)
				out = append(out, map[string]any{"type": "text", "text": tx})
			}
		case "input_image":
			u, _ := p["image_url"].(string)
			if u == "" {
				u, _ = p["url"].(string)
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}})
		}
	}
	return strings.Join(texts, "\n"), out
}

func buildChatRequest(req respReq, model string) (map[string]any, map[string]bool, error) {
	customTools := map[string]bool{}
	messages := []any{}
	if strings.TrimSpace(req.Instructions) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.Instructions})
	}

	appendToolCall := func(id, name, args string) {
		if len(messages) > 0 {
			if last, ok := messages[len(messages)-1].(map[string]any); ok {
				if last["role"] == "assistant" {
					if tcs, ok := last["tool_calls"].([]any); ok {
						tcs = append(tcs, toolCallObj(id, name, args))
						last["tool_calls"] = tcs
						return
					}
				}
			}
		}
		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    nil,
			"tool_calls": []any{toolCallObj(id, name, args)},
		})
	}

	for _, raw := range req.Input {
		var it respItem
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		switch it.Type {
		case "message", "":
			role := it.Role
			switch role {
			case "developer", "system":
				role = "system"
			case "assistant":
				role = "assistant"
			default:
				role = "user"
			}
			text, parts := contentParts(it.Content)
			msg := map[string]any{"role": role}
			if len(parts) > 0 {
				msg["content"] = parts
			} else {
				msg["content"] = text
			}
			if strings.TrimSpace(text) == "" && len(parts) == 0 {
				continue
			}
			messages = append(messages, msg)
		case "function_call":
			appendToolCall(it.CallID, it.Name, it.Args)
		case "custom_tool_call":
			in := rawToText(it.Input)
			appendToolCall(it.CallID, it.Name, jsonString(map[string]any{"input": in}))
		case "local_shell_call":
			var act struct {
				Command []string `json:"command"`
			}
			json.Unmarshal(it.Action, &act)
			appendToolCall(it.CallID, "shell", jsonString(map[string]any{"command": act.Command}))
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": it.CallID,
				"content":      rawToText(it.Output),
			})
		case "reasoning":
			// 丢弃：chat 模型不接受 reasoning 输入
		default:
			// 未知类型忽略
		}
	}

	tools := []any{}
	for _, raw := range req.Tools {
		var t struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		switch t.Type {
		case "function":
			fn := map[string]any{"name": t.Name, "description": t.Description}
			if len(t.Parameters) > 0 && string(t.Parameters) != "null" {
				fn["parameters"] = t.Parameters
			} else {
				fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		case "custom":
			customTools[t.Name] = true
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name":        t.Name,
				"description": t.Description + "\n\n把要提交的完整内容作为字符串放进 input 字段。",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"input": map[string]any{"type": "string"}},
					"required":   []string{"input"},
				},
			}})
		}
	}

	out := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if len(tools) > 0 {
		out["tools"] = tools
		if tc := chatToolChoice(req.ToolChoice); tc != nil {
			out["tool_choice"] = tc
		}
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		out["max_tokens"] = *req.MaxOutputTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	return out, customTools, nil
}

func toolCallObj(id, name, args string) map[string]any {
	if id == "" {
		id = "call_" + randHex(6)
	}
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
}

func chatToolChoice(raw json.RawMessage) any {
	if len(raw) == 0 {
		return "auto"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return "auto"
		}
		return s
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Name != "" {
		return map[string]any{"type": "function", "function": map[string]any{"name": obj.Name}}
	}
	return "auto"
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func extractCustomInput(args string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(args), &obj) == nil {
		if s, ok := obj["input"].(string); ok {
			return s
		}
	}
	var s string
	if json.Unmarshal([]byte(args), &s) == nil {
		return s
	}
	return args
}

// ---------- SSE 输出 ----------

type emitter struct {
	w       http.ResponseWriter
	fl      http.Flusher
	id      string
	model   string
	created int64
	seq     int
	on      bool
	last    map[string]any
}

func (e *emitter) send(typ string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["type"] = typ
	fields["sequence_number"] = e.seq
	e.seq++
	if !e.on {
		if typ == "response.completed" || typ == "response.failed" {
			e.last = fields
		}
		return
	}
	b, _ := json.Marshal(fields)
	fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", typ, b)
	if e.fl != nil {
		e.fl.Flush()
	}
}

func (e *emitter) respObj(status string, output []any, usage map[string]any) map[string]any {
	if output == nil {
		output = []any{}
	}
	o := map[string]any{
		"id":         e.id,
		"object":     "response",
		"created_at": e.created,
		"status":     status,
		"model":      e.model,
		"output":     output,
	}
	if usage != nil {
		o["usage"] = usage
	}
	return o
}

func mapUsage(u map[string]any) map[string]any {
	if u == nil {
		return nil
	}
	get := func(k string) int {
		if v, ok := u[k].(float64); ok {
			return int(v)
		}
		return 0
	}
	in := get("prompt_tokens")
	out := get("completion_tokens")
	tot := get("total_tokens")
	if tot == 0 {
		tot = in + out
	}
	return map[string]any{
		"input_tokens":  in,
		"output_tokens": out,
		"total_tokens":  tot,
	}
}

func (s *Server) translateResponses(w http.ResponseWriter, r *http.Request, raw []byte) error {
	gw := s.snapshot()
	var req respReq
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"}})
		return nil
	}
	model := req.Model
	if model == "" {
		model = gw.Model
	}
	chatReq, customTools, err := buildChatRequest(req, model)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"}})
		return nil
	}
	body, _ := json.Marshal(chatReq)
	log.Printf("responses->chat model=%s msgs=%d", model, len(chatReq["messages"].([]any)))

	up, err := http.NewRequestWithContext(r.Context(), "POST", strings.TrimRight(gw.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": map[string]any{"message": err.Error()}})
		return nil
	}
	up.Header.Set("Content-Type", "application/json")
	up.Header.Set("Authorization", "Bearer "+gw.APIKey)
	up.Header.Set("Accept", "text/event-stream")
	resp, err := upClient.Do(up)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": map[string]any{"message": err.Error(), "type": "upstream_error"}})
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		writeJSON(w, resp.StatusCode, map[string]any{"error": map[string]any{"message": truncate(string(b), 500), "type": "upstream_error"}})
		return nil
	}

	fl, _ := w.(http.Flusher)
	em := &emitter{w: w, fl: fl, id: "resp_" + randHex(10), model: model, created: time.Now().Unix(), on: req.Stream}
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(200)
	}
	em.send("response.created", map[string]any{"response": em.respObj("in_progress", nil, nil)})
	em.send("response.in_progress", map[string]any{"response": em.respObj("in_progress", nil, nil)})

	type tcall struct {
		id, name, args string
	}
	toolCalls := map[int]*tcall{}
	toolOrder := []int{}
	var usage map[string]any
	var text strings.Builder
	textStarted := false
	textIndex := 0
	outIndex := 0
	msgID := "msg_" + em.id

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			if !textStarted {
				textStarted = true
				textIndex = outIndex
				outIndex++
				em.send("response.output_item.added", map[string]any{
					"output_index": textIndex,
					"item":         map[string]any{"id": msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}},
				})
				em.send("response.content_part.added", map[string]any{
					"item_id": msgID, "output_index": textIndex, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": ""},
				})
			}
			text.WriteString(d.Content)
			em.send("response.output_text.delta", map[string]any{
				"item_id": msgID, "output_index": textIndex, "content_index": 0, "delta": d.Content,
			})
		}
		for _, tc := range d.ToolCalls {
			c := toolCalls[tc.Index]
			if c == nil {
				c = &tcall{}
				toolCalls[tc.Index] = c
				toolOrder = append(toolOrder, tc.Index)
			}
			if tc.ID != "" {
				c.id = tc.ID
			}
			if tc.Function.Name != "" && c.name == "" {
				c.name = tc.Function.Name
			}
			c.args += tc.Function.Arguments
		}
	}

	outputs := []any{}
	if textStarted {
		full := text.String()
		item := map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": full}},
		}
		em.send("response.output_text.done", map[string]any{"item_id": msgID, "output_index": textIndex, "content_index": 0, "text": full})
		em.send("response.content_part.done", map[string]any{"item_id": msgID, "output_index": textIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": full}})
		em.send("response.output_item.done", map[string]any{"output_index": textIndex, "item": item})
		outputs = append(outputs, item)
	}
	for i, idx := range toolOrder {
		c := toolCalls[idx]
		if c == nil || c.name == "" {
			continue
		}
		id := c.id
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", em.id, i)
		}
		oidx := outIndex + i
		if customTools[c.name] {
			in := extractCustomInput(c.args)
			item := map[string]any{
				"id": "ctc_" + id, "type": "custom_tool_call", "call_id": id,
				"name": c.name, "input": in, "status": "completed",
			}
			em.send("response.output_item.added", map[string]any{"output_index": oidx, "item": map[string]any{
				"id": "ctc_" + id, "type": "custom_tool_call", "call_id": id, "name": c.name, "input": "", "status": "in_progress",
			}})
			em.send("response.output_item.done", map[string]any{"output_index": oidx, "item": item})
			outputs = append(outputs, item)
			continue
		}
		item := map[string]any{
			"id": "fc_" + id, "type": "function_call", "call_id": id,
			"name": c.name, "arguments": c.args, "status": "completed",
		}
		em.send("response.output_item.added", map[string]any{"output_index": oidx, "item": map[string]any{
			"id": "fc_" + id, "type": "function_call", "call_id": id, "name": c.name, "arguments": "", "status": "in_progress",
		}})
		em.send("response.function_call_arguments.delta", map[string]any{"item_id": "fc_" + id, "output_index": oidx, "delta": c.args})
		em.send("response.function_call_arguments.done", map[string]any{"item_id": "fc_" + id, "output_index": oidx, "arguments": c.args})
		em.send("response.output_item.done", map[string]any{"output_index": oidx, "item": item})
		outputs = append(outputs, item)
	}

	em.send("response.completed", map[string]any{"response": em.respObj("completed", outputs, mapUsage(usage))})
	if !req.Stream {
		if em.last != nil {
			writeJSON(w, 200, em.last["response"])
		} else {
			writeJSON(w, 200, em.respObj("completed", outputs, mapUsage(usage)))
		}
	}
	return nil
}
