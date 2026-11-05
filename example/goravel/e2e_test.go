package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Ports from the 2600-2699 range this work may use.
const (
	capturePort = "2601"
	appPort     = "2602"
	smtpPort    = "2603"
	maxMails    = 12 // a runaway retry loop is stopped long before this is real load
)

type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func build(t *testing.T, dir, out string, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, output)
	}
}

// start runs a process that is always stopped when the test ends.
func start(t *testing.T, name string, env []string, workdir string, bin string, args ...string) (*exec.Cmd, *safeBuffer, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), env...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second
	logs := &safeBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start %s: %v", name, err)
	}

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
		}
	})

	return cmd, logs, done
}

func stop(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			_ = resp.Body.Close()

			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not answer", url)
}

func call(t *testing.T, method, path, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://127.0.0.1:"+appPort+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(body)
}

func loadRecords(t *testing.T, dir string) []map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "batch-*.json"))
	var all []map[string]any
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var batch struct{ Records []map[string]any }
		if err := json.Unmarshal(raw, &batch); err != nil {
			t.Fatal(err)
		}
		all = append(all, batch.Records...)
	}

	return all
}

func ofType(records []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, r := range records {
		if r["t"] == kind {
			out = append(out, r)
		}
	}

	return out
}

func TestExampleReportsEveryRecordType(t *testing.T) {
	if testing.Short() {
		t.Skip("end to end test")
	}
	root, _ := filepath.Abs("../..")
	bin := t.TempDir()
	build(t, root, filepath.Join(bin, "omni-capture"), "./cmd/omni-capture")
	build(t, ".", filepath.Join(bin, "example"), ".")

	appCtx, abort := context.WithCancel(context.Background())
	defer abort()
	mails := startFakeSMTP(t, "127.0.0.1:"+smtpPort, func(total int64) {
		if total > maxMails {
			abort()
		}
	})

	captureDir := t.TempDir()
	_, captureLog, _ := start(t, "omni-capture", nil, root, filepath.Join(bin, "omni-capture"), "-addr", "127.0.0.1:"+capturePort, "-dir", captureDir)
	waitHTTP(t, "http://127.0.0.1:"+capturePort+"/api/omni/ping")

	queueName := fmt.Sprintf("nwgo-e2e-%d", time.Now().UnixNano())
	work := t.TempDir()
	_ = os.MkdirAll(filepath.Join(work, "storage", "logs"), 0o750)
	t.Cleanup(func() { cleanRedis(queueName) })

	env := []string{
		"NIGHTWATCH_TOKEN=e2e", "NIGHTWATCH_BASE_URL=http://127.0.0.1:" + capturePort,
		"NIGHTWATCH_SERVER=example", "NIGHTWATCH_DEPLOY=e2e",
		"APP_KEY=01234567890123456789012345678901", "APP_ENV=local", "APP_PORT=" + appPort,
		"JWT_SECRET=0123456789abcdef0123456789abcdef", "DB_DATABASE=" + filepath.Join(work, "example.sqlite"),
		"MAIL_HOST=127.0.0.1", "MAIL_PORT=" + smtpPort, "MAIL_USERNAME=user", "MAIL_PASSWORD=pass",
		"OUTGOING_URL=http://127.0.0.1:" + capturePort + "/api/omni/ping",
	}
	appEnv := append(append([]string{}, env...), "QUEUE_NAME="+queueName)
	app, appLog, _ := start(t, "example", appEnv, work, filepath.Join(bin, "example"))
	go func() { <-appCtx.Done(); _ = app.Process.Kill() }() // watchdog: too many mails
	waitHTTP(t, "http://127.0.0.1:"+appPort+"/api/tasks/7")

	exercise(t, mails)
	waitForRecord(t, captureDir, "scheduled-task") // the scheduler ticks every second
	waitForRecord(t, captureDir, "job-attempt")
	stop(t, app)

	// A command runs in its own short process and flushes before it exits.
	cmdBin := filepath.Join(bin, "example")
	_, cmdLog, cmdDone := start(t, "command", env, work, cmdBin, "artisan", "hello")
	select {
	case <-cmdDone:
	case <-time.After(30 * time.Second):
		t.Error("command did not finish")
	}
	t.Logf("command output: %s", cmdLog.String())

	waitForRecord(t, captureDir, "command")
	keepCapture(t, captureDir)
	verify(t, captureDir, captureLog.String(), appLog.String())

	if mails.conns.Load() > 6 {
		t.Fatalf("too many SMTP connections: %d (retry loop?)", mails.conns.Load())
	}
}

