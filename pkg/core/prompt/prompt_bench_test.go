package prompt

import (
	"github.com/yeisme/clavis/pkg/core/config"
	"testing"
)

func BenchmarkRenderPrompt(b *testing.B) {
	t := config.PromptTemplate{Template: "Act as {{role=architect}}. Solve {{problem}} in {{lang:Go|Rust|TypeScript}}. Context={{selection}} {{cursor}}"}
	ctx := RenderContext{Selection: "selected", Cursor: "|"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RenderPrompt(t, map[string]string{"problem": "queue"}, ctx)
	}
}
