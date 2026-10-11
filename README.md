# ipmitool-microservice

A Go REST API for running `ipmitool` commands against remote equipment. It uses
Gin v1.12 for routing, Viper for configuration, Cobra for the CLI, and Zap v1.28
for structured logs.

Each operation receives the BMC IP/hostname, username, and password in the request
body. A fixed Bearer token from the server configuration protects the API. Command
responses contain the ipmitool output and execution metadata as JSON. No device
registration or database is required.

## Contents

- [Installation and startup](#installation-and-startup)
- [Configuration](#configuration)
- [HTTPS with TLS](#https-with-tls)
- [IP allowlist](#ip-allowlist)
- [Authentication](#authentication)
- [Docker](#docker)
- [Systemd service](#systemd-service)
- [Logging](#logging)
- [Connection data](#connection-data)
- [Preparing the examples](#preparing-the-examples)
- [All routes](#all-routes)
- [Examples by action](#examples-by-action)
- [Generic ipmitool commands](#generic-ipmitool-commands)
- [Responses and errors](#responses-and-errors)
- [Limits and behavior](#limits-and-behavior)
- [Troubleshooting](#troubleshooting)
- [Tests and project layout](#tests-and-project-layout)

## Installation and startup

### Requirements

- Go 1.26 or later to build the project or use `go run`.
- `ipmitool` on the API host, or Linux with `apt` and permission to install it automatically.
- Network access from that host to the BMC management network.
- Bash, curl 7.76+, and jq for the examples in this guide. The service itself does not require curl or jq.

On Debian/Ubuntu, you can install ipmitool and the example tools in advance:

```bash
sudo apt update
sudo apt install ipmitool curl jq
ipmitool -V
go version
```

### Configure the first run

From the project directory:

```bash
go mod download
cp -n config.example.yaml config.yaml
chmod 600 config.yaml
openssl rand -hex 32
```

Save the generated token as `auth.token` in `config.yaml`. If the file already
contains a token, you can keep it; `cp -n` preserves the existing configuration.
Git ignores `config.yaml`.

```yaml
server:
  address: "127.0.0.1:8080"
  allowed_ips: []
  tls:
    enabled: false
    cert_file: ""
    key_file: ""

auth:
  token: "PASTE_THE_GENERATED_TOKEN_HERE"

ipmitool:
  binary: "ipmitool"
  timeout: "30s"
  max_output_bytes: 1048576
  max_concurrent: 4
```

BMC credentials belong in individual requests, not in this file.

```bash
go run . serve --config config.yaml
```

Without `--config`, the service looks for `config.yaml` in the current directory:

```bash
go run . serve
```

Startup validates the configuration and, when TLS is enabled, loads the
certificate and private key before looking for ipmitool. It then finds the
executable, installs the package if necessary, and runs `ipmitool -V` before
opening the HTTP/HTTPS listener. The version check has a 5-second deadline, or the
configured command timeout if shorter. Any failure prevents the server from
starting.

### Automatic discovery and installation

With `ipmitool.binary: "ipmitool"`, the service searches `PATH` and common system
directories, including `/usr/local/bin`, `/usr/bin`, and `/bin`. A restricted PATH
therefore does not require reinstalling an existing system executable.

If the binary is absent on Linux and `apt` is available, startup runs:

```bash
apt update
apt install --yes --no-remove ipmitool
```

The installer sets `DEBIAN_FRONTEND=noninteractive`. A process running as a normal
user elevates only these commands with
`sudo -n --preserve-env=DEBIAN_FRONTEND --`, using the absolute apt path. Sudo must
already authorize noninteractive execution and preservation of that variable.
The application does not request or store a sudo password, and the HTTP server
continues running as the original user.

Installation has one shared 5-minute deadline, independent of the BMC command
timeout. Installer output appears in the startup logs. After installation, the
service locates the executable again and checks its version. Existing executables
and individual HTTP requests do not trigger package installation.

If apt is unavailable, the operating system is unsupported, or installation is
not authorized, startup returns an error. Install ipmitool with the host package
manager and start again. On Debian/Ubuntu, use `sudo apt update` and
`sudo apt install ipmitool`, then run the API as a normal user.

A custom executable name or path, such as
`ipmitool.binary: "/opt/ipmi/ipmitool"`, must already exist. Startup does not
install the standard package to repair an invalid custom path. Files without
execution permission also produce an error without triggering installation.

### Build, run, and stop

```bash
go build -o ipmitool-microservice .
./ipmitool-microservice serve --config config.yaml
```

To inspect CLI options:

```bash
./ipmitool-microservice --help
./ipmitool-microservice serve --help
```

`Ctrl+C` (`SIGINT`) and `SIGTERM` cancel active commands and shut down the server.
Go is not required on the runtime host when deploying a compatible compiled
binary. ipmitool must be installed there or installed automatically at startup.

## Configuration

| Field | Default | Purpose |
| --- | --- | --- |
| `server.address` | `127.0.0.1:8080` | Listener IP/hostname and port from 0 to 65535; `0` selects a free port |
| `server.allowed_ips` | `[]` | Allowed client IP addresses or CIDR ranges; empty allows all peers, with authentication still required |
| `server.tls.enabled` | `false` | Enable HTTPS; otherwise serve plain HTTP |
| `server.tls.cert_file` | `""` | PEM certificate chain path; required when TLS is enabled |
| `server.tls.key_file` | `""` | PEM private key path; required when TLS is enabled |
| `auth.token` | Required; no default | Fixed Bearer token; the hexadecimal value from `openssl rand -hex 32` is suitable |
| `ipmitool.binary` | `ipmitool` | Automatic discovery/installation for the default; a custom name or path must exist |
| `ipmitool.timeout` | `30s` | Per-command deadline; greater than zero, up to `24h` |
| `ipmitool.max_output_bytes` | `1048576` | Separate stdout and stderr limits; from 1 to 67108864 bytes |
| `ipmitool.max_concurrent` | `4` | Global simultaneous command limit; from 1 to 1024 |

The configuration rejects unknown fields, null values, and incorrect types. Use
strings for addresses, tokens, paths, and durations; integers for resource limits;
a boolean for `tls.enabled`; and a list of strings for `allowed_ips`. Environment
values go through the same validation after conversion to their expected types.

Environment variables override the file:

| Variable | Field |
| --- | --- |
| `IPMI_API_SERVER_ADDRESS` | `server.address` |
| `IPMI_API_SERVER_ALLOWED_IPS` | `server.allowed_ips`, as comma-separated IPs/CIDRs |
| `IPMI_API_SERVER_TLS_ENABLED` | `server.tls.enabled`, exactly `true` or `false` |
| `IPMI_API_SERVER_TLS_CERT_FILE` | `server.tls.cert_file` |
| `IPMI_API_SERVER_TLS_KEY_FILE` | `server.tls.key_file` |
| `IPMI_API_AUTH_TOKEN` | `auth.token` |
| `IPMI_API_IPMITOOL_BINARY` | `ipmitool.binary` |
| `IPMI_API_IPMITOOL_TIMEOUT` | `ipmitool.timeout` |
| `IPMI_API_IPMITOOL_MAX_OUTPUT_BYTES` | `ipmitool.max_output_bytes` |
| `IPMI_API_IPMITOOL_MAX_CONCURRENT` | `ipmitool.max_concurrent` |

Example with a different executable, deadline, and concurrency limit:

```bash
IPMI_API_IPMITOOL_BINARY=/usr/bin/ipmitool \
IPMI_API_IPMITOOL_TIMEOUT=2m \
IPMI_API_IPMITOOL_MAX_CONCURRENT=8 \
./ipmitool-microservice serve --config config.yaml
```

To listen on every IPv4 interface:

```bash
IPMI_API_SERVER_ADDRESS=0.0.0.0:8080 \
./ipmitool-microservice serve --config config.yaml
```

Use HTTPS for network access, either through the built-in TLS listener or a
reverse proxy. The token grants access to administrative commands, including
power operations, firmware operations, and files accessible to the process user.

The API **does not load `.env` automatically**. Export variables in the environment
that starts the process. Empty environment values also override the file: an
empty exported `IPMI_API_AUTH_TOKEN` prevents startup. An explicit `--config` path
must exist. With no configuration file and no `--config` argument, the service can
start from `IPMI_API_AUTH_TOKEN` and the other defaults.

Configuration changes take effect after restarting the service.

## HTTPS with TLS

To enable the built-in HTTPS listener, configure a certificate chain and matching
private key readable by the process user:

```yaml
server:
  address: "127.0.0.1:8443"
  allowed_ips: []
  tls:
    enabled: true
    cert_file: "/etc/ipmitool-microservice/tls/cert.pem"
    key_file: "/etc/ipmitool-microservice/tls/key.pem"
```

TLS requires version 1.2 or later. The certificate and key are loaded and checked
at startup with `tls.LoadX509KeyPair`, before binary discovery or package
installation. Missing, unreadable, malformed, or mismatched files stop startup.
With `enabled: false`, the listener uses plain HTTP and does not load TLS files.
The configured address serves one protocol at a time.

Replace certificates and restart the service to load them; there is no automatic
certificate renewal or live reload. For production, use a certificate trusted by
your clients and valid for the hostname they use.

### Local self-signed certificate

This example creates temporary local certificate files for `localhost` and
`127.0.0.1`:

```bash
TLS_DIR=$(mktemp -d)
chmod 700 "$TLS_DIR"
openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 30 \
  -subj '/CN=localhost' \
  -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
  -keyout "$TLS_DIR/key.pem" \
  -out "$TLS_DIR/cert.pem"
chmod 600 "$TLS_DIR/key.pem"
printf 'TLS_DIR=%s\n' "$TLS_DIR"
```

Start with the existing configuration and TLS environment overrides:

```bash
IPMI_API_SERVER_ADDRESS=127.0.0.1:8443 \
IPMI_API_SERVER_TLS_ENABLED=true \
IPMI_API_SERVER_TLS_CERT_FILE="$TLS_DIR/cert.pem" \
IPMI_API_SERVER_TLS_KEY_FILE="$TLS_DIR/key.pem" \
./ipmitool-microservice serve --config config.yaml
```

From another terminal, set `TLS_DIR` to the directory printed during certificate
generation, and set `API_TOKEN` to the configured token. Verify the server using
the certificate as a trusted CA:

```bash
curl --fail-with-body --silent --show-error \
  --cacert "$TLS_DIR/cert.pem" \
  --header "Authorization: Bearer $API_TOKEN" \
  https://127.0.0.1:8443/api/v1/health
```

For the remaining curl helpers with this local certificate, use:

```bash
export API_URL='https://127.0.0.1:8443/api/v1'
export CURL_CA_BUNDLE="$TLS_DIR/cert.pem"
```

## IP allowlist

`server.allowed_ips` restricts the source IPs that can reach every API route,
including health. Both individual IPv4/IPv6 addresses and CIDR prefixes are
accepted:

```yaml
server:
  address: "0.0.0.0:8443"
  allowed_ips:
    - "127.0.0.1"
    - "::1"
    - "192.0.2.0/24"
    - "2001:db8:1234::/48"
```

Merge this example into the existing configuration, retaining its TLS and token
settings. Hostnames, ports, and IPv6 zone identifiers are not allowlist entries.
An empty list allows every source IP; the Bearer token is still required.

The equivalent environment override is:

```bash
export IPMI_API_SERVER_ALLOWED_IPS='127.0.0.1,::1,192.0.2.0/24,2001:db8:1234::/48'
```

Setting that variable to an empty string clears the configured allowlist.

The middleware checks the actual TCP peer address. `X-Forwarded-For`,
`X-Real-IP`, and other forwarding headers cannot grant access. With a reverse
proxy, the allowlist therefore checks the proxy IP, not the original client's
IP. Apply client restrictions at the proxy or network layer as appropriate.
Container networking can also change the peer address seen by the application.
A valid token from a disallowed peer receives HTTP `403` with `ip_not_allowed`.

## Authentication

Every route requires exactly one authorization header, including `/health`:

```http
Authorization: Bearer YOUR_FIXED_TOKEN
```

The API token is separate from the BMC password. There is no login or JWT issuance
endpoint. The token does not expire and is preserved across restarts.

To rotate it, update `auth.token`, or `IPMI_API_AUTH_TOKEN` if using the environment
override, then restart the service and update clients.

## Docker

The [Dockerfile](Dockerfile) uses a Go 1.26 build stage and a Debian Trixie slim
runtime. The runtime contains ipmitool and CA certificates and runs as UID/GID
`10001:10001`. The configuration and its token are mounted at runtime. They are
not copied into the image; [.dockerignore](.dockerignore) also excludes local
configuration, environment files, private keys, Git data, binaries, and IDE files.

Build the image:

```bash
docker build -t ipmitool-microservice:local .
```

Prepare a separate runtime configuration from the existing `config.yaml`, which
must already contain your token. The numeric group makes the file readable by
the container user. The state directory holds files written by native commands:

```bash
sudo install -d -m 0750 /etc/ipmitool-microservice/docker
sudo install -m 0640 -o root -g 10001 config.yaml \
  /etc/ipmitool-microservice/docker/config.yaml
sudo install -d -m 0750 -o 10001 -g 10001 \
  /var/lib/ipmitool-microservice-docker
```

Run the container, exposing HTTP only on the host loopback interface:

```bash
docker run --detach --name ipmitool-api \
  --publish 127.0.0.1:8080:8080 \
  --mount type=bind,src=/etc/ipmitool-microservice/docker/config.yaml,dst=/etc/ipmitool-microservice/config.yaml,readonly \
  --mount type=bind,src=/var/lib/ipmitool-microservice-docker,dst=/var/lib/ipmitool-microservice \
  ipmitool-microservice:local
```

The image sets `IPMI_API_SERVER_ADDRESS=0.0.0.0:8080` and `GIN_MODE=release`.
Its default command is `serve --config /etc/ipmitool-microservice/config.yaml`.
Environment overrides take precedence over the mounted file. If that file enables
TLS, mount its certificate files too; the same container port then serves HTTPS.

```bash
docker logs --follow ipmitool-api
docker stop ipmitool-api
```

The process working directory is `/var/lib/ipmitool-microservice`. Files under
that directory persist in the bind mount. BMC traffic originates from the
container, so its network must be able to reach the BMC management interface.

### Docker with TLS

Copy the certificate and key from the local TLS example into a dedicated mount
directory. Both the directory and files must be readable by group `10001`:

```bash
sudo install -d -m 0750 -o root -g 10001 \
  /etc/ipmitool-microservice/docker/tls
sudo install -m 0640 -o root -g 10001 "$TLS_DIR/cert.pem" \
  /etc/ipmitool-microservice/docker/tls/cert.pem
sudo install -m 0640 -o root -g 10001 "$TLS_DIR/key.pem" \
  /etc/ipmitool-microservice/docker/tls/key.pem
```

Run a TLS-enabled container on host port 8443:

```bash
docker run --detach --name ipmitool-api-tls \
  --publish 127.0.0.1:8443:8080 \
  --env IPMI_API_SERVER_TLS_ENABLED=true \
  --env IPMI_API_SERVER_TLS_CERT_FILE=/etc/ipmitool-microservice/tls/cert.pem \
  --env IPMI_API_SERVER_TLS_KEY_FILE=/etc/ipmitool-microservice/tls/key.pem \
  --mount type=bind,src=/etc/ipmitool-microservice/docker/config.yaml,dst=/etc/ipmitool-microservice/config.yaml,readonly \
  --mount type=bind,src=/etc/ipmitool-microservice/docker/tls,dst=/etc/ipmitool-microservice/tls,readonly \
  --mount type=bind,src=/var/lib/ipmitool-microservice-docker,dst=/var/lib/ipmitool-microservice \
  ipmitool-microservice:local
```

Use the earlier `curl --cacert "$TLS_DIR/cert.pem"` health check against
`https://127.0.0.1:8443/api/v1/health`. Keep the private key outside the image and
restart the container after replacing mounted certificates or configuration.

## Systemd service

The [service unit](deploy/ipmitool-microservice.service) runs under the dedicated
`ipmitool-api` system user. Install ipmitool on the host before starting this unit;
its privilege restrictions intentionally prevent automatic apt installation.

Build the binary, install the dependency, and create the service account if needed:

```bash
go build -o ipmitool-microservice .
sudo apt update
sudo apt install ipmitool
id -u ipmitool-api >/dev/null 2>&1 || \
  sudo useradd --system --user-group --no-create-home \
    --home-dir /var/lib/ipmitool-microservice \
    --shell /usr/sbin/nologin ipmitool-api
```

Install the binary, a configured token file, and the unit:

```bash
sudo install -m 0755 -o root -g root ipmitool-microservice \
  /usr/local/bin/ipmitool-microservice
sudo install -d -m 0750 -o root -g ipmitool-api /etc/ipmitool-microservice
sudo install -m 0640 -o root -g ipmitool-api config.yaml \
  /etc/ipmitool-microservice/config.yaml
sudo install -m 0644 -o root -g root deploy/ipmitool-microservice.service \
  /etc/systemd/system/ipmitool-microservice.service
```

If TLS is enabled, install its files before starting the unit. This example uses
the paths shown in the TLS configuration:

```bash
sudo install -d -m 0750 -o root -g ipmitool-api /etc/ipmitool-microservice/tls
sudo install -m 0640 -o root -g ipmitool-api "$TLS_DIR/cert.pem" \
  /etc/ipmitool-microservice/tls/cert.pem
sudo install -m 0640 -o root -g ipmitool-api "$TLS_DIR/key.pem" \
  /etc/ipmitool-microservice/tls/key.pem
```

Review the deployed configuration, then load and start the unit:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ipmitool-microservice.service
sudo systemctl status ipmitool-microservice.service
sudo journalctl --unit ipmitool-microservice.service --follow
```

Systemd creates `/var/lib/ipmitool-microservice` as the writable state directory
and working directory. Put files used by native ipmitool operations there.
`ProtectSystem=strict` makes other system paths read-only, `ProtectHome=yes`
restricts home directories, and `PrivateTmp=yes` provides a private temporary
directory. `NoNewPrivileges=yes` prevents privilege elevation. The unit restarts
a failed service after 5 seconds and sets `GIN_MODE=release`.

The configuration and TLS keys use ownership `root:ipmitool-api` and mode `0640`.
After changing the token, allowlist, certificates, or other settings:

```bash
sudo systemctl restart ipmitool-microservice.service
```

## Logging

Zap writes structured JSON logs to stderr. Logs include server startup and
shutdown, the actual listener address, whether TLS is enabled, the detected
ipmitool version, and one entry per HTTP request.

Request entries contain `method`, the route template, HTTP `status`, `duration`,
and the TCP peer `client_ip`. For example, requests to `/api/v1/lan/1` use the
route template `/api/v1/lan/:channel`; unmatched paths use `unknown`. Request
headers, bodies, query strings, command arguments, stdin, and command output are
not included in request logs. Panic recovery logs a generic error without the
panic value or request contents.

Docker captures stderr through `docker logs`; systemd captures it in the journal.
Native command stdout and stderr are returned to the authenticated HTTP client
and can contain sensitive BMC information.

## Connection data

Every command endpoint accepts a `connection` object:

```json
{
  "connection": {
    "host": "192.0.2.10",
    "username": "ADMIN",
    "password": "bmc-password"
  }
}
```

| Field in `connection` | Required | Default / rule |
| --- | --- | --- |
| `host` | Yes | IP address or hostname, without a protocol or port |
| `username` | Yes | Nonempty; up to 16 bytes, without control characters |
| `password` | Yes | Up to 20 bytes with `lanplus` or 16 with `lan`; explicit `""` is accepted, omission and `null` are not |
| `interface` | No | `lanplus`; also accepts `lan` |
| `port` | No | `623` when omitted; otherwise an integer from 1 to 65535 |
| `privilege` | No | `CALLBACK`, `USER`, `OPERATOR`, `ADMINISTRATOR`, `OEM`, or `NO ACCESS`; the native `+` suffix is accepted |
| `auth_type` | No | `NONE`, `MD2`, `MD5`, `PASSWORD`, `OEM`, or `RMCP+` |
| `cipher_suite` | No | Uses the ipmitool default; accepts an integer from 0 to 255, subject to BMC support |

Example with optional IPMI 2.0 parameters:

```json
{
  "connection": {
    "host": "bmc.example.internal",
    "username": "ADMIN",
    "password": "bmc-password",
    "interface": "lanplus",
    "port": 623,
    "privilege": "ADMINISTRATOR",
    "cipher_suite": 17
  },
  "args": ["mc", "info"]
}
```

For IPv6, use an unbracketed address such as `"host":"2001:db8::10"` and keep
`port` separate. `host` does not accept `https://`, paths, or `host:port`.
Only the remote `lan` and `lanplus` interfaces are exposed.

Unknown fields are rejected, including inside `connection`. `password_env`,
`options`, and `binary` are not request fields. `privilege` and `auth_type` are
case-insensitive; omitting them preserves the ipmitool defaults. Optional fields
do not accept `null`; explicit empty strings are also rejected for `interface`,
`privilege`, and `auth_type`. Credential limits count bytes: UTF-8 characters may
use more than one byte.

The connection exists only for that execution. The API does not register devices,
persist credentials, or log request bodies. It passes the connection password to
the child process through `IPMI_PASSWORD` and `-E`, with a separate environment
for each request. Native command output can contain sensitive information.

## Preparing the examples

Use a second Bash terminal. Set the API address and enter the credentials:

```bash
export API_URL='http://127.0.0.1:8080/api/v1'
export BMC_HOST='192.0.2.10'
export BMC_USER='ADMIN'

read -r -s -p 'API token: ' API_TOKEN
printf '\n'
export API_TOKEN

read -r -s -p 'BMC password: ' BMC_PASSWORD
printf '\n'
export BMC_PASSWORD
```

For TLS, use your HTTPS URL and, if needed, `CURL_CA_BUNDLE` as shown earlier.

A complete call without helper functions:

```bash
jq -n '{connection: {
  host: env.BMC_HOST,
  username: env.BMC_USER,
  password: env.BMC_PASSWORD
}}' |
  curl --fail-with-body --silent --show-error \
    --request POST "$API_URL/power/status" \
    --header "Authorization: Bearer $API_TOKEN" \
    --header 'Content-Type: application/json' \
    --data-binary @-
```

jq escapes quotes, backslashes, and other characters in the password correctly.
The `192.0.2.x` and `2001:db8::x` addresses in this document are examples; replace
them with your equipment addresses.

To avoid repetition, define these functions in the same terminal. They construct
the JSON body and send the HTTP request:

```bash
api() {
  local method="$1"
  local path="$2"
  local extra='{}'
  local payload

  if [ "$#" -ge 3 ]; then
    extra="$3"
  fi

  payload=$(jq -cn --argjson extra "$extra" '
    {connection: {
      host: env.BMC_HOST,
      username: env.BMC_USER,
      password: env.BMC_PASSWORD
    }} * $extra
  ') || return

  curl --fail-with-body --silent --show-error \
    --request "$method" "$API_URL$path" \
    --header "Authorization: Bearer $API_TOKEN" \
    --header 'Content-Type: application/json' \
    --data-binary "$payload"
}

ipmi() {
  local args
  local payload

  args=$(jq -cn --args '$ARGS.positional' -- "$@") || return
  payload=$(jq -cn --argjson args "$args" '{args: $args}') || return
  api POST /commands "$payload"
}
```

`api METHOD ROUTE EXTRA_JSON` includes the connection automatically and allows
its fields to be overridden in the additional JSON. `ipmi command arguments...`
uses `/commands`, preserving separate arguments, including names with spaces
and negative values. Neither helper retries an operation automatically.

The examples below are independent. Run only the action you intend to perform,
especially examples that change power, users, networking, or logs.

## All routes

Prefix: `/api/v1`. Every route requires a Bearer token and passes the IP allowlist.
Except for health, every route requires `Content-Type: application/json` and a
`connection` object in the body.

| Method | Route | Action |
| --- | --- | --- |
| GET | `/health` | API status and verified ipmitool version |
| POST | `/commands` | Run the command and arguments supplied in `args` |
| POST | `/power/status` | Read power state |
| PUT | `/power` | Power on or off |
| POST | `/power/actions` | Power cycle, reset, diagnostic interrupt, or ACPI shutdown |
| POST | `/chassis/status` | Read chassis status |
| PUT | `/boot-device` | Set the boot device |
| POST | `/sensors` | List sensors |
| POST | `/sdr` | List SDR records |
| POST | `/fru` | Read FRU inventory |
| POST | `/sel/entries` | List SEL events |
| POST | `/sel/clear` | Clear SEL events |
| POST | `/mc/info` | Read management controller information |
| POST | `/mc/resets` | Reset the management controller |
| POST | `/lan/{channel}` | Read LAN settings for a channel |
| POST | `/users/{channel}` | List users for a channel |

BMC queries use POST so credentials can be sent in the body. Credentials are not
included in URLs, and GET requests do not require a body. The former
`/targets/{target}/...` routes do not exist.

## Examples by action

### Check the API

Health is the only route that does not require connection data:

```bash
curl --fail-with-body --silent --show-error \
  --header "Authorization: Bearer $API_TOKEN" \
  "$API_URL/health"
```

Example response:

```json
{
  "status": "ok",
  "ipmitool_version": "ipmitool version 1.8.19"
}
```

The version depends on the installed executable. Health confirms that the API
started successfully; it does not test connectivity to a BMC.

### Read power state

```bash
api POST /power/status
```

Runs `chassis power status`.

### Power on and off

Power on:

```bash
api PUT /power '{"state":"on"}'
```

Power off immediately:

```bash
api PUT /power '{"state":"off"}'
```

`state` accepts only `on` or `off`. To request an operating-system shutdown, use
the `soft` action below if the equipment supports ACPI.

### Run power actions

| `action` | Effect |
| --- | --- |
| `cycle` | Power the server off and on again |
| `reset` | Hardware reset |
| `diag` | Request a diagnostic interrupt, subject to hardware support |
| `soft` | Request an ACPI shutdown |

```bash
# Power off and on again.
api POST /power/actions '{"action":"cycle"}'

# Hardware reset.
api POST /power/actions '{"action":"reset"}'

# Diagnostic interrupt.
api POST /power/actions '{"action":"diag"}'

# Request an ACPI shutdown.
api POST /power/actions '{"action":"soft"}'
```

### Read chassis status

```bash
api POST /chassis/status
```

Runs `chassis status`, returning power, cooling, and fault information supported
by the equipment.

### Set the boot device

PXE for the next boot:

```bash
api PUT /boot-device '{"device":"pxe"}'
```

Persistent PXE:

```bash
api PUT /boot-device '{"device":"pxe","persistent":true}'
```

Disk for the next boot:

```bash
api PUT /boot-device '{"device":"disk","persistent":false}'
```

| `device` | Device/mode |
| --- | --- |
| `none` | No forced boot device |
| `pxe` | Network boot |
| `disk` | Disk |
| `safe` | Disk in safe mode, when supported |
| `diag` | Diagnostics |
| `cdrom` | CD/DVD |
| `bios` | BIOS/firmware setup |
| `floppy` | Floppy disk |

`persistent` is optional and defaults to `false`. Setting the boot device does
not restart the server; request the desired power action separately.

### Read sensors, SDR, and FRU inventory

```bash
# sensor list
api POST /sensors

# sdr list
api POST /sdr

# fru print
api POST /fru
```

Use the generic route below to query a specific sensor.

### List and clear SEL events

List events:

```bash
api POST /sel/entries
```

Clear the events stored on the BMC:

```bash
api POST /sel/clear
```

The second call runs `sel clear`; the API has no additional confirmation step.
Export the event history first if it must be preserved.

### Read information and reset the BMC

Read controller information:

```bash
api POST /mc/info
```

Warm reset:

```bash
api POST /mc/resets '{"mode":"warm"}'
```

Cold reset:

```bash
api POST /mc/resets '{"mode":"cold"}'
```

`mode` accepts `warm` or `cold`. These actions reset the management controller;
use `/power/actions` for server power operations. Management connectivity may be
unavailable while the BMC restarts.

### Read network settings and users

```bash
# lan print 1
api POST /lan/1

# user list 1
api POST /users/1
```

Replace `1` with the channel used by your equipment. The API accepts numbers from
0 to 15; the BMC determines which channels exist and which operations they support.

### Use another connection in the same terminal

Change the default host for subsequent examples:

```bash
export BMC_HOST='192.0.2.20'
api POST /power/status
```

Override connection fields for a single request:

```bash
api POST /mc/info '{"connection":{"host":"192.0.2.30","cipher_suite":17}}'
```

Use an IPMI 1.5 BMC when required:

```bash
api POST /mc/info '{"connection":{"interface":"lan","auth_type":"MD5"}}'
```

Explicitly send an empty password:

```bash
api POST /mc/info '{"connection":{"password":""}}'
```

The helper retains the other connection fields from the terminal environment.
The API does not keep a persistent connection or reuse credentials between requests.

## Generic ipmitool commands

`POST /commands` accepts `args`, an array of strings. It exposes operations that
do not have dedicated routes, including OEM commands and new subcommands from
the installed version. Optional `stdin` supplies finite input to the process.

`args` is required and contains 1 to 256 strings, without null elements or NUL
bytes, totaling at most 64 KiB. Its first element must be a command name beginning
with an ASCII letter and containing only ASCII letters, digits, `_`, or `-`.
`stdin` must be a string of at most 64 KiB; an empty string is accepted, but
`null` is not.

Complete request body:

```json
{
  "connection": {
    "host": "192.0.2.10",
    "username": "ADMIN",
    "password": "bmc-password"
  },
  "args": ["sensor", "get", "CPU Temp"]
}
```

Equivalent helper calls:

```bash
api POST /commands '{"args":["sensor","get","CPU Temp"]}'
ipmi sensor get 'CPU Temp'
```

Do not prepend `ipmitool`, include global flags such as `-H`/`-U`/`-P`, or combine
an entire command line into one string. Connection parameters belong in
`connection`. The API passes arguments literally without `sh -c`, separating
global options from the command with `--`. Shell operators such as `|`, `>`, and
`&&` are not executed.

### Discover available commands

```bash
# Command catalog from the installed executable.
ipmi help

# Help from commands that support this subcommand.
ipmi chassis help
ipmi sensor help
```

Native help may be written to stderr and return a nonzero exit code. In that
case, the API returns HTTP `502` with the text in `result.stderr`. Some command
families do not implement `help`, and family-specific help may require a valid
BMC connection. In particular, `exec help` treats `help` as a filename, while
`shell` enters its command interpreter instead of using an argv help subcommand.
Also consult `man ipmitool` on the API host and the
[upstream ipmitool manual](https://github.com/ipmitool/ipmitool/blob/master/doc/ipmitool.1.in).

The command families in ipmitool 1.8.19 include:

| Area | Families |
| --- | --- |
| Power and chassis | `chassis`, `power` |
| Sensors and inventory | `sensor`, `sdr`, `fru`, `spd`, `gendev` |
| Networking, channels, users, and sessions | `lan`, `lan6`, `channel`, `user`, `session` |
| Controller, events, and filters | `mc`, `event`, `sel`, `pef`, `firewall` |
| Power and platform management | `dcmi`, `nm`, `picmg`, `vita` |
| Low-level access | `raw`, `i2c` |
| Serial over LAN | `sol`, `isol`, `tsol` |
| Vendor commands | `delloem`, `sunoem`, `kontronoem` |
| Firmware and analysis | `hpm`, `fwum`, `ime`, `ekanalyzer` |
| Batches and session variables | `shell`, `exec`, `set` |

This catalog does not guarantee that your BMC supports every command. The API
forwards the command; the ipmitool build and hardware determine the result.
Local interfaces and global options without a corresponding `connection` field
are not exposed by the current HTTP contract.

### Sensors, SDR, and FRU

Queries:

```bash
ipmi sensor get 'CPU Temp'
ipmi sdr elist all
ipmi fru print 0
```

Change a sensor threshold after checking its name and a suitable hardware value:

```bash
ipmi sensor thresh 'CPU Temp' ucr 85
```

Example syntax for three lower thresholds, including a negative number:

```bash
ipmi sensor thresh 'Ambient Temp' lower -5 0 5
```

These values are illustrative. Both `thresh` calls change thresholds on the BMC.

### Events and controller information

```bash
ipmi sel info
ipmi sel get 0x0001
ipmi mc getenables
ipmi channel info 1
ipmi channel getaccess 1 2
ipmi session info all
ipmi dcmi power reading
ipmi sol info 1
```

`0x0001` is an example event ID; use an ID returned by the SEL listing.
`getaccess 1 2` queries user ID 2 on channel 1.

### Advanced chassis and boot operations

Read boot parameters:

```bash
ipmi chassis bootparam get 5
```

Activate the chassis identification indicator for 15 seconds, when supported:

```bash
ipmi chassis identify 15
```

Use native arguments for boot options absent from the dedicated route:

```bash
ipmi chassis bootdev pxe options=efiboot
```

This changes the boot configuration; EFI option support depends on the BMC.

### Manage users

List users before choosing an ID to modify:

```bash
ipmi user list 1
```

Independent examples for user ID 3:

```bash
# Change the username.
ipmi user set name 3 operator

# Change the password; use a password supported by your BMC.
ipmi user set password 3 'NEW_BMC_PASSWORD'

# Grant OPERATOR privilege (3) on channel 1.
ipmi user priv 3 3 1

# Enable or disable this user.
ipmi user enable 3
ipmi user disable 3
```

Check the ID before modifying an administrative account. The new password in
`user set password` is a native command argument. The connection password is
passed through the environment. Treat access to process information on the API
host as privileged.

### Manage BMC networking

Read the current configuration:

```bash
ipmi lan print 1
```

Example changes for a static network:

```bash
ipmi lan set 1 ipsrc static
ipmi lan set 1 ipaddr 192.0.2.50
```

After changing the address, update the destination for subsequent calls:

```bash
export BMC_HOST='192.0.2.50'
ipmi lan set 1 netmask 255.255.255.0
ipmi lan set 1 defgw ipaddr 192.0.2.1
```

These changes may interrupt management access. Use your actual network values
and a maintenance procedure appropriate for the equipment.

### Send RAW commands

Read Device ID with net function `0x06` and command `0x01`:

```bash
ipmi raw 0x06 0x01
```

Raw bytes and OEM commands depend on the vendor. When constructing JSON directly,
pass each byte as a string: `"args":["raw","0x06","0x01"]`.

### Files, exports, and batches

Save a textual API response on the **client computer**:

```bash
payload=$(api POST /sel/entries) && jq -r '.stdout' <<< "$payload" > sel.txt
payload=$(api POST /sensors) && jq -r '.stdout' <<< "$payload" > sensors.txt
```

Examples that read or write files **on the API host**:

```bash
ipmi fru read 0 /var/lib/ipmitool-microservice/fru-0.bin
ipmi sel save /var/lib/ipmitool-microservice/sel.txt
ipmi sdr dump /var/lib/ipmitool-microservice/sdr.bin
ipmi exec /var/lib/ipmitool-microservice/commands.txt
```

The directory must exist and be accessible to the API process user. These paths
are unrelated to the HTTP client's filesystem. The API has no file upload or
download endpoint; a command such as `fru read` writes on the server and does not
convert the binary file into JSON. `exec` reads a command file; `exec -` is not a
shortcut for stdin.

For a batch through stdin, a build with readline can use ipmitool's own shell:

```bash
api POST /commands '{"args":["shell"],"stdin":"chassis power status\nmc info\nquit\n"}'
```

To load a batch from a file on the client computer:

```bash
printf 'chassis power status\nmc info\nquit\n' > commands.txt
payload=$(jq -n --rawfile input commands.txt '{args:["shell"],stdin:$input}')
api POST /commands "$payload"
```

The `shell`/`exec` parser belongs to ipmitool. Use one request per operation when
you need individual command results. `set` affects only the current process and
does not persist between HTTP requests.

### Firmware, OEM, and consoles

Operations such as `hpm`, `fwum`, `ime`, `delloem`, `sunoem`, and `kontronoem` use
the same generic route. Convert the vendor's native command into an `args` array.
For example, `delloem <subcommand> <argument>` becomes
`["delloem","<subcommand>","<argument>"]`, with the placeholders replaced.

Inspect the syntax before constructing an operation:

```bash
ipmi hpm help
ipmi fwum help
ipmi ime help
ipmi delloem help
ipmi sunoem help
ipmi kontronoem help
```

Firmware files must be available on the API host. Configure a suitable timeout
before the operation and check the model, image, and syntax in the vendor manual.
There is no universal upgrade sequence for every BMC.

SOL queries and configuration, such as `sol info`, work like other commands.
`sol activate`, `isol activate`, `tsol`, and continuous consoles requiring a TTY
have no terminal session in this synchronous API. `stdin` does not provide a
WebSocket, PTY, or continuous interactive connection.

## Responses and errors

### Success

A command that finishes with exit code zero returns HTTP `200`:

```json
{
  "stdout": "Chassis Power is on\n",
  "stderr": "",
  "exit_code": 0,
  "truncated": false
}
```

`stdout` and `stderr` retain the original text. The API does not convert ipmitool
tables into object lists. Nonempty stderr alone does not indicate failure; the
execution status depends on the exit code.

### Input or authentication error

Example HTTP `401` response:

```json
{
  "error": {
    "code": "unauthorized",
    "message": "Invalid or missing authorization token"
  }
}
```

Check authentication without sending a token:

```bash
curl --include "$API_URL/health"
```

Check invalid connection input without executing a BMC command:

```bash
api POST /power/status '{"connection":{"password":null}}'
```

Other examples that return HTTP `400` before command execution:

```bash
api POST /power/status '{"connection":{"port":0}}'
api POST /mc/info '{"connection":{"privilege":"INVALID"}}'
api PUT /boot-device '{"device":"pxe","persistent":"true"}'
api POST /commands '{"args":[]}'
```

Boolean fields require `true` or `false`, numeric fields require integers, and
unknown fields are rejected. Accepted states, actions, devices, and modes appear
in the route examples above. Validation identifies the problem without echoing
submitted credentials.

### Execution error

Illustrative HTTP `502` response:

```json
{
  "error": {
    "code": "command_failed",
    "message": "ipmitool returned an error"
  },
  "result": {
    "stdout": "",
    "stderr": "Error returned by ipmitool\n",
    "exit_code": 1,
    "truncated": false
  }
}
```

Execution errors include `result`, even with partial output. The actual exit code
can differ from this example, including `-1` when no normal exit status exists.
Errors detected before execution can contain only `error`.

| HTTP | `error.code` | Cause |
| --- | --- | --- |
| 400 | `invalid_json` | Invalid JSON, unexpected fields/types, null values, or data after the object |
| 400 | `invalid_connection` | Missing or invalid connection data |
| 400 | `invalid_request` | Invalid state, action, device, mode, or channel |
| 400 | `invalid_command` | Invalid command/arguments or exceeded input limit |
| 401 | `unauthorized` | Missing/incorrect Bearer token or duplicate authorization header |
| 403 | `ip_not_allowed` | TCP peer IP is outside the configured allowlist |
| 404 | `not_found` | Unknown route |
| 405 | `method_not_allowed` | Unsupported HTTP method |
| 408 | `canceled` | Request context canceled |
| 413 | `body_too_large` | JSON body larger than 128 KiB |
| 415 | `unsupported_media_type` | Content-Type is not application/json |
| 500 | `execution_failed` | Failed to start or wait for the process |
| 500 | `invalid_runner_response` | Internal runner returned an inconsistent exit status |
| 500 | `internal_error` | An unexpected handler panic was recovered |
| 502 | `command_failed` | ipmitool finished with a nonzero exit code |
| 503 | `busy` | Global simultaneous command limit reached |
| 504 | `timeout` | Command deadline exceeded |

HTTP `503` includes `Retry-After: 1`; the API does not queue the request. A canceled
client may already have disconnected before a `408` response can reach it.
Timeouts and cancellation do not undo a change already sent to the BMC. Check its
state before retrying a write operation; the example helpers do not retry.

## Limits and behavior

| Item | Limit / behavior |
| --- | --- |
| HTTP body | 128 KiB of JSON |
| Arguments | Up to 256 strings, including the command name |
| Total argument size | 64 KiB |
| `stdin` | 64 KiB |
| Credentials | Username up to 16 bytes; password up to 16 (`lan`) or 20 (`lanplus`); NUL rejected |
| Output | Default 1 MiB for stdout and 1 MiB for stderr; configurable |
| Concurrency | Default 4 commands across the API, shared by every target |
| Execution | Synchronous; responds on completion, error, or timeout |
| Excess output | Discarded with `truncated: true`; does not change the exit code by itself |
| Client disconnect | Cancels the associated process |
| Persistence | No jobs, BMC registration, or execution history |

For longer operations, change `ipmitool.timeout` and restart the service. Adjust
the client/proxy timeout too; `timeout` is not a JSON request field.

Configuration controls the executable and global resource limits. Authentication
grants access to every operation; there are no per-route permission levels. Run
the service with a dedicated user and access only to necessary files. Local
configuration, `.env`, binaries, reports, and IDE files are excluded from Git by
[.gitignore](.gitignore).

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| Cannot locate ipmitool | Check the configured path and execution permission; use `binary: ipmitool` for automatic discovery |
| Automatic installation fails | Check repository access, apt, disk space, and permissions; installer output appears in startup logs |
| Sudo requires a password or rejects frontend preservation | Install ipmitool in advance with `sudo apt install ipmitool`, then run the API as a normal user |
| Systemd cannot install ipmitool | Preinstall ipmitool on the host; the unit prevents privilege elevation |
| `verify ipmitool` at startup | Run the configured executable with `-V`; check permissions and whether it completes within the deadline |
| Invalid `auth.token` | Set a nonempty Bearer-compatible token; check whether an empty environment variable overrides the file |
| Cannot read configuration | Check the `--config` path, YAML syntax, field types, and file permissions |
| TLS startup failure | Check certificate/key paths, PEM contents, matching keys, and readability by the service user |
| TLS verification failure in curl | Use the certificate hostname and the correct CA; for the local example use `--cacert` |
| Container cannot read config/key | Files must be readable by UID/GID 10001; mount config and certificate files read-only |
| HTTP 401 | Use the API token, not the BMC password, in a single Bearer header |
| HTTP 403 | Check the actual TCP peer IP against `server.allowed_ips`; proxy/container networking may change it |
| HTTP 400 | Check `connection`, types, required fields, and exact field names |
| HTTP 405 on a query | BMC queries use POST; only health uses GET |
| HTTP 415 | Send `Content-Type: application/json` |
| HTTP 502 | Read `result.stderr` and `exit_code`; check password, interface, privilege, cipher suite, and command support |
| BMC connection fails | Check the host, management network, and RMCP/UDP access, usually port 623, from the API host/container |
| HTTP 503 | Too many commands are active; reduce client concurrency or adjust the server limit |
| HTTP 504 | Check BMC state and the timeout; verify the result before repeating a write |
| `truncated: true` | Increase `max_output_bytes` if full output is needed, and rerun only when appropriate |
| Exported file is absent on the client | Native command paths refer to the API host; use client redirection for local text output |

## Tests and project layout

Run the tests and static analysis:

```bash
go test -race ./...
go vet ./...
```

Generate a coverage report if needed:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

Tests use `httptest` and a simulated executable. Coverage includes authentication,
routes, per-request connections, concurrent requests with distinct passwords,
literal argv, input validation, output, timeout, cancellation, and resource
limits. TLS, IP filtering, and request logging have dedicated tests. Installer
tests simulate the system and apt; they do not install packages or require root.
Lifecycle tests open temporary loopback ports. Tests do not require a BMC or
installed ipmitool and do not modify real equipment.

```text
main.go                         # Process entry point and signals
internal/cli/                   # Cobra and HTTP/HTTPS server lifecycle
internal/config/                # Viper, defaults, and validation
internal/logging/               # Zap JSON logger
internal/api/router.go          # Gin route registration
internal/api/middleware/        # Bearer auth, IP filtering, logs, and recovery
internal/api/handlers/          # One file per action and shared request helpers
internal/api/response/          # JSON responses and errors
internal/ipmi/                  # Binary discovery, connection, argv, and execution
Dockerfile                      # Multistage non-root container image
.dockerignore                   # Container build-context exclusions
deploy/ipmitool-microservice.service # Systemd service unit
```

[config.example.yaml](config.example.yaml) is the tracked configuration template.
Keep actual tokens, BMC credentials, and private keys outside version control.
