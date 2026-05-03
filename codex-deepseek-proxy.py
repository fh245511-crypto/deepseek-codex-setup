#!/usr/bin/env python3
"""
Codex → DeepSeek 专用代理
=========================
只做一件事：把 Codex Desktop 的 OpenAI Responses API 请求
翻译为 DeepSeek 的 Chat Completions API 请求，原样返回。

替代 CLIProxyAPI，无冗余校验，推理强度字段直接透传（含 xhigh/max）。

启动: python codex-deepseek-proxy.py [--port 8317] [--config proxy-config.yaml]
"""

import argparse, json, os, sys, time, uuid, yaml
from typing import Optional

import httpx
import uvicorn
from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse, JSONResponse

# ---------------------------------------------------------------------------
# 配置
# ---------------------------------------------------------------------------

DEFAULT_CONFIG = {
    "port": 8317,
    "api_keys": ["sk-cliproxyapi-my-key-2026"],
    "deepseek": {
        "base_url": "https://api.deepseek.com/v1",
        "api_key": "sk-your-deepseek-key",
        "models": {
            "gpt-5.4":      "deepseek-v4-pro",
            "gpt-5.3-codex": "deepseek-v4-pro",
            "gpt-5.2":      "deepseek-v4-pro",
            "deepseek-v4-pro":  "deepseek-v4-pro",
            "gpt-5.4-mini":     "deepseek-v4-flash",
            "deepseek-v4-flash": "deepseek-v4-flash",
        }
    }
}

def load_config(path: str) -> dict:
    if not os.path.exists(path):
        print(f"[!] 配置文件 {path} 不存在，使用内置默认值。请编辑 proxy-config.yaml 填入 DeepSeek API Key。")
        return DEFAULT_CONFIG
    with open(path, "r", encoding="utf-8") as f:
        cfg = yaml.safe_load(f)
    # 合并默认值
    merged = DEFAULT_CONFIG.copy()
    if cfg:
        merged.update(cfg)
        if "deepseek" in cfg:
            merged["deepseek"].update(cfg["deepseek"])
    return merged


# ---------------------------------------------------------------------------
# 翻译引擎
# ---------------------------------------------------------------------------

def translate_model(model: str, models_map: dict) -> str:
    """gpt-5.4 → deepseek-v4-pro"""
    return models_map.get(model, model)


def translate_messages(body: dict) -> list[dict]:
    """Responses API input[] → Chat Completions messages[]"""
    messages = []
    instructions = body.get("instructions", "")
    if instructions:
        messages.append({"role": "system", "content": instructions})

    for item in body.get("input", []):
        if item.get("type") != "message":
            continue
        role = item.get("role", "user")
        content_parts = item.get("content", [])
        texts = []
        for part in content_parts:
            if isinstance(part, dict) and part.get("type") == "input_text":
                texts.append(part.get("text", ""))
            elif isinstance(part, str):
                texts.append(part)
        msg = {"role": role, "content": "\n".join(texts)}
        messages.append(msg)
    return messages


def translate_tools(body: dict) -> Optional[list[dict]]:
    """Responses API tools[] → Chat Completions tools[]"""
    raw_tools = body.get("tools", [])
    if not raw_tools:
        return None
    out = []
    for t in raw_tools:
        if t.get("type") != "function":
            continue
        out.append({
            "type": "function",
            "function": {
                "name": t.get("name", ""),
                "description": t.get("description", ""),
                "parameters": t.get("parameters", {}),
            }
        })
    return out if out else None


def translate_request(body: dict, deepseek_cfg: dict) -> dict:
    """完整翻译一个 Responses API 请求体 → Chat Completions 请求体"""
    models_map = deepseek_cfg.get("models", {})
    upstream_model = translate_model(body.get("model", ""), models_map)
    messages = translate_messages(body)
    tools = translate_tools(body)
    reasoning = body.get("reasoning", {})
    effort = reasoning.get("effort", None) if reasoning else None

    req = {
        "model": upstream_model,
        "messages": messages,
        "stream": body.get("stream", False),
        "extra_body": {"thinking": {"type": "enabled"}},
    }

    if tools:
        req["tools"] = tools
    if effort:
        req["reasoning_effort"] = effort
    if "max_output_tokens" in body:
        req["max_tokens"] = body["max_output_tokens"]
    if "temperature" in body:
        req["temperature"] = body["temperature"]
    if "top_p" in body:
        req["top_p"] = body["top_p"]

    return req


