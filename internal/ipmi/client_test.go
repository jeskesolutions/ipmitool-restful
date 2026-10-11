package ipmi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testConnection() Connection {
	return Connection{Host: "192.0.2.5", Username: "admin", Password: "bmc-secret"}
}

func fakeBinary(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ipmitool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeClient(t *testing.T, script string, timeout time.Duration, limit int) *Client {
	t.Helper()
	binary := fakeBinary(t, "if [ \"$1\" = '-V' ]; then printf 'ipmitool version test\\n'; exit 0; fi\n"+script)
	c, err := New(context.Background(), binary, timeout, limit)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewVerifiesBinary(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		c := fakeClient(t, "exit 0\n", time.Second, 1024)
		if c.Version() != "ipmitool version test" {
			t.Fatalf("unexpected version: %q", c.Version())
		}
	})
	t.Run("not installed", func(t *testing.T) {
		_, err := New(context.Background(), filepath.Join(t.TempDir(), "missing"), time.Second, 1024)
		if err == nil {
			t.Fatal("missing binary was accepted")
		}
	})
	t.Run("not executable", func(t *testing.T) {
		binary := fakeBinary(t, "exit 0\n")
		if err := os.Chmod(binary, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(context.Background(), binary, time.Second, 1024); err == nil {
			t.Fatal("nonexecutable binary was accepted")
		}
	})
	t.Run("version failed", func(t *testing.T) {
		_, err := New(context.Background(), fakeBinary(t, "exit 9\n"), time.Second, 1024)
		if !errors.Is(err, ErrCommandFailed) {
			t.Fatalf("expected command failure, got %v", err)
		}
	})
	t.Run("version timeout", func(t *testing.T) {
		_, err := New(context.Background(), fakeBinary(t, "exec sleep 2\n"), 30*time.Millisecond, 1024)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected timeout, got %v", err)
		}
	})
	t.Run("canceled startup", func(t *testing.T) {
		binary := fakeBinary(t, "touch \"$0.started\"\n")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := New(ctx, binary, 24*time.Hour, 1024)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected startup cancellation, got %v", err)
		}
		if _, err := os.Stat(binary + ".started"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("version process ran after cancellation: %v", err)
		}
	})
	t.Run("invalid limits", func(t *testing.T) {
		binary := fakeBinary(t, "exit 0\n")
		for _, tc := range []struct {
			timeout time.Duration
			limit   int
		}{{0, 100}, {-time.Second, 100}, {time.Second, 0}, {time.Second, -1}} {
			if _, err := New(context.Background(), binary, tc.timeout, tc.limit); err == nil {
				t.Fatalf("accepted invalid limits: %+v", tc)
			}
		}
	})
}

