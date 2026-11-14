package mobile

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestMobileSessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// StateJSON initially hidden
	stJSON, err := sess.StateJSON()
	if err != nil {
		t.Fatalf("StateJSON failed: %v", err)
	}
	var st engine.State
	if err := json.Unmarshal([]byte(stJSON), &st); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if st.Phase != engine.PhaseHidden {
		t.Fatalf("expected PhaseHidden, got %s", st.Phase)
	}

	// ConfigJSON valid
	cfgJSON, err := sess.ConfigJSON()
	if err != nil || !strings.Contains(cfgJSON, `"right_option"`) {
		t.Fatalf("unexpected ConfigJSON: %v %s", err, cfgJSON)
	}

	// HandleEventJSON: open leader, press 'w', press 'h'
	resJSON, err := sess.HandleEventJSON(`{"type":"open_leader","context":{"appName":"Notes","selectedText":"clavis"}}`)
	if err != nil {
		t.Fatalf("HandleEventJSON open_leader failed: %v", err)
	}
	var res eventResult
	if err := json.Unmarshal([]byte(resJSON), &res); err != nil {
		t.Fatalf("unmarshal eventResult: %v", err)
	}
	if res.State.Phase != engine.PhaseLeader || len(res.Effects) != 1 || res.Effects[0].Type != engine.EffectPresentCapsule {
		t.Fatalf("unexpected open_leader result: %+v", res)
	}

	// FeedSnippetRune for ";sig"
	for _, ch := range ";si" {
		m, err := sess.FeedSnippetRune(string(ch), "com.apple.mail", "", "Mail")
		if err != nil || m != "" {
			t.Fatalf("unexpected early snippet match: (%q, %v)", m, err)
		}
	}
	matchJSON, err := sess.FeedSnippetRune("g", "com.apple.mail", "", "Mail")
	if err != nil || matchJSON == "" {
		t.Fatalf("expected snippet match for ;sig, got (%q, %v)", matchJSON, err)
	}
	var sm engine.SnippetMatch
	if err := json.Unmarshal([]byte(matchJSON), &sm); err != nil {
		t.Fatalf("unmarshal SnippetMatch: %v", err)
	}
	if sm.RenderedText != "Best regards," || sm.DeleteCount != 4 {
		t.Fatalf("unexpected snippet match: %+v", sm)
	}

	// NormalizeGlossary
	if got := sess.NormalizeGlossary("Use clavis daily."); got != "Use Clavis daily." {
		t.Fatalf("unexpected NormalizeGlossary: %q", got)
	}

	// SwitchAIProfile
	if err := sess.SwitchAIProfile("ollama"); err != nil {
		t.Fatalf("SwitchAIProfile(ollama) failed: %v", err)
	}
	cfgAfter, _ := sess.ConfigJSON()
	if !strings.Contains(cfgAfter, `"activeProfileId":"ollama"`) {
		t.Fatalf("expected activeProfileId ollama in ConfigJSON: %s", cfgAfter)
	}
	if err := sess.SwitchAIProfile("nonexistent"); err == nil {
		t.Fatal("expected error for unknown profile")
	}
	promptsJSON, err := sess.PromptsJSON()
	if err != nil || !strings.Contains(promptsJSON, "code-review") {
		t.Fatalf("unexpected PromptsJSON: %v %s", err, promptsJSON)
	}
	componentsJSON, err := sess.ComponentsJSON()
	if err != nil || !strings.Contains(componentsJSON, "ime_wpm") {
		t.Fatalf("unexpected ComponentsJSON: %v %s", err, componentsJSON)
	}
}

type mockStreamCallback struct {
	mu        sync.Mutex
	deltas    []string
	completed bool
	errMsg    string
	doneCh    chan struct{}
	once      sync.Once
}

func newMockStreamCallback() *mockStreamCallback {
	return &mockStreamCallback{doneCh: make(chan struct{})}
}

func (m *mockStreamCallback) OnDelta(chunk string) {
	m.mu.Lock()
	m.deltas = append(m.deltas, chunk)
	m.mu.Unlock()
}

func (m *mockStreamCallback) OnComplete() {
	m.mu.Lock()
	m.completed = true
	m.mu.Unlock()
	m.once.Do(func() { close(m.doneCh) })
}

