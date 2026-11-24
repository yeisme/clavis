package uiapp

import (
	"context"
	"time"

	"github.com/yeisme/clavis/pkg/core/annotate"
	"github.com/yeisme/clavis/pkg/core/config"
)

// controllerSubStates owns all per-controller UI and domain sub-states that
// previously lived in package-global map[*AppController]... / sync.Map side
// tables. Every field is lazily initialized under AppController.subStateMu so
// zero-value &AppController{} instances constructed in unit tests work safely
// without panicking, and Close() deterministically releases timers, bitmaps,
// and cached state.
type controllerSubStates struct {
	hubSurface       CapsuleHubSurface
	assetsSub        assetsSubTab
	manageSync       *manageSyncState
	vaultUI          *vaultViewState
	clipKeyMgr       *clipboardKeyManagerState
	capsuleUI        *capsuleUIState
	composer         *composerState
	settingsEditor   *settingsEditorState
	aiProfilePanel   *aiProfilePanelState
	annotate         *annotateControllerState
	ocrTranslate     *ocrTranslateControllerState
	mediaSearch      *mediaSearchControllerState
	agentManage      *agentManageUIState
	agentSessions    *agentSessionsSubState
	syncStatus       *syncStatusCacheEntry
	inputEngineUI    *inputEngineUIState
	clipboardTTLSync *clipboardTTLSyncState
	promptContract   *promptContractState
	templatesUI      *templatesUIState
}

// lifeCtx returns a context bound to the controller's lifetime. It is canceled
// when Close() runs. Safe to call on nil or zero-value &AppController{}.
func (c *AppController) lifeCtx() context.Context {
	if c == nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lifeContext == nil {
		c.lifeContext, c.lifeCancel = context.WithCancel(context.Background())
		if c.closed {
			c.lifeCancel()
		}
	}
	return c.lifeContext
}

// cancelLifeCtx cancels the controller lifetime context and marks the
// controller closed under c.mu.
func (c *AppController) cancelLifeCtx() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	if c.lifeContext == nil {
		c.lifeContext, c.lifeCancel = context.WithCancel(context.Background())
	}
	cancel := c.lifeCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *AppController) isClosed() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	return closed
}

