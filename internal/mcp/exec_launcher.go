package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultShutdownGrace  = 500 * time.Millisecond
	defaultStderrMaxBytes = 8 * 1024
)

var (
	// ErrCommandRejected indicates the requested executable failed allowlist or
	// path/shell-safety validation.
	ErrCommandRejected = errors.New("mcp: command rejected by launcher policy")
	// ErrServerNotAllowed indicates the server name is not permitted by the
	// launcher's AllowedServers policy.
	ErrServerNotAllowed = errors.New("mcp: server is not allowed by launcher policy")
	// ErrWorkingDirInvalid indicates the configured working directory is
	// relative, malformed, or does not exist.
	ErrWorkingDirInvalid = errors.New("mcp: working directory is invalid")
	// ErrEnvVarRejected indicates an explicitly declared server environment
	// variable is invalid or forbidden (such as a secret or loader override).
	ErrEnvVarRejected = errors.New("mcp: environment variable rejected by launcher policy")
)

var defaultBaseEnvAllowlist = []string{
	"PATH",
	"HOME",
	"USER",
	"TMPDIR",
	"LANG",
	"LC_ALL",
	"SystemRoot",
	"COMSPEC",
}

// deniedExactEnvKeys lists well-known credential and secret env vars that must
// never be forwarded to an MCP child process.
var deniedExactEnvKeys = map[string]struct{}{
	"DEEPSEEK_API_KEY":      {},
	"OPENAI_API_KEY":        {},
	"ANTHROPIC_API_KEY":     {},
	"GEMINI_API_KEY":        {},
	"GOOGLE_API_KEY":        {},
	"AZURE_OPENAI_API_KEY":  {},
	"OPENROUTER_API_KEY":    {},
	"GROQ_API_KEY":          {},
	"MISTRAL_API_KEY":       {},
	"COHERE_API_KEY":        {},
	"HF_TOKEN":              {},
	"HUGGINGFACE_TOKEN":     {},
	"AWS_ACCESS_KEY_ID":     {},
	"AWS_SECRET_ACCESS_KEY": {},
	"AWS_SESSION_TOKEN":     {},
	"GITHUB_TOKEN":          {},
	"GH_TOKEN":              {},
	"GITLAB_TOKEN":          {},
	"NPM_TOKEN":             {},
	"PYPI_API_TOKEN":        {},
	"DATABASE_URL":          {},
	"SSH_AUTH_SOCK":         {},
	"AUTHORIZATION":         {},
	"PRIVATE_KEY":           {},
	"SECRET_KEY":            {},
	"API_KEY":               {},
	"PASSWORD":              {},
}

var deniedEnvKeySuffixes = []string{
	"_API_KEY",
	"_APIKEY",
	"_SECRET",
	"_SECRET_KEY",
	"_ACCESS_KEY",
	"_TOKEN",
	"_PASSWORD",
	"_PASSWD",
	"_PRIVATE_KEY",
	"_CREDENTIAL",
	"_CREDENTIALS",
	"_AUTH",
}

// protectedRuntimeEnvKeys are dynamic-linker or shell startup hooks that must
// not be overridden via server-declared environment maps.
var protectedRuntimeEnvKeys = map[string]struct{}{
	"LD_PRELOAD":            {},
	"LD_LIBRARY_PATH":       {},
	"DYLD_INSERT_LIBRARIES": {},
	"DYLD_LIBRARY_PATH":     {},
	"DYLD_FRAMEWORK_PATH":   {},
	"BASH_ENV":              {},
	"ENV":                   {},
	"IFS":                   {},
	"COMSPEC":               {},
	"SYSTEMROOT":            {},
}

