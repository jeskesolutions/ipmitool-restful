package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"IPMI_API_AUTH_TOKEN", "IPMI_API_SERVER_ADDRESS", "IPMI_API_IPMITOOL_BINARY",
		"IPMI_API_IPMITOOL_TIMEOUT", "IPMI_API_IPMITOOL_MAX_OUTPUT_BYTES", "IPMI_API_IPMITOOL_MAX_CONCURRENT",
		"IPMI_API_SERVER_TLS_ENABLED", "IPMI_API_SERVER_TLS_CERT_FILE", "IPMI_API_SERVER_TLS_KEY_FILE",
		"IPMI_API_SERVER_ALLOWED_IPS",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestLoadDefaults(t *testing.T) {
	cleanEnvironment(t)
	cfg, err := Load(writeConfig(t, `auth:
  token: api-secret
`))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth.Token != "api-secret" || cfg.Server.Address != "127.0.0.1:8080" {
		t.Fatal("token or default address was not loaded")
	}

	if cfg.IPMITool.Binary != "ipmitool" || cfg.IPMITool.Timeout != 30*time.Second || cfg.IPMITool.MaxOutputBytes != 1<<20 || cfg.IPMITool.MaxConcurrent != 4 {
		t.Errorf("unexpected ipmitool defaults: %+v", cfg.IPMITool)
	}

	if cfg.Server.TLS.Enabled || len(cfg.Server.AllowedIPs) != 0 {
		t.Fatal("TLS and the IP whitelist must be disabled by default")
	}
}

