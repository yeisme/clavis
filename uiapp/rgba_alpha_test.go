package uiapp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// evalConstFloat evaluates simple numeric constant AST expressions (literals,
// unary +/-, and binary +, -, *, /). Returns ok=false for non-constant exprs.
func evalConstFloat(expr ast.Expr) (float64, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return evalConstFloat(e.X)
	case *ast.BasicLit:
		if e.Kind != token.INT && e.Kind != token.FLOAT {
			return 0, false
		}
		v, err := strconv.ParseFloat(e.Value, 64)
		return v, err == nil
	case *ast.UnaryExpr:
		v, ok := evalConstFloat(e.X)
		if !ok {
			return 0, false
		}
		switch e.Op {
		case token.SUB:
			return -v, true
		case token.ADD:
			return v, true
		default:
			return 0, false
		}
	case *ast.BinaryExpr:
		lv, lok := evalConstFloat(e.X)
		rv, rok := evalConstFloat(e.Y)
		if !lok || !rok {
			return 0, false
		}
		switch e.Op {
		case token.ADD:
			return lv + rv, true
		case token.SUB:
			return lv - rv, true
		case token.MUL:
			return lv * rv, true
		case token.QUO:
			if rv == 0 {
				return 0, false
			}
			return lv / rv, true
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}

func checkAlphaExpr(expr ast.Expr) (bad bool, reason string) {
	if v, ok := evalConstFloat(expr); ok {
		if v < 0 || v > 1 {
			return true, "constant value outside [0, 1]"
		}
		return false, ""
	}
	divided, over, negative := false, false, false
	ast.Inspect(expr, func(m ast.Node) bool {
		switch e := m.(type) {
		case *ast.BinaryExpr:
			if e.Op == token.QUO {
				divided = true
			}
		case *ast.UnaryExpr:
			if e.Op == token.SUB {
				if v, ok := evalConstFloat(e.X); ok && v > 0 {
					negative = true
				}
			}
		case *ast.BasicLit:
			if v, err := strconv.ParseFloat(e.Value, 64); err == nil && v > 1 {
				over = true
			}
		}
		return true
	})
	if negative {
		return true, "negative literal"
	}
	if over && !divided {
		return true, "literal > 1 without division (write n/255)"
	}
	return false, ""
}

func scanColorAlphaViolations(filename string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Alpha" {
			return true
		}
		arg := call.Args[0]
		if bad, reason := checkAlphaExpr(arg); bad {
			snippet := string(src[fset.Position(arg.Pos()).Offset:fset.Position(arg.End()).Offset])
			violations = append(violations, fset.Position(call.Pos()).String()+": .Alpha("+snippet+") "+reason)
		}
		return true
	})
	return violations, nil
}

// TestRGBAAlphaIsUnitInterval guards a mistake that shipped once: ui.RGBA's
// alpha is 0..1, and passing a 0..255 byte (ui.RGBA(0, 0, 0, 65)) clamps to
// fully opaque, turning soft shadows into hard black ones. It flags any
// ui.RGBA whose alpha expression holds a numeric literal above 1 without a
// division (65.0/255 and (40+30*lift)/255 are fine).
func TestRGBAAlphaIsUnitInterval(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RGBA" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "ui" {
				return true
			}
			alpha := call.Args[3]
			if bad, reason := checkAlphaExpr(alpha); bad {
				t.Errorf("%s: ui.RGBA alpha %s is invalid (%s); the API takes 0..1 (write n/255)",
					fset.Position(call.Pos()), src[fset.Position(alpha.Pos()).Offset:fset.Position(alpha.End()).Offset], reason)
			}
			return true
		})
	}
}

// TestColorAlphaIsUnitInterval ensures every .Alpha(...) call across uiapp/*.go
// uses a 0..1 opacity instead of a 0..255 byte or negative literal.
func TestColorAlphaIsUnitInterval(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		violations, err := scanColorAlphaViolations(name, src)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, v := range violations {
			t.Error(v)
		}
	}
}

func TestColorAlphaGuardCatchesOutOfRangeLiterals(t *testing.T) {
	badSrc := `package uiapp
func demo(c color) {
	_ = c.Alpha(210)
	_ = c.Alpha(1.5)
	_ = c.Alpha(-0.2)
	_ = c.Alpha(300.0 / 255.0)
}
`
	violations, err := scanColorAlphaViolations("bad.go", []byte(badSrc))
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %v", len(violations), violations)
	}

	goodSrc := `package uiapp
func demo(c color, x float32) {
	_ = c.Alpha(0)
	_ = c.Alpha(1)
	_ = c.Alpha(0.45)
	_ = c.Alpha(210.0 / 255.0)
	_ = c.Alpha(x * 0.13)
}
`
	violations, err = scanColorAlphaViolations("good.go", []byte(goodSrc))
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for valid alpha calls, got %v", violations)
	}
}

func TestShaderEpochAvoidsFloat32Freeze(t *testing.T) {
	if shaderEpoch.IsZero() {
		t.Fatal("shaderEpoch must be initialized")
	}
	t0 := float32(shaderEpoch.Add(100 * time.Millisecond).Sub(shaderEpoch).Seconds())
	t1 := float32(shaderEpoch.Add(116 * time.Millisecond).Sub(shaderEpoch).Seconds())
	if t1 <= t0 {
		t.Fatalf("relative shader Time did not advance across 16ms frame: t0=%v t1=%v", t0, t1)
	}
}
