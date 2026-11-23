package sync

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestPairingTicketPairsWithBothFingerprintsAndIsOneShot(t *testing.T) {
	s := NewServer()
	a, err := NewClient(s, "authorizer")
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
	if err := a.PairDeviceWithCredential(); err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(s, "new-device")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.LoginWithCredential("acct", credential); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC()
	bundle, err := a.CreatePairingBundle("new-device", b.privateKey.Public().(ed25519.PublicKey), created)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.PairDeviceWithTicket(bundle.Ticket, bundle.ShortCode, PairingFingerprint(b.privateKey.Public().(ed25519.PublicKey)), created.Add(time.Second)); !errors.Is(err, ErrPairingFingerprint) {
		t.Fatalf("wrong authorizer fingerprint error=%v, want %v", err, ErrPairingFingerprint)
	}
	if err := b.PairDeviceWithTicket(bundle.Ticket, bundle.ShortCode, PairingFingerprint(a.privateKey.Public().(ed25519.PublicKey)), created.Add(time.Second)); err != nil {
		t.Fatalf("pair with signed ticket: %v", err)
	}
	if err := b.PairDeviceWithTicket(bundle.Ticket, bundle.ShortCode, PairingFingerprint(a.privateKey.Public().(ed25519.PublicKey)), created.Add(2*time.Second)); !errors.Is(err, ErrPairingCodeUsed) {
		t.Fatalf("replayed pairing ticket error=%v, want %v", err, ErrPairingCodeUsed)
	}
}

func TestPairingShortCodeExpiresAfter120Seconds(t *testing.T) {
	s := NewServer()
	a, err := NewClient(s, "authorizer")
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
	if err := a.PairDeviceWithCredential(); err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(s, "new-device")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.LoginWithCredential("acct", credential); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC()
	bundle, err := a.CreatePairingBundle("new-device", b.privateKey.Public().(ed25519.PublicKey), created)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ShortCode == "" || bundle.QRCode == "" || bundle.Fingerprint == "" {
		t.Fatal("pairing bundle is missing QR, short code, or fingerprint")
	}
	if err := b.PairDeviceWithTicket(bundle.Ticket, bundle.ShortCode, PairingFingerprint(a.privateKey.Public().(ed25519.PublicKey)), created.Add(PairingCodeTTL+time.Nanosecond)); !errors.Is(err, ErrPairingCodeExpired) {
		t.Fatalf("expired pairing ticket error=%v, want %v", err, ErrPairingCodeExpired)
	}
}

func TestPairDeviceWithoutAuthorizationTicketIsRejected(t *testing.T) {
	s := NewServer()
	c, err := NewClient(s, "device")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.LoginWithCredential("acct", credential); err != nil {
		t.Fatal(err)
	}
	if err := c.PairDevice(); !errors.Is(err, ErrPairingTicket) {
		t.Fatalf("unauthorized PairDevice error=%v, want %v", err, ErrPairingTicket)
	}
}

func TestRemoveDeviceRejectsFutureUploads(t *testing.T) {
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
	for _, c := range []*Client{a, b} {
		if err := c.LoginWithCredential("acct", credential); err != nil {
			t.Fatal(err)
		}
		if err := c.PairDeviceWithCredential(); err != nil {
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
	if err := b.RemoveDevice("a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Upload("after-revoke", 1, []byte("must fail")); err == nil {
		t.Fatal("revoked device upload unexpectedly succeeded")
	}
}