// shellExecutableNames lists shell interpreters that must not be launched
// directly to avoid shell-command injection tricks.
var shellExecutableNames = map[string]struct{}{
	"sh":             {},
	"bash":           {},
	"zsh":            {},
	"dash":           {},
	"ash":            {},
	"ksh":            {},
	"csh":            {},
	"tcsh":           {},
	"fish":           {},
	"cmd":            {},
	"cmd.exe":        {},
	"command.com":    {},
	"powershell":     {},
	"powershell.exe": {},
	"pwsh":           {},
	"pwsh.exe":       {},
	"wscript":        {},
	"wscript.exe":    {},
	"cscript":        {},
	"cscript.exe":    {},
	"mshta":          {},
	"mshta.exe":      {},
}

var (
	secretTokenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`AIza[0-9A-Za-z_-]{20,}`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`),
	}
	bearerPattern   = regexp.MustCompile(`(?i)\b(Bearer\s+)([A-Za-z0-9._~+/=-]{8,})`)
	secretKVPattern = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API_KEY|APIKEY|SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE_KEY|ACCESS_KEY|CREDENTIAL)[A-Z0-9_]*)\s*([=:])\s*([^\s"',;}\]]+)`)
)

// DiagnosticProcess extends Process with bounded diagnostic inspection helpers.
type DiagnosticProcess interface {
	Process
	StderrTail() string
	Done() <-chan struct{}
	WaitErr() error
}

// ExecLauncherOptions configures security policy and lifecycle limits for
// ExecProcessLauncher.
type ExecLauncherOptions struct {
	// AllowedCommands restricts the executables that may be launched.
	// Entries must be either clean absolute paths or bare executable names
	// without path separators. Relative or shell-trick entries are ignored.
	// When non-empty, any command not matching AllowedCommands is rejected.
	AllowedCommands []string

	// AllowedServers restricts ProcessSpec.ServerName when non-empty.
	// When non-empty, specs with empty or unlisted ServerName are rejected.
	AllowedServers []string

	// BaseEnvAllowlist overrides the default minimal parent environment
	// allowlist (PATH, HOME, USER, TMPDIR, LANG, LC_ALL, SystemRoot, COMSPEC).
	BaseEnvAllowlist []string

	// DeniedEnvKeys adds extra case-insensitive environment variable names to
	// refuse when constructing the child environment.
	DeniedEnvKeys []string

	// ShutdownGrace is how long Close waits after closing stdin before forcing
	// Process.Kill and reaping the child. Defaults to 500ms.
	ShutdownGrace time.Duration

	// StderrMaxBytes caps the retained stderr diagnostic tail buffer.
	// Defaults to 8 KiB.
	StderrMaxBytes int

	// LookupEnv overrides os.LookupEnv for testing.
	LookupEnv func(string) (string, bool)

	// LookPath overrides exec.LookPath for testing.
	LookPath func(string) (string, error)
}

// ExecProcessLauncher is a real os/exec ProcessLauncher with explicit command
// and server allowlisting, sanitized minimal environment construction, bounded
// redacted stderr capture, and graceful stdin-EOF shutdown with clean reaping.
type ExecProcessLauncher struct {
	hasCommandAllowlist bool
	allowedAbs          map[string]struct{}
	allowedBare         map[string]struct{}

	hasServerAllowlist bool
	allowedServers     map[string]struct{}

	baseEnvAllowlist []string
	deniedEnvKeys    map[string]struct{}

	shutdownGrace  time.Duration
	stderrMaxBytes int

	lookupEnv func(string) (string, bool)
	lookPath  func(string) (string, error)
}

