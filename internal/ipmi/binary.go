package ipmi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const installTimeout = 5 * time.Minute

// EnsureBinary locates ipmitool and installs the default executable with apt if missing.
func EnsureBinary(ctx context.Context, configured string, output io.Writer) (string, error) {
	installer := binaryInstaller{
		lookPath: exec.LookPath,
		run:      runPackageCommand,
		goos:     runtime.GOOS,
		euid:     os.Geteuid(),
		dirs: append(filepath.SplitList(os.Getenv("PATH")),
			"/usr/local/bin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/sbin", "/sbin",
			"/opt/homebrew/bin", "/opt/local/bin"),
	}
	return installer.ensure(ctx, configured, output)
}

type binaryInstaller struct {
	lookPath func(string) (string, error)
	run      func(context.Context, string, []string, io.Writer) error
	goos     string
	euid     int
	dirs     []string
}

func (installer binaryInstaller) ensure(ctx context.Context, configured string, output io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if output == nil {
		output = io.Discard
	}
	if configured != "ipmitool" {
		path, err := installer.lookPath(configured)
		if err != nil {
			return "", fmt.Errorf("locate ipmitool configured as %q: %w", configured, err)
		}
		return path, nil
	}

	path, err := installer.find("ipmitool")
	if err == nil {
		return path, nil
	}
	if !missingExecutable(err) {
		return "", fmt.Errorf("locate ipmitool: %w", err)
	}
	if installer.goos != "linux" {
		return "", errors.New("ipmitool was not found; automatic installation requires Linux and apt")
	}
	apt, err := installer.find("apt")
	if err != nil {
		return "", fmt.Errorf("ipmitool was not found; locate apt for installation: %w", err)
	}
	command := apt
	var prefix []string
	if installer.euid != 0 {
		command, err = installer.find("sudo")
		if err != nil {
			return "", fmt.Errorf("installing ipmitool requires sudo without a password prompt; locate sudo: %w", err)
		}
		prefix = []string{"-n", "--preserve-env=DEBIAN_FRONTEND", "--", apt}
	}

	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	for _, step := range []struct {
		message string
		args    []string
	}{
		{"ipmitool not found: updating apt package indexes", []string{"update"}},
		{"Installing ipmitool with apt", []string{"install", "--yes", "--no-remove", "ipmitool"}},
	} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fmt.Fprintln(output, step.message)
		args := append(append([]string{}, prefix...), "-o", "Dpkg::Use-Pty=0")
		args = append(args, step.args...)
		if err := installer.run(ctx, command, args, output); err != nil {
			if ctx.Err() != nil {
				return "", fmt.Errorf("install ipmitool: %w", ctx.Err())
			}
			return "", fmt.Errorf("apt %s failed; install ipmitool manually or authorize noninteractive sudo preserving DEBIAN_FRONTEND: %w", step.args[0], err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err = installer.find("ipmitool")
	if err != nil {
		return "", fmt.Errorf("apt finished but ipmitool is not executable: %w", err)
	}
	return path, nil
}

func (installer binaryInstaller) find(name string) (string, error) {
	path, err := installer.lookPath(name)
	if err == nil && !filepath.IsAbs(path) {
		return "", &exec.Error{Name: name, Err: exec.ErrDot}
	}
	if err == nil || !missingExecutable(err) {
		return path, err
	}
	var unavailable error
	for _, dir := range installer.dirs {
		candidate, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		path, err := installer.lookPath(candidate)
		if err == nil {
			if !filepath.IsAbs(dir) {
				return "", &exec.Error{Name: name, Err: exec.ErrDot}
			}
			return path, nil
		}
		// LookPath(name) hides permission errors; explicit PATH candidates preserve them.
		if !missingExecutable(err) && unavailable == nil {
			unavailable = err
		}
	}
	if unavailable != nil {
		return "", unavailable
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func missingExecutable(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

func runPackageCommand(ctx context.Context, binary string, args []string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DEBIAN_FRONTEND=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdout, cmd.Stderr = output, output
	// Let sudo forward interruption to apt before enforcing the wait limit.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}