func TestRunPassesLiteralArgumentsAndConnectionFlags(t *testing.T) {
	c := fakeClient(t, "printf '%s\\000' \"$@\"\n", time.Second, 8192)
	cipherSuite := 17
	conn := Connection{
		Interface: "lanplus", Host: "192.0.2.5", Port: 624, Username: "admin",
		Password: "never-put-me-in-argv", Privilege: "ADMINISTRATOR", AuthType: "PASSWORD",
		CipherSuite: &cipherSuite,
	}
	args := []string{"raw", "-V", "-H", "other-host", "$(echo bad)", "; echo bad", "line\nbreak", ""}
	result, err := c.Run(context.Background(), conn, args)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00")
	want := []string{
		"-I", "lanplus", "-H", "192.0.2.5", "-U", "admin", "-L", "ADMINISTRATOR", "-A", "PASSWORD",
		"-p", "624", "-C", "17", "-E", "--",
	}
	want = append(want, args...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments changed:\ngot  %#v\nwant %#v", got, want)
	}
	if result.ExitCode != 0 || result.Truncated {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunIsolatesEnvironmentAndUsesRequestPassword(t *testing.T) {
	t.Setenv("SERVICE_SECRET", "api-secret")
	t.Setenv("BMC_TEST_PASSWORD", "bmc-secret")
	t.Setenv("IPMI_PASSWORD", "inherited-wrong-secret")
	t.Setenv("IPMITOOL_PASSWORD", "higher-priority-wrong-secret")
	t.Setenv("POSIXLY_CORRECT", "1")
	c := fakeClient(t, `printf '%s\n' "${IPMI_PASSWORD-unset}" "${IPMITOOL_PASSWORD-unset}" "${SERVICE_SECRET-unset}" "${BMC_TEST_PASSWORD-unset}" "${POSIXLY_CORRECT-unset}" "$LANG"
`, time.Second, 1024)
	conn := testConnection()
	result, err := c.Run(context.Background(), conn, []string{"power", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "bmc-secret\nunset\nunset\nunset\nunset\nC\n"; result.Stdout != want {
		t.Fatalf("unexpected child environment: %q", result.Stdout)
	}
	conn.Password = ""
	result, err = c.Run(context.Background(), conn, []string{"mc", "info"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "\nunset\nunset\nunset\nunset\nC\n"; result.Stdout != want {
		t.Fatalf("empty password or environment isolation failed: %q", result.Stdout)
	}
}

func TestRunKeepsConcurrentRequestPasswordsIsolated(t *testing.T) {
	c := fakeClient(t, `printf '%s' "$IPMI_PASSWORD"`, time.Second, 1024)
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			conn := testConnection()
			conn.Password = fmt.Sprintf("request-password-%d", i)
			result, err := c.Run(context.Background(), conn, []string{"mc", "info"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Stdout != conn.Password {
				t.Fatal("request received another request's credentials")
			}
		})
	}
}

func TestRunUsesDefaultRemoteConnection(t *testing.T) {
	c := fakeClient(t, "printf '%s\\000' \"$@\"\n", time.Second, 1024)
	conn := testConnection()
	conn.Password = ""
	result, err := c.Run(context.Background(), conn, []string{"power", "status"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00")
	want := []string{"-I", "lanplus", "-H", "192.0.2.5", "-U", "admin", "-p", "623", "-E", "--", "power", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected connection arguments: %#v", got)
	}
}

func TestRunCapturesFailureWithoutLeakingArguments(t *testing.T) {
	c := fakeClient(t, "printf 'partial result'; printf 'BMC rejected request' >&2; exit 7\n", time.Second, 1024)
	conn := testConnection()
	conn.Password = "private-password"
	result, err := c.Run(context.Background(), conn, []string{"user", "set", "password", "2", "another-private-password"})
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("expected command failure, got %v", err)
	}
	if result.ExitCode != 7 || result.Stdout != "partial result" || result.Stderr != "BMC rejected request" {
		t.Fatalf("unexpected failure result: %+v", result)
	}
	if strings.Contains(err.Error(), "private-password") {
		t.Fatalf("error leaked credentials: %v", err)
	}
}

func TestRunReportsSignaledCommandAsFailure(t *testing.T) {
	c := fakeClient(t, "kill -TERM $$\n", time.Second, 1024)
	result, err := c.Run(context.Background(), testConnection(), []string{"mc", "info"})
	if !errors.Is(err, ErrCommandFailed) || result.ExitCode != -1 {
		t.Fatalf("expected signaled command failure with exit code -1, got result %+v, error %v", result, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("external signal was reported as context cancellation: %v", err)
	}
}

func TestRunLimitsEachOutputStream(t *testing.T) {
	c := fakeClient(t, "printf '123456789'; printf 'abcdefghi' >&2\n", time.Second, 4)
	result, err := c.Run(context.Background(), testConnection(), []string{"sdr"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "1234" || result.Stderr != "abcd" || !result.Truncated || result.ExitCode != 0 {
		t.Fatalf("output limit not enforced: %+v", result)
	}
}

func TestRunTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		c := fakeClient(t, "exec sleep 2\n", 30*time.Millisecond, 1024)
		_, err := c.Run(context.Background(), testConnection(), []string{"sensor"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected timeout, got %v", err)
		}
	})
	t.Run("canceled request", func(t *testing.T) {
		c := fakeClient(t, "exec sleep 2\n", time.Second, 1024)
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(30*time.Millisecond, cancel)
		defer timer.Stop()
		defer cancel()
		_, err := c.Run(ctx, testConnection(), []string{"sensor"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	})
}

func TestRunRejectsInvalidCommands(t *testing.T) {
	c := fakeClient(t, "printf 'should not execute'; exit 0\n", time.Second, 1024)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing command"},
		{name: "flag as command", args: []string{"-H", "unconfigured-host"}},
		{name: "shell as command", args: []string{"raw; echo unsafe"}},
		{name: "NUL argument", args: []string{"raw", "bad\x00arg"}},
		{name: "too many arguments", args: append([]string{"raw"}, make([]string, maxArguments)...)},
		{name: "large arguments", args: []string{"raw", strings.Repeat("x", maxInputSize)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := c.Run(context.Background(), testConnection(), tc.args)
			if !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("expected invalid command, got result %+v, error %v", result, err)
			}
			if result.Stdout != "" {
				t.Fatal("invalid command was executed")
			}
		})
	}
}

func TestRunWithInput(t *testing.T) {
	c := fakeClient(t, "exec cat\n", time.Second, maxInputSize)
	input := "mc info\npower status\n"
	result, err := c.RunWithInput(context.Background(), testConnection(), []string{"shell"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != input {
		t.Fatalf("stdin changed: %q", result.Stdout)
	}
	_, err = c.RunWithInput(context.Background(), testConnection(), []string{"shell"}, strings.Repeat("x", maxInputSize+1))
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("expected oversized stdin rejection, got %v", err)
	}
}

func TestValidateCommandPreservesNativeInputAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"OEM command", []string{"oem-command_123", "--help"}, ""},
		{"empty native argument", []string{"user", "set", "password", "2", ""}, ""},
		{"maximum arguments", append([]string{"raw"}, make([]string, maxArguments-1)...), ""},
		{"maximum argument bytes", []string{"raw", strings.Repeat("x", maxInputSize-len("raw"))}, ""},
		{"maximum stdin", []string{"shell"}, strings.Repeat("x", maxInputSize)},
		{"binary stdin", []string{"exec", "/dev/stdin"}, "a\x00b\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCommand(tc.args, tc.stdin); err != nil {
				t.Fatal(err)
			}
		})
	}
}
