package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/pluginprotocol"
)

const testHelperEnvKey = "CLAVIS_MCP_TEST_HELPER"

// TestMCPHelperProcess is not a regular unit test. When invoked by
// ExecProcessLauncher with CLAVIS_MCP_TEST_HELPER=1, it acts as a real MCP
// JSON-RPC stdio server subprocess for end-to-end integration tests.
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv(testHelperEnvKey) != "1" {
		return
	}

	mode := "normal"
	crashLimit := 0
	counterFile := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			for _, extra := range os.Args[i+1:] {
				switch {
				case strings.HasPrefix(extra, "mode="):
					mode = strings.TrimPrefix(extra, "mode=")
				case strings.HasPrefix(extra, "crash-first="):
					n, _ := strconv.Atoi(strings.TrimPrefix(extra, "crash-first="))
					crashLimit = n
				case strings.HasPrefix(extra, "counter-file="):
					counterFile = strings.TrimPrefix(extra, "counter-file=")
				}
			}
			break
		}
	}

	if mode == "crash-n-times" && counterFile != "" {
		count := incrementCounterFile(counterFile)
		if count <= crashLimit {
			_, _ = fmt.Fprintf(os.Stderr, "simulated startup crash attempt=%d\n", count)
			os.Exit(1)
		}
	}

	runMCPHelperServer(mode)
	os.Exit(0)
}

func incrementCounterFile(path string) int {
	raw, _ := os.ReadFile(path)
	n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	n++
	_ = os.WriteFile(path, []byte(strconv.Itoa(n)), 0o600)
	return n
}

func runMCPHelperServer(mode string) {
	decoder := pluginprotocol.NewDecoder(os.Stdin)
	encoder := pluginprotocol.NewEncoder(os.Stdout)

	var (
		mu           sync.Mutex
		inflight     = make(map[string]context.CancelFunc)
		cancelledIDs []string
		wg           sync.WaitGroup
	)

	sendResult := func(id any, payload any) {
		raw, _ := json.Marshal(payload)
		_ = encoder.Write(pluginprotocol.Response{
			JSONRPC: pluginprotocol.Version,
			ID:      id,
			Result:  raw,
		})
	}
	sendError := func(id any, code int, msg string) {
		_ = encoder.Write(pluginprotocol.Response{
			JSONRPC: pluginprotocol.Version,
			ID:      id,
			Error:   &pluginprotocol.ErrorObject{Code: code, Message: msg},
		})
	}

	for {
		msg, err := decoder.Read()
		if err != nil {
			if mode == "hang-on-eof" && errors.Is(err, io.EOF) {
				// Deliberately ignore stdin EOF to verify bounded kill & reap.
				time.Sleep(30 * time.Second)
			}
			break
		}
		if msg.Notification != nil {
			if msg.Notification.Method == MethodCancelled {
				var params struct {
					RequestID string `json:"requestId"`
				}
				_ = json.Unmarshal(msg.Notification.Params, &params)
				if params.RequestID != "" {
					mu.Lock()
					cancelledIDs = append(cancelledIDs, params.RequestID)
					if cancel := inflight[params.RequestID]; cancel != nil {
						cancel()
					}
					mu.Unlock()
				}
			}
			continue
		}
		if msg.Request == nil {
			continue
		}

		req := msg.Request
		reqID := responseID(req.ID)
		switch req.Method {
		case "initialize":
			sendResult(req.ID, map[string]any{
				"protocolVersion": DefaultProtocolVersion,
				"serverInfo":      map[string]any{"name": "helper-mcp", "version": "1.0.0"},
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			})
		case "tools/list":
			sendResult(req.ID, map[string]any{
				"tools": []map[string]any{
					{"name": "echo", "description": "echo text"},
					{"name": "inspect_env", "description": "return child process env map"},
					{"name": "slow", "description": "sleep until cancelled"},
					{"name": "cancelled_ids", "description": "return cancelled request IDs"},
					{"name": "emit_stderr", "description": "write diagnostic stderr"},
					{"name": "crash", "description": "exit non-zero abruptly"},
				},
			})
		case "tools/call":
			var call struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &call); err != nil {
				sendError(req.ID, -32602, "invalid params")
				continue
			}
			switch call.Name {
			case "echo":
				text, _ := call.Arguments["text"].(string)
				sendResult(req.ID, map[string]any{
					"content":           []map[string]any{{"type": "text", "text": text}},
					"structuredContent": map[string]any{"echo": text},
				})
			case "inspect_env":
				envMap := make(map[string]string)
				for _, kv := range os.Environ() {
					if parts := strings.SplitN(kv, "=", 2); len(parts) == 2 {
						envMap[parts[0]] = parts[1]
					}
				}
				sendResult(req.ID, map[string]any{
					"content":           []map[string]any{{"type": "text", "text": "ok"}},
					"structuredContent": map[string]any{"env": envMap},
				})
			case "slow":
				delayMs := 5000
				if v, ok := call.Arguments["delayMs"].(float64); ok && v > 0 {
					delayMs = int(v)
				}
				callCtx, cancel := context.WithCancel(context.Background())
				mu.Lock()
				inflight[reqID] = cancel
				mu.Unlock()
				wg.Add(1)
				go func(id any, idStr string, cctx context.Context, cfn context.CancelFunc, d time.Duration) {
					defer wg.Done()
					defer cfn()
					defer func() {
						mu.Lock()
						delete(inflight, idStr)
						mu.Unlock()
					}()
					timer := time.NewTimer(d)
					defer timer.Stop()
					select {
					case <-timer.C:
						sendResult(id, map[string]any{
							"content": []map[string]any{{"type": "text", "text": "completed"}},
						})
					case <-cctx.Done():
						// Cancelled via notifications/cancelled; do not reply.
					}
				}(req.ID, reqID, callCtx, cancel, time.Duration(delayMs)*time.Millisecond)
			case "cancelled_ids":
				mu.Lock()
				ids := append([]string(nil), cancelledIDs...)
				mu.Unlock()
				sendResult(req.ID, map[string]any{
					"content":           []map[string]any{{"type": "text", "text": strings.Join(ids, ",")}},
					"structuredContent": map[string]any{"ids": ids},
				})
			case "emit_stderr":
				msgText, _ := call.Arguments["message"].(string)
				_, _ = io.WriteString(os.Stderr, msgText)
				sendResult(req.ID, map[string]any{
					"content": []map[string]any{{"type": "text", "text": "emitted"}},
				})
			case "crash":
				_, _ = io.WriteString(os.Stderr, "crashing on request\n")
				os.Exit(2)
			default:
				sendError(req.ID, -32601, "unknown tool "+call.Name)
			}
		default:
			sendError(req.ID, -32601, "unknown method "+req.Method)
		}
	}

	mu.Lock()
	for _, cancel := range inflight {
		cancel()
	}
	mu.Unlock()
	wg.Wait()
}

