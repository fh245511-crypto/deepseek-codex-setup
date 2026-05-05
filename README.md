# DeepSeek V4 + Codex Desktop -- Go Proxy

Let Codex Desktop use DeepSeek V4 models via a local Go proxy.
The proxy translates OpenAI Responses API calls to DeepSeek Chat Completions API in real time,
solving the compatibility issue introduced in Codex v0.80+.

## Architecture

```
Codex Desktop --(Responses API)--> Go Proxy (localhost:8317) --(Chat API)--> DeepSeek V4
```

## Quick Start

### 1. Configure the proxy

Copy the example config and fill in your DeepSeek API Key:

```bash
cp proxy-config.example.yaml proxy-config.yaml
```

Edit `proxy-config.yaml`: set `deepseek.api_key` to your real key
(from https://platform.deepseek.com/api_keys).

### 2. Build and start the proxy

```bash
cd codex-deepseek-proxy
go build -o codex-deepseek-proxy.exe .
./codex-deepseek-proxy.exe --config ../proxy-config.yaml
```

The proxy listens on `http://localhost:8317` by default.

### 3. Configure Codex Desktop

Copy `codex-config.toml` to the Codex config directory:

- **Windows:** `%USERPROFILE%\.codex\config.toml`
- **macOS/Linux:** `~/.codex/config.toml`

Copy `codex-auth.json` to:
- **Windows:** `%USERPROFILE%\.codex\auth.json`
- **macOS/Linux:** `~/.codex/auth.json`

> The key in `auth.json` must match one of the `api_keys` values in `proxy-config.yaml`
> (default: `sk-cliproxyapi-my-key-2026`).

### 4. Launch Codex

```bash
codex                          # Default: DeepSeek V4 Pro
codex --profile ds-pro         # Force V4 Pro
codex --profile ds-flash       # Use V4 Flash
```

## Features

- **Dual model support:** `deepseek-v4-pro` (xhigh) and `deepseek-v4-flash` (medium)
- **Reasoning effort passthrough:** xhigh / high / medium / low fully supported
- **Reasoning cache:** call_id -> reasoning_text in-memory map for multi-turn conversations
- **Tool call merging:** consecutive function_calls merged into a single assistant message
- **Complete SSE event chain:** reasoning_text.delta -> output_text.delta -> function_call_arguments.delta
- **Graceful shutdown:** SIGINT/SIGTERM triggers 10s drain
- **Health check:** `/health` endpoint with upstream connectivity probe and cache/request metrics

## Files

| File | Purpose |
|------|---------|
| `codex-deepseek-proxy/main.go` | Go proxy source (single file, ~1300 lines) |
| `proxy-config.example.yaml` | Proxy config example |
| `codex-config.toml` | Codex client config example |
| `codex-auth.json` | Codex auth config example |
| `setup.bat` | Windows quick-deploy script |

## FAQ

**Q: "insufficient tool messages" error on startup**
A: You are connecting directly to DeepSeek instead of the proxy. Ensure `base_url` in
`codex-config.toml` points to `http://localhost:8317/v1`.

**Q: Port already in use**
A: Change `port` in `proxy-config.yaml` and update all `base_url` values in
`codex-config.toml` accordingly.

**Q: Codex reports authentication failure**
A: Verify `OPENAI_API_KEY` in `codex-auth.json` matches one of the `api_keys`
in `proxy-config.yaml`.
