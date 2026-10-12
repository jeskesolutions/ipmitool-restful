package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"ipmitool-microservice/internal/api"
	"ipmitool-microservice/internal/api/handlers"
	"ipmitool-microservice/internal/config"
	"ipmitool-microservice/internal/ipmi"
)

const token = "test-fixed-token"

type stubRunner struct {
	run func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error)
}

func (s stubRunner) Version() string { return "ipmitool version test" }

func (s stubRunner) RunWithInput(ctx context.Context, conn ipmi.Connection, args []string, stdin string) (ipmi.Result, error) {
	if s.run == nil {
		return ipmi.Result{}, nil
	}

	return s.run(ctx, conn, args, stdin)
}

const connectionJSON = `{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password"}`

func newAPI(cfg config.Config, runner handlers.Runner) http.Handler {
	return api.New(cfg, runner, zap.NewNop())
}

func testConfig() config.Config {
	return config.Config{Auth: config.Auth{Token: token}, IPMITool: config.IPMITool{MaxConcurrent: 1}}
}

func defaultConnection() ipmi.Connection {
	return ipmi.Connection{Interface: "lanplus", Host: "192.0.2.10", Port: 623, Username: "ADMIN", Password: "private-bmc-password"}
}

func connectionBody(fields string) string {
	if fields != "" {
		fields = "," + fields
	}

	return `{"connection":` + connectionJSON + fields + `}`
}

func request(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestRoutesRequireAuthentication(t *testing.T) {
	called := false
	handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
		called = true
		return ipmi.Result{}, nil
	}})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/health"}, {http.MethodPost, "/api/v1/commands"}, {http.MethodPost, "/api/v1/power/status"},
		{http.MethodGet, "/api/v1/health/"}, {http.MethodGet, "/API/v1/health"},
		{http.MethodGet, "/missing"}, {http.MethodDelete, "/api/v1/health"},
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, nil))
		if res.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want 401", tc.path, res.Code)
		}

		if res.Header().Get("Location") != "" || !json.Valid(res.Body.Bytes()) {
			t.Errorf("unauthenticated response must be JSON without redirect: %s", res.Body.String())
		}
	}

	if called {
		t.Fatal("unauthenticated request invoked ipmitool")
	}
}

func TestGinRoutingPreservesJSONErrorsWithoutRedirects(t *testing.T) {
	handler := newAPI(testConfig(), stubRunner{})
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/health/", http.StatusNotFound},
		{http.MethodGet, "/API/v1/health", http.StatusNotFound},
		{http.MethodGet, "/api//v1/health", http.StatusNotFound},
		{http.MethodDelete, "/api/v1/health", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/power/status", http.StatusMethodNotAllowed},
	} {
		res := request(handler, tc.method, tc.path, "")
		if res.Code != tc.want || !json.Valid(res.Body.Bytes()) || res.Header().Get("Location") != "" {
			t.Errorf("%s %s status = %d, want %d: %s", tc.method, tc.path, res.Code, tc.want, res.Body.String())
		}
	}
}

func TestHealthAndRemovedTargets(t *testing.T) {
	handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
		t.Fatal("metadata route invoked ipmitool")
		return ipmi.Result{}, nil
	}})
	res := request(handler, http.MethodGet, "/api/v1/health", "")
	if res.Code != http.StatusOK || !json.Valid(res.Body.Bytes()) || !strings.Contains(res.Body.String(), "ipmitool version test") {
		t.Fatalf("health status = %d: %s", res.Code, res.Body.String())
	}

	for _, path := range []string{"/api/v1/targets", "/api/v1/targets/alpha/power", "/api/v1/targets/alpha/commands"} {
		res = request(handler, http.MethodGet, path, "")
		if res.Code != http.StatusNotFound {
			t.Errorf("removed route %s status = %d, want 404", path, res.Code)
		}
	}
}

func TestResourceRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		args               []string
	}{
		{http.MethodPost, "/power/status", "", []string{"chassis", "power", "status"}},
		{http.MethodPost, "/chassis/status", "", []string{"chassis", "status"}},
		{http.MethodPost, "/sensors", "", []string{"sensor", "list"}},
		{http.MethodPost, "/sdr", "", []string{"sdr", "list"}},
		{http.MethodPost, "/fru", "", []string{"fru", "print"}},
		{http.MethodPost, "/sel/entries", "", []string{"sel", "list"}},
		{http.MethodPost, "/mc/info", "", []string{"mc", "info"}},
		{http.MethodPut, "/power", `{"state":"on"}`, []string{"chassis", "power", "on"}},
		{http.MethodPut, "/power", `{"state":"off"}`, []string{"chassis", "power", "off"}},
		{http.MethodPost, "/power/actions", `{"action":"cycle"}`, []string{"chassis", "power", "cycle"}},
		{http.MethodPost, "/power/actions", `{"action":"reset"}`, []string{"chassis", "power", "reset"}},
		{http.MethodPost, "/power/actions", `{"action":"diag"}`, []string{"chassis", "power", "diag"}},
		{http.MethodPost, "/power/actions", `{"action":"soft"}`, []string{"chassis", "power", "soft"}},
		{http.MethodPut, "/boot-device", `{"device":"pxe","persistent":true}`, []string{"chassis", "bootdev", "pxe", "options=persistent"}},
		{http.MethodPut, "/boot-device", `{"device":"disk"}`, []string{"chassis", "bootdev", "disk"}},
		{http.MethodPost, "/sel/clear", "", []string{"sel", "clear"}},
		{http.MethodPost, "/mc/resets", `{"mode":"warm"}`, []string{"mc", "reset", "warm"}},
		{http.MethodPost, "/mc/resets", `{"mode":"cold"}`, []string{"mc", "reset", "cold"}},
		{http.MethodPost, "/lan/0", "", []string{"lan", "print", "0"}},
		{http.MethodPost, "/lan/15", "", []string{"lan", "print", "15"}},
		{http.MethodPost, "/users/0", "", []string{"user", "list", "0"}},
		{http.MethodPost, "/users/15", "", []string{"user", "list", "15"}},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			cfg := testConfig()
			called := 0
			want := ipmi.Result{Stdout: "CPU Temp: 42 C\n", Stderr: "IPMI Diagnostic\n", ExitCode: 0, Truncated: true}
			handler := newAPI(cfg, stubRunner{run: func(_ context.Context, conn ipmi.Connection, args []string, stdin string) (ipmi.Result, error) {
				called++
				if !reflect.DeepEqual(conn, defaultConnection()) || !reflect.DeepEqual(args, tc.args) || stdin != "" {
					t.Errorf("runner received conn=%+v args=%q stdin=%q; want args=%q", conn, args, stdin, tc.args)
				}

				return want, nil
			}})
			fields := strings.TrimSuffix(strings.TrimPrefix(tc.body, "{"), "}")
			res := request(handler, tc.method, "/api/v1"+tc.path, connectionBody(fields))
			if res.Code != http.StatusOK || called != 1 {
				t.Fatalf("status = %d, runner calls = %d: %s", res.Code, called, res.Body.String())
			}

			var got ipmi.Result
			if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil || got != want {
				t.Fatalf("result = %+v, error = %v; want %+v", got, err, want)
			}
		})
	}
}

