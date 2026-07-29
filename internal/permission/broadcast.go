package permission

import (
	"sync"

	"github.com/google/uuid"
)

// Broadcaster is an in-process, single-binary publish/subscribe hub for
// "this firm's permission grants changed" events - the mechanism behind
// Vision §3's "active sessions for the affected role are reconnected ...
// and other admin screens are notified of the change live."
//
// Deliberately in-process rather than a message broker (NATS JetStream,
// Redis pub/sub, etc.): ZonaryOS is a single deployable binary (Never-
// Violate Rule 5), there is no existing pub/sub infrastructure to reuse
// (the NATS JetStream mentioned in Vision §4 is earmarked for the Edge
// Agent's offline buffering - a different concern), and Keycloak-backed
// bearer-token auth carries no server-side session store this could hook
// into either. An in-memory map keyed by firmID, guarded by one mutex, is
// the least invasive mechanism that satisfies the requirement today; it
// stops working the moment ZonaryOS runs as more than one process, which
// is an explicit tradeoff worth revisiting if/when horizontal scaling
// becomes real (see docs/DEVELOPMENT.md's note on this).
type Broadcaster struct {
	mu          sync.Mutex
	subscribers map[uuid.UUID]map[chan struct{}]struct{}
}

// NewBroadcaster constructs an empty Broadcaster. One instance is shared
// across the whole server process (see cmd/server/main.go) - grant/revoke
// handlers publish to it, the SSE handler subscribes from it.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subscribers: make(map[uuid.UUID]map[chan struct{}]struct{})}
}

// Subscribe registers a new listener for firmID's events, returning a
// channel that receives a value every time Publish(firmID) is called and
// an unsubscribe function the caller must always run (typically via
// defer) to avoid leaking the channel from the internal map once the
// connection closes.
func (b *Broadcaster) Subscribe(firmID uuid.UUID) (ch <-chan struct{}, unsubscribe func()) {
	c := make(chan struct{}, 1)

	b.mu.Lock()
	if b.subscribers[firmID] == nil {
		b.subscribers[firmID] = make(map[chan struct{}]struct{})
	}
	b.subscribers[firmID][c] = struct{}{}
	b.mu.Unlock()

	return c, func() {
		b.mu.Lock()
		delete(b.subscribers[firmID], c)
		if len(b.subscribers[firmID]) == 0 {
			delete(b.subscribers, firmID)
		}
		b.mu.Unlock()
	}
}

// Publish notifies every current subscriber of firmID that something
// changed. Sends are non-blocking (a slow or stuck subscriber can never
// make a grant/revoke call hang) and the channel is buffered to size 1:
// it's a "there is news, go re-fetch the current state" flag, not a
// queue of individual events, so multiple Publish calls between two
// wakeups coalesce into one - subscribers always re-fetch full current
// state on wakeup, never try to apply a diff.
func (b *Broadcaster) Publish(firmID uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for c := range b.subscribers[firmID] {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}
