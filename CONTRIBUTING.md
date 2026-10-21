# Contributing

This project is an early-stage local protocol adapter. Please keep changes small and reproducible.

The original project code uses the MIT License. Do not add code copied from another project without recording its source, confirming license compatibility, and preserving required notices.

For bug reports, include:
- OS, Go version, Codex client version, and repository commit.
- A minimal redacted request or synthetic fixture, expected result, and actual result.
- Whether the issue reproduces without a live upstream account.

Never include API keys, authentication files, real prompts, private repository content, or unredacted logs. If a credential was exposed, revoke it with its provider rather than posting it in a public issue.

For proposed changes:
1. Describe the behavior being changed and why.
2. Add a regression test using synthetic data. Prefer local `httptest` servers over live APIs.
3. Run `go test ./...`, `go test -race ./...`, and `go vet ./...` from `codex-deepseek-proxy`.
4. Report passed, failed, and not-run checks separately, with toolchain version.
5. Document compatibility limits. Do not mark a client or model supported without a reproducible result.

Review priorities are correctness, privacy, reproducible tests, and safe configuration. No response-time or release-cadence guarantee is made.