func (m *mockStreamCallback) OnError(err string) {
	m.mu.Lock()
	m.errMsg = err
	m.mu.Unlock()
	m.once.Do(func() { close(m.doneCh) })
}

type mockHTTPDoer struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (m mockHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

func TestMobileStreamAIAndCancel(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	sess.setHTTPDoerForTest(mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Mobile\"}}]}\n\ndata: [DONE]\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(sse)),
			}, nil
		},
	})

	cb := newMockStreamCallback()
	token := sess.StreamAI("polish", "Polish", "hello mobile", `{"appName":"Notes"}`, "sk-test", cb)
	if token == nil {
		t.Fatal("expected non-nil CancelToken")
	}

	select {
	case <-cb.doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for StreamAI completion")
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if !cb.completed || cb.errMsg != "" || strings.Join(cb.deltas, "") != "Hello Mobile" {
		t.Fatalf("unexpected callback state: completed=%v err=%q deltas=%v", cb.completed, cb.errMsg, cb.deltas)
	}

	// Verify CancelToken cancels an active stream
	pr, pw := io.Pipe()
	sess.setHTTPDoerForTest(mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       pr,
			}, nil
		},
	})
	cbCancel := newMockStreamCallback()
	cancelTok := sess.StreamAI("ask", "Wait forever", "", "", "sk-test", cbCancel)
	cancelTok.Cancel()
	_ = pw.Close()

	select {
	case <-cbCancel.doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for cancelled StreamAI")
	}
	cbCancel.mu.Lock()
	defer cbCancel.mu.Unlock()
	if cbCancel.errMsg == "" {
		t.Fatalf("expected error on cancelled stream, got empty")
	}
}

// TestPureStdlibDependencies verifies via both `go list -deps` and `go/ast`
// that pkg/core/... and pkg/mobile/... import ZERO github.com/egoist/mygo,
// ZERO CGo ("C"), and ZERO third-party external dependencies.
func TestPureStdlibDependencies(t *testing.T) {
	// 1. Check `go list -deps`
	cmd := exec.Command("go", "list", "-deps", "../core/...", "../mobile/...")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps failed: %v\nstderr: %s", err, stderr.String())
	}

	for _, line := range strings.Split(stdout.String(), "\n") {
		dep := strings.TrimSpace(line)
		if dep == "" {
			continue
		}
		if dep == "C" {
			t.Fatalf("forbidden CGo dependency 'C' found in pkg/core or pkg/mobile")
		}
		if strings.Contains(dep, "github.com/egoist/mygo") {
			t.Fatalf("forbidden mygo dependency %q found in pkg/core or pkg/mobile", dep)
		}
		// Standard library packages do not contain a dot in the first path segment;
		// internal packages must start with github.com/yeisme/clavis/pkg/core or pkg/mobile.
		firstSeg := strings.SplitN(dep, "/", 2)[0]
		if strings.Contains(firstSeg, ".") {
			if !strings.HasPrefix(dep, "github.com/yeisme/clavis/pkg/core") &&
				!strings.HasPrefix(dep, "github.com/yeisme/clavis/pkg/mobile") {
				t.Fatalf("forbidden third-party dependency %q found in pkg/core or pkg/mobile", dep)
			}
		}
	}

	// 2. Parse non-test AST files under pkg/core and pkg/mobile to verify imports directly
	roots := []string{"../core", "."}
	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)
				if impPath == "C" {
					t.Fatalf("file %s imports CGo \"C\"", path)
				}
				if strings.Contains(impPath, "github.com/egoist/mygo") {
					t.Fatalf("file %s imports forbidden %q", path, impPath)
				}
				firstSeg := strings.SplitN(impPath, "/", 2)[0]
				if strings.Contains(firstSeg, ".") && !strings.HasPrefix(impPath, "github.com/yeisme/clavis/pkg/core") {
					t.Fatalf("file %s imports external package %q", path, impPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s failed: %v", root, err)
		}
	}
}

