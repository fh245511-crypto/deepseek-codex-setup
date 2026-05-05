package main

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// ---- domain constants ----
const (

	defaultPort       = 8317
	defaultMaxBody    = 5 << 20
	defaultCacheSize  = 200
	defaultCacheTTL   = 30 * time.Minute
	defaultHTTPTimeout = 300 * time.Second
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

type DeepSeekConfig struct {
	BaseURL string            `yaml:"base_url"`
	APIKey  string            `yaml:"api_key"`
	Models  map[string]string `yaml:"models"`
}

type Config struct {
	Port     int            `yaml:"port"`
	APIKeys  []string       `yaml:"api_keys"`
	DeepSeek DeepSeekConfig `yaml:"deepseek"`
}

// ---------------------------------------------------------------------------
// OpenAI API types (subset)
// ---------------------------------------------------------------------------

// --- Responses API (Codex Desktop �?us) ---

type RespContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type RespInputItem struct {
	Type             string              `json:"type"`
	Role             string              `json:"role"`
	Content          []RespContentPart   `json:"content"`
	Summary          []RespOutputSummary `json:"summary,omitempty"`
	EncryptedContent string              `json:"encrypted_content,omitempty"`
	ID               string              `json:"id,omitempty"`
	CallID           string              `json:"call_id,omitempty"`
	Name             string              `json:"name,omitempty"`
	Arguments        string              `json:"arguments,omitempty"`
	Output           interface{}         `json:"output,omitempty"`
	Reasoning        *RespReasoning      `json:"reasoning,omitempty"`
}

// flattenOutput converts Output (string or array) to a plain string.
func flattenOutput(v interface{}) string {
	if v == nil { return "" }
	switch val := v.(type) {
	case string:
		return val
	case []string:
			return strings.Join(val, "\n")
		case []interface{}:
			var parts []string
		for _, e := range val {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Responses API tools: name/description/parameters at top level
// Chat Completions tools: nested under "function" key �?we convert in translateTools
type RespTool struct {
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type RespReasoning struct {
	Effort string `json:"effort"`
}

type RespRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions"`
	Input           []RespInputItem `json:"input"`
	Tools           []RespTool      `json:"tools"`
	Reasoning       *RespReasoning  `json:"reasoning,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Stream          bool            `json:"stream"`
}

// --- Chat Completions API (us �?DeepSeek) ---

type CCToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type CCToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function CCToolCallFunction `json:"function"`
}

type CCMessage struct {
	Role             string       `json:"role"`
	Content          string       `json:"content,omitempty"`
	ReasoningContent string       `json:"reasoning_content,omitempty"`
	ToolCallID       string       `json:"tool_call_id,omitempty"`
	ToolCalls        []CCToolCall `json:"tool_calls,omitempty"`
}

type CCToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type CCTool struct {
	Type     string         `json:"type"`
	Function CCToolFunction `json:"function"`
}

type CCRequest struct {
	Model          string      `json:"model"`
	Messages       []CCMessage `json:"messages"`
	Tools          []CCTool    `json:"tools,omitempty"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	MaxTokens      int         `json:"max_tokens,omitempty"`
	Temperature    *float64    `json:"temperature,omitempty"`
	TopP           *float64    `json:"top_p,omitempty"`
	Stream         bool        `json:"stream"`
	StreamOptions  *StreamOpts `json:"stream_options,omitempty"`
	ExtraBody      ExtraBody   `json:"extra_body"`
}

type StreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type ExtraBody struct {
	Thinking ThinkingConfig `json:"thinking"`
}

type ThinkingConfig struct {
	Type string `json:"type"`
}

// --- Responses API output (us �?Codex Desktop) ---

type RespOutputSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type RespOutputItem struct {
	ID               string              `json:"id"`
	Type             string              `json:"type"`
	EncryptedContent string              `json:"encrypted_content,omitempty"`
	Summary          []RespOutputSummary `json:"summary,omitempty"`
	Role             string              `json:"role,omitempty"`
	Content          []RespContentPart   `json:"content,omitempty"`
	Name             string              `json:"name,omitempty"`
	CallID           string              `json:"call_id,omitempty"`
	Arguments        string              `json:"arguments,omitempty"`
	Output           string              `json:"output,omitempty"`
}

type RespUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type RespResponse struct {
	ID                string           `json:"id"`
	Object            string           `json:"object"`
	CreatedAt         int64            `json:"created_at"`
	Status            string           `json:"status"`
	Model             string           `json:"model"`
	Output            []RespOutputItem `json:"output"`
	Usage             RespUsage        `json:"usage"`
	Error             *RespError       `json:"error,omitempty"`
	IncompleteDetails *string          `json:"incomplete_details,omitempty"`
}

type RespError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// --- Chat Completions response from DeepSeek ---

type CCUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type CCToolCallDeltaFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type CCToolCallDelta struct {
	Index    int                     `json:"index"`
	ID       string                  `json:"id,omitempty"`
	Type     string                  `json:"type,omitempty"`
	Function CCToolCallDeltaFunction `json:"function,omitempty"`
}

type CCDelta struct {
	ReasoningContent string            `json:"reasoning_content"`
	Content          string            `json:"content"`
	ToolCalls        []CCToolCallDelta `json:"tool_calls"`
}

type CCChoice struct {
	Index        int     `json:"index"`
	Delta        CCDelta `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type CCChunk struct {
	ID      string     `json:"id"`
	Object  string     `json:"object"`
	Choices []CCChoice `json:"choices"`
	Usage   *CCUsage   `json:"usage,omitempty"`
}

type CCMessageFull struct {
	Role             string       `json:"role"`
	Content          string       `json:"content"`
	ReasoningContent string       `json:"reasoning_content"`
	ToolCalls        []CCToolCall `json:"tool_calls"`
}

type CCChoiceFull struct {
	Index   int           `json:"index"`
	Message CCMessageFull `json:"message"`
}

type CCResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Model   string          `json:"model"`
	Choices []CCChoiceFull  `json:"choices"`
	Usage   CCUsage         `json:"usage"`
}

// --- SSE event types ---

type SSEEvent struct {
	Type     string      `json:"type"`
	Response interface{} `json:"response,omitempty"`
	Item     interface{} `json:"item,omitempty"`
	Delta    string      `json:"delta,omitempty"`
	ItemID   string      `json:"item_id,omitempty"`
	OutputIndex int      `json:"output_index,omitempty"`
}

// ---------------------------------------------------------------------------
// Translation
// ---------------------------------------------------------------------------

func translateModel(model string, models map[string]string) string {
	if mapped, ok := models[model]; ok {
		return mapped
	}
	return model
}

type cacheLookupFn func(string) (string, bool)

func translateMessages(r *RespRequest, lookup cacheLookupFn) []CCMessage {
	var msgs []CCMessage
	var pendingReasoning string
	var pendingToolCalls []CCToolCall // accumulate consecutive function_calls

	if r.Instructions != "" {
		msgs = append(msgs, CCMessage{Role: "system", Content: r.Instructions})
	}

	flushToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		msg := CCMessage{Role: "assistant", ToolCalls: pendingToolCalls}
		if pendingReasoning != "" {
			msg.ReasoningContent = pendingReasoning
		} else if len(pendingToolCalls) > 0 {
			// Try cache for the first call_id as fallback
			if cached, ok := lookup(pendingToolCalls[0].ID); ok {
				msg.ReasoningContent = cached
			}
		}
		msgs = append(msgs, msg)
		pendingToolCalls = nil
		pendingReasoning = ""
	}

	for _, item := range r.Input {
		switch item.Type {
		case "message":
			flushToolCalls()
			var texts []string
			for _, part := range item.Content {
				if part.Type == "input_text" {
					texts = append(texts, part.Text)
				}
			}
			role := item.Role
			if role == "developer" {
				role = "system"
			}
			msgs = append(msgs, CCMessage{Role: role, Content: strings.Join(texts, "\n")})

		case "reasoning":
			if len(item.Summary) > 0 {
				pendingReasoning = item.Summary[0].Text
			}

		case "function_call":
			pendingToolCalls = append(pendingToolCalls, CCToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: CCToolCallFunction{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})

		case "function_call_output":
			flushToolCalls()
			msgs = append(msgs, CCMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    flattenOutput(item.Output),
			})
		}
	}
	flushToolCalls()
	return msgs
}

func translateTools(r *RespRequest) []CCTool {
	if len(r.Tools) == 0 {
		return nil
	}
	var out []CCTool
	var skipped []string
	for _, t := range r.Tools {
		if t.Name == "" {
			skipped = append(skipped, fmt.Sprintf("type=%s(empty_name)", t.Type))
			continue
		}
		out = append(out, CCTool{
			Type: "function",
			Function: CCToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	if len(skipped) > 0 {
		log.Printf("[WARN] dropped %d tools: %v", len(skipped), skipped)
	}
	return out
}

func translateRequest(r *RespRequest, cfg DeepSeekConfig, lookup cacheLookupFn) CCRequest {
	upstreamModel := translateModel(r.Model, cfg.Models)

	req := CCRequest{
		Model:    upstreamModel,
		Messages: translateMessages(r, lookup),
		Tools:    translateTools(r),
		Stream:   r.Stream,
		ExtraBody: ExtraBody{
			Thinking: ThinkingConfig{Type: "enabled"},
		},
	}

	// Reasoning effort: Codex explicit > per-model default
	if r.Reasoning != nil && r.Reasoning.Effort != "" {
		req.ReasoningEffort = r.Reasoning.Effort
	} else {
		defEffort, _ := perModelDefaults(upstreamModel)
		if defEffort != "" {
			req.ReasoningEffort = defEffort
		}
	}

	// Max tokens: Codex explicit > per-model default
	if r.MaxOutputTokens > 0 {
		req.MaxTokens = r.MaxOutputTokens
	} else {
		_, defTokens := perModelDefaults(upstreamModel)
		if defTokens > 0 {
			req.MaxTokens = defTokens
		}
	}

	req.Temperature = r.Temperature
	req.TopP = r.TopP

	if r.Stream {
		req.StreamOptions = &StreamOpts{IncludeUsage: true}
	}

	return req
}

// ---------------------------------------------------------------------------
// Non-streaming response translation
// ---------------------------------------------------------------------------

func genID(prefix string) string {
	var b [8]byte
	rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func translateNonStream(ccResp *CCResponse, model string) RespResponse {
	now := time.Now().Unix()
	respID := genID("resp_")

	output := make([]RespOutputItem, 0)
	if len(ccResp.Choices) > 0 {
		msg := ccResp.Choices[0].Message
		if msg.ReasoningContent != "" {
			rsID := genID("rs_")
			output = append(output, RespOutputItem{
				ID:               rsID,
				Type:             "reasoning",
				EncryptedContent: "",
				Summary: []RespOutputSummary{
					{Type: "summary_text", Text: msg.ReasoningContent},
				},
			})
		}
		if msg.Content != "" {
			msgID := genID("msg_")
			output = append(output, RespOutputItem{
				ID:   msgID,
				Type: "message",
				Role: "assistant",
				Content: []RespContentPart{
					{Type: "output_text", Text: msg.Content},
				},
			})
		}
		for _, tc := range msg.ToolCalls {
			output = append(output, RespOutputItem{
				ID:        tc.ID,
				CallID:    tc.ID,
				Type:      "function_call",
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	return RespResponse{
		ID:        respID,
		Object:    "response",
		CreatedAt: now,
		Status:    "completed",
		Model:     model,
		Output:    output,
		Usage: RespUsage{
			InputTokens:  ccResp.Usage.PromptTokens,
			OutputTokens: ccResp.Usage.CompletionTokens,
			TotalTokens:  ccResp.Usage.TotalTokens,
		},
	}
}

// ---------------------------------------------------------------------------
// SSE streaming translation
// ---------------------------------------------------------------------------

type toolCallState struct {
	id        string
	name      string
	argsBuf   strings.Builder
	emitted   bool
	outputIdx int
}

func writeSSE(w http.ResponseWriter, event SSEEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[SSE_ERR] marshal %s: %v", event.Type, err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
}

func streamTranslate(ctx context.Context, w http.ResponseWriter, body io.Reader, model string, flusher http.Flusher, storeFn func(string, string)) {
	respID := genID("resp_")
	rsID := genID("rs_")
	msgID := genID("msg_")
	now := time.Now().Unix()
	var reasoningBuf, contentBuf strings.Builder
	startedContent := false
	startedToolCalls := false
	var usage *RespUsage

	toolCallStates := make(map[int]*toolCallState)
	nextOutputIdx := 1 // 0 is reasoning

	closeToolCall := func(tc *toolCallState) {
		if tc.emitted {
			id := tc.id
			if id == "" {
				id = genID("fc_")
				tc.id = id
			}
			if tc.argsBuf.Len() > 0 {
				writeSSE(w, SSEEvent{
					Type:   "response.function_call_arguments.done",
					Delta:  tc.argsBuf.String(),
					ItemID: id,
				})
			}
			writeSSE(w, SSEEvent{
				Type:        "response.output_item.done",
				OutputIndex: tc.outputIdx,
				Item: map[string]interface{}{
					"id": id, "type": "function_call",
					"name": tc.name, "arguments": tc.argsBuf.String(),
					"call_id": id, "status": "completed",
				},
			})
		}
	}

	closeOpenToolCalls := func() {
		for _, tc := range toolCallStates {
			closeToolCall(tc)
		}
	}

	// 1) response.created �?include key request fields Codex expects
	writeSSE(w, SSEEvent{
		Type: "response.created",
		Response: map[string]interface{}{
			"id": respID, "object": "response", "status": "in_progress",
			"model": model, "created_at": now, "output": []interface{}{},
			"background": false, "parallel_tool_calls": true,
		},
	})
	flusher.Flush()

	// 2) reasoning output_item
	writeSSE(w, SSEEvent{
		Type: "response.output_item.added",
		Item: map[string]interface{}{
			"id": rsID, "type": "reasoning", "encrypted_content": "", "summary": []interface{}{},
		},
		OutputIndex: 0,
	})
	flusher.Flush()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	chunkCount := 0

	for scanner.Scan() {
		// Check if client disconnected
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(line[5:])
		if dataStr == "[DONE]" {
			break
		}
		var chunk CCChunk
		if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
			continue
		}
		chunkCount++
		if len(chunk.Choices) == 0 {
			continue
		}

		if chunk.Usage != nil {
			usage = &RespUsage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
				TotalTokens:  chunk.Usage.TotalTokens,
			}
		}

		delta := chunk.Choices[0].Delta

		// --- Reasoning ---
		if delta.ReasoningContent != "" {
			closeOpenToolCalls()
			reasoningBuf.WriteString(delta.ReasoningContent)
			writeSSE(w, SSEEvent{
				Type:   "response.reasoning_text.delta",
				Delta:  delta.ReasoningContent,
				ItemID: rsID,
			})
		}

		// --- Tool calls ---
		for _, tcDelta := range delta.ToolCalls {
			state := toolCallStates[tcDelta.Index]
			if state == nil {
				// First tool call: close reasoning if not already done
				if !startedToolCalls && reasoningBuf.Len() > 0 {
					startedToolCalls = true
					writeSSE(w, SSEEvent{
						Type:   "response.reasoning_text.done",
						ItemID: rsID, Delta: reasoningBuf.String(),
					})
				}
				state = &toolCallState{outputIdx: nextOutputIdx}
				nextOutputIdx++
				toolCallStates[tcDelta.Index] = state
			}
			if tcDelta.ID != "" {
				state.id = tcDelta.ID
			}
			if tcDelta.Function.Name != "" {
				state.name = tcDelta.Function.Name
			}
			if !state.emitted {
				// Emit output_item.added for this function call
				if state.id == "" {
					state.id = genID("fc_")
				}
				writeSSE(w, SSEEvent{
					Type: "response.output_item.added",
					Item: map[string]interface{}{
						"id": state.id, "type": "function_call",
						"name": state.name, "arguments": "",
					},
					OutputIndex: state.outputIdx,
				})
				state.emitted = true
			}
			if tcDelta.Function.Arguments != "" {
				state.argsBuf.WriteString(tcDelta.Function.Arguments)
				writeSSE(w, SSEEvent{
					Type:   "response.function_call_arguments.delta",
					Delta:  tcDelta.Function.Arguments,
					ItemID: state.id,
				})
			}
		}

		// --- Content ---
		if delta.Content != "" {
			closeOpenToolCalls()
			if !startedContent {
				startedContent = true
				if reasoningBuf.Len() > 0 {
					writeSSE(w, SSEEvent{
						Type:   "response.reasoning_text.done",
						ItemID: rsID,
						Delta:  reasoningBuf.String(),
					})
				}
				writeSSE(w, SSEEvent{
					Type: "response.output_item.added",
					Item: map[string]interface{}{
						"id": msgID, "type": "message", "role": "assistant", "content": []interface{}{},
					},
					OutputIndex: nextOutputIdx,
				})
				writeSSE(w, SSEEvent{
					Type: "response.content_part.added",
					Item: map[string]interface{}{"id": msgID},
				})
			}
			contentBuf.WriteString(delta.Content)
			writeSSE(w, SSEEvent{
				Type:   "response.output_text.delta",
				Delta:  delta.Content,
				ItemID: msgID,
			})
		}
		flusher.Flush()
	}

	// Finalize open tool calls
	closeOpenToolCalls()

	// 3) Done events
	if reasoningBuf.Len() > 0 && !startedContent && len(toolCallStates) == 0 {
		writeSSE(w, SSEEvent{
			Type:   "response.reasoning_text.done",
			ItemID: rsID, Delta: reasoningBuf.String(),
		})
	}
	if startedContent {
		writeSSE(w, SSEEvent{
			Type:   "response.output_text.done",
			ItemID: msgID, Delta: contentBuf.String(),
		})
		writeSSE(w, SSEEvent{
			Type: "response.content_part.done",
			Item: map[string]interface{}{"id": msgID},
		})
		writeSSE(w, SSEEvent{
			Type: "response.output_item.done",
			Item: map[string]interface{}{"id": msgID, "type": "message", "role": "assistant"},
		})
	}
	flusher.Flush()

	// 4) Build completed response
	output := make([]RespOutputItem, 0)
	if reasoningBuf.Len() > 0 {
		output = append(output, RespOutputItem{
			ID: rsID, Type: "reasoning", EncryptedContent: "",
			Summary: []RespOutputSummary{{Type: "summary_text", Text: reasoningBuf.String()}},
		})
	}
	// Add tool calls to output in index order
	indices := make([]int, 0, len(toolCallStates))
	for idx := range toolCallStates {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		tc := toolCallStates[idx]
		if tc == nil {
			continue
		}
		output = append(output, RespOutputItem{
			ID:        tc.id,
			CallID:    tc.id,
			Type:      "function_call",
			Name:      tc.name,
			Arguments: tc.argsBuf.String(),
			
		})
	}
	if contentBuf.Len() > 0 {
		output = append(output, RespOutputItem{
			ID:   msgID,
			Type: "message",
			Role: "assistant",
			Content: []RespContentPart{
				{Type: "output_text", Text: contentBuf.String()},
			},
		})
	}
	u := RespUsage{}
	if usage != nil {
		u = *usage
	}

	// Check for scanner errors BEFORE sending completed
	scanErr := scanner.Err()
	if scanErr != nil {
		log.Printf("[STREAM_ERR] %v (chunks=%d)", scanErr, chunkCount)
	}
	log.Printf("[STREAM_DONE] chunks=%d reasoning=%d content=%d tool_calls=%d",
		chunkCount, reasoningBuf.Len(), contentBuf.Len(), len(toolCallStates))

	// Save reasoning �?call_id for next turn (agent loop)
	if reasoningBuf.Len() > 0 && storeFn != nil {
		for _, idx := range indices {
			tc := toolCallStates[idx]
			if tc != nil && tc.id != "" {
				storeFn(tc.id, reasoningBuf.String())
			}
		}
	}

	status := "completed"
	if scanErr != nil {
		status = "incomplete"
	}
	writeSSE(w, SSEEvent{
		Type: "response.completed",
		Response: RespResponse{
			ID: respID, Object: "response", CreatedAt: now,
			Status: status, Model: model, Output: output, Usage: u,
		},
	})
	// Responses API does not use [DONE] �?response.completed is the terminal event
	flusher.Flush()
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// lruCache is a bounded LRU map for call_id �?reasoning_text.
type lruCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // oldest at front
	maxSize int
}

type lruItem struct {
	key       string
	reasoning string
	createdAt time.Time
}

func newLRUCache(maxSize int) *lruCache {
	return &lruCache{
		entries: make(map[string]*list.Element),
		order:   list.New(),
		maxSize: maxSize,
	}
}

func (c *lruCache) evictExpired(now time.Time) {
	for e := c.order.Front(); e != nil; e = c.order.Front() {
		item := e.Value.(*lruItem)
		if now.Sub(item.createdAt) < defaultCacheTTL {
			break
		}
		c.order.Remove(e)
		delete(c.entries, item.key)
	}
}

func (c *lruCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpired(time.Now())
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	c.order.MoveToBack(e)
	return e.Value.(*lruItem).reasoning, true
}

func (c *lruCache) put(key, reasoning string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.evictExpired(now)
	if e, exists := c.entries[key]; exists {
		item := e.Value.(*lruItem)
		item.reasoning = reasoning
		item.createdAt = now
		c.order.MoveToBack(e)
	} else {
		for c.order.Len() >= c.maxSize {
			oldest := c.order.Front()
			if oldest != nil {
				oldItem := oldest.Value.(*lruItem)
				c.order.Remove(oldest)
				delete(c.entries, oldItem.key)
			}
		}
		item := &lruItem{key: key, reasoning: reasoning, createdAt: now}
		c.entries[key] = c.order.PushBack(item)
	}
}

func (c *lruCache) stats() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
type Server struct {
	lastUpstreamCheck time.Time
	lastUpstreamOK    bool
	config      Config
	validKeys   map[string]bool
	deepseekURL string
	httpClient  *http.Client
	logFile     *os.File
	logger      *log.Logger
	cache       *lruCache
	maxBodySize int64
	debug       bool
	startTime   time.Time
	// metrics
	reqTotal    atomic.Int64
	reqErrors   atomic.Int64
	streamTotal atomic.Int64
	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
}

func NewServer(cfg Config) *Server {
	// Env var override for API key (safer than config file)
	if envKey := os.Getenv("DEEPSEEK_API_KEY"); envKey != "" {
		cfg.DeepSeek.APIKey = envKey
	}
	if envPort := os.Getenv("PROXY_PORT"); envPort != "" {
		fmt.Sscanf(envPort, "%d", &cfg.Port)
	}

	keys := make(map[string]bool)
	for _, k := range cfg.APIKeys {
		keys[k] = true
	}

	// Daily rotating log file
	logName := fmt.Sprintf("proxy-%s.log", time.Now().Format("2006-01-02"))
	f, err := os.OpenFile(logName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[WARN] cannot create log %s: %v", logName, err)
		f = nil
	}

	var lgr *log.Logger
	if f != nil {
		lgr = log.New(f, "", log.LstdFlags)
	}

	srv := &Server{
		config:      cfg,
		validKeys:   keys,
		deepseekURL: strings.TrimRight(cfg.DeepSeek.BaseURL, "/") + "/chat/completions",
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				IdleConnTimeout:     90 * time.Second,
				MaxConnsPerHost:     10,
				DisableCompression:  false,
			},
		},
		logFile:     f,
		logger:      lgr,
		cache:       newLRUCache(defaultCacheSize),
		maxBodySize: defaultMaxBody,
		debug:       os.Getenv("PROXY_DEBUG") == "1",
		startTime:   time.Now(),
	}
	return srv
}

// checkUpstream pings DeepSeek API on startup to catch config errors early.
func (s *Server) checkUpstream() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET",
		strings.TrimRight(s.config.DeepSeek.BaseURL, "/")+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+s.config.DeepSeek.APIKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upstream unreachable: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream returned %d (check API key)", resp.StatusCode)
	}
	return nil
}

func (s *Server) log(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Print(msg)
	if s.logger != nil {
		s.logger.Print(msg)
	}
}

func (s *Server) authError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(401)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{"message": "invalid api key", "type": "auth_error"},
	})
}

func (s *Server) serverError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{"message": msg, "type": "server_error"},
	})
}

func (s *Server) HandleResponses(w http.ResponseWriter, r *http.Request) {
	s.reqTotal.Add(1)
	if r.Method != http.MethodPost {
		s.reqErrors.Add(1)
		s.serverError(w, "method not allowed", 405)
		return
	}

	// Auth
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var authorized bool
	for k := range s.validKeys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
			authorized = true
			break
		}
	}
	if !authorized {
		s.reqErrors.Add(1)
		s.log("REQ AUTH_FAIL")
		s.authError(w)
		return
	}

	// Parse (with body size limit)
	var reqBody RespRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, s.maxBodySize)).Decode(&reqBody); err != nil {
		s.reqErrors.Add(1)
		s.log("REQ PARSE_ERROR: %v", err)
		s.serverError(w, "bad request: "+err.Error(), 400)
		return
	}

	s.log("REQ model=%s stream=%v tools=%d reasoning=%v input_items=%d instructions_len=%d",
		reqBody.Model, reqBody.Stream, len(reqBody.Tools),
		reqBody.Reasoning, len(reqBody.Input), len(reqBody.Instructions))

	// Translate
	upstreamReq := translateRequest(&reqBody, s.config.DeepSeek, s.cacheLookup)

	bodyBytes, _ := json.Marshal(upstreamReq)
	s.log("UPSTREAM model=%s msgs=%d tools=%d effort=%q stream=%v size=%d",
		upstreamReq.Model, len(upstreamReq.Messages), len(upstreamReq.Tools),
		upstreamReq.ReasoningEffort, upstreamReq.Stream, len(bodyBytes))
	if s.debug {
		bodyPreview := string(bodyBytes)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500] + "..."
		}
		s.log("UPSTREAM_BODY %s", bodyPreview)
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), "POST", s.deepseekURL, bytes.NewReader(bodyBytes))
		if err != nil {
			s.reqErrors.Add(1)
			s.log("UPSTREAM_REQ_ERR: %v", err)
			s.serverError(w, "upstream request error", 500)
			return
		}
	httpReq.Header.Set("Authorization", "Bearer "+s.config.DeepSeek.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if reqBody.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		s.reqErrors.Add(1)
		s.log("UPSTREAM_ERR: %v", err)
		s.serverError(w, "upstream request failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	s.log("UPSTREAM_RESP status=%d", resp.StatusCode)

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if len(bodyStr) > 500 {
			bodyStr = bodyStr[:500]
		}
		s.log("UPSTREAM_ERR_BODY: %s", bodyStr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		w.Write(body)
		return
	}

	if reqBody.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			s.serverError(w, "streaming not supported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		s.streamTotal.Add(1); s.log("RESP stream_start")
		streamTranslate(r.Context(), w, resp.Body, reqBody.Model, flusher, s.cacheStore)
		s.log("RESP stream_done")
		return
	}

	// Non-streaming
	rawBody, _ := io.ReadAll(resp.Body)
	var ccResp CCResponse
	if err := json.Unmarshal(rawBody, &ccResp); err != nil {
		preview := rawBody
		if len(preview) > 200 {
			preview = preview[:200]
		}
		s.log("RESP PARSE_ERROR: %v raw=%s", err, string(preview))
		s.serverError(w, "upstream response parse error: "+err.Error(), 502)
		return
	}

	translated := translateNonStream(&ccResp, reqBody.Model)
	s.saveReasoning(translated.Output)
	tcCount := 0
	for _, o := range translated.Output {
		if o.Type == "function_call" {
			tcCount++
		}
	}
	s.log("RESP nonstream model=%s tokens_in=%d tokens_out=%d output_items=%d tool_calls=%d",
		translated.Model, translated.Usage.InputTokens, translated.Usage.OutputTokens, len(translated.Output), tcCount)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(translated)
}

func (s *Server) cacheLookup(callID string) (string, bool) {
	v, ok := s.cache.get(callID)
	if ok { s.cacheHits.Add(1) } else { s.cacheMisses.Add(1) }
	return v, ok
}
func (s *Server) cacheStore(callID, reasoning string) { s.cache.put(callID, reasoning) }

func (s *Server) saveReasoning(output []RespOutputItem) {
	var reasoning string
	for _, o := range output {
		if o.Type == "reasoning" && len(o.Summary) > 0 {
			reasoning = o.Summary[0].Text
		} else if o.Type == "function_call" && reasoning != "" && o.CallID != "" {
			s.cache.put(o.CallID, reasoning)
		}
	}
}

// handleModels returns the model list that Codex Desktop can discover.
func (s *Server) HandleModels(w http.ResponseWriter, r *http.Request) {
	models := []map[string]interface{}{
		{
			"id": "deepseek-v4-pro", "object": "model",
			"owned_by": "deepseek",
			"metadata": map[string]interface{}{
				"reasoning_effort": []string{"low", "medium", "high", "xhigh"},
				"max_tokens":       65536,
				"default_effort":   "xhigh",
			},
		},
		{
			"id": "deepseek-v4-flash", "object": "model",
			"owned_by": "deepseek",
			"metadata": map[string]interface{}{
				"reasoning_effort": []string{"low", "medium", "high"},
				"max_tokens":       65536,
				"default_effort":   "medium",
			},
		},
		// Aliases Codex Desktop may use internally
		{ "id": "gpt-5.4", "object": "model", "owned_by": "deepseek" },
		{ "id": "gpt-5.4-mini", "object": "model", "owned_by": "deepseek" },
		{ "id": "gpt-5.3-codex", "object": "model", "owned_by": "deepseek" },
		{ "id": "gpt-5.2", "object": "model", "owned_by": "deepseek" },
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// perModelDefaults returns tuned defaults based on the upstream model.
func perModelDefaults(model string) (reasoningEffort string, maxTokens int) {
	switch model {
	case "deepseek-v4-flash":
		return "medium", 4096
	default: // deepseek-v4-pro and everything else
		return "xhigh", 8192
	}
}

func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	upstreamOK := s.lastUpstreamOK
	if time.Since(s.lastUpstreamCheck) > 30*time.Second {
		s.lastUpstreamOK = s.checkUpstream() == nil
		s.lastUpstreamCheck = time.Now()
		upstreamOK = s.lastUpstreamOK
	}
	resp := map[string]interface{}{
		"status":       "ok",
		"uptime_sec":   int(time.Since(s.startTime).Seconds()),
		"cache_size":   s.cache.stats(),
		"req_total":    s.reqTotal.Load(),
		"req_errors":   s.reqErrors.Load(),
		"stream_total": s.streamTotal.Load(),
		"cache_hits":   s.cacheHits.Load(),
		"cache_misses": s.cacheMisses.Load(),
		"upstream_ok":  upstreamOK,
		"go_version":   runtime.Version(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func truncate(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "..."
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	configPath := flag.String("config", "proxy-config.yaml", "config file path")
	port := flag.Int("port", 0, "listen port (overrides config)")
	flag.Parse()

	// Load config
	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("无法读取配置文件 %s: %v", *configPath, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("配置文件解析失败: %v", err)
	}
	if *port > 0 {
		cfg.Port = *port
	}
	if cfg.Port == 0 {
		cfg.Port = 8317
	}

	srv := NewServer(cfg)

	// Startup upstream connectivity check
	log.Printf("checking upstream connectivity...")
	if err := srv.checkUpstream(); err != nil {
		log.Printf("[WARN] upstream check failed: %v (proxy will still start)", err)
	} else {
		log.Printf("[OK] upstream reachable")
	}
	log.Printf("cache capacity: %d, TTL: %v", defaultCacheSize, defaultCacheTTL)

	// Panic recovery middleware �?prevents a single crash from killing the server
	recovery := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[PANIC] %v �?request recovered", rec)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(500)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error": map[string]string{"message": "internal server error", "type": "server_error"},
					})
				}
			}()
			next(w, r)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", recovery(srv.HandleResponses))
	mux.HandleFunc("/v1/models", recovery(srv.HandleModels))
	mux.HandleFunc("/health", recovery(srv.HandleHealth))

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	httpServer := &http.Server{Addr: addr, Handler: mux, ReadTimeout: 30 * time.Second, WriteTimeout: 300 * time.Second, IdleTimeout: 120 * time.Second}

	go func() {
		<-sigCh
		log.Println("正在关闭 (等待活跃请求完成)...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
		if srv.logFile != nil { srv.logFile.Close() }
	}()

	log.Printf("Codex �?DeepSeek 代理启动: http://%s", addr)
	log.Printf("  上游: %s", srv.deepseekURL)
	log.Printf("  模型映射: %v", cfg.DeepSeek.Models)
	log.Printf("  推理强度: 直接透传 (xhigh/max �?DeepSeek Chat Completions 原生支持)")

	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("服务异常: %v", err)
	}
}
