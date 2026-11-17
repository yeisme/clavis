package sync

import (
	"crypto/ed25519"
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
	for _, c := range []*Client{one, two} {
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
	for _, event := range s.Audit() {
		if event.ObjectID != "obj" || event.DeviceID != "device-one" || event.Action != "delete" {
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
	v.Revoke()
	if _, err := v.Get("secret"); err == nil {
		t.Fatal("revoked vault read succeeded")
	}
}

func TestDeleteInstructionSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	d := DeleteInstruction{AccountID: "a", ObjectID: "o", DeviceID: "d", Version: 1}
	d.Signature = ed25519.Sign(priv, d.canonical())
	if !d.Verify(pub) {
		t.Fatal("valid signature rejected")
	}
	d.ObjectID = "other"
	if d.Verify(pub) {
		t.Fatal("tampered instruction accepted")
	}
}
