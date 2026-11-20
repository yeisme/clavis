package uiapp

import (
	"sync"

	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// candidateWidthStep is the bucket the candidate panel's width grows in.
const candidateWidthStep = 32

// CandidatePanelSizer smooths the candidate panel's size across one
// composition. The exact content size changes on nearly every keystroke;
// resizing the window that often makes its edge twitch and, near the right
// screen edge, makes the clamp shift every candidate sideways under the
// user's eyes. Within a composition the sizer only grows (width in
// candidateWidthStep buckets, height for vertical pages), and forgets the
// size once the composition ends. Safe for concurrent use.
type CandidatePanelSizer struct {
	mu     sync.Mutex
	active bool
	w, h   int
}

// Next returns the panel size for v: never smaller than what this
// composition has shown so far, never larger than the orientation's cap.
// cfg's style only feeds parameters into PreferredCandidatePanelSizeFor; the
// steps and caps here are the same for every style.
func (s *CandidatePanelSizer) Next(v inputengine.View, cfg CandidatePanelConfig) (width, height int) {
	w, h := PreferredCandidatePanelSizeFor(v, cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !v.Composing {
		s.active, s.w, s.h = false, 0, 0
		return w, h
	}
	maxW := candidatePanelMaxWidth
	if isCandidateVertical(cfg.Appearance) {
		maxW = candVertMaxWidth
	}
	w = min(roundUpTo(w, candidateWidthStep), maxW)
	if s.active {
		w = max(w, s.w)
		h = max(h, s.h)
	}
	s.active, s.w, s.h = true, w, h
	return w, h
}

// Reset forgets the current composition's size (the panel was hidden).
func (s *CandidatePanelSizer) Reset() {
	s.mu.Lock()
	s.active, s.w, s.h = false, 0, 0
	s.mu.Unlock()
}

func roundUpTo(v, step int) int {
	if step <= 0 || v%step == 0 {
		return v
	}
	return (v/step + 1) * step
}