// NewExecProcessLauncher constructs an ExecProcessLauncher from opts.
func NewExecProcessLauncher(opts ExecLauncherOptions) *ExecProcessLauncher {
	l := &ExecProcessLauncher{
		hasCommandAllowlist: len(opts.AllowedCommands) > 0,
		allowedAbs:          make(map[string]struct{}),
		allowedBare:         make(map[string]struct{}),
		hasServerAllowlist:  len(opts.AllowedServers) > 0,
		allowedServers:      make(map[string]struct{}),
		deniedEnvKeys:       make(map[string]struct{}),
		shutdownGrace:       opts.ShutdownGrace,
		stderrMaxBytes:      opts.StderrMaxBytes,
		lookupEnv:           opts.LookupEnv,
		lookPath:            opts.LookPath,
	}
	if l.shutdownGrace <= 0 {
		l.shutdownGrace = defaultShutdownGrace
	}
	if l.stderrMaxBytes <= 0 {
		l.stderrMaxBytes = defaultStderrMaxBytes
	}
	if l.lookupEnv == nil {
		l.lookupEnv = os.LookupEnv
	}
	if l.lookPath == nil {
		l.lookPath = exec.LookPath
	}

	if len(opts.BaseEnvAllowlist) > 0 {
		l.baseEnvAllowlist = append([]string(nil), opts.BaseEnvAllowlist...)
	} else {
		l.baseEnvAllowlist = append([]string(nil), defaultBaseEnvAllowlist...)
	}

	for _, key := range opts.DeniedEnvKeys {
		if k := strings.ToUpper(strings.TrimSpace(key)); k != "" {
			l.deniedEnvKeys[k] = struct{}{}
		}
	}

	for _, raw := range opts.AllowedServers {
		if name := strings.TrimSpace(raw); name != "" {
			l.allowedServers[name] = struct{}{}
		}
	}

	for _, raw := range opts.AllowedCommands {
		entry := strings.TrimSpace(raw)
		if entry == "" || entry != raw {
			continue
		}
		if hasControlChars(entry) || hasShellMetachars(entry) || hasDotSegments(entry) {
			continue
		}
		if hasPathSepOrVolume(entry) {
			if os.PathSeparator == '/' && strings.ContainsRune(entry, '\\') {
				continue
			}
			if !filepath.IsAbs(entry) {
				continue
			}
			clean := filepath.Clean(entry)
			if isShellExecutable(filepath.Base(clean)) {
				continue
			}
			l.allowedAbs[clean] = struct{}{}
			continue
		}
		if strings.ContainsAny(entry, " \t") || isShellExecutable(entry) {
			continue
		}
		l.allowedBare[entry] = struct{}{}
	}

	return l
}

// Launch validates spec against the launcher policy, constructs a sanitized
// minimal environment, starts the child via os/exec.CommandContext, and wires
// stdio pipes plus bounded stderr capture.
func (l *ExecProcessLauncher) Launch(ctx context.Context, spec ProcessSpec) (Process, error) {
	if l == nil {
		return nil, errors.New("mcp: nil exec launcher")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := l.validateServer(spec.ServerName); err != nil {
		return nil, err
	}
	resolvedCmd, err := l.validateAndResolveCommand(spec.Command)
	if err != nil {
		return nil, err
	}
	if err := validateArgs(spec.Args); err != nil {
		return nil, err
	}
	if err := validateWorkingDir(spec.WorkingDir); err != nil {
		return nil, err
	}
	env, err := l.buildSanitizedEnv(spec.Environment)
	if err != nil {
		return nil, err
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: create stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, fmt.Errorf("mcp: create stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, fmt.Errorf("mcp: create stderr pipe: %w", err)
	}

	procCtx, procCancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, resolvedCmd, spec.Args...)
	cmd.Env = env
	cmd.Dir = spec.WorkingDir
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		procCancel()
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		return nil, fmt.Errorf("mcp: start stdio server %q: %w", spec.ServerName, err)
	}

	// Close the child-owned pipe ends in the parent immediately so EOF is
	// observed deterministically when the child exits.
	_ = stdinR.Close()
	_ = stdoutW.Close()
	_ = stderrW.Close()

	grace := l.shutdownGrace
	if grace <= 0 {
		grace = defaultShutdownGrace
	}
	maxStderr := l.stderrMaxBytes
	if maxStderr <= 0 {
		maxStderr = defaultStderrMaxBytes
	}

	p := &execProcess{
		cmd:           cmd,
		cancel:        procCancel,
		stdin:         stdinW,
		stdout:        stdoutR,
		stderr:        stderrR,
		stderrBuf:     newStderrTailBuffer(maxStderr),
		shutdownGrace: grace,
		waitDone:      make(chan struct{}),
		stderrDone:    make(chan struct{}),
	}

	go func() {
		defer close(p.stderrDone)
		defer p.closeStderr()
		_, _ = io.Copy(p.stderrBuf, p.stderr)
	}()

	go func() {
		defer close(p.waitDone)
		defer procCancel()
		p.waitErr = cmd.Wait()
	}()

	return p, nil
}

