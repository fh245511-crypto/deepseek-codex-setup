# DeepSeek V4 + Codex Desktop + CLIProxyAPI 接入指南

## 背景

Codex v0.80+ 废弃了 `wire_api = "chat"`，改为仅支持 OpenAI Responses API，
而 DeepSeek 只支持 Chat Completions API，因此无法直接接入。
CLIProxyAPI 作为本地中转代理，将 Chat 协议包装为 Responses 协议，解决兼容问题。

## 架构

```
Codex Desktop ──(Responses协议)──> CLIProxyAPI(localhost:8317) ──(Chat协议)──> DeepSeek API
```

## 步骤概览

### 第一步：下载 CLIProxyAPI

**Windows:**
从 https://github.com/router-for-me/CLIProxyAPI/releases/latest 下载 `windows_amd64.zip`

**macOS:**
```bash
brew install cliproxyapi
```

**Linux:**
```bash
curl -fsSL https://raw.githubusercontent.com/brokechubb/cliproxyapi-installer/refs/heads/master/cliproxyapi-installer | bash
```

或者从源码编译（需 Go 1.24+）:
```bash
git clone https://github.com/router-for-me/CLIProxyAPI.git
cd CLIProxyAPI
go build -o cli-proxy-api ./cmd/server
```

### 第二步：配置 config.yaml

编辑 `config.yaml`，**必须修改两处**：

1. `api-keys` — 自定义一个密钥，这是 Codex 调用代理时用的
2. `api-key-entries[0].api-key` — 填入你的 DeepSeek API Key

> DeepSeek API Key 在 https://platform.deepseek.com/api_keys 获取

```yaml
api-keys:
  - "sk-cliproxyapi-my-key-2026"        # 自定义：Codex 用这个调用代理

openai-compatibility:
  - name: "deepseek"
    base-url: "https://api.deepseek.com/v1"
    api-key-entries:
      - api-key: "sk-你的真实DeepSeek-Key"  # 改为你的 DeepSeek Key
    models:
      - name: "deepseek-v4-pro"
        alias: "deepseek-v4-pro"
      - name: "deepseek-v4-flash"
        alias: "deepseek-v4-flash"
```

### 第三步：复制 Codex 配置文件

**Windows:**
```
复制 codex-config.toml → %USERPROFILE%\.codex\config.toml
复制 codex-auth.json   → %USERPROFILE%\.codex\auth.json
```

**macOS / Linux:**
```bash
cp codex-config.toml ~/.codex/config.toml
cp codex-auth.json ~/.codex/auth.json
```

> 注意：auth.json 中的 key 要和 config.yaml 中 `api-keys` 的值一致。
> 这里是填 `sk-cliproxyapi-my-key-2026`，不是 DeepSeek 原始 Key。

### 第四步：启动服务

**终端 1 — 启动 CLIProxyAPI:**
```bash
cliproxyapi --config config.yaml
# 默认监听 http://localhost:8317
```

**Windows 快捷方式:** 双击 `setup.bat` 一键完成第二、三步。

### 第五步：启动 Codex

**终端 2 — 启动 Codex:**
```bash
codex                          # 默认: DeepSeek V4 Pro
codex --profile ds-pro         # DeepSeek V4 Pro
codex --profile ds-flash       # DeepSeek V4 Flash
```

## 文件清单

| 文件 | 用途 | 部署位置 |
|------|------|----------|
| `config.yaml` | CLIProxyAPI 服务配置 | `~/.cli-proxy-api/` 或任意位置通过 `--config` 指定 |
| `codex-config.toml` | Codex 客户端配置 | `~/.codex/config.toml` |
| `codex-auth.json` | Codex 认证密钥 | `~/.codex/auth.json` |
| `setup.bat` | Windows 一键部署脚本 | 与配置文件放在同一目录运行 |

## 常见问题

**Q: 启动报错 "insufficient tool messages"**
A: 说明直接连接了 DeepSeek 而没有走 CLIProxyAPI 中转。确认 codex-config.toml 中 `base_url` 指向 `http://localhost:8317/v1`。

**Q: CLIProxyAPI 启动报端口占用**
A: 修改 config.yaml 中 `port` 的值（如 8318），同时修改 codex-config.toml 中所有 `base_url` 的端口号。

**Q: Codex 提示认证失败**
A: 检查 auth.json 中 `OPENAI_API_KEY` 的值是否和 config.yaml 中 `api-keys` 的值完全一致。

**Q: 能不能跳过 CLIProxyAPI 直接连 DeepSeek？**
A: 截至 Codex v0.80+，已不支持。必须通过中转代理做协议转换。
