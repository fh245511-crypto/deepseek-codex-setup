# Maintenance priorities

## Baseline

The source review on 2026-10-21 used commit `4ed90792b08c9063e72058bf883a211c4f642be1`. This documentation branch adds an MIT license, dependency notices, and maintenance guidance, and retires the unsafe legacy installer. It does not change the Go proxy, add tests, or establish live client compatibility.

## Open work

1. Confirm historical exposed credentials were revoked. Deleting a file does not revoke a key; do not reproduce secret values.
2. Add and run regression tests using synthetic requests and local mock upstreams.
3. Correct cache expiry: LRU order is not insertion/expiry order, so stopping at the first unexpired item can leave expired entries accessible.
4. Synchronize `lastUpstreamCheck` and `lastUpstreamOK` across health requests and test with the race detector.
5. Resolve listener port configuration: `NewServer` changes its copy of config for `PROXY_PORT`, while `main` uses the earlier copy.
6. Test SSE termination, tool argument fragments, client disconnects, malformed/oversized requests, and upstream errors.
7. Record an exact client/model/OS version matrix with dates, reproduction steps, and limitations.
8. Review any future packaged binary for required dependency notices before distribution.

Historical commits document implementation work in May 2026. A documentation update alone does not demonstrate continued production maintenance or broad adoption. Track progress through reproducible bug fixes, tests, reviewed changes, and releases; use actual external adoption evidence rather than inferred usage.