func TestCommandsForwardArgumentsInputAndContext(t *testing.T) {
	type contextKey struct{}
	args := []string{"sensor", "thresh", "CPU Temp; $(anything)", "lower", "-5", "0", "5"}
	input := "power status\nsensor list\n"
	called := false
	handler := newAPI(testConfig(), stubRunner{run: func(ctx context.Context, _ ipmi.Connection, got []string, stdin string) (ipmi.Result, error) {
		called = true
		if !reflect.DeepEqual(got, args) || stdin != input || ctx.Value(contextKey{}) != "request-context" {
			t.Errorf("request was not forwarded intact: args=%q stdin=%q context=%v", got, stdin, ctx.Value(contextKey{}))
		}

		return ipmi.Result{Stdout: "ok"}, nil
	}})
	body, err := json.Marshal(map[string]any{"connection": json.RawMessage(connectionJSON), "args": args, "stdin": input})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(string(body)))
	req = req.WithContext(context.WithValue(req.Context(), contextKey{}, "request-context"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !called {
		t.Fatalf("status = %d, called = %v: %s", res.Code, called, res.Body.String())
	}
}

func TestInvalidRequestsDoNotExecute(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, contentType string
		want                                  int
	}{
		{"wrong method", http.MethodGet, "/power/status", "", "", 405},
		{"missing media type", http.MethodPost, "/commands", connectionBody(`"args":["raw"]`), "", 415},
		{"wrong media type", http.MethodPost, "/commands", connectionBody(`"args":["raw"]`), "text/plain", 415},
		{"malformed JSON", http.MethodPost, "/commands", `{`, "application/json", 400},
		{"empty JSON", http.MethodPost, "/commands", "", "application/json", 400},
		{"unknown field", http.MethodPost, "/commands", connectionBody(`"args":["raw"],"binary":"sh"`), "application/json", 400},
		{"trailing JSON", http.MethodPost, "/commands", connectionBody(`"args":["raw"]`) + ` {}`, "application/json", 400},
		{"trailing garbage", http.MethodPost, "/commands", connectionBody(`"args":["raw"]`) + ` rubbish`, "application/json", 400},
		{"wrong args type", http.MethodPost, "/commands", connectionBody(`"args":"raw"`), "application/json", 400},
		{"oversized JSON", http.MethodPost, "/commands", connectionBody(`"args":["raw","` + strings.Repeat("x", 129<<10) + `"]`), "application/json", 413},
		{"invalid state", http.MethodPut, "/power", connectionBody(`"state":"reset"`), "application/json", 400},
		{"invalid action", http.MethodPost, "/power/actions", connectionBody(`"action":"status"`), "application/json", 400},
		{"invalid boot device", http.MethodPut, "/boot-device", connectionBody(`"device":"arbitrary"`), "application/json", 400},
		{"invalid reset", http.MethodPost, "/mc/resets", connectionBody(`"mode":"arbitrary"`), "application/json", 400},
		{"large channel", http.MethodPost, "/lan/16", connectionBody(""), "application/json", 400},
		{"negative channel", http.MethodPost, "/users/-1", connectionBody(""), "application/json", 400},
		{"invalid channel", http.MethodPost, "/lan/abc", connectionBody(""), "application/json", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
				called = true
				return ipmi.Result{}, nil
			}})
			req := httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want || called {
				t.Fatalf("status = %d, want %d; runner called = %v: %s", res.Code, tc.want, called, res.Body.String())
			}

			if strings.Contains(res.Body.String(), "private-bmc-password") {
				t.Fatal("validation error exposed password")
			}
		})
	}
}

func TestCommandErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"invalid command", fmt.Errorf("validation: %w", ipmi.ErrInvalidCommand), http.StatusBadRequest},
		{"command failure", fmt.Errorf("execution: %w", ipmi.ErrCommandFailed), http.StatusBadGateway},
		{"timeout", fmt.Errorf("execution: %w", context.DeadlineExceeded), http.StatusGatewayTimeout},
		{"canceled", fmt.Errorf("execution: %w", context.Canceled), http.StatusRequestTimeout},
		{"internal failure", errors.New("sensitive-executor-detail"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := ipmi.Result{Stdout: "CPU Temp: 42 C\n", Stderr: "IPMI Diagnostic\n", ExitCode: 1}
			handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
				return want, tc.err
			}})
			res := request(handler, http.MethodPost, "/api/v1/commands", connectionBody(`"args":["raw"]`))
			if res.Code != tc.want || !json.Valid(res.Body.Bytes()) {
				t.Fatalf("status = %d, want %d: %s", res.Code, tc.want, res.Body.String())
			}

			if strings.Contains(res.Body.String(), "sensitive-executor-detail") {
				t.Fatal("internal execution error leaked to client")
			}

			var envelope struct {
				Result ipmi.Result `json:"result"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil || envelope.Result != want {
				t.Fatalf("result = %+v, error = %v; want %+v", envelope.Result, err, want)
			}
		})
	}
}

func TestRequestCancellationReachesRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := newAPI(testConfig(), stubRunner{run: func(ctx context.Context, _ ipmi.Connection, _ []string, _ string) (ipmi.Result, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("runner context error = %v, want cancellation", ctx.Err())
		}

		return ipmi.Result{}, ctx.Err()
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/power/status", strings.NewReader(connectionBody(""))).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusRequestTimeout {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
}

func TestConcurrentLimitAndRelease(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls atomic.Int32
	handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-release
			return ipmi.Result{ExitCode: 1}, ipmi.ErrCommandFailed
		}

		return ipmi.Result{Stdout: "next request"}, nil
	}})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- request(handler, http.MethodPost, "/api/v1/power/status", connectionBody(""))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first command did not start")
	}

	res := request(handler, http.MethodPost, "/api/v1/power/status", connectionBody(""))
	if res.Code != http.StatusServiceUnavailable || res.Header().Get("Retry-After") != "1" || calls.Load() != 1 {
		t.Fatalf("busy request status = %d, retry = %q, calls = %d", res.Code, res.Header().Get("Retry-After"), calls.Load())
	}

	close(release)
	select {
	case first := <-done:
		if first.Code != http.StatusBadGateway {
			t.Errorf("first command status = %d, want 502", first.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first command did not finish")
	}

	res = request(handler, http.MethodPost, "/api/v1/power/status", connectionBody(""))
	if res.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("request after release status = %d, calls = %d", res.Code, calls.Load())
	}
}

func TestInvalidConnectionsDoNotExecuteOrExposeCredentials(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"missing", `{}`},
		{"null", `{"connection":null}`},
		{"missing host", `{"connection":{"username":"ADMIN","password":"private-bmc-password"}}`},
		{"empty host", `{"connection":{"host":"","username":"ADMIN","password":"private-bmc-password"}}`},
		{"missing username", `{"connection":{"host":"192.0.2.10","password":"private-bmc-password"}}`},
		{"missing password", `{"connection":{"host":"192.0.2.10","username":"ADMIN"}}`},
		{"null password", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":null}}`},
		{"open interface", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password","interface":"open"}}`},
		{"unsupported interface", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password","interface":"dummy"}}`},
		{"negative port", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password","port":-1}}`},
		{"large port", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password","port":65536}}`},
		{"host control", `{"connection":{"host":"192.0.2.10\n","username":"ADMIN","password":"private-bmc-password"}}`},
		{"username control", `{"connection":{"host":"192.0.2.10","username":"ADMIN\t","password":"private-bmc-password"}}`},
		{"password control", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password\u0000"}}`},
		{"unrestricted options", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password","options":["-I","open"]}}`},
		{"password environment", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password","password_env":"PRIVATE_ENV"}}`},
		{"binary override", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":"private-bmc-password","binary":"sh"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
				called = true
				return ipmi.Result{}, nil
			}})
			res := request(handler, http.MethodPost, "/api/v1/power/status", tc.body)
			if res.Code != http.StatusBadRequest || called {
				t.Fatalf("status = %d, called = %v: %s", res.Code, called, res.Body.String())
			}

			if strings.Contains(res.Body.String(), "private-bmc-password") {
				t.Fatal("connection validation error exposed password")
			}
		})
	}
}

func TestConnectionOptionsAndExplicitEmptyPassword(t *testing.T) {
	cipher := 17
	for _, tc := range []struct {
		name, body string
		want       ipmi.Connection
	}{
		{"empty password", `{"connection":{"host":"192.0.2.10","username":"ADMIN","password":""}}`, ipmi.Connection{Host: "192.0.2.10", Username: "ADMIN", Interface: "lanplus", Port: 623}},
		{"explicit options", `{"connection":{"host":"192.0.2.11","username":"USER","password":"another-password","interface":"lan","port":624,"privilege":"OPERATOR","auth_type":"MD5","cipher_suite":17}}`, ipmi.Connection{Host: "192.0.2.11", Username: "USER", Password: "another-password", Interface: "lan", Port: 624, Privilege: "OPERATOR", AuthType: "MD5", CipherSuite: &cipher}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := newAPI(testConfig(), stubRunner{run: func(_ context.Context, conn ipmi.Connection, _ []string, _ string) (ipmi.Result, error) {
				called = true
				if !reflect.DeepEqual(conn, tc.want) {
					t.Errorf("connection = %+v, want %+v", conn, tc.want)
				}

				return ipmi.Result{}, nil
			}})
			res := request(handler, http.MethodPost, "/api/v1/power/status", tc.body)
			if res.Code != http.StatusOK || !called {
				t.Fatalf("status = %d, called = %v: %s", res.Code, called, res.Body.String())
			}
		})
	}
}