func (l *ExecProcessLauncher) validateServer(serverName string) error {
	if !l.hasServerAllowlist {
		return nil
	}
	name := strings.TrimSpace(serverName)
	if name == "" || name != serverName {
		return fmt.Errorf("%w: server name is required when allowlist is configured", ErrServerNotAllowed)
	}
	if _, ok := l.allowedServers[name]; !ok {
		return fmt.Errorf("%w: %q", ErrServerNotAllowed, name)
	}
	return nil
}

func (l *ExecProcessLauncher) validateAndResolveCommand(command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("%w: empty command", ErrCommandRejected)
	}
	if command != strings.TrimSpace(command) {
		return "", fmt.Errorf("%w: leading or trailing whitespace in command", ErrCommandRejected)
	}
	if hasControlChars(command) {
		return "", fmt.Errorf("%w: control characters in command", ErrCommandRejected)
	}
	if hasShellMetachars(command) {
		return "", fmt.Errorf("%w: shell metacharacters in command", ErrCommandRejected)
	}
	if hasDotSegments(command) {
		return "", fmt.Errorf("%w: relative dot segments in command", ErrCommandRejected)
	}

	if hasPathSepOrVolume(command) {
		if os.PathSeparator == '/' && strings.ContainsRune(command, '\\') {
			return "", fmt.Errorf("%w: invalid path separator in command", ErrCommandRejected)
		}
		if !filepath.IsAbs(command) {
			return "", fmt.Errorf("%w: relative command path %q is not allowed", ErrCommandRejected, command)
		}
		clean := filepath.Clean(command)
		if clean != command {
			return "", fmt.Errorf("%w: non-canonical command path %q", ErrCommandRejected, command)
		}
		if isShellExecutable(filepath.Base(clean)) {
			return "", fmt.Errorf("%w: shell executable %q is not allowed", ErrCommandRejected, filepath.Base(clean))
		}
		if l.hasCommandAllowlist {
			if _, ok := l.allowedAbs[clean]; !ok {
				return "", fmt.Errorf("%w: %q is not in AllowedCommands", ErrCommandRejected, clean)
			}
		}
		return clean, nil
	}

	if strings.ContainsAny(command, " \t") {
		return "", fmt.Errorf("%w: whitespace in bare command %q", ErrCommandRejected, command)
	}
	if isShellExecutable(command) {
		return "", fmt.Errorf("%w: shell executable %q is not allowed", ErrCommandRejected, command)
	}
	if l.hasCommandAllowlist {
		if _, ok := l.allowedBare[command]; !ok {
			return "", fmt.Errorf("%w: %q is not in AllowedCommands", ErrCommandRejected, command)
		}
	}

	lookPath := l.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	resolved, err := lookPath(command)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %v", ErrCommandRejected, command, err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("%w: resolved path %q is not absolute", ErrCommandRejected, resolved)
	}
	cleanResolved := filepath.Clean(resolved)
	if isShellExecutable(filepath.Base(cleanResolved)) {
		return "", fmt.Errorf("%w: resolved shell executable %q is not allowed", ErrCommandRejected, filepath.Base(cleanResolved))
	}
	return cleanResolved, nil
}

func validateArgs(args []string) error {
	for i, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("%w: argument %d contains NUL byte", ErrCommandRejected, i)
		}
	}
	return nil
}

