package ipmi

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var (
	ErrInvalidCommand = errors.New("invalid ipmitool command")
	ErrCommandFailed  = errors.New("ipmitool command failed")
)

const (
	maxArguments = 256
	maxInputSize = 64 << 10
)

type Result struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

type Client struct {
	binary         string
	timeout        time.Duration
	maxOutputBytes int
	version        string
}

// New verifies the executable before the HTTP server starts accepting requests.
func New(ctx context.Context, binary string, timeout time.Duration, maxOutputBytes int) (*Client, error) {
	if timeout <= 0 || maxOutputBytes <= 0 {
		return nil, errors.New("ipmitool timeout and output limit must be positive")
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("locate ipmitool: %w", err)
	}
	c := &Client{binary: path, timeout: timeout, maxOutputBytes: maxOutputBytes}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := c.execute(probeCtx, []string{"-V"}, minimalEnv(), "")
	if err != nil {
		return nil, fmt.Errorf("verify ipmitool: %w", err)
	}
	c.version = strings.TrimSpace(result.Stdout)
	if c.version == "" {
		c.version = strings.TrimSpace(result.Stderr)
	}
	return c, nil
}

func (c *Client) Version() string { return c.version }

func (c *Client) Run(ctx context.Context, conn Connection, args []string) (Result, error) {
	return c.RunWithInput(ctx, conn, args, "")
}

// RunWithInput runs one finite command. Arguments are passed directly to ipmitool;
// stdin also supports commands that read their input from a pipe.
func (c *Client) RunWithInput(ctx context.Context, conn Connection, args []string, stdin string) (Result, error) {
	conn = conn.WithDefaults()
	if err := conn.Validate(); err != nil {
		return Result{}, err
	}
	if err := ValidateCommand(args, stdin); err != nil {
		return Result{}, err
	}
	return c.execute(ctx, conn.arguments(args), conn.environment(), stdin)
}

// ValidateCommand checks argument boundaries without restricting native subcommands.
func ValidateCommand(args []string, stdin string) error {
	if len(args) == 0 || len(args) > maxArguments || !commandName(args[0]) {
		return invalid("provide a command name followed by at most 255 arguments")
	}
	if err := validateStrings(args); err != nil {
		return err
	}
	if len(stdin) > maxInputSize {
		return invalid("stdin exceeds 64 KiB")
	}
	return nil
}

func (c *Client) execute(parent context.Context, argv, env []string, stdin string) (Result, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.binary, argv...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	// A child retaining stdout/stderr must not keep a canceled request open.
	cmd.WaitDelay = 250 * time.Millisecond
	stdout := &limitedBuffer{limit: c.maxOutputBytes}
	stderr := &limitedBuffer{limit: c.maxOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	result := Result{
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1,
		Truncated: stdout.truncated || stderr.truncated,
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, fmt.Errorf("%w: exit code %d", ErrCommandFailed, result.ExitCode)
		}
		return result, fmt.Errorf("execute ipmitool: %w", err)
	}
	return result, nil
}

func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidCommand, reason) }

func validateStrings(values []string) error {
	total := 0
	for _, value := range values {
		total += len(value)
		if strings.IndexByte(value, 0) >= 0 {
			return invalid("arguments must not contain NUL bytes")
		}
	}
	if total > maxInputSize {
		return invalid("arguments exceed 64 KiB")
	}
	return nil
}

func commandName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}
