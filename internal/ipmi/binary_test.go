package ipmi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testInstaller(t *testing.T) binaryInstaller {
	t.Helper()
	return binaryInstaller{
		goos: "linux", euid: 0, dirs: []string{"/limited", "/usr/bin"},
		lookPath: lookupExecutables(map[string]string{"apt": "/usr/bin/apt", "sudo": "/usr/bin/sudo"}),
		run: func(context.Context, string, []string, io.Writer) error {
			t.Fatal("unexpected package installation")
			return nil
		},
	}
}

func lookupExecutables(paths map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, found := paths[name]; found {
			return path, nil
		}

		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
}

func TestEnsureBinaryRespectsConfiguredExecutable(t *testing.T) {
	binary := fakeBinary(t, "exit 0\n")
	path, err := EnsureBinary(context.Background(), binary, nil)
	if err != nil || path != binary {
		t.Fatalf("configured executable: path=%q, error=%v", path, err)
	}
}

func TestEnsureBinaryFindsExistingWithoutInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, configured, foundAt, path string
	}{
		{"PATH", "ipmitool", "ipmitool", "/opt/bin/ipmitool"},
		{"common directory", "ipmitool", "/usr/bin/ipmitool", "/usr/bin/ipmitool"},
		{"custom executable name", "custom-ipmi", "custom-ipmi", "/opt/bin/custom-ipmi"},
		{"custom path", "/opt/ipmitool", "/opt/ipmitool", "/opt/ipmitool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer := testInstaller(t)
			installer.lookPath = lookupExecutables(map[string]string{tc.foundAt: tc.path})
			path, err := installer.ensure(context.Background(), tc.configured, nil)
			if err != nil || path != tc.path {
				t.Fatalf("path=%q, error=%v", path, err)
			}
		})
	}
}

func TestEnsureBinaryInstallsWithAPTInOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		euid   int
		binary string
		prefix []string
	}{
		{"root", 0, "/usr/bin/apt", nil},
		{"sudo", 1000, "/usr/bin/sudo", []string{"-n", "--preserve-env=DEBIAN_FRONTEND", "--", "/usr/bin/apt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer := testInstaller(t)
			installer.euid = tc.euid
			paths := map[string]string{"/usr/bin/apt": "/usr/bin/apt", "/usr/bin/sudo": "/usr/bin/sudo"}
			installer.lookPath = lookupExecutables(paths)
			var calls [][]string
			var deadline time.Time
			var output bytes.Buffer
			installer.run = func(ctx context.Context, binary string, args []string, writer io.Writer) error {
				if binary != tc.binary || writer != &output {
					t.Fatalf("unexpected command/output: binary=%q", binary)
				}

				gotDeadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("package installation has no timeout")
				}

				if len(calls) == 0 {
					deadline = gotDeadline
					remaining := time.Until(deadline)
					if remaining > 5*time.Minute || remaining < 4*time.Minute {
						t.Fatalf("unexpected installation timeout: %v", remaining)
					}
				} else if gotDeadline != deadline {
					t.Fatal("installation timeout restarted between apt steps")
				}

				calls = append(calls, append([]string(nil), args...))
				if len(calls) == 2 {
					paths["/usr/bin/ipmitool"] = "/usr/bin/ipmitool"
				}

				return nil
			}
			path, err := installer.ensure(context.Background(), "ipmitool", &output)
			if err != nil || path != "/usr/bin/ipmitool" {
				t.Fatalf("installed path=%q, error=%v", path, err)
			}

			want := [][]string{
				append(append([]string{}, tc.prefix...), "-o", "Dpkg::Use-Pty=0", "update"),
				append(append([]string{}, tc.prefix...), "-o", "Dpkg::Use-Pty=0", "install", "--yes", "--no-remove", "ipmitool"),
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("apt commands:\ngot  %#v\nwant %#v", calls, want)
			}

			if output.Len() == 0 {
				t.Fatal("installation progress was not reported")
			}
		})
	}
}

func TestEnsureBinaryRejectsUnavailableInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, configured, want string
		change                 func(*binaryInstaller)
	}{
		{"custom name missing", "custom-ipmi", "locate ipmitool", func(*binaryInstaller) {}},
		{"custom path missing", "/custom/ipmitool", "locate ipmitool", func(*binaryInstaller) {}},
		{"unsupported OS", "ipmitool", "requires linux", func(i *binaryInstaller) { i.goos = "darwin" }},
		{"apt missing", "ipmitool", "locate apt", func(i *binaryInstaller) { i.lookPath = lookupExecutables(nil) }},
		{"sudo missing", "ipmitool", "locate sudo", func(i *binaryInstaller) {
			i.euid = 1000
			i.lookPath = lookupExecutables(map[string]string{"apt": "/usr/bin/apt"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer := testInstaller(t)
			tc.change(&installer)
			_, err := installer.ensure(context.Background(), tc.configured, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestEnsureBinaryDoesNotInstallAfterLookupErrors(t *testing.T) {
	for _, tc := range []struct {
		name, candidate string
		err             error
	}{
		{"unsafe PATH", "ipmitool", exec.ErrDot},
		{"permission denied", "ipmitool", os.ErrPermission},
		{"nonexecutable common file", "/usr/bin/ipmitool", os.ErrPermission},
		{"invalid common file", "/usr/bin/ipmitool", errors.New("is a directory")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer := testInstaller(t)
			lookup := installer.lookPath
			installer.lookPath = func(name string) (string, error) {
				if name == tc.candidate {
					return "", &exec.Error{Name: name, Err: tc.err}
				}

				return lookup(name)
			}
			_, err := installer.ensure(context.Background(), "ipmitool", nil)
			if !errors.Is(err, tc.err) {
				t.Fatalf("expected preserved lookup error, got %v", err)
			}
		})
	}
}

func TestEnsureBinaryDetectsNonexecutablePATHCandidate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ipmitool"), []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)
	installer := testInstaller(t)
	installer.lookPath, installer.dirs = exec.LookPath, []string{dir}
	_, err := installer.ensure(context.Background(), "ipmitool", nil)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("nonexecutable file was treated as missing: %v", err)
	}
}

func TestEnsureBinaryAbortsAfterAPTFailure(t *testing.T) {
	for _, failingStep := range []string{"update", "install"} {
		t.Run(failingStep, func(t *testing.T) {
			installer := testInstaller(t)
			failure := errors.New("package manager rejected operation")
			calls := 0
			installer.run = func(_ context.Context, _ string, args []string, _ io.Writer) error {
				calls++
				if args[2] == failingStep {
					return failure
				}

				return nil
			}
			_, err := installer.ensure(context.Background(), "ipmitool", nil)
			wantCalls := 1
			if failingStep == "install" {
				wantCalls = 2
			}

			if !errors.Is(err, failure) || calls != wantCalls || !strings.Contains(err.Error(), "apt "+failingStep) {
				t.Fatalf("failed step was not preserved: calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestEnsureBinaryRequiresExecutableAfterInstallation(t *testing.T) {
	installer := testInstaller(t)
	installer.run = func(context.Context, string, []string, io.Writer) error { return nil }
	_, err := installer.ensure(context.Background(), "ipmitool", nil)
	if err == nil || !strings.Contains(err.Error(), "apt finished but ipmitool is not executable") {
		t.Fatalf("installation without executable accepted: %v", err)
	}
}

func TestEnsureBinaryHonorsCancellation(t *testing.T) {
	t.Run("before installation", func(t *testing.T) {
		installer := testInstaller(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := installer.ensure(ctx, "ipmitool", nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	})
	t.Run("during update", func(t *testing.T) {
		installer := testInstaller(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		installer.run = func(context.Context, string, []string, io.Writer) error {
			calls++
			cancel()
			return errors.New("command interrupted")
		}
		_, err := installer.ensure(ctx, "ipmitool", nil)
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("installation continued after cancellation: calls=%d error=%v", calls, err)
		}
	})
	t.Run("parent deadline", func(t *testing.T) {
		installer := testInstaller(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		calls := 0
		installer.run = func(ctx context.Context, _ string, _ []string, _ io.Writer) error {
			calls++
			<-ctx.Done()
			return ctx.Err()
		}
		_, err := installer.ensure(ctx, "ipmitool", nil)
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
			t.Fatalf("installation continued after deadline: calls=%d error=%v", calls, err)
		}
	})
}

func TestRunPackageCommandUsesLiteralArgumentsAndNoninteractiveEnvironment(t *testing.T) {
	t.Setenv("DEBIAN_FRONTEND", "dialog")
	binary := fakeBinary(t, `printf '%s\000' "$DEBIAN_FRONTEND" "$@"
if read -r line; then printf 'unexpected stdin'; fi
exit 0
`)
	args := []string{"-o", "Dpkg::Use-Pty=0", "install", "--yes", "--no-remove", "ipmitool", "$(echo literal)"}
	var output bytes.Buffer
	if err := runPackageCommand(context.Background(), binary, args, &output); err != nil {
		t.Fatal(err)
	}

	got := strings.Split(strings.TrimSuffix(output.String(), "\x00"), "\x00")
	want := append([]string{"noninteractive"}, args...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected subprocess environment or arguments: %#v", got)
	}
}

func TestRunPackageCommandInterruptsBeforeKilling(t *testing.T) {
	binary := fakeBinary(t, "trap 'printf interrupted; exit 0' INT\nwhile :; do :; done\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	err := runPackageCommand(ctx, binary, nil, &output)
	if !errors.Is(err, context.DeadlineExceeded) || output.String() != "interrupted" {
		t.Fatalf("expected interrupt before forced kill: output=%q error=%v", output.String(), err)
	}
}
