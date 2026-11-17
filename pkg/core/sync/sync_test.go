package sync

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanListsExplicitBoundaries(t *testing.T) {
	p := DefaultSyncPlan()
	items := p.Items()
	if len(items) != 8 || items[5].Class != SensitiveVault || !items[5].Sensitive {
		t.Fatalf("unexpected consent items: %+v", items)
	}
	if items[6].Allowed || items[7].Allowed {
		t.Fatal("password and key-manager data must never be allowed")
	}
	p.Passwords = true
	if p.Validate() == nil {
		t.Fatal("unsafe plan accepted")
	}
}

func TestSignedDeleteAndOldDeviceSyncBeforeUpload(t *testing.T) {
	s := NewServer()
	one, err := NewClient(s, "device-one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := NewClient(s, "device-two")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.RegisterAccount("account")
	if err != nil {
		t.Fatal(err)
	}
	if err := one.LoginWithCredential("account", credential); err != nil {
		t.Fatal(err)
	}
	if err := one.PairDevice(); err != nil {
		t.Fatal(err)
	}
	if err := two.LoginWithCredential("account", credential); err != nil {
		t.Fatal(err)
	}
	if err := two.PairDevice(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*Client{one, two} {
		code, err := c.PrepareRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.ConfirmRecoveryCode(code); err != nil {
			t.Fatal(err)
		}
	}
	if err := one.Upload("obj", 1, []byte("opaque")); err != nil {
		t.Fatal(err)
	}
	one.Cache().PutCleartext("obj", []byte("clear"))
	one.Cache().PutCiphertext("obj", []byte("cipher"))
	one.Cache().PutMedia("obj", []byte("media"))
	one.Cache().PutBackup("old-backup", []string{"obj"}, []byte("backup"))
	one.Cache().PutExport("old-export", []string{"obj"}, []byte("export"))
	if err := one.Delete("obj"); err != nil {
		t.Fatal(err)
	}
	if err := two.server.Upload("account", "device-two", "new", 1, []byte("x")); err == nil {
		t.Fatal("old device uploaded before receiving tombstone")
	}
	two.Cache().PutCleartext("obj", []byte("clear"))
	two.Cache().PutCiphertext("obj", []byte("cipher"))
	two.Cache().PutMedia("obj", []byte("media"))
	two.Cache().PutBackup("old-backup", []string{"obj"}, []byte("backup"))
	two.Cache().PutExport("old-export", []string{"obj"}, []byte("export"))
	ts, report, err := two.Sync()
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 1 {
		t.Fatalf("unexpected tombstone sync: %+v", ts)
	}
	if report.Cleartext != 1 || report.Ciphertext != 1 || report.Media != 1 || report.Backups != 1 || report.Exports != 1 {
		t.Fatalf("tombstone did not clean all local copies: %+v", report)
	}
	if err := two.Upload("new", 2, []byte("new")); err != nil {
		t.Fatalf("upload after sync: %v", err)
	}
	for _, event := range s.AuditForAccount("account") {
		if event.ObjectDigest == "" || event.DeviceID != "device-one" || event.Action != "delete" {
			t.Fatalf("unsafe audit event: %+v", event)
		}
	}
}

func TestPlanAndVaultUploadBoundaries(t *testing.T) {
	s := NewServer()
	c, err := NewClient(s, "device")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("account"); err != nil {
		t.Fatal(err)
	}
	if err := c.PairDevice(); err != nil {
		t.Fatal(err)
	}
	code, err := c.PrepareRecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmRecoveryCode(code); err != nil {
		t.Fatal(err)
	}
	if err := c.UploadClass(Passwords, "password", 1, []byte("secret")); err == nil {
		t.Fatal("password upload accepted")
	}
	plan := DefaultSyncPlan()
	plan.SensitiveVault = true
	if err := c.ConfigurePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := c.UploadClass(SensitiveVault, "secret", 1, []byte("cipher")); err == nil {
		t.Fatal("unauthorized vault upload accepted")
	}
	if err := c.Vault().ConfigureAuthorization("approval"); err != nil {
		t.Fatal(err)
	}
	if err := c.Vault().Authorize("approval"); err != nil {
		t.Fatal(err)
	}
	if err := c.UploadClass(SensitiveVault, "secret", 1, []byte("cipher")); err != nil {
		t.Fatal(err)
	}
	if err := c.Vault().Put("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Vault().Ciphertext("secret"); err == nil {
		t.Fatal("vault ciphertext survived tombstone")
	}
}

func TestVaultRequiresIndependentAuthorization(t *testing.T) {
	v := NewVault("approval")
	if err := v.Put("secret", []byte("value")); err == nil {
		t.Fatal("unauthorized vault write succeeded")
	}
	if _, err := v.Ciphertext("secret"); err == nil {
		t.Fatal("unauthorized vault ciphertext read succeeded")
	}
	if err := v.Authorize("wrong"); err == nil {
		t.Fatal("wrong authorization accepted")
	}
	if err := v.Authorize("approval"); err != nil {
		t.Fatal(err)
	}
	if err := v.Put("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("secret")
	if err != nil || string(got) != "value" {
		t.Fatalf("vault get = %q, %v", got, err)
	}
	if err := v.Revoke(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get("secret"); err == nil {
		t.Fatal("revoked vault read succeeded")
	}
}

func TestExistingAccountLoginRequiresExplicitCredential(t *testing.T) {
	s := NewServer()
	first, err := NewClient(s, "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Login("acct"); err != nil {
		t.Fatal(err)
	}
	second, err := NewClient(s, "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Login("acct"); err == nil {
		t.Fatal("existing account Login recovered a server credential")
	}
	credential, err := s.RegisterAccount("new-acct")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.LoginWithCredential("acct", credential); err == nil {
		t.Fatal("credential for another account was accepted")
	}
	// The credential returned at first-account registration is the explicit
	// secret needed by another device; the Server never exposes it later.
	if err := second.LoginWithCredential("acct", first.credential); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteInstructionSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	d := DeleteInstruction{AccountID: "a", ObjectID: "o", DeviceID: "d", Version: 1, ProtocolVersion: ProtocolVersion}
	d.Signature = ed25519.Sign(priv, d.canonical())
	if !d.Verify(pub) {
		t.Fatal("valid signature rejected")
	}
	d.ObjectID = "other"
	if d.Verify(pub) {
		t.Fatal("tampered instruction accepted")
	}
}

func setupClients(t *testing.T) (*Server, *Client, *Client) {
	t.Helper()
	s := NewServer()
	a, err := NewClient(s, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(s, "b")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.LoginWithCredential("acct", credential); err != nil {
		t.Fatal(err)
	}
	if err := a.PairDevice(); err != nil {
		t.Fatal(err)
	}
	if err := b.LoginWithCredential("acct", credential); err != nil {
		t.Fatal(err)
	}
	if err := b.PairDevice(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*Client{a, b} {
		code, err := c.PrepareRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.ConfirmRecoveryCode(code); err != nil {
			t.Fatal(err)
		}
	}
	return s, a, b
}

func TestDeleteIsPermanentAtAnyHigherVersion(t *testing.T) {
	_, a, b := setupClients(t)
	if err := a.Upload("object", 1, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := b.Upload("object", 2, []byte("resurrection")); err == nil {
		t.Fatal("deleted object resurrected")
	}
}

func TestClientSyncVerifiesHistoricalTombstonesForRepeatedObjectDelete(t *testing.T) {
	s, a, b := setupClients(t)
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	first, err := s.SyncTombstones("acct", "b", 0)
	if err != nil || len(first) != 1 {
		t.Fatalf("first issued batch = %+v, %v", first, err)
	}
	firstTombstone := first[0]
	if !s.VerifyTombstone(firstTombstone) {
		t.Fatal("first tombstone did not verify before replacement")
	}

	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	if s.VerifyTombstone(firstTombstone) == false {
		t.Fatal("historical tombstone rejected after newer object delete")
	}
	if _, _, err := b.Sync(); err != nil {
		t.Fatalf("client failed to sync historical tombstone: %v", err)
	}
	if got := b.Watermark(); got != firstTombstone.Sequence {
		t.Fatalf("watermark after historical batch = %d, want %d", got, firstTombstone.Sequence)
	}

	second, err := s.SyncTombstones("acct", "b", firstTombstone.Sequence)
	if err != nil || len(second) != 1 {
		t.Fatalf("second issued batch = %+v, %v", second, err)
	}
	if second[0].ObjectID != firstTombstone.ObjectID || second[0].Sequence <= firstTombstone.Sequence {
		t.Fatalf("second tombstone = %+v, want newer delete for same object", second[0])
	}
	if !s.VerifyTombstone(second[0]) {
		t.Fatal("new tombstone did not verify")
	}
	if _, _, err := b.Sync(); err != nil {
		t.Fatalf("client failed to sync new tombstone: %v", err)
	}
	if got := b.Watermark(); got != second[0].Sequence {
		t.Fatalf("watermark after new batch = %d, want %d", got, second[0].Sequence)
	}
	if s.VerifyTombstone(firstTombstone) == false {
		t.Fatal("historical tombstone disappeared after confirmation")
	}
}

func TestStaleDeleteStillPurgesLocalCopies(t *testing.T) {
	_, a, b := setupClients(t)
	b.Cache().PutCleartext("object", []byte("clear"))
	b.Cache().PutCiphertext("object", []byte("cipher"))
	b.Cache().PutMedia("object", []byte("media"))
	b.Cache().PutBackup("backup", []string{"object"}, []byte("backup"))
	b.Cache().PutExport("export", []string{"object"}, []byte("export"))
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete("object"); err == nil {
		t.Fatal("stale delete unexpectedly succeeded")
	}
	if len(b.Cache().Cleartext) != 0 || len(b.Cache().Ciphertext) != 0 || len(b.Cache().Media) != 0 || len(b.Cache().Backups) != 0 || len(b.Cache().Exports) != 0 {
		t.Fatal("stale delete did not purge local copies")
	}
}

func TestCursorCannotSkipPendingTombstone(t *testing.T) {
	s, a, b := setupClients(t)
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	got, err := s.SyncTombstones("acct", "b", ^uint64(0))
	if err != nil || len(got) != 1 {
		t.Fatalf("cursor bypass returned %+v, %v", got, err)
	}
	if err := s.Upload("acct", "b", "new", 1, []byte("plain")); err == nil {
		t.Fatal("plain upload or unconfirmed tombstone accepted")
	}
	if _, _, err := b.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := b.Upload("new", 1, []byte("new")); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmZeroCannotSkipPendingTombstone(t *testing.T) {
	s, a, b := setupClients(t)
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmTombstones("acct", "b", 0); err == nil {
		t.Fatal("zero confirmation skipped a pending tombstone")
	}
	if _, _, err := b.Sync(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSyncIsSerialized(t *testing.T) {
	s, a, b := setupClients(t)
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _, err := b.Sync()
			results <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent sync failed: %v", err)
		}
	}
	if b.Watermark() != s.LatestSequence() {
		t.Fatalf("watermark = %d, want %d", b.Watermark(), s.LatestSequence())
	}
}

func TestConfirmRequiresIssuedTombstoneBatch(t *testing.T) {
	s, a, b := setupClients(t)
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	latest := s.LatestSequence()
	if err := s.ConfirmTombstones("acct", "b", latest); err == nil {
		t.Fatal("confirmation without SyncTombstones succeeded")
	}
	if err := b.Upload("new", 1, []byte("new")); err == nil {
		t.Fatal("upload succeeded after forged confirmation")
	}
	if _, _, err := b.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := b.Upload("new", 1, []byte("new")); err != nil {
		t.Fatalf("upload after normal tombstone sync failed: %v", err)
	}
}

func TestIssuedTombstoneBatchSurvivesNewDeleteAcrossServerStores(t *testing.T) {
	server := NewServer()
	first, err := NewServerStore(server)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewServerStore(server)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := first.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	deleteTombstone := func(object string) Tombstone {
		t.Helper()
		instruction := DeleteInstruction{AccountID: "acct", ObjectID: object, DeviceID: "device", Version: 1, ProtocolVersion: ProtocolVersion}
		instruction.Signature = ed25519.Sign(priv, instruction.canonical())
		tombstone, err := first.SubmitDelete(instruction)
		if err != nil {
			t.Fatal(err)
		}
		return tombstone
	}
	firstTombstone := deleteTombstone("first")
	firstBatch, err := first.SyncTombstones("acct", "device", 0)
	if err != nil || len(firstBatch) != 1 || firstBatch[0].Sequence != firstTombstone.Sequence {
		t.Fatalf("first batch = %+v, %v", firstBatch, err)
	}
	secondTombstone := deleteTombstone("second")
	retriedBatch, err := second.SyncTombstones("acct", "device", 0)
	if err != nil || len(retriedBatch) != 1 || retriedBatch[0].Sequence != firstTombstone.Sequence {
		t.Fatalf("pending batch was replaced: %+v, %v", retriedBatch, err)
	}
	if err := second.ConfirmTombstones("acct", "device", firstTombstone.Sequence); err != nil {
		t.Fatalf("confirm old batch: %v", err)
	}
	watermark, err := first.Watermark("acct", "device")
	if err != nil || watermark != firstTombstone.Sequence {
		t.Fatalf("watermark after old confirmation = %d, %v", watermark, err)
	}
	nextBatch, err := first.SyncTombstones("acct", "device", 0)
	if err != nil || len(nextBatch) != 1 || nextBatch[0].Sequence != secondTombstone.Sequence {
		t.Fatalf("next batch = %+v, %v", nextBatch, err)
	}
	if err := second.ConfirmTombstones("acct", "device", secondTombstone.Sequence); err != nil {
		t.Fatalf("confirm next batch: %v", err)
	}
}

func TestRepeatedTombstoneConfirmationIsRejected(t *testing.T) {
	s, a, b := setupClients(t)
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	got, err := s.SyncTombstones("acct", "b", 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("issued batch = %+v, %v", got, err)
	}
	sequence := got[0].Sequence
	if err := s.ConfirmTombstones("acct", "b", sequence); err != nil {
		t.Fatalf("first confirmation failed: %v", err)
	}
	if err := s.ConfirmTombstones("acct", "b", sequence); err == nil {
		t.Fatal("repeated confirmation of an already consumed batch succeeded")
	}
	if err := s.ConfirmTombstones("acct", "b", sequence+1); err == nil {
		t.Fatal("confirmation beyond the issued batch succeeded")
	}
	if err := b.Upload("new", 1, []byte("new")); err != nil {
		t.Fatalf("upload after the first confirmation failed: %v", err)
	}
	if err := s.ConfirmTombstones("acct", "b", 0); err == nil {
		t.Fatal("confirmation moved backwards")
	}
}

func TestPairCannotOverwriteKeyAndCredentialRequired(t *testing.T) {
	s := NewServer()
	credential, err := s.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if err := s.PairDeviceWithCredential("acct", "dev", key.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if err := s.PairDeviceWithCredential("acct", "dev", other.Public().(ed25519.PublicKey), credential); err == nil {
		t.Fatal("device key overwritten")
	}
	if err := s.PairDeviceWithCredential("acct", "other", other.Public().(ed25519.PublicKey), []byte("wrong")); err == nil {
		t.Fatal("invalid credential paired device")
	}
}

func TestEnvelopeAADUsesLengthPrefixedFields(t *testing.T) {
	base := EncryptedEnvelope{ProtocolVersion: ProtocolVersion, AccountID: "acct", DeviceID: "device", ObjectID: "object", Version: 1, Class: InputMethod}
	if got, want := string(base.aad()), string(base.aad()); got != want {
		t.Fatalf("AAD is not stable: %q != %q", got, want)
	}
	ambiguous := base
	ambiguous.AccountID = "acct:device"
	ambiguous.DeviceID = "object"
	other := base
	other.AccountID = "acct"
	other.DeviceID = "device:object"
	if string(ambiguous.aad()) == string(other.aad()) {
		t.Fatal("AAD aliases fields containing a colon")
	}
}

func TestEnvelopeSignatureRequired(t *testing.T) {
	s, a, _ := setupClients(t)
	e, err := clientEnvelope(a.privateKey, "acct", "a", "object", 1, InputMethod, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	e.Signature = nil
	if err := s.UploadEnvelope("acct", "a", "object", 1, e); err == nil {
		t.Fatal("unsigned envelope accepted")
	}
	e.Signature = make([]byte, ed25519.SignatureSize)
	if err := s.UploadEnvelope("acct", "a", "object", 1, e); err == nil {
		t.Fatal("random envelope signature accepted")
	}
}

func TestEnvelopeRejectsPlaintextAndMismatches(t *testing.T) {
	s, a, _ := setupClients(t)
	if err := s.Upload("acct", "a", "object", 1, []byte("plaintext")); err == nil {
		t.Fatal("plaintext accepted")
	}
	e, err := clientEnvelope(a.privateKey, "acct", "a", "object", 1, InputMethod, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UploadEnvelope("acct", "a", "other", 1, e); err == nil {
		t.Fatal("mismatched envelope accepted")
	}
	e.Class = Passwords
	if err := s.UploadEnvelope("acct", "a", "object", 1, e); err == nil {
		t.Fatal("unsafe envelope class accepted")
	}
	e.Class = InputMethod
	e.ProtocolVersion++
	if err := s.UploadEnvelope("acct", "a", "object", 1, e); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
}

func TestVaultAuthorizationChangesKeyAndRevokeRotates(t *testing.T) {
	v, err := NewVaultWithError("one")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Authorize("one"); err != nil {
		t.Fatal(err)
	}
	if err := v.Put("id", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	old, err := v.Ciphertext("id")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Revoke(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Ciphertext("id"); err == nil {
		t.Fatal("revoked vault ciphertext read succeeded")
	}
	if err := v.ConfigureAuthorization("two"); err != nil {
		t.Fatal(err)
	}
	if err := v.Authorize("two"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get("id"); err == nil {
		t.Fatal("old ciphertext decrypted after authorization change")
	}
	if err := v.Put("id", []byte("new")); err != nil {
		t.Fatal(err)
	}
	newer, _ := v.Ciphertext("id")
	if string(old) == string(newer) {
		t.Fatal("ciphertext key did not change")
	}
	if err := v.Delete("id"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Ciphertext("id"); err == nil {
		t.Fatal("delete did not remove vault object")
	}
}

func TestBaselineOverlayAndAuditDoNotExposeSecrets(t *testing.T) {
	p := DefaultSyncPlan()
	b, err := NewPersonalBaseline("acct", p)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "value") {
		t.Fatal("baseline leaked values")
	}
	o, err := NewDeviceOverlay("dev", map[string]any{"secret": "value"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(o)
	if strings.Contains(string(raw), "value") {
		t.Fatal("overlay leaked values")
	}
	s, a, _ := setupClients(t)
	if err := a.Delete("object"); err != nil {
		t.Fatal(err)
	}
	events := s.Audit("acct")
	if len(events) != 1 || strings.Contains(string(mustJSON(events[0])), `"objectId"`) {
		t.Fatal("audit exposed object id")
	}
	other, err := NewClient(s, "other-device")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Login("other-account"); err != nil {
		t.Fatal(err)
	}
	if err := other.PairDevice(); err != nil {
		t.Fatal(err)
	}
	if err := other.Delete("other-object"); err != nil {
		t.Fatal(err)
	}
	if len(s.Audit("acct")) != 1 || len(s.Audit("other-account")) != 1 {
		t.Fatal("audit was not isolated by account")
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestDeleteRoundTripAndTombstoneValidation(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	d := DeleteInstruction{AccountID: "a", ObjectID: "o", DeviceID: "d", Version: 1, ProtocolVersion: ProtocolVersion}
	d.Signature = ed25519.Sign(priv, d.canonical())
	raw, err := MarshalDeleteInstruction(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseDeleteInstruction(raw)
	if err != nil || !got.Verify(priv.Public().(ed25519.PublicKey)) {
		t.Fatalf("delete round trip failed: %v", err)
	}
	got.ObjectID = "other"
	if got.Verify(priv.Public().(ed25519.PublicKey)) {
		t.Fatal("scope tampering accepted")
	}
}