func testExecutablePath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		exe, err = filepath.Abs(os.Args[0])
		if err != nil {
			t.Fatalf("resolve test binary path: %v", err)
		}
	}
	return filepath.Clean(exe)
}

func TestExecProcessLauncherPolicyValidation(t *testing.T) {
	exe := testExecutablePath(t)
	tempDir := t.TempDir()

	launcher := NewExecProcessLauncher(ExecLauncherOptions{
		AllowedCommands: []string{exe, "uvx"},
		AllowedServers:  []string{"helper"},
		LookPath: func(name string) (string, error) {
			return filepath.Join(tempDir, name), nil
		},
	})

	t.Run("rejects empty and malformed commands", func(t *testing.T) {
		badCommands := []string{
			"",
			"   ",
			" " + exe,
			exe + " ",
			"./helper",
			"../helper",
			"subdir/helper",
			`.\helper.exe`,
			`..\helper.exe`,
			"/usr/bin/../bin/helper",
			"/usr/bin/./helper",
			"sh",
			"/bin/sh",
			"bash",
			"cmd.exe",
			"powershell.exe",
			"uvx; rm -rf /",
			"uvx | cat",
			"uvx $(whoami)",
			"uvx --arg",
			"uvx\x00evil",
		}
		for _, cmd := range badCommands {
			_, err := launcher.Launch(context.Background(), ProcessSpec{
				ServerName: "helper",
				Command:    cmd,
			})
			if !errors.Is(err, ErrCommandRejected) {
				t.Errorf("command %q: got err=%v, want ErrCommandRejected", cmd, err)
			}
		}
	})

	t.Run("rejects non-allowlisted command and path-spoofed bare command", func(t *testing.T) {
		for _, cmd := range []string{"npx", filepath.Join(tempDir, "uvx"), filepath.Join(tempDir, "other")} {
			_, err := launcher.Launch(context.Background(), ProcessSpec{
				ServerName: "helper",
				Command:    cmd,
			})
			if !errors.Is(err, ErrCommandRejected) {
				t.Errorf("command %q: got err=%v, want ErrCommandRejected", cmd, err)
			}
		}
	})

	t.Run("rejects non-allowlisted or empty server name", func(t *testing.T) {
		for _, server := range []string{"", "   ", "evil-server", " helper "} {
			_, err := launcher.Launch(context.Background(), ProcessSpec{
				ServerName: server,
				Command:    exe,
			})
			if !errors.Is(err, ErrServerNotAllowed) {
				t.Errorf("server %q: got err=%v, want ErrServerNotAllowed", server, err)
			}
		}
	})

	t.Run("rejects relative or nonexistent working directory", func(t *testing.T) {
		for _, wd := range []string{"relative/dir", "../escape", filepath.Join(tempDir, "does-not-exist")} {
			_, err := launcher.Launch(context.Background(), ProcessSpec{
				ServerName: "helper",
				Command:    exe,
				WorkingDir: wd,
			})
			if !errors.Is(err, ErrWorkingDirInvalid) {
				t.Errorf("workingDir %q: got err=%v, want ErrWorkingDirInvalid", wd, err)
			}
		}
	})

	t.Run("rejects secret and loader env vars declared on server spec", func(t *testing.T) {
		forbidden := []string{
			"OPENAI_API_KEY",
			"DEEPSEEK_API_KEY",
			"ANTHROPIC_API_KEY",
			"GEMINI_API_KEY",
			"AWS_SECRET_ACCESS_KEY",
			"MY_CUSTOM_API_KEY",
			"SERVICE_TOKEN",
			"DB_PASSWORD",
			"LD_PRELOAD",
			"DYLD_INSERT_LIBRARIES",
		}
		for _, key := range forbidden {
			_, err := launcher.Launch(context.Background(), ProcessSpec{
				ServerName:  "helper",
				Command:     exe,
				Environment: map[string]string{key: "should-not-pass"},
			})
			if !errors.Is(err, ErrEnvVarRejected) {
				t.Errorf("env key %q: got err=%v, want ErrEnvVarRejected", key, err)
			}
		}
	})

	t.Run("fails closed if AllowedCommands contains only unsafe entries", func(t *testing.T) {
		unsafeLauncher := NewExecProcessLauncher(ExecLauncherOptions{
			AllowedCommands: []string{"./relative", "../escape", "sh"},
		})
		_, err := unsafeLauncher.Launch(context.Background(), ProcessSpec{
			ServerName: "helper",
			Command:    exe,
		})
		if !errors.Is(err, ErrCommandRejected) {
			t.Fatalf("got err=%v, want ErrCommandRejected", err)
		}
	})
	t.Run("applies safe default shutdown grace and stderr bounds", func(t *testing.T) {
		defaultLauncher := NewExecProcessLauncher(ExecLauncherOptions{})
		if defaultLauncher.shutdownGrace != defaultShutdownGrace {
			t.Fatalf("default shutdownGrace = %v, want %v", defaultLauncher.shutdownGrace, defaultShutdownGrace)
		}
		if defaultLauncher.stderrMaxBytes != defaultStderrMaxBytes {
			t.Fatalf("default stderrMaxBytes = %d, want %d", defaultLauncher.stderrMaxBytes, defaultStderrMaxBytes)
		}
	})
}