func TestConsecutiveRequestsDoNotReuseConnection(t *testing.T) {
	var got []ipmi.Connection
	handler := newAPI(testConfig(), stubRunner{run: func(_ context.Context, conn ipmi.Connection, _ []string, _ string) (ipmi.Result, error) {
		got = append(got, conn)
		return ipmi.Result{Stdout: "ok"}, nil
	}})
	for _, body := range []string{connectionBody(""), `{"connection":{"host":"192.0.2.11","username":"OTHER","password":"different-password"}}`} {
		res := request(handler, http.MethodPost, "/api/v1/power/status", body)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", res.Code, res.Body.String())
		}

		if strings.Contains(res.Body.String(), "password") || strings.Contains(res.Body.String(), "192.0.2.") {
			t.Fatal("response exposed connection")
		}
	}

	want := []ipmi.Connection{defaultConnection(), {Interface: "lanplus", Host: "192.0.2.11", Port: 623, Username: "OTHER", Password: "different-password"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connections = %+v, want %+v", got, want)
	}

	res := request(handler, http.MethodPost, "/api/v1/power/status", `{}`)
	if res.Code != http.StatusBadRequest || len(got) != 2 {
		t.Fatalf("request without connection: status = %d, executions = %d", res.Code, len(got))
	}
}

func TestConcurrentRequestsKeepConnectionsSeparate(t *testing.T) {
	cfg := testConfig()
	cfg.IPMITool.MaxConcurrent = 2
	entered := make(chan ipmi.Connection, 2)
	release := make(chan struct{})
	defer close(release)
	handler := newAPI(cfg, stubRunner{run: func(_ context.Context, conn ipmi.Connection, _ []string, _ string) (ipmi.Result, error) {
		entered <- conn
		<-release
		return ipmi.Result{}, nil
	}})
	done := make(chan *httptest.ResponseRecorder, 2)
	for _, body := range []string{connectionBody(""), `{"connection":{"host":"192.0.2.11","username":"OTHER","password":"different-password"}}`} {
		go func() { done <- request(handler, http.MethodPost, "/api/v1/power/status", body) }()
	}

	seen := make(map[string]ipmi.Connection)
	for range 2 {
		select {
		case conn := <-entered:
			seen[conn.Host] = conn
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent commands did not both start")
		}
	}

	if !reflect.DeepEqual(seen["192.0.2.10"], defaultConnection()) || seen["192.0.2.11"].Username != "OTHER" || seen["192.0.2.11"].Password != "different-password" {
		t.Fatalf("connections mixed between concurrent requests: %+v", seen)
	}

	release <- struct{}{}
	release <- struct{}{}
	for range 2 {
		select {
		case res := <-done:
			if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "password") {
				t.Errorf("concurrent response status = %d: %s", res.Code, res.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent command did not finish")
		}
	}
}

func TestCommandFieldsAreValidatedBeforeRunner(t *testing.T) {
	for _, tc := range []struct{ name, fields string }{
		{"missing args", ""},
		{"null args", `"args":null`},
		{"empty args", `"args":[]`},
		{"empty command", `"args":[""]`},
		{"global flag instead of command", `"args":["-V"]`},
		{"null command", `"args":[null]`},
		{"null argument", `"args":["raw",null]`},
		{"numeric argument", `"args":["raw",1]`},
		{"boolean argument", `"args":["raw",false]`},
		{"object argument", `"args":["raw",{}]`},
		{"NUL command", `"args":["raw\u0000"]`},
		{"NUL argument", `"args":["raw","\u0000"]`},
		{"too many arguments", `"args":["raw"` + strings.Repeat(`,"x"`, 256) + `]`},
		{"arguments too large", `"args":["raw","` + strings.Repeat("x", 64<<10) + `"]`},
		{"null stdin", `"args":["raw"],"stdin":null`},
		{"numeric stdin", `"args":["raw"],"stdin":1`},
		{"stdin too large", `"args":["raw"],"stdin":"` + strings.Repeat("x", (64<<10)+1) + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertInvalidBeforeRunner(t, http.MethodPost, "/api/v1/commands", connectionBody(tc.fields))
		})
	}
}

