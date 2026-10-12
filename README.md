# ipmitool-microservice

> [!WARNING]
> This project is experimental and intended for testing only. It is not recommended for production use.

REST API for remote `ipmitool` commands, built with Go, Gin, Viper, Cobra, and Zap.
Every command receives BMC credentials in its JSON body. Access requires a fixed
Bearer token, with optional HTTPS and an IP allowlist.

See [BMC testing and WIP](BMC_TESTS.md) for hardware test status and planned features.

## Quick start

Requires Go 1.26+ and network access to the BMCs.

```bash
cp -n config.example.yaml config.yaml
chmod 600 config.yaml
openssl rand -hex 32
```

Set the generated value as `auth.token` in `config.yaml`, then start the API:

```bash
go run . serve --config config.yaml
```

For live reload, install Air, `goimports`, and `golangci-lint`, then run
`"$(go env GOPATH)/bin/air" -c .air.toml`. Each rebuild formats, lints, builds,
and starts `serve --config config.yaml`. Go files, module files, and local config
changes trigger reloads; failed checks stop the API.

Startup finds `ipmitool` in `PATH` or standard system directories and verifies it
with `ipmitool -V`. If missing on Linux, it runs `apt update` and
`apt install --yes --no-remove ipmitool`, using noninteractive sudo when needed.
Installation requires root or suitable sudo permissions and has a 5-minute limit.
A custom `ipmitool.binary` path must already exist.

## Configuration

[config.example.yaml](config.example.yaml) contains all settings:

```yaml
server:
  address: "127.0.0.1:8080"
  allowed_ips: []
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
auth:
  token: "YOUR_GENERATED_TOKEN"
ipmitool:
  binary: "ipmitool"
  timeout: "30s"
  max_output_bytes: 1048576
  max_concurrent: 4
```

- **TLS:** set `enabled: true` and readable PEM certificate/key paths to serve
  HTTPS (TLS 1.2+). With `false`, the listener serves HTTP. Restart after changes.
- **Allowlist:** accepts IPv4/IPv6 addresses and CIDRs, such as
  `["127.0.0.1", "192.0.2.0/24"]`. Empty allows all peers, still requiring a token.
  It checks the TCP peer IP and ignores forwarded headers; behind a proxy, it
  checks the proxy's IP.
- **Limits:** timeout up to `24h`, output from 1 to 67108864 bytes per stream,
  concurrency from 1 to 1024. Defaults are shown above.
- **Environment:** override any field with `IPMI_API_` plus its uppercase path,
  replacing dots with underscores, for example `IPMI_API_AUTH_TOKEN` or
  `IPMI_API_SERVER_TLS_ENABLED=true`. `IPMI_API_SERVER_ALLOWED_IPS` takes a
  comma-separated list. The Go service does not load `.env` files.

Keep tokens and private keys outside Git. BMC credentials belong in each request.
Use HTTPS for network access. Zap writes JSON lifecycle and request logs to stderr
without logging credentials, request bodies, or command output.

## Laravel HTTP client

