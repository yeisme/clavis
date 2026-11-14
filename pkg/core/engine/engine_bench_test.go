package engine

import (
	"github.com/yeisme/clavis/pkg/core/config"
	"testing"
)

func BenchmarkOmniboxQueryCached(b *testing.B) {
	m := NewStateMachine(config.DefaultConfig())
	m.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.Handle(Event{Type: EventUpdateQuery, Query: "review"})
	}
}