func TestActionFieldsRejectMissingNullAndWrongTypes(t *testing.T) {
	for _, action := range []struct{ method, path, field string }{
		{http.MethodPut, "/api/v1/power", "state"},
		{http.MethodPost, "/api/v1/power/actions", "action"},
		{http.MethodPut, "/api/v1/boot-device", "device"},
		{http.MethodPost, "/api/v1/mc/resets", "mode"},
	} {
		for _, value := range []string{"", "null", "1", "true", `""`, `"invalid"`, `{}`, `[]`} {
			t.Run(action.field+"="+value, func(t *testing.T) {
				fields := ""
				if value != "" {
					fields = fmt.Sprintf("%q:%s", action.field, value)
				}

				assertInvalidBeforeRunner(t, action.method, action.path, connectionBody(fields))
			})
		}
	}

	for _, value := range []string{"null", "1", `"true"`, `{}`, `[]`} {
		t.Run("persistent="+value, func(t *testing.T) {
			assertInvalidBeforeRunner(t, http.MethodPut, "/api/v1/boot-device", connectionBody(`"device":"pxe","persistent":`+value))
		})
	}
}

func TestEveryConnectionFieldRejectsNullAndWrongTypes(t *testing.T) {
	fields := []string{"host", "username", "password", "interface", "port", "privilege", "auth_type", "cipher_suite"}
	for _, field := range fields {
		for _, value := range []any{nil, true, map[string]any{}} {
			t.Run(fmt.Sprintf("%s=%v", field, value), func(t *testing.T) {
				conn := map[string]any{"host": "192.0.2.10", "username": "ADMIN", "password": "private-bmc-password", field: value}
				body, err := json.Marshal(map[string]any{"connection": conn})
				if err != nil {
					t.Fatal(err)
				}

				assertInvalidBeforeRunner(t, http.MethodPost, "/api/v1/power/status", string(body))
			})
		}
	}

	for _, tc := range []struct{ field, value string }{
		{"interface", `""`}, {"privilege", `""`}, {"auth_type", `""`},
		{"port", "0"}, {"port", "1.5"}, {"port", `"623"`},
		{"cipher_suite", "-1"}, {"cipher_suite", "256"}, {"cipher_suite", "1.5"}, {"cipher_suite", `"17"`},
		{"privilege", `"invalid"`}, {"auth_type", `"invalid"`},
		{"username", `"12345678901234567"`}, {"password", `"123456789012345678901"`},
	} {
		t.Run(tc.field+"="+tc.value, func(t *testing.T) {
			conn := map[string]json.RawMessage{"host": json.RawMessage(`"192.0.2.10"`), "username": json.RawMessage(`"ADMIN"`), "password": json.RawMessage(`"secret"`)}
			conn[tc.field] = json.RawMessage(tc.value)
			body, err := json.Marshal(map[string]any{"connection": conn})
			if err != nil {
				t.Fatal(err)
			}

			assertInvalidBeforeRunner(t, http.MethodPost, "/api/v1/power/status", string(body))
		})
	}
}

func assertInvalidBeforeRunner(t *testing.T, method, path, body string) {
	t.Helper()
	called := false
	handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
		called = true
		return ipmi.Result{}, nil
	}})
	res := request(handler, method, path, body)
	if res.Code != http.StatusBadRequest || called || !json.Valid(res.Body.Bytes()) {
		t.Fatalf("status = %d, runner called = %v: %s", res.Code, called, res.Body.String())
	}

	if strings.Contains(res.Body.String(), "private-bmc-password") {
		t.Fatal("validation error exposed password")
	}
}

