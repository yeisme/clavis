package mobile

import "github.com/yeisme/clavis/pkg/core/ai"

func (s *Session) setHTTPDoerForTest(doer ai.HTTPDoer) {
	s.mu.Lock()
	s.httpDoer = doer
	s.mu.Unlock()
}
