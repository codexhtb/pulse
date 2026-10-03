package main

import (
	"os"
	"sync"
	"testing"
	"time"
)

func testPendingStore(t *testing.T, dir string) (*pendingStore, pendingRecovery) {
	t.Helper()
	store, recovery, err := openPendingStore(dir, 2*time.Millisecond, 5*time.Millisecond, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return store, recovery
}

func applyTestRecovery(c *collector, store *pendingStore, recovery pendingRecovery) {
	c.pendingStore = store
	c.pending = recovery.Pending
	c.recent = recovery.Recent
	c.nextPendingID = recovery.NextPendingID
	for key, pending := range c.pending {
		c.expiryQueue = append(c.expiryQueue, expiryEntry{Key: key, ID: pending.ID, ExpiresAt: pending.ExpiresAt})
	}
}

func TestPendingRecoveryAfterGracefulRestart(t *testing.T) {
	dir := t.TempDir()
	store, recovery := testPendingStore(t, dir)
	c, _ := testCollector(time.Second)
	applyTestRecovery(c, store, recovery)
	queryAt := time.Now().UTC()
	c.handleClientQuery("dns1", testMessage(t, false, 0, 501, queryAt))
	if err := c.checkpointPending(); err != nil {
		t.Fatal(err)
	}
	if err := store.close(true); err != nil {
		t.Fatal(err)
	}

	reopened, recovered := testPendingStore(t, dir)
	defer reopened.close(true)
	if recovered.Recovered != 1 || len(recovered.Pending) != 1 {
		t.Fatalf("recovered=%d pending=%d", recovered.Recovered, len(recovered.Pending))
	}
	restarted, events := testCollector(time.Second)
	applyTestRecovery(restarted, reopened, recovered)
	restarted.handleClientResponse("dns1", testMessage(t, true, 0, 501, queryAt.Add(20*time.Millisecond)))
	if len(*events) != 1 || (*events)[0].Outcome != outcomeResponse {
		t.Fatalf("events=%+v", *events)
	}
}

func TestPendingRecoveryTruncatedWALTail(t *testing.T) {
	dir := t.TempDir()
	store, recovery := testPendingStore(t, dir)
	c, _ := testCollector(time.Second)
	applyTestRecovery(c, store, recovery)
	c.handleClientQuery("dns1", testMessage(t, false, 0, 502, time.Now().UTC()))
	if err := store.flush(true); err != nil {
		t.Fatal(err)
	}
	if err := store.close(false); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(store.walPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("truncated-tail")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, recovered := testPendingStore(t, dir)
	defer reopened.close(true)
	if recovered.Recovered != 1 || recovered.Errors == 0 || recovered.Truncated == 0 || !recovered.Unclean {
		t.Fatalf("recovery=%+v", recovered)
	}
}

func TestNoDuplicateResponseAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	store, recovery := testPendingStore(t, dir)
	c, events := testCollector(time.Second)
	applyTestRecovery(c, store, recovery)
	at := time.Now().UTC()
	c.handleClientQuery("dns1", testMessage(t, false, 0, 503, at))
	response := testMessage(t, true, 0, 503, at.Add(time.Millisecond))
	c.handleClientResponse("dns1", response)
	if len(*events) != 1 {
		t.Fatalf("events=%d", len(*events))
	}
	if err := c.checkpointPending(); err != nil {
		t.Fatal(err)
	}
	if err := store.close(true); err != nil {
		t.Fatal(err)
	}

	reopened, recovered := testPendingStore(t, dir)
	defer reopened.close(true)
	restarted, replayedEvents := testCollector(time.Second)
	applyTestRecovery(restarted, reopened, recovered)
	restarted.handleClientResponse("dns1", response)
	if len(*replayedEvents) != 0 || restarted.metrics.duplicatesSuppressed.Load() != 1 {
		t.Fatalf("events=%d suppressed=%d", len(*replayedEvents), restarted.metrics.duplicatesSuppressed.Load())
	}
}

func TestConcurrentGracefulShutdownWaitsForPendingHandlers(t *testing.T) {
	dir := t.TempDir()
	store, recovery := testPendingStore(t, dir)
	c, _ := testCollector(time.Hour)
	applyTestRecovery(c, store, recovery)
	c.backgroundStop = make(chan struct{})

	const handlers = 200
	release := make(chan struct{})
	var emittedMu sync.Mutex
	emitted := 0
	c.emitHook = func(Event) {
		emittedMu.Lock()
		emitted++
		emittedMu.Unlock()
	}

	c.handlerWG.Add(handlers)
	queryAt := time.Now().UTC()
	for i := 0; i < handlers; i++ {
		id := uint16(1000 + i)
		go func() {
			defer c.handlerWG.Done()
			<-release
			c.handleClientQuery("dns1", testMessage(t, false, 0, id, queryAt))
			if id%2 == 0 {
				c.handleClientResponse("dns1", testMessage(t, true, 0, id, queryAt.Add(time.Millisecond)))
			}
		}()
	}

	shutdownDone := make(chan struct{})
	go func() {
		c.gracefulShutdown(nil)
		close(shutdownDone)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		c.handlerMu.Lock()
		started := c.shuttingDown
		c.handlerMu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not start")
		}
		time.Sleep(time.Millisecond)
	}

	// Every handler performs its pending-store work after shutdown has begun.
	// gracefulShutdown must wait for all of them before closing the store.
	close(release)
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("graceful shutdown timed out")
	}

	if errors := store.writeErrors.Load(); errors != 0 {
		t.Fatalf("pending store writes after close=%d", errors)
	}
	emittedMu.Lock()
	completed := emitted
	emittedMu.Unlock()

	reopened, recovered := testPendingStore(t, dir)
	defer reopened.close(true)
	if completed != handlers/2 || recovered.Recovered != handlers/2 {
		t.Fatalf("completed=%d recovered=%d accepted=%d", completed, recovered.Recovered, handlers)
	}
	if completed+recovered.Recovered != handlers {
		t.Fatalf("accepted work lost: completed=%d recovered=%d accepted=%d", completed, recovered.Recovered, handlers)
	}
}