func TestIPWhitelistUsesOnlySocketPeer(t *testing.T) {
	for _, tc := range []struct {
		name, peer string
		allowed    []string
		want       int
	}{
		{"empty whitelist", "", nil, http.StatusOK},
		{"IPv4 exact", "192.0.2.10:1234", []string{"192.0.2.10"}, http.StatusOK},
		{"IPv4 CIDR", "192.0.2.10:1234", []string{"192.0.2.0/24"}, http.StatusOK},
		{"IPv6 exact", "[2001:db8::10]:1234", []string{"2001:db8::10"}, http.StatusOK},
		{"IPv6 CIDR", "[2001:db8::10]:1234", []string{"2001:db8::/32"}, http.StatusOK},
		{"IPv4 mapped peer", "[::ffff:192.0.2.10]:1234", []string{"192.0.2.0/24"}, http.StatusOK},
		{"IPv4 mapped network", "192.0.2.10:1234", []string{"::ffff:192.0.2.0/120"}, http.StatusOK},
		{"outside IPv4 CIDR", "203.0.113.10:1234", []string{"192.0.2.0/24"}, http.StatusForbidden},
		{"outside IPv6 CIDR", "[2001:db9::10]:1234", []string{"2001:db8::/32"}, http.StatusForbidden},
		{"missing peer", "", []string{"192.0.2.0/24"}, http.StatusForbidden},
		{"missing port", "192.0.2.10", []string{"192.0.2.0/24"}, http.StatusForbidden},
		{"invalid port", "192.0.2.10:not-a-port", []string{"192.0.2.0/24"}, http.StatusForbidden},
		{"non-IP peer", "localhost:1234", []string{"127.0.0.1"}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Server.AllowedIPs = tc.allowed
			handler := newAPI(cfg, stubRunner{})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			req.RemoteAddr = tc.peer
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Forwarded-For", "192.0.2.10")
			req.Header.Set("X-Real-IP", "192.0.2.10")
			req.Header.Set("Forwarded", "for=192.0.2.10")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want || !json.Valid(res.Body.Bytes()) {
				t.Fatalf("status = %d, want %d: %s", res.Code, tc.want, res.Body.String())
			}

			if tc.want == http.StatusForbidden && !strings.Contains(res.Body.String(), "ip_not_allowed") {
				t.Fatal("missing whitelist error code")
			}
		})
	}
}

func TestIPWhitelistCoversCommandsUnknownRoutesAndMethods(t *testing.T) {
	cfg := testConfig()
	cfg.Server.AllowedIPs = []string{"203.0.113.0/24"}
	called := false
	handler := newAPI(cfg, stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
		called = true
		return ipmi.Result{}, nil
	}})
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/commands"}, {http.MethodGet, "/missing"},
		{http.MethodDelete, "/api/v1/health"}, {http.MethodGet, "/api/v1/health/"},
	} {
		res := request(handler, tc.method, tc.path, connectionBody(`"args":["raw"]`))
		if res.Code != http.StatusForbidden || res.Header().Get("Location") != "" {
			t.Errorf("%s %s status = %d, want 403", tc.method, tc.path, res.Code)
		}
	}

	if called {
		t.Fatal("blocked IP invoked runner")
	}
}

func TestRequestLogsExcludeCredentialsBodiesAndQueryStrings(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	handler := api.New(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
		return ipmi.Result{Stdout: "private-runner-output"}, nil
	}}, zap.New(core))
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/commands?token=query-secret", connectionBody(`"args":["raw","argument-secret"],"stdin":"stdin-secret"`)},
		{http.MethodPost, "/api/v1/lan/1?password=query-secret", connectionBody("")},
		{http.MethodGet, "/path-secret?token=query-secret", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.RemoteAddr = "192.0.2.20:1234"
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113.100")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	entries := logs.All()
	if len(entries) != 3 {
		t.Fatalf("log count = %d, want 3", len(entries))
	}

	for i, route := range []string{"/api/v1/commands", "/api/v1/lan/:channel", "unknown"} {
		fields := entries[i].ContextMap()
		if fields["route"] != route || fields["client_ip"] != "192.0.2.20" || fields["method"] == nil || fields["status"] == nil || fields["duration"] == nil {
			t.Errorf("unexpected log fields: %v", fields)
		}

		encoded := fmt.Sprint(entries[i])
		for _, secret := range []string{token, "private-bmc-password", "query-secret", "argument-secret", "stdin-secret", "path-secret", "private-runner-output", "203.0.113.100"} {
			if strings.Contains(encoded, secret) {
				t.Errorf("log exposed %q", secret)
			}
		}
	}
}