func (c *AppController) hubSurfaceState() CapsuleHubSurface {
	if c == nil {
		return HubSurfaceNone
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	return c.subStates.hubSurface
}

func (c *AppController) setHubSurfaceState(surface CapsuleHubSurface) {
	if c == nil {
		return
	}
	c.subStateMu.Lock()
	c.subStates.hubSurface = surface
	c.subStateMu.Unlock()
}

func (c *AppController) toggleHubSurfaceState(surface CapsuleHubSurface) CapsuleHubSurface {
	if c == nil {
		return HubSurfaceNone
	}
	c.subStateMu.Lock()
	next := surface
	if c.subStates.hubSurface == surface {
		next = HubSurfaceNone
	}
	c.subStates.hubSurface = next
	c.subStateMu.Unlock()
	return next
}

func (c *AppController) assetsSubTabState() assetsSubTab {
	if c == nil {
		return assetsSubMedia
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	return c.subStates.assetsSub
}

func (c *AppController) setAssetsSubTabState(tab assetsSubTab) {
	if c == nil {
		return
	}
	c.subStateMu.Lock()
	c.subStates.assetsSub = tab
	c.subStateMu.Unlock()
}

func (c *AppController) manageSyncStateFor() *manageSyncState {
	if c == nil {
		return &manageSyncState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.manageSync == nil {
		c.subStates.manageSync = &manageSyncState{}
	}
	return c.subStates.manageSync
}

func (c *AppController) vaultUIStateFor() *vaultViewState {
	if c == nil {
		return &vaultViewState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.vaultUI == nil {
		c.subStates.vaultUI = &vaultViewState{}
	}
	return c.subStates.vaultUI
}

func (c *AppController) clipboardKeyManagerStateFor() *clipboardKeyManagerState {
	if c == nil {
		return &clipboardKeyManagerState{
			policy:             config.DefaultClipboardKeyManagerPolicy(),
			recentSecretHashes: make(map[string]time.Time),
		}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.clipKeyMgr == nil {
		c.subStates.clipKeyMgr = &clipboardKeyManagerState{
			policy:             config.DefaultClipboardKeyManagerPolicy(),
			recentSecretHashes: make(map[string]time.Time),
		}
	}
	return c.subStates.clipKeyMgr
}

func (c *AppController) capsuleUIStateFor() *capsuleUIState {
	if c == nil {
		return &capsuleUIState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.capsuleUI == nil {
		c.subStates.capsuleUI = &capsuleUIState{}
	}
	return c.subStates.capsuleUI
}

func (c *AppController) composerStateFor() *composerState {
	if c == nil {
		return &composerState{historyIdx: -1}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.composer == nil {
		c.subStates.composer = &composerState{historyIdx: -1}
	}
	return c.subStates.composer
}

func (c *AppController) settingsEditorStateFor() *settingsEditorState {
	if c == nil {
		return &settingsEditorState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.settingsEditor == nil {
		c.subStates.settingsEditor = &settingsEditorState{}
	}
	return c.subStates.settingsEditor
}

func (c *AppController) aiProfilePanelStateFor() *aiProfilePanelState {
	if c == nil {
		return &aiProfilePanelState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.aiProfilePanel == nil {
		c.subStates.aiProfilePanel = &aiProfilePanelState{}
	}
	return c.subStates.aiProfilePanel
}

func (c *AppController) annotateStateForCtrl() *annotateControllerState {
	if c == nil {
		return &annotateControllerState{
			tool:        annotate.ToolRect,
			strokeWidth: 4,
		}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.annotate == nil {
		c.subStates.annotate = &annotateControllerState{
			tool:        annotate.ToolRect,
			strokeWidth: 4,
		}
	}
	return c.subStates.annotate
}

func (c *AppController) ocrTranslateStateForCtrl() *ocrTranslateControllerState {
	if c == nil {
		return &ocrTranslateControllerState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.ocrTranslate == nil {
		c.subStates.ocrTranslate = &ocrTranslateControllerState{}
	}
	return c.subStates.ocrTranslate
}

func (c *AppController) mediaSearchStateFor() *mediaSearchControllerState {
	if c == nil {
		return &mediaSearchControllerState{
			category: MediaCategoryAll,
			probes:   make(map[string]MediaProbeSnapshot),
			inFlight: make(map[string]bool),
		}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.mediaSearch == nil {
		c.subStates.mediaSearch = &mediaSearchControllerState{
			category: MediaCategoryAll,
			probes:   make(map[string]MediaProbeSnapshot),
			inFlight: make(map[string]bool),
		}
	}
	return c.subStates.mediaSearch
}

func (c *AppController) agentManageStateForCtrl() *agentManageUIState {
	if c == nil {
		return &agentManageUIState{
			startCLI: "codex",
			watching: make(map[string]bool),
			sessions: make(map[string]*agentSessionControlState),
		}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.agentManage == nil {
		c.subStates.agentManage = &agentManageUIState{
			startCLI: "codex",
			watching: make(map[string]bool),
			sessions: make(map[string]*agentSessionControlState),
		}
	}
	return c.subStates.agentManage
}

func (c *AppController) agentSessionsStateFor() *agentSessionsSubState {
	if c == nil {
		return &agentSessionsSubState{
			outputs:  make(map[string]agentOutputSnapshot),
			inFlight: make(map[string]bool),
		}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.agentSessions == nil {
		c.subStates.agentSessions = &agentSessionsSubState{
			outputs:  make(map[string]agentOutputSnapshot),
			inFlight: make(map[string]bool),
		}
	}
	return c.subStates.agentSessions
}

func (c *AppController) syncStatusCacheStateFor() *syncStatusCacheEntry {
	if c == nil {
		return &syncStatusCacheEntry{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.syncStatus == nil {
		c.subStates.syncStatus = &syncStatusCacheEntry{}
	}
	return c.subStates.syncStatus
}

func (c *AppController) inputEngineUIStateFor() *inputEngineUIState {
	if c == nil {
		return &inputEngineUIState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.inputEngineUI == nil {
		c.subStates.inputEngineUI = &inputEngineUIState{}
	}
	return c.subStates.inputEngineUI
}

func (c *AppController) promptContractStateFor() *promptContractState {
	if c == nil {
		return &promptContractState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.promptContract == nil {
		c.subStates.promptContract = &promptContractState{}
	}
	return c.subStates.promptContract
}

func (c *AppController) templatesUIStateFor() *templatesUIState {
	if c == nil {
		return &templatesUIState{}
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.templatesUI == nil {
		c.subStates.templatesUI = &templatesUIState{}
	}
	return c.subStates.templatesUI
}

// withClipboardTTLSync provides synchronized lazy access to the per-controller
// clipboardTTLSyncState (staged for settings_clipboard.go migration).
func (c *AppController) withClipboardTTLSync(fn func(*clipboardTTLSyncState)) {
	if c == nil || fn == nil {
		return
	}
	c.subStateMu.Lock()
	defer c.subStateMu.Unlock()
	if c.subStates.clipboardTTLSync == nil {
		c.subStates.clipboardTTLSync = &clipboardTTLSyncState{}
	}
	fn(c.subStates.clipboardTTLSync)
}

// closeSubStates stops all timers, releases cached bitmaps/buffers, and nils
// out owned per-controller sub-states during AppController.Close().
func (c *AppController) closeSubStates() {
	if c == nil {
		return
	}
	c.subStateMu.Lock()
	subs := c.subStates
	c.subStates = controllerSubStates{}
	c.subStateMu.Unlock()

	if st := subs.clipKeyMgr; st != nil {
		st.mu.Lock()
		if st.sensitiveTimer != nil {
			st.sensitiveTimer.Stop()
			st.sensitiveTimer = nil
		}
		st.sensitiveGen++
		st.sensitiveActive = false
		st.sensitiveHash = ""
		st.sensitiveSeq = 0
		st.sensitiveHasSeq = false
		st.sensitiveLabel = ""
		st.sensitiveExpiresAt = time.Time{}
		st.sensitiveVaultToken = ""
		vaultClean := st.sensitiveVaultClean
		st.sensitiveVaultClean = nil
		vc := st.vaultClipboard
		st.vaultClipboard = nil
		clear(st.recentSecretHashes)
		st.mu.Unlock()

		// Invalidate clipboard ownership before calling vaultClean so Vault
		// stops its internal TTL timer without overwriting the user's clipboard.
		if vc != nil {
			vc.invalidate()
		}
		if vaultClean != nil {
			_ = vaultClean()
		}
	}

	if st := subs.annotate; st != nil {
		st.mu.Lock()
		st.open = false
		st.dragging = false
		st.textEditing = false
		st.textDraft = ""
		st.doc = nil
		st.cachedBmp = nil
		st.mu.Unlock()
	}

	if st := subs.ocrTranslate; st != nil {
		st.mu.Lock()
		st.pendingImgs = nil
		st.lastOCRImg = nil
		st.mu.Unlock()
	}

	if st := subs.mediaSearch; st != nil {
		st.mu.Lock()
		st.customItems = nil
		clear(st.probes)
		clear(st.inFlight)
		st.mu.Unlock()
	}

	if st := subs.agentManage; st != nil {
		st.mu.Lock()
		clear(st.watching)
		clear(st.sessions)
		st.mu.Unlock()
	}

	if st := subs.agentSessions; st != nil {
		st.mu.Lock()
		clear(st.outputs)
		clear(st.inFlight)
		st.mu.Unlock()
	}

	if st := subs.capsuleUI; st != nil {
		st.mu.Lock()
		st.rowMetaCache = nil
		st.previewCache = nil
		st.mu.Unlock()
	}

	if st := subs.vaultUI; st != nil {
		st.mu.Lock()
		st.valueDraft = ""
		st.recordDraft = ""
		st.labelDraft = ""
		st.mu.Unlock()
	}

	if st := subs.promptContract; st != nil {
		st.mu.Lock()
		st.active = false
		st.reviewPending = false
		st.reviewPromptHash = ""
		st.diagnostics = nil
		st.aiRunStatus = ""
		st.aiRunError = ""
		st.mu.Unlock()
	}

	if st := subs.templatesUI; st != nil {
		st.mu.Lock()
		st.catalogSnap = nil
		st.importedItems = nil
		st.diagnostics = nil
		st.overlayDraft = ""
		st.mu.Unlock()
	}
}
