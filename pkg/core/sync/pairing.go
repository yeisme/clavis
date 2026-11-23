package sync

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const PairingCodeTTL = 120 * time.Second

var (
	ErrPairingCodeExpired = errors.New("sync: pairing code expired")
	ErrPairingCodeUsed    = errors.New("sync: pairing code already used")
	ErrPairingTicket      = errors.New("sync: invalid pairing authorization ticket")
	ErrPairingFingerprint = errors.New("sync: device fingerprint confirmation required")
)

// PairingTicket is an authorization ticket signed by an already authorized
// device. It is bound to the account, new device, public key, short code and
// both device fingerprints.
type PairingTicket struct {
	Account          string    `json:"account"`
	Device           string    `json:"device"`
	PublicKey        []byte    `json:"publicKey"`
	X25519PublicKey  []byte    `json:"x25519PublicKey,omitempty"`
	WrappedADK       []byte    `json:"wrappedADK,omitempty"`
	ShortCode        string    `json:"shortCode"`
	ExpiresAt        time.Time `json:"expiresAt"`
	Nonce            string    `json:"nonce"`
	Authorizer       string    `json:"authorizer"`
	AuthorizerPubKey []byte    `json:"authorizerPublicKey"`
	NewFingerprint   string    `json:"newFingerprint"`
	Signature        []byte    `json:"signature"`
}

// PairingBundle is suitable for QR transport. The short code is also shown
// separately for manual entry; both are covered by the ticket signature.
type PairingBundle struct {
	Ticket      PairingTicket `json:"ticket"`
	QRCode      string        `json:"qrCode"`
	ShortCode   string        `json:"shortCode"`
	Fingerprint string        `json:"fingerprint"`
	ExpiresAt   time.Time     `json:"expiresAt"`
}

func pairingTicketCanonical(t PairingTicket) []byte {
	copyTicket := t
	copyTicket.Signature = nil
	b, _ := json.Marshal(copyTicket)
	return append([]byte("clavis.sync.pairing-ticket.v1\x00"), b...)
}

func PairingFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

func generatePairingCode() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:8]), nil
}