func validateWorkingDir(dir string) error {
	if dir == "" {
		return nil
	}
	if hasControlChars(dir) || hasDotSegments(dir) {
		return fmt.Errorf("%w: %q", ErrWorkingDirInvalid, dir)
	}
	if os.PathSeparator == '/' && strings.ContainsRune(dir, '\\') {
		return fmt.Errorf("%w: %q", ErrWorkingDirInvalid, dir)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %q must be absolute", ErrWorkingDirInvalid, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWorkingDirInvalid, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q is not a directory", ErrWorkingDirInvalid, dir)
	}
	return nil
}

func (l *ExecProcessLauncher) buildSanitizedEnv(declared map[string]string) ([]string, error) {
	lookupEnv := l.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	baseList := l.baseEnvAllowlist
	if len(baseList) == 0 {
		baseList = defaultBaseEnvAllowlist
	}

	merged := make(map[string]string, len(baseList)+len(declared))
	for _, key := range baseList {
		if key == "" || isDeniedEnvKey(key, l.deniedEnvKeys) {
			continue
		}
		if val, ok := lookupEnv(key); ok {
			merged[key] = val
		}
	}

	keys := make([]string, 0, len(declared))
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := declared[k]
		if k == "" || k != strings.TrimSpace(k) || strings.ContainsAny(k, "= \t\r\n\x00") {
			return nil, fmt.Errorf("%w: invalid key %q", ErrEnvVarRejected, k)
		}
		if strings.ContainsRune(v, '\x00') {
			return nil, fmt.Errorf("%w: key %q contains NUL byte", ErrEnvVarRejected, k)
		}
		if isDeniedEnvKey(k, l.deniedEnvKeys) || isProtectedRuntimeEnvKey(k) {
			return nil, fmt.Errorf("%w: %q is forbidden", ErrEnvVarRejected, k)
		}
		merged[k] = v
	}

	// Never return a nil slice: os/exec interprets nil Cmd.Env as inheriting
	// the entire parent process environment.
	outKeys := make([]string, 0, len(merged))
	for k := range merged {
		outKeys = append(outKeys, k)
	}
	sort.Strings(outKeys)

	env := make([]string, 0, len(outKeys))
	for _, k := range outKeys {
		env = append(env, k+"="+merged[k])
	}
	return env, nil
}

func isDeniedEnvKey(key string, extra map[string]struct{}) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return true
	}
	if _, ok := extra[upper]; ok {
		return true
	}
	if _, ok := deniedExactEnvKeys[upper]; ok {
		return true
	}
	for _, suffix := range deniedEnvKeySuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	if strings.Contains(upper, "API_KEY") ||
		strings.Contains(upper, "SECRET_KEY") ||
		strings.Contains(upper, "PRIVATE_KEY") ||
		strings.Contains(upper, "ACCESS_TOKEN") ||
		strings.Contains(upper, "REFRESH_TOKEN") ||
		strings.Contains(upper, "BEARER_TOKEN") {
		return true
	}
	return false
}

func isProtectedRuntimeEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	_, ok := protectedRuntimeEnvKeys[upper]
	return ok
}

func isShellExecutable(base string) bool {
	_, ok := shellExecutableNames[strings.ToLower(strings.TrimSpace(base))]
	return ok
}

func hasControlChars(s string) bool {
	return strings.ContainsAny(s, "\x00\r\n")
}

func hasShellMetachars(s string) bool {
	return strings.ContainsAny(s, ";|&`$<>()*?{}'\"!")
}