func TestExecProcessLauncherEndToEndMCP(t *testing.T) {
	// Populate parent secrets to prove child process environment isolation.
	parentSecrets := map[string]string{
		"OPENAI_API_KEY":        "sk-parent-openai-secret-9876543210",
		"DEEPSEEK_API_KEY":      "sk-parent-deepseek-secret-9876543210",
		"ANTHROPIC_API_KEY":     "sk-ant-parent-secret-9876543210",
		"GEMINI_API_KEY":        "AIzaSyParentSecretKey123456789012345",
		"AWS_SECRET_ACCESS_KEY": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"UNLISTED_PARENT_VAR":   "must-not-leak",
	}
	for k, v := range parentSecrets {
		t.Setenv(k, v)
	}

	exe := testExecutablePath(t)
	workDir := t.TempDir()

	// Use a 3s grace window in this test so Go's -race runtime finalizer (~1s
	// on macOS arm64) still exits cleanly with status 0 on stdin EOF before
	// forced kill triggers.
	baseLauncher := NewExecProcessLauncher(ExecLauncherOptions{
		AllowedCommands: []string{exe},
		AllowedServers:  []string{"helper"},
		ShutdownGrace:   3 * time.Second,
	})

	var (
		procMu       sync.Mutex
		launchedProc DiagnosticProcess
	)
	capturingLauncher := ProcessLauncherFunc(func(ctx context.Context, spec ProcessSpec) (Process, error) {
		p, err := baseLauncher.Launch(ctx, spec)
		if err != nil {
			return nil, err
		}
		if dp, ok := p.(DiagnosticProcess); ok {
			procMu.Lock()
			launchedProc = dp
			procMu.Unlock()
		}
		return p, nil
	})

	manager := NewManager([]Spec{{
		ID: "helper",
		Config: ServerConfig{
			Name:    "helper",
			Command: exe,
			Args:    []string{"-test.run=^TestMCPHelperProcess$", "--", "mode=normal"},
			Environment: map[string]string{
				testHelperEnvKey: "1",
				"MCP_HELPER_TAG": "e2e-verified",
			},
			WorkingDir:   workDir,
			AllowedTools: []string{"echo", "inspect_env", "slow", "cancelled_ids"},
			Timeout:      5 * time.Second,
		},
		Launcher:       capturingLauncher,
		MaxConcurrency: 4,
	}}, Options{
		ClientOptions: ClientOptions{ClientName: "clavis-test", ClientVersion: "1.0"},
	})
	t.Cleanup(func() { _ = manager.StopAll(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Start (initialize + notifications/initialized + tools/list).
	if err := manager.Start(ctx, "helper"); err != nil {
		t.Fatalf("manager.Start: %v", err)
	}
	statuses := manager.Status()
	if len(statuses) != 1 || !statuses[0].Running || len(statuses[0].Tools) < 4 {
		t.Fatalf("unexpected status after Start: %+v", statuses)
	}

	client := manager.Client("helper")
	if client == nil {
		t.Fatal("manager.Client returned nil for running server")
	}

	// 2. tools/call echo.
	echoRes, err := client.Invoke(ctx, "echo", map[string]any{"text": "hello-real-stdio"})
	if err != nil {
		t.Fatalf("Invoke(echo): %v", err)
	}
	if len(echoRes.Content) != 1 || echoRes.Content[0]["text"] != "hello-real-stdio" {
		t.Fatalf("echo result = %#v", echoRes)
	}

	// 3. Sanitized env assertion: parent secrets must NOT be inherited;
	// explicit non-secret declared vars MUST be present.
	envRes, err := client.Invoke(ctx, "inspect_env", nil)
	if err != nil {
		t.Fatalf("Invoke(inspect_env): %v", err)
	}
	structured, ok := envRes.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("unexpected structuredContent type: %#v", envRes.StructuredContent)
	}
	childEnv, ok := structured["env"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected env payload: %#v", structured)
	}
	for secretKey := range parentSecrets {
		if val, leaked := childEnv[secretKey]; leaked {
			t.Fatalf("parent env var %q leaked to child process (val=%v)", secretKey, val)
		}
	}
	if got := childEnv["MCP_HELPER_TAG"]; got != "e2e-verified" {
		t.Fatalf("declared non-secret env MCP_HELPER_TAG = %v, want e2e-verified", got)
	}

	// 4. Cancellation: cancel an in-flight slow tool call and confirm the child
	// receives notifications/cancelled and stays healthy afterwards.
	slowCtx, slowCancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer slowCancel()
	_, cancelledReqID, slowErr := client.InvokeWithID(slowCtx, "slow", map[string]any{"delayMs": 5000})
	if !errors.Is(slowErr, context.DeadlineExceeded) {
		t.Fatalf("slow tool err = %v, want context.DeadlineExceeded", slowErr)
	}
	if cancelledReqID == "" {
		t.Fatal("expected non-empty request ID for cancelled call")
	}

	// Poll briefly for the async notifications/cancelled delivery to be processed by the child.
	deadline := time.Now().Add(2 * time.Second)
	var observedCancelled bool
	for time.Now().Before(deadline) {
		checkRes, err := client.Invoke(ctx, "cancelled_ids", nil)
		if err != nil {
			t.Fatalf("Invoke(cancelled_ids): %v", err)
		}
		if len(checkRes.Content) > 0 {
			if txt, _ := checkRes.Content[0]["text"].(string); strings.Contains(txt, cancelledReqID) {
				observedCancelled = true
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !observedCancelled {
		t.Fatalf("child MCP server did not record notifications/cancelled for request %q", cancelledReqID)
	}

	// 5. Graceful shutdown should exit cleanly with status 0 via stdin EOF
	// before the grace window expires and leave zero zombie processes.
	stopStart := time.Now()
	if err := manager.StopAll(context.Background()); err != nil {
		t.Fatalf("manager.StopAll: %v", err)
	}
	if elapsed := time.Since(stopStart); elapsed > 2500*time.Millisecond {
		t.Fatalf("graceful shutdown took %v, expected clean stdin-EOF exit before 3s grace", elapsed)
	}
	procMu.Lock()
	dp := launchedProc
	procMu.Unlock()
	if dp == nil {
		t.Fatal("expected DiagnosticProcess to be captured")
	}
	select {
	case <-dp.Done():
	default:
		t.Fatal("child process was not reaped when StopAll returned")
	}
	if waitErr := dp.WaitErr(); waitErr != nil {
		t.Fatalf("expected clean exit code 0 on graceful stdin-EOF shutdown, got waitErr=%v", waitErr)
	}
}

func TestExecProcessLauncherCrashRestartBackoff(t *testing.T) {
	exe := testExecutablePath(t)
	counterFile := filepath.Join(t.TempDir(), "crash-count.txt")

	launcher := NewExecProcessLauncher(ExecLauncherOptions{
		AllowedCommands: []string{exe},
		AllowedServers:  []string{"flaky"},
		ShutdownGrace:   200 * time.Millisecond,
	})

	var (
		sleepMu   sync.Mutex
		sleepDurs []time.Duration
	)

	manager := NewManager([]Spec{{
		ID: "flaky",
		Config: ServerConfig{
			Name:    "flaky",
			Command: exe,
			Args: []string{
				"-test.run=^TestMCPHelperProcess$",
				"--",
				"mode=crash-n-times",
				"crash-first=2",
				"counter-file=" + counterFile,
			},
			Environment: map[string]string{
				testHelperEnvKey: "1",
			},
			AllowedTools: []string{"echo", "crash"},
			Timeout:      3 * time.Second,
		},
		Launcher: launcher,
	}}, Options{
		MaxRestartAttempts: 3,
		BaseBackoff:        10 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleepMu.Lock()
			sleepDurs = append(sleepDurs, d)
			sleepMu.Unlock()
			return nil
		},
	})
	t.Cleanup(func() { _ = manager.StopAll(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Restart triggers:
	//   attempt 0 -> helper crashes (count=1 <= 2) -> sleeps 10ms, Restarts=1
	//   attempt 1 -> helper crashes (count=2 <= 2) -> sleeps 20ms, Restarts=2
	//   attempt 2 -> helper succeeds (count=3 > 2)!
	if err := manager.Restart(ctx, "flaky"); err != nil {
		t.Fatalf("manager.Restart failed to recover after transient crashes: %v", err)
	}

	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleepDurs...)
	sleepMu.Unlock()
	if len(gotSleeps) != 2 || gotSleeps[0] != 10*time.Millisecond || gotSleeps[1] != 20*time.Millisecond {
		t.Fatalf("backoff durations = %v, want [10ms 20ms]", gotSleeps)
	}

	status := manager.Status()[0]
	if !status.Running || status.Restarts != 2 {
		t.Fatalf("status after restart = %+v, want Running=true Restarts=2", status)
	}

	// Verify server responds after recovery.
	res, err := manager.Client("flaky").Invoke(ctx, "echo", map[string]any{"text": "recovered"})
	if err != nil || len(res.Content) != 1 || res.Content[0]["text"] != "recovered" {
		t.Fatalf("Invoke after restart = %#v, err=%v", res, err)
	}

	// Trigger a mid-session crash, confirm Invoke fails, then recover with Restart.
	if _, err := manager.Client("flaky").Invoke(ctx, "crash", nil); err == nil {
		t.Fatal("expected error when helper crashes mid-session")
	}
	if err := manager.Restart(ctx, "flaky"); err != nil {
		t.Fatalf("manager.Restart after mid-session crash: %v", err)
	}
	res2, err := manager.Client("flaky").Invoke(ctx, "echo", map[string]any{"text": "recovered-again"})
	if err != nil || len(res2.Content) != 1 || res2.Content[0]["text"] != "recovered-again" {
		t.Fatalf("Invoke after second restart = %#v, err=%v", res2, err)
	}
}

func TestExecProcessLauncherBoundedStderrRedactionAndForcedKillReap(t *testing.T) {
	exe := testExecutablePath(t)
	launcher := NewExecProcessLauncher(ExecLauncherOptions{
		AllowedCommands: []string{exe},
		AllowedServers:  []string{"stubborn"},
		ShutdownGrace:   100 * time.Millisecond,
		StderrMaxBytes:  256,
	})

	proc, err := launcher.Launch(context.Background(), ProcessSpec{
		ServerName: "stubborn",
		Command:    exe,
		Args:       []string{"-test.run=^TestMCPHelperProcess$", "--", "mode=hang-on-eof"},
		Environment: map[string]string{
			testHelperEnvKey: "1",
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	diagProc, ok := proc.(DiagnosticProcess)
	if !ok {
		t.Fatalf("launched process %T does not implement DiagnosticProcess", proc)
	}

	// Wrap launched process in a StdioTransport via a one-shot launcher so we
	// can exercise real JSON-RPC plus inspect StderrTail and forced Kill/reap.
	transport, err := NewStdioTransport(context.Background(), ServerConfig{
		Name:    "stubborn",
		Command: exe,
	}, ProcessLauncherFunc(func(context.Context, ProcessSpec) (Process, error) {
		return proc, nil
	}))
	if err != nil {
		_ = proc.Close()
		t.Fatalf("NewStdioTransport: %v", err)
	}
	client := NewConfiguredClient(transport, ServerConfig{
		Name:         "stubborn",
		AllowedTools: []string{"emit_stderr"},
		Timeout:      3 * time.Second,
	}, ClientOptions{})

	ctx := context.Background()
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		t.Fatalf("Initialize: %v", err)
	}

	// Emit >256 bytes of stderr containing secrets to verify both tail bounding
	// and secret redaction.
	noisyPrefix := strings.Repeat("A", 512)
	secretPayload := noisyPrefix + " diag OPENAI_API_KEY=sk-1234567890abcdef Bearer supersecretvalue999 tail-marker\n"
	if _, err := client.Invoke(ctx, "emit_stderr", map[string]any{"message": secretPayload}); err != nil {
		_ = client.Close()
		t.Fatalf("Invoke(emit_stderr): %v", err)
	}

	// Wait briefly for stderr drain goroutine to buffer the emitted output.
	deadline := time.Now().Add(2 * time.Second)
	var tail string
	for time.Now().Before(deadline) {
		tail = diagProc.StderrTail()
		if strings.Contains(tail, "tail-marker") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(tail, "tail-marker") {
		t.Fatalf("stderr tail missing expected suffix: %q", tail)
	}
	if len(tail) > 256 {
		t.Fatalf("stderr tail exceeded bound: len=%d > 256", len(tail))
	}
	if strings.Contains(tail, "sk-1234567890abcdef") || strings.Contains(tail, "supersecretvalue999") {
		t.Fatalf("stderr tail leaked secret: %q", tail)
	}
	if !strings.Contains(tail, "[REDACTED]") {
		t.Fatalf("stderr tail did not redact secrets: %q", tail)
	}

	// Close must wait the ~100ms grace window (because hang-on-eof ignores stdin
	// EOF), then force-kill and cleanly reap the child process.
	closeStart := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("client.Close: %v", err)
	}
	elapsed := time.Since(closeStart)
	if elapsed < 80*time.Millisecond {
		t.Fatalf("Close returned in %v, expected at least grace window wait (~100ms)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Close took %v, expected bounded force-kill and reap", elapsed)
	}

	select {
	case <-diagProc.Done():
		// Child was reaped cleanly (zero zombie process).
	default:
		t.Fatal("child process was not reaped when Close returned")
	}
	if diagProc.WaitErr() == nil {
		t.Fatal("expected non-nil WaitErr for force-killed hang-on-eof child")
	}
}