func TestLoadEnvironmentOverrides(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("IPMI_API_AUTH_TOKEN", "env-token")
	t.Setenv("IPMI_API_SERVER_ADDRESS", "[::1]:9090")
	t.Setenv("IPMI_API_IPMITOOL_BINARY", "/opt/ipmitool")
	t.Setenv("IPMI_API_IPMITOOL_TIMEOUT", "2m")
	t.Setenv("IPMI_API_IPMITOOL_MAX_OUTPUT_BYTES", "8192")
	t.Setenv("IPMI_API_IPMITOOL_MAX_CONCURRENT", "2")
	t.Setenv("IPMI_API_SERVER_TLS_ENABLED", "true")
	t.Setenv("IPMI_API_SERVER_TLS_CERT_FILE", "/etc/ipmi/server.pem")
	t.Setenv("IPMI_API_SERVER_TLS_KEY_FILE", "/etc/ipmi/server.key")
	t.Setenv("IPMI_API_SERVER_ALLOWED_IPS", "192.0.2.5, 2001:db8::/32")
	cfg, err := Load(writeConfig(t, `auth:
  token: file-token
server:
  address: 127.0.0.1:8081
ipmitool:
  binary: other-ipmitool
  timeout: 1s
  max_output_bytes: 4096
  max_concurrent: 1
`))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Auth.Token != "env-token" || cfg.Server.Address != "[::1]:9090" || cfg.IPMITool.Binary != "/opt/ipmitool" || cfg.IPMITool.Timeout != 2*time.Minute || cfg.IPMITool.MaxOutputBytes != 8192 || cfg.IPMITool.MaxConcurrent != 2 {
		t.Fatal("environment values did not override file configuration")
	}

	if !cfg.Server.TLS.Enabled || cfg.Server.TLS.CertFile != "/etc/ipmi/server.pem" || cfg.Server.TLS.KeyFile != "/etc/ipmi/server.key" || strings.Join(cfg.Server.AllowedIPs, ",") != "192.0.2.5,2001:db8::/32" {
		t.Fatal("TLS or IP whitelist environment values were not loaded")
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	const valid = "auth:\n  token: api-secret\n"
	tests := []struct {
		name    string
		content string
	}{
		{"missing token", "server:\n  address: 127.0.0.1:8080\n"},
		{"whitespace token", strings.Replace(valid, "api-secret", "\"api secret\"", 1)},
		{"NUL token", strings.Replace(valid, "api-secret", "\"api\\0secret\"", 1)},
		{"control in token", strings.Replace(valid, "api-secret", "\"api\\x7fsecret\"", 1)},
		{"non-ASCII token", strings.Replace(valid, "api-secret", "secret-é", 1)},
		{"token with unsupported punctuation", strings.Replace(valid, "api-secret", "'secret!value'", 1)},
		{"token with misplaced padding", strings.Replace(valid, "api-secret", "secret=value", 1)},
		{"only token padding", strings.Replace(valid, "api-secret", "'==='", 1)},
		{"boolean token", "auth:\n  token: true\n"},
		{"number token", "auth:\n  token: 123456\n"},
		{"null token", "auth:\n  token: null\n"},
		{"null auth section", "auth: null\n"},
		{"obsolete targets", valid + "targets:\n  remote:\n    host: 192.0.2.10\n    password: never-reveal-this-secret\n"},
		{"empty obsolete targets", valid + "targets: {}\n"},
		{"zero timeout", valid + "ipmitool:\n  timeout: 0s\n"},
		{"negative timeout", valid + "ipmitool:\n  timeout: -1s\n"},
		{"timeout too high", valid + "ipmitool:\n  timeout: 25h\n"},
		{"numeric timeout", valid + "ipmitool:\n  timeout: 1000000\n"},
		{"null timeout", valid + "ipmitool:\n  timeout: null\n"},
		{"empty binary", valid + "ipmitool:\n  binary: ''\n"},
		{"NUL binary", valid + "ipmitool:\n  binary: \"ipmi\\0tool\"\n"},
		{"control in binary", valid + "ipmitool:\n  binary: \"ipmi\\ntool\"\n"},
		{"number binary", valid + "ipmitool:\n  binary: 123\n"},
		{"map binary", valid + "ipmitool:\n  binary: {}\n"},
		{"null binary", valid + "ipmitool:\n  binary: null\n"},
		{"zero output limit", valid + "ipmitool:\n  max_output_bytes: 0\n"},
		{"output limit too high", valid + "ipmitool:\n  max_output_bytes: 67108865\n"},
		{"float output limit", valid + "ipmitool:\n  max_output_bytes: 1024.5\n"},
		{"string output limit", valid + "ipmitool:\n  max_output_bytes: '1024'\n"},
		{"zero concurrency", valid + "ipmitool:\n  max_concurrent: 0\n"},
		{"concurrency too high", valid + "ipmitool:\n  max_concurrent: 1025\n"},
		{"null concurrency", valid + "ipmitool:\n  max_concurrent: null\n"},
		{"boolean concurrency", valid + "ipmitool:\n  max_concurrent: true\n"},
		{"float concurrency", valid + "ipmitool:\n  max_concurrent: 1.0\n"},
		{"address without port", valid + "server:\n  address: localhost\n"},
		{"address port too high", valid + "server:\n  address: localhost:65536\n"},
		{"address negative port", valid + "server:\n  address: localhost:-1\n"},
		{"address port not numeric", valid + "server:\n  address: localhost:http\n"},
		{"address with whitespace", valid + "server:\n  address: 'local host:8080'\n"},
		{"address with NUL", valid + "server:\n  address: \"local\\0host:8080\"\n"},
		{"address with path", valid + "server:\n  address: example.com/path:8080\n"},
		{"address with empty DNS label", valid + "server:\n  address: example..com:8080\n"},
		{"address with bracketed hostname", valid + "server:\n  address: '[hostname]:8080'\n"},
		{"null address", valid + "server:\n  address: null\n"},
		{"null server section", valid + "server: null\n"},
		{"TLS without certificate", valid + "server:\n  tls:\n    enabled: true\n    key_file: server.key\n"},
		{"TLS without key", valid + "server:\n  tls:\n    enabled: true\n    cert_file: server.pem\n"},
		{"TLS certificate control", valid + "server:\n  tls:\n    cert_file: \"server\\n.pem\"\n"},
		{"TLS key control", valid + "server:\n  tls:\n    key_file: \"server\\x7f.key\"\n"},
		{"TLS enabled as string", valid + "server:\n  tls:\n    enabled: 'true'\n"},
		{"TLS enabled as number", valid + "server:\n  tls:\n    enabled: 1\n"},
		{"TLS enabled null", valid + "server:\n  tls:\n    enabled: null\n"},
		{"TLS null", valid + "server:\n  tls: null\n"},
		{"TLS unexpected field", valid + "server:\n  tls:\n    unexpected: true\n"},
		{"whitelist null", valid + "server:\n  allowed_ips: null\n"},
		{"whitelist not a list", valid + "server:\n  allowed_ips: 192.0.2.5\n"},
		{"whitelist numeric member", valid + "server:\n  allowed_ips: [123]\n"},
		{"whitelist null member", valid + "server:\n  allowed_ips: [null]\n"},
		{"whitelist empty member", valid + "server:\n  allowed_ips: ['']\n"},
		{"whitelist hostname", valid + "server:\n  allowed_ips: [localhost]\n"},
		{"whitelist invalid IPv4", valid + "server:\n  allowed_ips: [192.0.2.300]\n"},
		{"whitelist invalid prefix", valid + "server:\n  allowed_ips: [192.0.2.0/33]\n"},
		{"whitelist IPv6 scope", valid + "server:\n  allowed_ips: ['fe80::1%eth0']\n"},
		{"unknown field", valid + "unexpected: true\n"},
		{"wrong value type", valid + "ipmitool:\n  timeout: never-reveal-this-secret\n"},
		{"invalid YAML", valid + "private: [never-reveal-this-secret\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanEnvironment(t)
			_, err := Load(writeConfig(t, tt.content))
			if err == nil {
				t.Fatal("expected invalid configuration to fail")
			}

			if strings.Contains(err.Error(), "never-reveal-this-secret") || strings.Contains(err.Error(), "api-secret") {
				t.Fatalf("configuration error exposes a secret: %s", err)
			}
		})
	}
}