func exercise(t *testing.T, mails *fakeSMTP) {
	t.Helper()
	for _, step := range []struct{ method, path string }{
		{"GET", "/api/tasks/7"},
		{"GET", "/api/boom"},
		{"GET", "/api/handled"},
		{"POST", "/api/mail"},
		{"GET", "/api/outgoing"},
		{"POST", "/api/notify"},
		{"POST", "/api/welcome"},
	} {
		call(t, step.method, step.path, "")
	}

	_, body := call(t, "POST", "/api/login", "")
	var login struct{ Token string }
	_ = json.Unmarshal([]byte(body), &login)
	if status, _ := call(t, "GET", "/api/me", login.Token); status != 200 {
		t.Fatalf("me: %d", status)
	}

	deadline := time.Now().Add(20 * time.Second)
	for mails.messages.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
}

func verify(t *testing.T, dir, captureLog, appLog string) {
	t.Helper()
	records := loadRecords(t, dir)

	if strings.Contains(captureLog, "batch rejected") {
		t.Fatalf("Omni rules rejected a batch:\n%s", captureLog)
	}

	for _, kind := range []string{
		"request", "query", "exception", "log", "command", "queued-job", "job-attempt",
		"cache-event", "scheduled-task", "mail", "notification", "outgoing-request", "user",
	} {
		if len(ofType(records, kind)) == 0 {
			t.Errorf("no %q record arrived\napp log tail:\n%s", kind, tail(appLog))
		}
	}

	checkLinks(t, records)
	checkValues(t, records)
}

func tail(text string) string {
	if len(text) > 1500 {
		return text[len(text)-1500:]
	}

	return text
}

// checkLinks makes sure every child record names an execution that arrived.
func checkLinks(t *testing.T, records []map[string]any) {
	t.Helper()
	ids := map[string]bool{}
	for _, kind := range []string{"request", "command", "scheduled-task"} {
		for _, r := range ofType(records, kind) {
			ids[r["trace_id"].(string)] = true
		}
	}
	for _, r := range ofType(records, "job-attempt") {
		ids[r["attempt_id"].(string)] = true
	}

	for _, r := range records {
		if id, _ := r["execution_id"].(string); id != "" && !ids[id] {
			t.Errorf("%v record points to a missing execution %s", r["t"], id)
		}
	}
}

func checkValues(t *testing.T, records []map[string]any) {
	t.Helper()
	queued := ofType(records, "queued-job")
	attempts := ofType(records, "job-attempt")

	if len(queued) == 0 || len(attempts) == 0 {
		return
	}
	if len(attempts) > 3 {
		t.Errorf("job ran %d times, expected at most 3", len(attempts))
	}
	if attempts[0]["job_id"] != queued[0]["job_id"] || attempts[0]["trace_id"] != queued[0]["trace_id"] {
		t.Errorf("job attempt does not continue the request trace: %v vs %v", attempts[0], queued[0])
	}

	handled := map[bool]bool{}
	for _, r := range ofType(records, "exception") {
		handled[r["handled"].(bool)] = true
	}
	if !handled[true] || !handled[false] {
		t.Errorf("want handled and unhandled exceptions, got %v", handled)
	}

	routes := map[string]bool{}
	for _, r := range ofType(records, "request") {
		routes[r["route_path"].(string)] = true
	}
	if !routes["/api/tasks/{id}"] && !routes["/api/tasks/:id"] {
		t.Errorf("route_path of the task route missing: %v", routes)
	}
}