func (t PairingTicket) Verify(now time.Time) error {
	if strings.TrimSpace(t.Account) == "" || strings.TrimSpace(t.Device) == "" || len(t.PublicKey) != ed25519.PublicKeySize || len(t.AuthorizerPubKey) != ed25519.PublicKeySize || len(t.Signature) != ed25519.SignatureSize || t.ShortCode == "" || t.Nonce == "" || t.Authorizer == "" {
		return ErrPairingTicket
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !now.Before(t.ExpiresAt) {
		return ErrPairingCodeExpired
	}
	if t.NewFingerprint != PairingFingerprint(ed25519.PublicKey(t.PublicKey)) {
		return ErrPairingFingerprint
	}
	if !ed25519.Verify(ed25519.PublicKey(t.AuthorizerPubKey), pairingTicketCanonical(t), t.Signature) {
		return ErrPairingTicket
	}
	return nil
}

func (c *Client) CreatePairingBundle(device string, newPublicKey ed25519.PublicKey, now time.Time) (PairingBundle, error) {
	return c.createPairingBundle(device, newPublicKey, nil, now)
}

// CreatePairingBundleWithX25519 includes the receiver key and the authorizer's
// wrapped ADK in the signed QR/short-code payload, so the new device can become
// usable without a second unbound key exchange.
func (c *Client) CreatePairingBundleWithX25519(device string, newPublicKey ed25519.PublicKey, newX25519PublicKey []byte, now time.Time) (PairingBundle, error) {
	return c.createPairingBundle(device, newPublicKey, newX25519PublicKey, now)
}

func (c *Client) createPairingBundle(device string, newPublicKey ed25519.PublicKey, newX25519PublicKey []byte, now time.Time) (PairingBundle, error) {
	c.mu.Lock()
	account := c.accountID
	authorizer := c.deviceID
	privateKey := append(ed25519.PrivateKey(nil), c.privateKey...)
	xPrivateKey := c.xPrivateKey
	adk := append([]byte(nil), c.adk...)
	c.mu.Unlock()
	if account == "" {
		return PairingBundle{}, errors.New("sync: login is required")
	}
	if strings.TrimSpace(device) == "" || len(newPublicKey) != ed25519.PublicKeySize {
		return PairingBundle{}, ErrPairingTicket
	}
	if len(newX25519PublicKey) != 0 && len(newX25519PublicKey) != 32 {
		return PairingBundle{}, ErrPairingTicket
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	code, err := generatePairingCode()
	if err != nil {
		return PairingBundle{}, err
	}
	ticket := PairingTicket{Account: account, Device: device, PublicKey: append([]byte(nil), newPublicKey...), X25519PublicKey: append([]byte(nil), newX25519PublicKey...), ShortCode: code, ExpiresAt: now.Add(PairingCodeTTL), Nonce: randomNonce(), Authorizer: authorizer, AuthorizerPubKey: append([]byte(nil), privateKey.Public().(ed25519.PublicKey)...), NewFingerprint: PairingFingerprint(newPublicKey)}
	if len(newX25519PublicKey) == 32 && len(adk) == 32 {
		newPub, err := ecdh.X25519().NewPublicKey(newX25519PublicKey)
		if err != nil {
			return PairingBundle{}, ErrPairingTicket
		}
		shared, err := xPrivateKey.ECDH(newPub)
		if err != nil {
			return PairingBundle{}, err
		}
		wrapped, err := wrapWithSharedKey(shared, adk)
		if err != nil {
			return PairingBundle{}, err
		}
		ticket.WrappedADK = append(append([]byte(nil), xPrivateKey.PublicKey().Bytes()...), wrapped...)
	}
	ticket.Signature = ed25519.Sign(privateKey, pairingTicketCanonical(ticket))
	encoded, err := json.Marshal(ticket)
	if err != nil {
		return PairingBundle{}, err
	}
	return PairingBundle{Ticket: ticket, QRCode: string(encoded), ShortCode: code, Fingerprint: ticket.NewFingerprint, ExpiresAt: ticket.ExpiresAt}, nil
}

func randomNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (c *Client) PairDeviceWithTicket(ticket PairingTicket, shortCode, confirmedAuthorizerFingerprint string, now time.Time) error {
	if shortCode != ticket.ShortCode || confirmedAuthorizerFingerprint == "" || confirmedAuthorizerFingerprint != PairingFingerprint(ed25519.PublicKey(ticket.AuthorizerPubKey)) {
		return ErrPairingFingerprint
	}
	if err := ticket.Verify(now); err != nil {
		return err
	}
	c.mu.Lock()
	account, device := c.accountID, c.deviceID
	pub := append(ed25519.PublicKey(nil), c.privateKey.Public().(ed25519.PublicKey)...)
	credential := append([]byte(nil), c.credential...)
	c.mu.Unlock()
	if account != ticket.Account || device != ticket.Device || !bytes.Equal(pub, ticket.PublicKey) {
		return ErrPairingTicket
	}
	store, err := c.syncStore()
	if err != nil {
		return err
	}
	if pairingStore, ok := store.(PairingTicketStore); ok {
		return pairingStore.PairDeviceWithTicket(ticket)
	}
	// Durable stores that predate the ticket API still receive the same
	// cryptographic gate: pin the authorizer key from the store before using
	// the credential-backed insert primitive.
	authorizerPub, err := store.DevicePublicKey(ticket.Account, ticket.Authorizer)
	if err != nil || !bytes.Equal(authorizerPub, ticket.AuthorizerPubKey) {
		return ErrPairingTicket
	}
	return store.PairDeviceWithCredential(ticket.Account, ticket.Device, ed25519.PublicKey(ticket.PublicKey), credential)
}

// PairingTicketStore is implemented by server-side sync stores.
type PairingTicketStore interface {
	PairDeviceWithTicket(PairingTicket) error
}

// PairingRevocationStore is implemented by server-side sync stores.
type PairingRevocationStore interface {
	RemoveDevice(account, device string, credential []byte) error
}

// RemoveDevice revokes a paired device using the account credential. Future
// uploads signed by the revoked device are rejected by the store.
func (c *Client) RemoveDevice(device string) error {
	c.mu.Lock()
	account := c.accountID
	credential := append([]byte(nil), c.credential...)
	c.mu.Unlock()
	store, err := c.syncStore()
	if err != nil {
		return err
	}
	revoker, ok := store.(PairingRevocationStore)
	if !ok {
		return errors.New("sync: device revocation is not supported by this store")
	}
	return revoker.RemoveDevice(account, device, credential)
}