func hasPathSepOrVolume(s string) bool {
	return strings.ContainsAny(s, `/\`) || filepath.VolumeName(s) != ""
}

func hasDotSegments(s string) bool {
	if s == "." || s == ".." {
		return true
	}
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

type execProcess struct {
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	stdin         *os.File
	stdout        *os.File
	stderr        *os.File
	stderrBuf     *stderrTailBuffer
	shutdownGrace time.Duration

	stdinMu     sync.Mutex
	stdinClosed bool

	stdoutCloseOnce sync.Once
	stderrCloseOnce sync.Once
	closeOnce       sync.Once
	closeErr        error

	waitDone   chan struct{}
	waitErr    error
	stderrDone chan struct{}
}

func (p *execProcess) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

func (p *execProcess) Write(b []byte) (int, error) {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if p.stdinClosed {
		return 0, io.ErrClosedPipe
	}
	return p.stdin.Write(b)
}

func (p *execProcess) closeStdin() error {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if p.stdinClosed {
		return nil
	}
	p.stdinClosed = true
	return p.stdin.Close()
}

func (p *execProcess) closeStdout() {
	p.stdoutCloseOnce.Do(func() {
		_ = p.stdout.Close()
	})
}

func (p *execProcess) closeStderr() {
	p.stderrCloseOnce.Do(func() {
		_ = p.stderr.Close()
	})
}

// Kill forcefully terminates the child process if it is still running and waits
// until the OS process has been reaped.
func (p *execProcess) Kill() error {
	select {
	case <-p.waitDone:
		return nil
	default:
	}

	var killErr error
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			select {
			case <-p.waitDone:
				killErr = nil
			default:
				killErr = err
			}
		}
	}
	p.cancel()
	<-p.waitDone
	return killErr
}

// Close performs a bounded graceful shutdown: closes stdin to signal EOF,
// waits up to ShutdownGrace for clean exit, force-kills on timeout, reaps the
// child cleanly, and releases all stdio descriptors without leaking goroutines.
func (p *execProcess) Close() error {
	p.closeOnce.Do(func() {
		_ = p.closeStdin()

		grace := p.shutdownGrace
		if grace <= 0 {
			grace = defaultShutdownGrace
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()

		select {
		case <-p.waitDone:
		case <-timer.C:
			if err := p.Kill(); err != nil {
				p.closeErr = err
			}
		}

		p.cancel()
		p.closeStdout()

		stderrTimer := time.NewTimer(50 * time.Millisecond)
		defer stderrTimer.Stop()
		select {
		case <-p.stderrDone:
		case <-stderrTimer.C:
			p.closeStderr()
			<-p.stderrDone
		}
	})
	return p.closeErr
}

// StderrTail returns the bounded diagnostic stderr tail with secrets redacted.
func (p *execProcess) StderrTail() string {
	if p == nil || p.stderrBuf == nil {
		return ""
	}
	return redactSecrets(p.stderrBuf.String())
}

// Done is closed once the child process has exited and been reaped.
func (p *execProcess) Done() <-chan struct{} {
	return p.waitDone
}

// WaitErr returns the exit error from cmd.Wait once Done is closed, or nil if
// the process has not yet exited.
func (p *execProcess) WaitErr() error {
	select {
	case <-p.waitDone:
		return p.waitErr
	default:
		return nil
	}
}

type stderrTailBuffer struct {
	mu       sync.Mutex
	maxBytes int
	buf      []byte
}

func newStderrTailBuffer(maxBytes int) *stderrTailBuffer {
	if maxBytes <= 0 {
		maxBytes = defaultStderrMaxBytes
	}
	return &stderrTailBuffer{maxBytes: maxBytes}
}

func (b *stderrTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) >= b.maxBytes {
		b.buf = append(b.buf[:0], p[len(p)-b.maxBytes:]...)
		return len(p), nil
	}
	overflow := len(b.buf) + len(p) - b.maxBytes
	if overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:len(b.buf)-overflow]
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *stderrTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func redactSecrets(s string) string {
	if s == "" {
		return ""
	}
	out := secretKVPattern.ReplaceAllString(s, "${1}${2}[REDACTED]")
	out = bearerPattern.ReplaceAllString(out, "${1}[REDACTED]")
	for _, pat := range secretTokenPatterns {
		out = pat.ReplaceAllString(out, "[REDACTED]")
	}
	return out
}

var (
	_ ProcessLauncher   = (*ExecProcessLauncher)(nil)
	_ Process           = (*execProcess)(nil)
	_ DiagnosticProcess = (*execProcess)(nil)
)
