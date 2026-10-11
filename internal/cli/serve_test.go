package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ipmitool-microservice/internal/config"
	"ipmitool-microservice/internal/ipmi"
)

type startupWriter chan string

func (w startupWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func commandConfig(t *testing.T, binary string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := fmt.Sprintf(`server:
  address: 127.0.0.1:0
auth:
  token: cli-test-token
ipmitool:
  binary: %q
`, binary)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestServeCommandLifecycle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%t", enabled), func(t *testing.T) {
			testServeCommandLifecycle(t, enabled)
		})
	}
}

func testServeCommandLifecycle(t *testing.T, tlsEnabled bool) {
	binary := filepath.Join(t.TempDir(), "ipmitool")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = '-V' ]; then echo 'ipmitool version test'; else printf 'command completed'; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := NewCommand()
	configPath := commandConfig(t, binary)
	scheme := "http"
	client := &http.Client{Timeout: 5 * time.Second}
	if tlsEnabled {
		settings, roots := testCertificate(t)
		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		content = append([]byte(fmt.Sprintf("server:\n  address: 127.0.0.1:0\n  tls:\n    enabled: true\n    cert_file: %q\n    key_file: %q\n", settings.CertFile, settings.KeyFile)), content[strings.Index(string(content), "auth:"):]...)
		if err := os.WriteFile(configPath, content, 0600); err != nil {
			t.Fatal(err)
		}
		scheme = "https"
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	}
	defer client.CloseIdleConnections()
	cmd.SetArgs([]string{"serve", "--config", configPath})
	started := make(startupWriter, 32)
	cmd.SetErr(started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	var address string
	select {
	case line := <-started:
		var entry struct {
			Message string `json:"msg"`
			Address string `json:"address"`
			TLS     bool   `json:"tls"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Message != "API started" || entry.Address == "" || entry.TLS != tlsEnabled {
			t.Fatalf("unexpected startup message: %q", line)
		}
		address = entry.Address
	case err := <-done:
		t.Fatalf("server stopped before startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}
	for _, authorized := range []bool{false, true} {
		requestBody := `{
			"connection": {
				"host": "192.0.2.10",
				"username": "ADMIN",
				"password": "test-password"
			},
			"args": ["help"]
		}`
		req, err := http.NewRequest(http.MethodPost, scheme+"://"+address+"/api/v1/commands", strings.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if authorized {
			req.Header.Set("Authorization", "Bearer cli-test-token")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !authorized {
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("unauthorized status: %d", res.StatusCode)
			}
			continue
		}
		var result ipmi.Result
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || result.ExitCode != 0 || result.Stdout != "command completed" {
			t.Fatalf("unexpected command response: %d %s", res.StatusCode, body)
		}
	}
	if tlsEnabled {
		connection, err := tls.Dial("tcp", address, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS11, MaxVersion: tls.VersionTLS11})
		if err == nil {
			connection.Close()
			t.Fatal("server accepted TLS older than 1.2")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown failed: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

func testCertificate(t *testing.T) (config.TLS, *x509.CertPool) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	settings := config.TLS{Enabled: true, CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem")}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(settings.CertFile, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("invalid test certificate")
	}
	return settings, roots
}

func TestLoadTLS(t *testing.T) {
	if cfg, err := loadTLS(config.TLS{CertFile: "missing", KeyFile: "missing"}); cfg != nil || err != nil {
		t.Fatalf("disabled TLS must not read certificates: %v", err)
	}
	settings, _ := testCertificate(t)
	cfg, err := loadTLS(settings)
	if err != nil || cfg.MinVersion != tls.VersionTLS12 || len(cfg.Certificates) != 1 {
		t.Fatalf("valid TLS configuration rejected: %v", err)
	}
	other, _ := testCertificate(t)
	for _, invalid := range []config.TLS{
		{Enabled: true, CertFile: "missing", KeyFile: settings.KeyFile},
		{Enabled: true, CertFile: settings.CertFile, KeyFile: "missing"},
		{Enabled: true, CertFile: settings.CertFile, KeyFile: other.KeyFile},
	} {
		if _, err := loadTLS(invalid); err == nil {
			t.Fatal("invalid or mismatched certificate accepted")
		}
	}
}

func TestServeRejectsMissingConfiguredIPMITool(t *testing.T) {
	cmd := NewCommand()
	cmd.SetArgs([]string{"serve", "--config", commandConfig(t, filepath.Join(t.TempDir(), "missing-ipmitool"))})
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "locate ipmitool configured as") {
		t.Fatalf("missing custom binary must fail startup without installation: %v", err)
	}
}