These examples run inside a Laravel application using the
[HTTP Facade](https://laravel.com/docs/13.x/http-client). Set the API URL and token
in the **Laravel application's** `.env`:

```dotenv
IPMITOOL_API_URL=http://127.0.0.1:8080/api/v1
IPMITOOL_API_TOKEN=YOUR_GENERATED_TOKEN
```

Add this entry to the array returned by `config/services.php`:

```php
'ipmitool' => [
    'base_url' => env('IPMITOOL_API_URL', 'http://127.0.0.1:8080/api/v1'),
    'token' => env('IPMITOOL_API_TOKEN'),
],
```

Create a reusable client factory and the connection for the requested BMC:

```php
use Illuminate\Support\Facades\Http;

$api = fn () => Http::baseUrl(config('services.ipmitool.base_url'))
    ->withToken(config('services.ipmitool.token'))
    ->acceptJson()
    ->asJson()
    ->connectTimeout(5)
    ->timeout(40);

$connection = [
    'host' => '192.0.2.10',
    'username' => 'ADMIN',
    'password' => 'bmc-password',
];

$payload = ['connection' => $connection];
```

All paths below are relative to the base URL, which includes `/api/v1`. The
factory creates a fresh request for each call. Set the client timeout above the
server's command timeout. For a private CA, add
`->withOptions(['verify' => '/path/to/ca.pem'])` to the factory and use an HTTPS URL.

### Read operations

```php
$health = $api()->get('health')->throw()->json();
$power = $api()->post('power/status', $payload)->throw()->json();
$chassis = $api()->post('chassis/status', $payload)->throw()->json();
$sensors = $api()->post('sensors', $payload)->throw()->json();
$sdr = $api()->post('sdr', $payload)->throw()->json();
$fru = $api()->post('fru', $payload)->throw()->json();
$events = $api()->post('sel/entries', $payload)->throw()->json();
$controller = $api()->post('mc/info', $payload)->throw()->json();
$lan = $api()->post('lan/1', $payload)->throw()->json();
$users = $api()->post('users/1', $payload)->throw()->json();
```

Only `health` uses GET and needs no connection body. Every route requires the API
token. List responses preserve native text in `stdout`; they are not parsed arrays.

### Change power or boot settings

The following are independent operations. Run only the action you intend:

```php
$powerOn = $api()->put('power', $payload + ['state' => 'on'])->throw()->json();

$restart = $api()->post('power/actions', $payload + [
    'action' => 'cycle',
])->throw()->json();

$boot = $api()->put('boot-device', $payload + [
    'device' => 'pxe',
    'persistent' => true,
])->throw()->json();

$reset = $api()->post('mc/resets', $payload + ['mode' => 'warm'])->throw()->json();

$clearEvents = $api()->post('sel/clear', $payload)->throw()->json();
```

### Run any native command

Use `commands` for operations without a dedicated route, including OEM commands:

```php
$result = $api()->post('commands', $payload + [
    'args' => ['sensor', 'get', 'CPU Temp'],
])->throw()->json();

$deviceId = $api()->post('commands', $payload + [
    'args' => ['raw', '0x06', '0x01'],
])->throw()->json();
```

Pass the command and each argument separately, without `ipmitool` or global
connection flags. Arguments are passed literally, without a shell. Optional
`stdin` accepts a string. Command availability depends on the installed ipmitool
and BMC; see the [ipmitool manual](https://github.com/ipmitool/ipmitool/blob/master/doc/ipmitool.1.in).
Native file paths refer to the API host. Interactive consoles requiring a TTY are
not supported by this synchronous API.

### Routes and accepted values

All command routes require `connection` in addition to the fields below.

| Method | Relative path | Additional fields |
| --- | --- | --- |
| GET | `health` | None; no connection required |
| POST | `power/status`, `chassis/status` | None |
| POST | `sensors`, `sdr`, `fru`, `sel/entries`, `mc/info` | None |
| POST | `lan/{channel}`, `users/{channel}` | Channel from 0 to 15 |
| PUT | `power` | `state`: `on`, `off` |
| POST | `power/actions` | `action`: `cycle`, `reset`, `diag`, `soft` |
| PUT | `boot-device` | `device`: `none`, `pxe`, `disk`, `safe`, `diag`, `cdrom`, `bios`, `floppy`; optional boolean `persistent` |
| POST | `mc/resets` | `mode`: `warm`, `cold` |
| POST | `sel/clear` | None; deletes event history |
| POST | `commands` | `args`: 1–256 strings; optional `stdin` |

### Connection fields

| Field | Rule |
| --- | --- |
| `host` | Required IP or hostname, without protocol or port |
| `username` | Required; up to 16 bytes, no control characters |
| `password` | Required; up to 20 bytes for `lanplus` or 16 for `lan`; explicit empty string allowed |
| `interface` | Optional: `lanplus` (default) or `lan` |
| `port` | Optional integer from 1 to 65535; default 623 |
| `privilege` | Optional: `CALLBACK`, `USER`, `OPERATOR`, `ADMINISTRATOR`, `OEM`, `NO ACCESS`; native `+` suffix accepted |
| `auth_type` | Optional: `NONE`, `MD2`, `MD5`, `PASSWORD`, `OEM`, `RMCP+` |
| `cipher_suite` | Optional integer from 0 to 255 |

`privilege` and `auth_type` are case-insensitive. Unknown fields, invalid types,
and null values are rejected. Maximum JSON body: 128 KiB. Arguments total at most
64 KiB; `stdin` has a separate 64 KiB limit. Connection strings and arguments
cannot contain NUL bytes.

### Handle responses and errors

Successful command responses contain `stdout`, `stderr`, `exit_code`, and
`truncated`. Read the output through Laravel:

```php
$response = $api()->post('sensors', $payload);

if ($response->failed()) {
    $code = $response->json('error.code');
    $message = $response->json('error.message');
    $diagnostic = $response->json('result.stderr');
    // Handle the error without exposing BMC diagnostics to untrusted users.
}

$output = $response->throw()->json('stdout');
```

`throw()` raises `RequestException` for HTTP errors; connection failures/timeouts
raise `ConnectionException`. Common statuses: `400` invalid input, `401` invalid
token, `403` blocked IP, `502` ipmitool failure, `503` concurrency limit, `504`
command timeout. Execution failures include partial output in `result`.
API error messages use lowercase except for acronyms such as IP and JSON.
Native `stdout` and `stderr` keep their original case.
Do not automatically retry operations that change BMC state.

## Deployment

### Docker

The [Dockerfile](Dockerfile) includes ipmitool and runs as UID/GID `10001`.
Prepare a configured copy of `config.yaml` readable by that group:

```bash
docker build -t ipmitool-microservice .
sudo install -d -m 0750 /etc/ipmitool-microservice/docker
sudo install -m 0640 -o root -g 10001 config.yaml /etc/ipmitool-microservice/docker/config.yaml
docker run --detach --name ipmitool-api \
  --publish 127.0.0.1:8080:8080 \
  --mount type=bind,src=/etc/ipmitool-microservice/docker/config.yaml,dst=/etc/ipmitool-microservice/config.yaml,readonly \
  ipmitool-microservice
docker logs --follow ipmitool-api
```

The image listens on `0.0.0.0:8080` inside the container. If TLS is enabled, mount
the certificate/key read-only at their configured paths, readable by UID/GID
10001. Mount `/var/lib/ipmitool-microservice` for persistent native command files.

### Systemd

Preinstall ipmitool, then install the binary, configuration, and
[service unit](deploy/ipmitool-microservice.service):

```bash
sudo apt install ipmitool
go build -o ipmitool-microservice .
id -u ipmitool-api >/dev/null 2>&1 || \
  sudo useradd --system --user-group --no-create-home \
    --home-dir /var/lib/ipmitool-microservice --shell /usr/sbin/nologin ipmitool-api
sudo install -m 0755 ipmitool-microservice /usr/local/bin/ipmitool-microservice
sudo install -d -m 0750 -o root -g ipmitool-api /etc/ipmitool-microservice
sudo install -m 0640 -o root -g ipmitool-api config.yaml /etc/ipmitool-microservice/config.yaml
sudo install -m 0644 deploy/ipmitool-microservice.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ipmitool-microservice
```

The unit runs as `ipmitool-api`, writes state under `/var/lib/ipmitool-microservice`,
and prevents privilege elevation, including automatic apt installation. TLS files
must be readable by the service user. Use `journalctl -u ipmitool-microservice -f`
for logs and restart the service after configuration changes.

## Checks

```bash
goimports -local ipmitool-microservice -w main.go internal
golangci-lint run ./...
go test -race ./...
go vet ./...
go build ./...
```

Tests use simulated commands and temporary HTTP/HTTPS listeners. They cover
validation, authentication, IP filtering, runner responses, logging, installation,
and process lifecycle without requiring a real BMC.
