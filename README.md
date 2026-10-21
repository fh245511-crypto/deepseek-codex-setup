# Codex–DeepSeek local protocol adapter

A small Go proxy that translates a subset of OpenAI Responses requests into DeepSeek Chat Completions requests, with configuration examples for Codex clients. This is an independent community project, not an official OpenAI or DeepSeek integration.

## What the source implements

- A loopback HTTP server with `/v1/responses`, `/v1/models`, and `/health` routes.
- Model alias mapping, developer-to-system role conversion, and function-tool conversion.
- Streaming and non-streaming response translation.
- An in-memory reasoning cache keyed by tool call ID, with capacity and expiry logic.
- Local bearer-key checks for response requests, an upstream API-key environment override, and shutdown handling.

These are implementation descriptions, not a claim of complete Responses API compatibility. The model IDs in the examples are project configuration values, not independent verification of upstream availability or capabilities. A tested Codex/client/model/version matrix is still needed.

## Intended use and limits

The adapter is intended for local experimentation with text and function-tool workflows. It does not implement the complete Responses API. The request schema expects an array of input items, and message conversion currently reads `input_text` parts only. Do not assume images, audio, built-in tools, encrypted reasoning, or persisted response retrieval are supported.

The server binds to `127.0.0.1`. Do not expose it to other users or the Internet. Requests can contain source code, tool outputs, and private information; these are sent to the configured upstream provider. Use only data you are authorized to share with that provider.

## Manual setup

Requirements: a Go toolchain satisfying `go.mod` (Go 1.23.4 or newer), a Codex client whose provider configuration you have checked, and an upstream account/API key.

1. Copy `proxy-config.example.yaml` to `proxy-config.yaml`.
2. Set the upstream base URL and model mappings to values actually available to your account. Set `DEEPSEEK_API_KEY` in your local process environment, or fill the untracked config locally. Never put a real key in an issue, commit, screenshot, or chat.
3. Replace the sample local proxy bearer key with a unique local value; the Codex client credential must match it. This is separate from the upstream DeepSeek key.
4. Build and run from the repository root:

```sh
cd codex-deepseek-proxy
go build -o proxy .
./proxy --config ../proxy-config.yaml
```

On Windows use `go build -o proxy.exe .` and `.\proxy.exe --config ..\proxy-config.yaml`.

5. Back up your existing Codex configuration and authentication files. Review and merge the relevant settings from `codex-config.toml`; do not blindly overwrite an existing setup. The example endpoint is `http://localhost:8317/v1`. Review authentication handling for your installed client before using the sample `codex-auth.json`.
6. Restart the client and perform a minimal test with non-sensitive input. Record the exact client version, model ID, OS, and result when reporting compatibility.

**`setup.bat` is retired.** It exits without modifying files and directs users to this manual flow. The prior installer referenced a removed `config.yaml` and overwrote existing Codex files; it must not be used.

## Checks

```sh
cd codex-deepseek-proxy
go test ./...
go test -race ./...
go vet ./...
```

There are currently no automated tests in this repository. The commands above are the intended development checks, not evidence of test coverage or successful execution. Regression tests and end-to-end compatibility checks remain planned work. See [verification notes](docs/VERIFICATION.md) for what has and has not been run.

## Security and known issues

Do not use the public example credential unchanged. Leave `PROXY_DEBUG` unset for sensitive work: debug logging includes request content. Logs and cached reasoning may contain private information.

A historical commit message records removal of a real API key. Removing a file does not revoke credentials or remove historical exposure. The owner must confirm the old key was revoked or rotated; do not copy it into reports.

Current review items include cache expiry after LRU reordering, synchronization of health-check state, and the `PROXY_PORT` override not feeding the final listener address. Use the YAML `port` or `--port` argument, and check the startup address. See [maintenance plan](docs/MAINTENANCE.md).

## Contributing and licensing

See [CONTRIBUTING.md](CONTRIBUTING.md). The original project code is available under the [MIT License](LICENSE). Third-party dependencies retain their own terms; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and [provenance notes](docs/PROVENANCE.md).
