# Project instructions

These instructions apply throughout this repository.

## Purpose and conventions

- This is an experimental REST API for remote `ipmitool` operations, intended for testing and not recommended for production. Preserve the warning at the beginning of the README.
- Keep project code, comments, error messages, logs, documentation, and commit messages in English.
- Use the existing stack: Go 1.26+, Gin v1.12, Viper, Cobra, and Zap. Exact dependency versions are in [go.mod](go.mod).
- Keep code readable, use one handler file per action, and reuse the shared validation and execution paths. Prefer the standard library and existing dependencies for small changes.
- Keep [README.md](README.md) concise. API examples use Laravel's HTTP Facade with `baseUrl()`, a Bearer token, and credentials in the request body. Laravel is a client example, not a runtime dependency of this service.
- Keep [config.example.yaml](config.example.yaml) free of unnecessary comments and real secrets.

## Repository map

| Path | Responsibility |
| --- | --- |
| [main.go](main.go) | Process entry point, Gin release mode, signals, top-level errors |
| [internal/cli](internal/cli) | Cobra commands, startup sequence, HTTP/HTTPS server, shutdown |
| [internal/config](internal/config) | Viper defaults, environment overrides, strict configuration validation |
| [internal/api/router.go](internal/api/router.go) | Route registration and middleware order |
| [internal/api/handlers](internal/api/handlers) | Request decoding, per-action handlers, shared execution, runner response validation |
| [internal/api/middleware](internal/api/middleware) | Bearer authentication, peer-IP allowlist, request logging, panic recovery |
| [internal/api/response](internal/api/response) | Shared JSON response and error envelopes |
| [internal/ipmi](internal/ipmi) | Executable discovery/installation, connection validation, argv, process execution, bounded output |
| [internal/logging](internal/logging) | Zap JSON logger |
| [Dockerfile](Dockerfile), [deploy/ipmitool-microservice.service](deploy/ipmitool-microservice.service) | Container and systemd deployment |

## Development and verification

Format changed Go files with `gofmt`. For Go changes, run the relevant package tests, then:

```bash
go test -race ./...
go vet ./...
go build ./...
```

- Tests use fake runners, temporary executables, and `httptest`; no real BMC or installed ipmitool is required. CLI lifecycle tests open temporary loopback ports. Installer tests must never invoke a real package manager.
- Add regression coverage for changed behavior, especially validation before execution, credential isolation, cancellation, concurrent requests, output limits, and error responses. Reuse the existing test helpers.
- Documentation-only changes need syntax/link/contract checks rather than a full Go test run. Laravel snippets can be checked with PHP; shell snippets with `bash -n`. Do not execute BMC-changing examples to validate documentation.
- If a sandbox prevents loopback listeners or Docker access, distinguish that environment restriction from a test failure. Report checks that could not run; do not silently omit them.
- Update affected callers, tests, configuration examples, environment overrides, deployment files, and README examples together when changing a public contract.

## HTTP contract and validation

- Register endpoints under `/api/v1` using native Gin handlers (`*gin.Context`). Consult the router and README for the route catalog. Only health uses GET; command queries use POST because their connection credentials belong in the JSON body.
- Every command requires `connection` with `host`, `username`, and `password`. Credentials are specific to that request; do not move them into server configuration, persist them, or reuse them across requests. There is no device registry or database.
- Keep strict JSON decoding: `application/json`, one object, no unknown fields or invalid types, and a 128 KiB body limit. Do not replace it with permissive binding that loses these checks.
- Preserve omission versus explicit values: `password: ""` is valid, but missing/null is not; omitted port defaults to 623, explicit zero is invalid at the HTTP boundary; cipher suite zero is valid; optional fields reject null.
- Validate connection fields and command arguments before acquiring a concurrency slot or invoking the runner. Keep the domain checks in `internal/ipmi` as well, so direct client calls receive the same protection.
- Connections support `lanplus` (default) and `lan`. Username is limited to 16 bytes; password to 20 bytes for `lanplus` or 16 for `lan`. Preserve native privilege/authentication values supported by the validators, including privilege `+` and `RMCP+`.
- `POST /commands` accepts native commands through `args` and optional finite `stdin`. Validate boundaries without introducing a hardcoded list that blocks OEM or newly supported native subcommands. Limits are 256 arguments, 64 KiB total argument bytes, and 64 KiB of stdin. NUL is invalid in argv but may occur in stdin.
- Successful command responses expose `stdout`, `stderr`, `exit_code`, and `truncated`. Preserve native text, including list output; empty successful lists are valid. Nonempty stderr alone is not failure. Execution errors retain partial output in `result` alongside `error.code` and `error.message`.
- Validate runner consistency: success requires exit code zero. `ErrCommandFailed` requires a positive exit code or `-1` for signal termination; zero and values below `-1` are inconsistent. Do not reject a legitimate signal result as malformed.
- Preserve error classification: invalid input is 400, authentication 401, allowlist 403, canceled request 408, process failure 502, capacity limit 503, and timeout 504. Inconsistent runner responses are 500 `invalid_runner_response`.
- Concurrency is globally bounded and requests are not queued. Exhaustion returns 503 with `Retry-After: 1`; always release a slot after completion, failure, cancellation, or panic.