// TestMobileGomobileBindCompatibility verifies via AST that all exported declarations
// in pkg/mobile (non-test files) obey gomobile bind type constraints.
func TestMobileGomobileBindCompatibility(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("ParseDir failed: %v", err)
	}

	pkg, ok := pkgs["mobile"]
	if !ok {
		t.Fatal("mobile package not found")
	}

	exportedStructs := map[string]bool{}
	exportedInterfaces := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				if ts.TypeParams != nil && ts.TypeParams.NumFields() > 0 {
					t.Fatalf("gomobile forbids generic exported type %s", ts.Name.Name)
				}
				switch ts.Type.(type) {
				case *ast.StructType:
					exportedStructs[ts.Name.Name] = true
				case *ast.InterfaceType:
					exportedInterfaces[ts.Name.Name] = true
				default:
					t.Fatalf("exported type %s must be struct or interface for gomobile bind", ts.Name.Name)
				}
			}
		}
	}

	isValidGomobileType := func(expr ast.Expr, allowError bool, isParamOrResult bool) bool {
		switch typ := expr.(type) {
		case *ast.Ident:
			switch typ.Name {
			case "string", "bool", "int", "int8", "int16", "int32", "int64", "float32", "float64", "byte":
				return true
			case "error":
				return allowError
			default:
				return exportedInterfaces[typ.Name]
			}
		case *ast.StarExpr:
			if id, ok := typ.X.(*ast.Ident); ok {
				return exportedStructs[id.Name]
			}
			return false
		case *ast.ArrayType:
			if typ.Len != nil {
				return false // fixed arrays unsupported
			}
			if id, ok := typ.Elt.(*ast.Ident); ok && id.Name == "byte" {
				return true // []byte is supported
			}
			return false
		default:
			_ = isParamOrResult
			return false
		}
	}

	checkFuncType := func(name string, ft *ast.FuncType) {
		if ft.TypeParams != nil && ft.TypeParams.NumFields() > 0 {
			t.Fatalf("gomobile forbids generic function %s", name)
		}
		if ft.Params != nil {
			for _, p := range ft.Params.List {
				if !isValidGomobileType(p.Type, false, true) {
					t.Fatalf("exported func %s has gomobile-incompatible param type %T", name, p.Type)
				}
			}
		}
		if ft.Results != nil {
			var resTypes []ast.Expr
			for _, r := range ft.Results.List {
				count := len(r.Names)
				if count == 0 {
					count = 1
				}
				for i := 0; i < count; i++ {
					resTypes = append(resTypes, r.Type)
				}
			}
			if len(resTypes) > 2 {
				t.Fatalf("exported func %s returns %d values (gomobile max is 2)", name, len(resTypes))
			}
			if len(resTypes) == 2 {
				if id, ok := resTypes[1].(*ast.Ident); !ok || id.Name != "error" {
					t.Fatalf("exported func %s with 2 return values must have error as 2nd return", name)
				}
				if !isValidGomobileType(resTypes[0], false, true) {
					t.Fatalf("exported func %s has gomobile-incompatible 1st return type %T", name, resTypes[0])
				}
			} else if len(resTypes) == 1 {
				if !isValidGomobileType(resTypes[0], true, true) {
					t.Fatalf("exported func %s has gomobile-incompatible return type %T", name, resTypes[0])
				}
			}
		}
	}

	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv != nil && len(d.Recv.List) > 0 {
					// Check receiver is exported struct (or pointer to it)
					recvType := d.Recv.List[0].Type
					if star, ok := recvType.(*ast.StarExpr); ok {
						recvType = star.X
					}
					if id, ok := recvType.(*ast.Ident); !ok || !id.IsExported() {
						continue
					}
				}
				checkFuncType(d.Name.Name, d.Type)

			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					switch st := ts.Type.(type) {
					case *ast.StructType:
						if st.Fields != nil {
							for _, f := range st.Fields.List {
								for _, name := range f.Names {
									if name.IsExported() && !isValidGomobileType(f.Type, false, false) {
										t.Fatalf("exported struct %s field %s has gomobile-incompatible type %T", ts.Name.Name, name.Name, f.Type)
									}
								}
							}
						}
					case *ast.InterfaceType:
						if st.Methods != nil {
							for _, m := range st.Methods.List {
								if len(m.Names) > 0 && m.Names[0].IsExported() {
									ft, ok := m.Type.(*ast.FuncType)
									if !ok {
										t.Fatalf("exported interface %s method %s is not FuncType", ts.Name.Name, m.Names[0].Name)
									}
									checkFuncType(ts.Name.Name+"."+m.Names[0].Name, ft)
								}
							}
						}
					}
				}
			}
		}
	}
}
