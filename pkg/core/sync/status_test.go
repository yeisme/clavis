package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

type statusTestStore struct {
	loadErr     error
	status      LocalStatus
	loadCalls   int
	statusCalls int
}

func (s *statusTestStore) Close() error { return nil }
func (s *statusTestStore) Load(context.Context) (uint64, bool, error) {
	s.loadCalls++
	if s.loadErr != nil {
		return 0, false, s.loadErr
	}
	return uint64(s.status.LocalVersion), true, nil
}
func (s *statusTestStore) Save(context.Context, uint64, bool) error { return nil }
func (s *statusTestStore) LoadStatus(context.Context) (LocalStatus, error) {
	s.statusCalls++
	if s.loadErr != nil {
		return LocalStatus{}, s.loadErr
	}
	return s.status, nil
}

func newStatusTestClient(t *testing.T, server *Server) *Client {
	t.Helper()
	identity, err := newDeviceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	client, err := newClient(server, "device-long-1234", identity)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestStatusSnapshotUnconfiguredIsReadyWithoutNetwork(t *testing.T) {
	server := NewServer()
	client := newStatusTestClient(t, server)
	got, result, err := client.StatusSnapshot(context.Background())
	if err != nil || result.State != host.StateReady || got.LoggedIn {
		t.Fatalf("snapshot=%+v result=%+v err=%v", got, result, err)
	}
}

func TestStatusSnapshotReadsPendingCountsAndRedactsIdentifiers(t *testing.T) {
	server := NewServer()
	client := newStatusTestClient(t, server)
	store := &statusTestStore{status: LocalStatus{
		LocalVersion:      17,
		PendingUploads:    3,
		PendingTombstones: 2,
		LastSyncAt:        time.Unix(123, 0).UTC(),
		Devices:           4,
	}}
	client.mu.Lock()
	client.accountID = "account-123456789"
	client.stateStore = store
	client.mu.Unlock()

	got, result, err := client.StatusSnapshot(context.Background())
	if err != nil || result.State != host.StateReady {
		t.Fatalf("snapshot=%+v result=%+v err=%v", got, result, err)
	}
	if got.Account == "account-123456789" || got.DeviceID == "device-long-1234" {
		t.Fatalf("identifiers not redacted: %+v", got)
	}
	if !got.LoggedIn || got.LocalVersion != 17 || got.PendingUploads != 3 || got.PendingTombstones != 2 || got.Devices != 4 || got.LastSyncAt.IsZero() {
		t.Fatalf("local status=%+v", got)
	}
	if store.loadCalls != 1 || store.statusCalls != 1 {
		t.Fatalf("unexpected local reads: load=%d status=%d", store.loadCalls, store.statusCalls)
	}
}

func TestStatusSnapshotReadFailureIsUnknownNotFailed(t *testing.T) {
	client := newStatusTestClient(t, NewServer())
	client.mu.Lock()
	client.accountID = "account-123456789"
	client.stateStore = &statusTestStore{loadErr: errors.New("local state unavailable")}
	client.mu.Unlock()

	_, result, err := client.StatusSnapshot(context.Background())
	if err == nil || result.State != host.StateUnknown || result.State == host.StateFailed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestStatusSnapshotDoesNotCallServer(t *testing.T) {
	server := NewServer()
	client := newStatusTestClient(t, server)
	client.mu.Lock()
	client.accountID = "account-123456789"
	client.stateStore = &statusTestStore{status: LocalStatus{LocalVersion: 1}}
	client.server = nil
	client.mu.Unlock()

	_, result, err := client.StatusSnapshot(context.Background())
	if err != nil || result.State != host.StateReady {
		t.Fatalf("local-only snapshot touched server or failed: result=%+v err=%v", result, err)
	}
}
