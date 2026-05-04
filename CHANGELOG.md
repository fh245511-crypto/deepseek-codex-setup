# Changelog

## v1.0.0 (2026-05-03) — Stable Release

### 架构

Codex Desktop (Responses API) → Go Proxy (协议翻译) → DeepSeek (Chat Completions API)

### 核心特性

- **模型名映射**: `gpt-5.4` → `deepseek-v4-pro`, `gpt-5.4-mini` → `deepseek-v4-flash`
- **推理强度透传**: `xhigh`/`high`/`medium` 不做校验，直接传递给 DeepSeek Chat Completions
- **推理缓存**: `call_id → reasoning_text` 内存映射，解决 Codex Desktop 不回传 `reasoning` item 的问题
- **工具调用合并**: 连续 `function_call` → 单条 `assistant` 消息的多个 `tool_calls`
- **SSE 流式完整事件链**: `event:` 前缀 + 全部 `done` 事件
- **角色映射**: `developer` → `system`
- **并发安全**: `sync.RWMutex` + 函数封装
- **缓存驱逐**: 上限 200 条
- **Graceful shutdown**: `Shutdown(ctx)` 排水

---

## 开发迭代记录

### Phase 1: CLIProxyAPI 配置调试

**问题 1 — 模型名不匹配**
- Codex Desktop 发 `gpt-5.4`，CLIProxyAPI 路由表只有 `deepseek-v4-pro`
- 错误: `unknown provider for model gpt-5.4`
- 修复: `config.yaml` 添加 `alias: gpt-5.4`

**问题 2 — 推理强度校验**
- `model_reasoning_effort = "xhigh"` 被 CLIProxyAPI 拦截
- 错误: `level "xhigh" not supported, valid levels: low, medium, high`
- 尝试: `payload.filter` 删除字段 → DeepSeek 自动 `max`
- 尝试: `payload.override` → CLIProxyAPI 出站再次校验，仍拒绝
- 结论: CLIProxyAPI 无法透传 `xhigh`

### Phase 2: 自建 Python 代理

- FastAPI + httpx + uvicorn
- 实现 Responses API → Chat Completions 翻译
- 问题: Windows Store Python 限制，无法稳定运行
- 废弃，转向 Go

### Phase 3: Go 代理核心开发

**Bug 1 — 非 message 类型被丢弃**
- `translateMessages` 只处理 `type: "message"`，跳过 `function_call`/`function_call_output`
- 多轮对话工具调用历史全部丢失
- 修复: switch-case 处理所有 input 类型

**Bug 2 — `reasoning_content` 未回传**
- Codex Desktop 的 Responses API 不回传 `reasoning` item
- DeepSeek 要求有工具调用的回合必须回传 `reasoning_content`
- 错误: `The reasoning_content in the thinking mode must be passed back to the API`
- 修复: 推理缓存机制 — 代理记住每轮的 reasoning，下次根据 `call_id` 自动注入

**Bug 3 — `summary_text` 字段名错误**
- `RespOutputSummary.Text` 的 json tag 是 `"summary_text"`，实际 JSON 是 `"text"`
- 导致 reasoning 解析为空
- 修复: `json:"text"`

**Bug 4 — CCResponse 结构体解析失败**
- DeepSeek 返回 `{"choices": [{"message": {...}}]}`，嵌套在 `message` 下
- 代码把 `choices` 直接映射为 `[]CCMessageFull`，跳过 `message` 层
- 结果: 非流式 output 永远为空
- 修复: 恢复 `CCChoiceFull` 中间结构体

**Bug 5 — SSE 格式不完整**
- 缺失 `event:` 行前缀 — Codex Desktop 无法路由事件类型
- 缺失 `[DONE]` 移除 — Responses API 不需要此信号
- 缺失 `done` 事件链 — `reasoning_text.done`, `output_text.done`, `content_part.done`, `output_item.done`
- `response.completed` 的 `output` 为空
- 修复: 累积流式内容，完整填充所有事件

**Bug 6 — function_call 输出格式错误**
- 非流式路径用 `Content` 数组包裹参数: `"content": [{"type": "input_text", "text": "..."}]`
- Codex Desktop 期望独立字段: `"call_id": "...", "arguments": "..."` 
- 流式路径 `output_item.done` 缺少 `output_index`, `call_id`, `status`
- 修复: 统一使用 `CallID` + `Arguments` 字段

**Bug 7 — 流式不处理 tool_calls delta**
- DeepSeek 流式的工具调用通过 `delta.tool_calls` 渐进传递
- 代理完全不处理 → Agent 收不到工具调用指令
- 修复: 完整实现 `function_call_arguments.delta` → `.done` → `output_item.done` 事件链

**Bug 8 — 连续 function_call 被拆散**
- 两个连续 `function_call` → 两条独立 assistant 消息
- DeepSeek 要求第一个的 tool_calls 在下一个 assistant 前必须有 tool 响应
- 错误: `insufficient tool messages following tool_calls message`
- 修复: 累积合并到单条 assistant 的多个 `tool_calls`

### Phase 4: Code Review & 修复

**P0 — 竞态条件** (#1 #2)
- `reasoningCache` 并发读写无锁保护
- 修复: `sync.RWMutex` + `cacheLookup`(RLock) / `cacheStore`(Lock) 封装

**P0 — 流式缓存写无锁**
- `streamTranslate` 直接写 `cache[tc.id]` 无锁
- 修复: `storeFn` 回调，统一走 `cacheStore`

**P1 — genID 碰撞风险**
- `time.Now().UnixNano()` 同纳秒可能重复
- 修复: 添加 `crypto/rand` 4 字节前缀

**P2 — SSE 扫描错误顺序**
- `scanner.Err()` 在 `response.completed` 之后检查
- 修复: 先检查错误，有错误时 `status: "incomplete"`

**P3 — toolCallState 索引间隙**
- `idx → slot` 模式，非连续 index 被丢弃
- 修复: `sort.Ints(indices)` 排序遍历

**P4 — 缓存无界增长**
- `reasoningCache` 永不过期
- 修复: `cacheMaxSize=200` + eviction

**其他**
- `writeSSE` 不再静默吞 marshal 错误
- `httpServer.Close()` → `httpServer.Shutdown(ctx)` 10s 排水