// cleanRedis removes only the keys of this run's queue.
func cleanRedis(queueName string) {
	out, err := exec.Command("redis-cli", "-n", "15", "--scan", "--pattern", "*"+queueName+"*").Output()
	if err != nil {
		return
	}
	for _, key := range strings.Fields(string(out)) {
		_ = exec.Command("redis-cli", "-n", "15", "del", key).Run()
	}
}

// keepCapture copies the batches to $NW_E2E_KEEP_DIR so nwdiff can read them.
func keepCapture(t *testing.T, dir string) {
	t.Helper()
	keep := os.Getenv("NW_E2E_KEEP_DIR")
	if keep == "" {
		return
	}

	_ = os.MkdirAll(keep, 0o750)
	files, _ := filepath.Glob(filepath.Join(dir, "batch-*.json"))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(keep, filepath.Base(file)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// waitForRecord polls the capture folder until a record of the type arrived.
func waitForRecord(t *testing.T, dir, kind string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if len(ofType(loadRecords(t, dir), kind)) > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no %q record arrived in 60s", kind)
}

// With Nightwatch disabled the app must behave exactly as without the library:
// nothing reaches Omni, and a queued job still gets only its own arguments.
func TestExampleWorksWithNightwatchDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("end to end test")
	}
	root, _ := filepath.Abs("../..")
	bin := t.TempDir()
	build(t, root, filepath.Join(bin, "omni-capture"), "./cmd/omni-capture")
	build(t, ".", filepath.Join(bin, "example"), ".")

	mails := startFakeSMTP(t, "127.0.0.1:"+smtpPort, func(total int64) {
		if total > maxMails {
			t.Errorf("runaway mail loop: %d connections", total)
		}
	})
	captureDir := t.TempDir()
	_, captureLog, _ := start(t, "omni-capture", nil, root, filepath.Join(bin, "omni-capture"), "-addr", "127.0.0.1:"+capturePort, "-dir", captureDir)
	waitHTTP(t, "http://127.0.0.1:"+capturePort+"/api/omni/ping")

	queueName := fmt.Sprintf("nwgo-e2e-off-%d", time.Now().UnixNano())
	work := t.TempDir()
	_ = os.MkdirAll(filepath.Join(work, "storage", "logs"), 0o750)
	t.Cleanup(func() { cleanRedis(queueName) })

	env := []string{
		"NIGHTWATCH_ENABLED=false", "NIGHTWATCH_TOKEN=e2e", "NIGHTWATCH_BASE_URL=http://127.0.0.1:" + capturePort,
		"APP_KEY=01234567890123456789012345678901", "APP_ENV=local", "APP_PORT=" + appPort, "QUEUE_NAME=" + queueName,
		"JWT_SECRET=0123456789abcdef0123456789abcdef", "DB_DATABASE=" + filepath.Join(work, "example.sqlite"),
		"MAIL_HOST=127.0.0.1", "MAIL_PORT=" + smtpPort, "MAIL_USERNAME=user", "MAIL_PASSWORD=pass",
		"OUTGOING_URL=http://127.0.0.1:" + capturePort + "/api/omni/ping",
	}
	app, _, _ := start(t, "example", env, work, filepath.Join(bin, "example"))
	waitHTTP(t, "http://127.0.0.1:"+appPort+"/api/tasks/7")

	for _, step := range []struct{ method, path string }{
		{"GET", "/api/tasks/7"}, {"GET", "/api/boom"}, {"POST", "/api/mail"}, {"GET", "/api/outgoing"}, {"POST", "/api/welcome"},
	} {
		call(t, step.method, step.path, "")
	}

	deadline := time.Now().Add(20 * time.Second)
	for mails.messages.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if mails.messages.Load() < 2 {
		t.Fatalf("the queued job did not run: %d mails (it must get only its own arguments)", mails.messages.Load())
	}

	stop(t, app)
	if records := loadRecords(t, captureDir); len(records) != 0 {
		t.Fatalf("a disabled client must send nothing, got %d records", len(records))
	}
	if strings.Contains(captureLog.String(), "batch") {
		t.Fatalf("Omni saw traffic: %s", captureLog.String())
	}
}