func TestLoadAcceptsTLSAndWhitelist(t *testing.T) {
	cleanEnvironment(t)
	cfg, err := Load(writeConfig(t, `auth:
  token: A-valid._~+/token==
server:
  address: "[::]:443"
  tls:
    enabled: true
    cert_file: "/etc/ipmi/server cert.pem"
    key_file: "/etc/ipmi/server key.pem"
  allowed_ips:
    - "192.0.2.5"
    - "192.0.2.0/24"
    - "2001:db8::5"
    - "2001:db8::/32"
`))
	if err != nil {
		t.Fatal(err)
	}

	if !cfg.Server.TLS.Enabled || cfg.Server.TLS.CertFile != "/etc/ipmi/server cert.pem" || cfg.Server.TLS.KeyFile != "/etc/ipmi/server key.pem" || len(cfg.Server.AllowedIPs) != 4 {
		t.Fatal("TLS or whitelist fields were not preserved")
	}
}

func TestLoadRejectsInvalidEnvironmentValues(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"IPMI_API_SERVER_TLS_ENABLED", "1"},
		{"IPMI_API_SERVER_TLS_ENABLED", "yes"},
		{"IPMI_API_SERVER_TLS_ENABLED", ""},
		{"IPMI_API_IPMITOOL_MAX_CONCURRENT", "1.5"},
		{"IPMI_API_IPMITOOL_MAX_CONCURRENT", "true"},
		{"IPMI_API_IPMITOOL_MAX_OUTPUT_BYTES", ""},
		{"IPMI_API_SERVER_ALLOWED_IPS", "192.0.2.5,"},
		{"IPMI_API_SERVER_ALLOWED_IPS", "localhost"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			cleanEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load(writeConfig(t, "auth:\n  token: api-secret\n"))
			if err == nil {
				t.Fatal("invalid environment value was accepted")
			}
		})
	}
}

func TestLoadCanDisableTLSAndWhitelistThroughEnvironment(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("IPMI_API_SERVER_TLS_ENABLED", "false")
	t.Setenv("IPMI_API_SERVER_ALLOWED_IPS", "")
	cfg, err := Load(writeConfig(t, `auth:
  token: api-secret
server:
  tls:
    enabled: true
    cert_file: server.pem
    key_file: server.key
  allowed_ips: [192.0.2.5]
`))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.TLS.Enabled || len(cfg.Server.AllowedIPs) != 0 {
		t.Fatal("explicit environment values did not disable TLS and the whitelist")
	}
}

func TestLoadConfigDiscoveryAndMissingFiles(t *testing.T) {
	cleanEnvironment(t)
	t.Chdir(t.TempDir())
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "auth.token") {
		t.Fatalf("absent implicit configuration should fail required validation: %v", err)
	}

	t.Setenv("IPMI_API_AUTH_TOKEN", "env-token")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("environment token should allow loading defaults without a file: %v", err)
	}

	if cfg.Auth.Token != "env-token" || cfg.Server.Address != "127.0.0.1:8080" {
		t.Fatal("environment token or default address was not loaded")
	}

	if _, err := Load("does-not-exist.yaml"); err == nil || !strings.Contains(err.Error(), "configuration file") {
		t.Fatalf("missing explicit configuration should fail file loading: %v", err)
	}

	if err := os.WriteFile("config.yaml", []byte("server:\n  address: 127.0.0.1:9090\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err = Load("")
	if err != nil {
		t.Fatalf("load config.yaml from current directory: %v", err)
	}

	if cfg.Server.Address != "127.0.0.1:9090" {
		t.Fatal("configuration from the current directory was not loaded")
	}
}

func TestLoadEmptyTokenEnvironmentDoesNotUseFileToken(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("IPMI_API_AUTH_TOKEN", "")
	_, err := Load(writeConfig(t, "auth:\n  token: file-token\n"))
	if err == nil || !strings.Contains(err.Error(), "auth.token") {
		t.Fatalf("explicitly empty environment token must fail: %v", err)
	}
}