func TestPanicRecoveryDoesNotExposePanicValueAndReleasesSlot(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	calls := 0
	handler := api.New(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
		calls++
		if calls == 1 {
			panic("panic-containing-private-credentials")
		}

		return ipmi.Result{}, nil
	}}, zap.New(core))
	res := request(handler, http.MethodPost, "/api/v1/power/status", connectionBody(""))
	if res.Code != http.StatusInternalServerError || !json.Valid(res.Body.Bytes()) || strings.Contains(res.Body.String(), "private-credentials") {
		t.Fatalf("panic response status = %d: %s", res.Code, res.Body.String())
	}

	for _, entry := range logs.All() {
		if strings.Contains(fmt.Sprint(entry), "private-credentials") {
			t.Fatal("panic value exposed in logs")
		}

		if entry.Level == zap.ErrorLevel && entry.Message != "HTTP request panic recovered" {
			t.Errorf("unexpected error log message: %q", entry.Message)
		}
	}

	res = request(handler, http.MethodPost, "/api/v1/power/status", connectionBody(""))
	if res.Code != http.StatusOK || calls != 2 {
		t.Fatalf("request after panic status = %d, calls = %d", res.Code, calls)
	}
}

func TestListRoutesPreserveRunnerResponses(t *testing.T) {
	for _, path := range []string{"/api/v1/sensors", "/api/v1/sdr", "/api/v1/users/1", "/api/v1/sel/entries"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%v", path, fail), func(t *testing.T) {
				want := ipmi.Result{Stdout: "", Stderr: "empty list diagnostic", Truncated: true}
				var runErr error
				status := http.StatusOK
				if fail {
					want.ExitCode = 2
					runErr = ipmi.ErrCommandFailed
					status = http.StatusBadGateway
				}

				handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) { return want, runErr }})
				res := request(handler, http.MethodPost, path, connectionBody(""))
				if res.Code != status {
					t.Fatalf("status = %d, want %d", res.Code, status)
				}

				var got ipmi.Result
				if fail {
					var envelope struct {
						Result ipmi.Result `json:"result"`
					}
					if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}

					got = envelope.Result
				} else if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}

				if got != want {
					t.Fatalf("result = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestListRoutesRejectInconsistentRunnerResponses(t *testing.T) {
	for _, path := range []string{"/api/v1/sensors", "/api/v1/sdr", "/api/v1/users/1", "/api/v1/sel/entries"} {
		for _, tc := range []struct {
			name   string
			result ipmi.Result
			err    error
		}{
			{"nonzero success", ipmi.Result{ExitCode: 1}, nil},
			{"negative success", ipmi.Result{ExitCode: -1}, nil},
			{"zero failure", ipmi.Result{}, ipmi.ErrCommandFailed},
			{"invalid negative failure", ipmi.Result{ExitCode: -2}, fmt.Errorf("wrapped: %w", ipmi.ErrCommandFailed)},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
					return tc.result, tc.err
				}})
				res := request(handler, http.MethodPost, path, connectionBody(""))
				if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "invalid_runner_response") {
					t.Fatalf("status = %d: %s", res.Code, res.Body.String())
				}
			})
		}
	}
}

func TestListRoutesPreserveSignalTermination(t *testing.T) {
	for _, path := range []string{"/api/v1/sensors", "/api/v1/sdr", "/api/v1/users/1", "/api/v1/sel/entries"} {
		t.Run(path, func(t *testing.T) {
			want := ipmi.Result{Stdout: "partial output", Stderr: "interrupted", ExitCode: -1}
			handler := newAPI(testConfig(), stubRunner{run: func(context.Context, ipmi.Connection, []string, string) (ipmi.Result, error) {
				return want, fmt.Errorf("signal termination: %w", ipmi.ErrCommandFailed)
			}})
			res := request(handler, http.MethodPost, path, connectionBody(""))
			var envelope struct {
				Result ipmi.Result `json:"result"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}

			if res.Code != http.StatusBadGateway || envelope.Result != want {
				t.Fatalf("status = %d, result = %+v, want %+v", res.Code, envelope.Result, want)
			}
		})
	}
}