## Authentication, execution, and logging

- Require exactly one Bearer authorization header on every route, including health and routing errors. Keep constant-time token comparison and fail closed on an empty configured token.
- Keep Gin's automatic path redirects disabled so routing does not bypass middleware. Preserve JSON 404/405 responses and safe panic recovery.
- The IP allowlist uses the TCP socket peer, supports IPv4/IPv6 and CIDRs, and normalizes IPv4-mapped addresses. Do not trust `X-Forwarded-For` or `X-Real-IP`. An empty allowlist permits all peers but still requires authentication.
- Invoke ipmitool directly with `exec.CommandContext` and an argv slice, never a shell command string. Pass the connection password through a per-process `IPMI_PASSWORD` environment and `-E`, never a connection `-P` argument. Keep `--` between global options and native command arguments.
- Preserve the minimal child environment, request context propagation, command timeout, bounded stdout/stderr, truncation flag, and bounded waits on cancellation. Keep caller cancellation distinct from process failure.
- Keep `limitedBuffer` composed rather than embedding `bytes.Buffer`: promoted `ReadFrom` methods can bypass the limit. Its `Write` must report the full input length while discarding excess bytes so the subprocess output keeps draining.
- Native commands can modify equipment or read/write files on the API host. The generic endpoint is administrative access, not an operating-system sandbox. Do not automatically retry writes or use real hardware in automated checks. Interactive TTY/streaming consoles are outside the current synchronous contract.
- Use Zap JSON logs on stderr. Request logs contain method, route template, status, duration, and actual peer IP. Do not log tokens, passwords, bodies, query strings, native arguments/output, or raw panic values. Validation errors identify fields without echoing secret values.

## Configuration and startup

- Configuration defaults and limits live in `internal/config`; the tracked template is `config.example.yaml`. The local `config.yaml`, environment files, keys, build output, and IDE artifacts are ignored. Never include real credentials in tracked files or Docker build context.
- Environment overrides use `IPMI_API_` plus the uppercase field path with dots replaced by underscores. Explicitly empty values override file values. TLS enablement accepts exactly `true`/`false`; the allowlist environment value is comma-separated. The Go service does not load `.env` automatically.
- Preserve strict configuration types, unknown-field rejection, and null rejection before Viper applies defaults or coerces values. A missing implicit `config.yaml` is allowed with valid environment settings; an explicit `--config` path must exist.
- Startup order is configuration validation, optional TLS certificate/key loading, executable discovery/installation, `ipmitool -V`, then the listener. A startup failure must prevent serving requests. TLS requires a matching readable PEM pair and TLS 1.2+; disabled TLS must not read certificate files.
- Health returns the cached executable version and service status; it does not probe a BMC or establish that hardware is reachable.
- `EnsureBinary` searches PATH and standard directories for the default `ipmitool`. Only an absent default executable on Linux triggers apt installation. Invalid custom paths, permission errors, or unsafe relative PATH results must not trigger installation.
- Installation runs apt update followed by install of ipmitool, with noninteractive settings, a shared 5-minute timeout, and noninteractive sudo only when needed. Recheck the executable afterward; version probing is bounded by 5 seconds and the command timeout. Never elevate the HTTP server just to install a package.
- Keep SIGINT/SIGTERM handling and graceful HTTP shutdown. Settings and certificates take effect after restart; there is no live reload.

## Deployment constraints

- Docker uses a Go build stage and Debian slim runtime with ipmitool preinstalled. Preserve UID/GID 10001, runtime-mounted configuration, and read-only secret mounts. The image listens on `0.0.0.0:8080`; its default config path is `/etc/ipmitool-microservice/config.yaml`.
- Systemd uses the `ipmitool-api` user, the same config path, and `/var/lib/ipmitool-microservice` for writable state and working directory. Preserve its privilege restrictions; ipmitool must be installed before starting the unit because automatic privileged installation is blocked there.
- Keep certificate/config permissions compatible with the runtime user and persistent command files within the configured writable state directory. Update both deployment options when changing paths or startup requirements.
