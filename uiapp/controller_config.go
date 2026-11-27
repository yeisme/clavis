package uiapp

import "github.com/yeisme/clavis/pkg/core/config"

// SetConfig updates the controller's state machine configuration under c.mu,
// keeping config swaps synchronized with background Dispatch/Handle calls.
func (c *AppController) SetConfig(cfg config.ClavisConfig) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.StateMachine != nil {
		c.StateMachine.SetConfig(cfg)
	}
	c.mu.Unlock()
}