# ---------------------------------------------------------------------------
# 响应翻译 (非流式)
# ---------------------------------------------------------------------------

def translate_nonstream_response(cc_resp: dict, req_body: dict) -> dict:
    """DeepSeek Chat Completion → OpenAI Responses API 格式"""
    now = int(time.time())
    resp_id = f"resp_{uuid.uuid4().hex[:24]}"
    choice = cc_resp.get("choices", [{}])[0]
    message = choice.get("message", {})
    reasoning_content = message.get("reasoning_content", "")
    content = message.get("content", "")
    usage = cc_resp.get("usage", {})

    output = []
    if reasoning_content:
        rs_id = f"rs_{uuid.uuid4().hex[:24]}"
        output.append({
            "id": rs_id,
            "type": "reasoning",
            "encrypted_content": "",
            "summary": [{"type": "summary_text", "text": reasoning_content}]
        })
    if content:
        msg_id = f"msg_{uuid.uuid4().hex[:24]}"
        output.append({
            "id": msg_id,
            "type": "message",
            "role": "assistant",
            "content": [{"type": "output_text", "text": content}]
        })

    return {
        "id": resp_id,
        "object": "response",
        "created_at": now,
        "status": "completed",
        "model": req_body.get("model", ""),
        "output": output,
        "usage": {
            "input_tokens": usage.get("prompt_tokens", 0),
            "output_tokens": usage.get("completion_tokens", 0),
            "total_tokens": usage.get("total_tokens", 0),
        }
    }


# ---------------------------------------------------------------------------
# SSE 流式翻译
# ---------------------------------------------------------------------------

async def translate_stream(client_response: httpx.Response, req_body: dict):
    """逐行翻译 DeepSeek SSE → OpenAI Responses API SSE"""
    resp_id = f"resp_{uuid.uuid4().hex[:24]}"
    rs_id = f"rs_{uuid.uuid4().hex[:24]}"
    msg_id = f"msg_{uuid.uuid4().hex[:24]}"
    model = req_body.get("model", "")
    now = int(time.time())
    started_reasoning = False
    started_content = False

    # 1) response.created
    yield f"data: {json.dumps({'type': 'response.created', 'response': {'id': resp_id, 'object': 'response', 'status': 'in_progress', 'model': model, 'created_at': now, 'output': []}})}\n\n"

    # 2) reasoning item (预留)
    yield f"data: {json.dumps({'type': 'response.output_item.added', 'item': {'id': rs_id, 'type': 'reasoning', 'encrypted_content': '', 'summary': []}, 'output_index': 0})}\n\n"

    async for line_bytes in client_response.aiter_lines():
        line = line_bytes.strip()
        if not line or not line.startswith("data:"):
            continue
        data_str = line[5:].strip()
        if data_str == "[DONE]":
            break
        try:
            chunk = json.loads(data_str)
        except json.JSONDecodeError:
            continue

        choice = (chunk.get("choices", [{}]) or [{}])[0]
        delta = choice.get("delta", {})
        rc = delta.get("reasoning_content", "")
        ct = delta.get("content", "")

        if rc:
            if not started_reasoning:
                started_reasoning = True
            yield f"data: {json.dumps({'type': 'response.reasoning_text.delta', 'delta': rc, 'item_id': rs_id})}\n\n"

        if ct:
            if not started_content:
                started_content = True
                yield f"data: {json.dumps({'type': 'response.output_item.added', 'item': {'id': msg_id, 'type': 'message', 'role': 'assistant', 'content': []}, 'output_index': 1})}\n\n"
                yield f"data: {json.dumps({'type': 'response.output_text.delta', 'delta': '', 'item_id': msg_id})}\n\n"
            yield f"data: {json.dumps({'type': 'response.output_text.delta', 'delta': ct, 'item_id': msg_id})}\n\n"

    # 3) response.completed
    yield f"data: {json.dumps({'type': 'response.completed', 'response': {'id': resp_id, 'object': 'response', 'status': 'completed', 'model': model, 'created_at': now, 'output': []}})}\n\n"
    yield "data: [DONE]\n\n"


# ---------------------------------------------------------------------------
# FastAPI 应用
# ---------------------------------------------------------------------------

config: dict = {}
deepseek_cfg: dict = {}
valid_keys: set = set()
DEEPSEEK_BASE: str = ""
DEEPSEEK_KEY: str = ""

app = FastAPI(title="Codex→DeepSeek Proxy", version="1.0.0")


@app.post("/v1/responses")
async def proxy_responses(request: Request):
    # 鉴权
    auth = request.headers.get("Authorization", "")
    token = auth.removeprefix("Bearer ").strip()
    if token not in valid_keys:
        return JSONResponse({"error": {"message": "invalid api key", "type": "auth_error"}}, 401)

    body = await request.json()
    stream = body.get("stream", False)

    try:
        upstream_req = translate_request(body, deepseek_cfg)
    except Exception as e:
        return JSONResponse({"error": {"message": f"translation error: {e}", "type": "server_error"}}, 500)

    headers = {
        "Authorization": f"Bearer {DEEPSEEK_KEY}",
        "Content-Type": "application/json",
    }

    if stream:
        upstream_req["stream"] = True
        upstream_req.setdefault("stream_options", {"include_usage": True})
        client = httpx.AsyncClient(timeout=httpx.Timeout(300.0))
        upstream_resp = await client.send(
            client.build_request("POST", f"{DEEPSEEK_BASE}/chat/completions",
                                 json=upstream_req, headers=headers, timeout=300.0),
            stream=True
        )
        if upstream_resp.status_code != 200:
            body_text = await upstream_resp.aread()
            await client.aclose()
            return JSONResponse({"error": {"message": f"upstream error: {body_text.decode()}", "type": "upstream_error"}}, 502)
        return StreamingResponse(
            translate_stream(upstream_resp, body),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
                "X-Accel-Buffering": "no",
            }
        )
    else:
        async with httpx.AsyncClient(timeout=httpx.Timeout(300.0)) as client:
            upstream_resp = await client.post(
                f"{DEEPSEEK_BASE}/chat/completions",
                json=upstream_req, headers=headers
            )
        if upstream_resp.status_code != 200:
            return JSONResponse({"error": {"message": upstream_resp.text, "type": "upstream_error"}}, 502)
        return JSONResponse(translate_nonstream_response(upstream_resp.json(), body))


@app.get("/health")
async def health():
    return {"status": "ok"}


# ---------------------------------------------------------------------------
# 启动
# ---------------------------------------------------------------------------

def main():
    global config, deepseek_cfg, valid_keys, DEEPSEEK_BASE, DEEPSEEK_KEY

    parser = argparse.ArgumentParser(description="Codex → DeepSeek 专用代理")
    parser.add_argument("--port", type=int, default=None, help="监听端口 (默认 8317)")
    parser.add_argument("--config", type=str, default="proxy-config.yaml", help="配置文件路径")
    args = parser.parse_args()

    config = load_config(args.config)
    deepseek_cfg = config.get("deepseek", {})
    valid_keys = set(config.get("api_keys", []))
    DEEPSEEK_BASE = deepseek_cfg.get("base_url", "https://api.deepseek.com/v1")
    DEEPSEEK_KEY = deepseek_cfg.get("api_key", "")

    if DEEPSEEK_KEY.startswith("sk-your-"):
        print("[!] 警告: DeepSeek API Key 未配置，请编辑 proxy-config.yaml")

    port = args.port or config.get("port", 8317)
    print(f"[*] Codex→DeepSeek 代理启动: http://localhost:{port}")
    print(f"    上游: {DEEPSEEK_BASE}/chat/completions")
    print(f"    模型映射: {json.dumps(deepseek_cfg.get('models', {}), indent=2, ensure_ascii=False)}")
    print(f"    推理强度: 直接透传 (xhigh/max 由 DeepSeek Chat Completions 原生支持)")
    uvicorn.run(app, host="127.0.0.1", port=port, log_level="warning")


if __name__ == "__main__":
    main()
