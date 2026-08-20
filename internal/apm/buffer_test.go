// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestBuffer_ConcurrentRecordIsGoroutineSafe is this batch's own
// required test: concurrent writes to the buffer must not race or lose
// updates. pool is nil throughout (write's own nil-pool guard makes
// that a safe no-op), and goroutines*perGoroutine is kept well under
// maxBufferSize so no inline flush-swap ever detaches the slice this
// test asserts against - go test -race is what actually proves the
// mutex is doing its job; the count assertion proves no update was
// silently lost to a race.
func TestBuffer_ConcurrentRecordIsGoroutineSafe(t *testing.T) {
	b := NewBuffer(nil)
	const goroutines = 20
	const perGoroutine = 4

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				b.Record("GET", "/api/firms/3aa7de0c-a5a2-8181-ac86-ef800f628f5d/products", 200, 5)
			}
		}()
	}
	wg.Wait()

	b.mu.Lock()
	got := len(b.records)
	b.mu.Unlock()

	want := goroutines * perGoroutine
	if got != want {
		t.Errorf("expected %d buffered records after concurrent Record calls, got %d", want, got)
	}
}

// TestBuffer_RunFlushLoopDrainsBufferOnContextCancellation is this
// batch's own required "flush-on-shutdown confirmed" test: cancelling
// RunFlushLoop's own context must drain whatever is still buffered
// before it returns - the mechanism cmd/server/main.go's own graceful
// shutdown sequence relies on to guarantee a SIGTERM never silently
// drops the tail of a buffer that hadn't yet reached maxBufferSize or
// the next flushInterval tick.
func TestBuffer_RunFlushLoopDrainsBufferOnContextCancellation(t *testing.T) {
	b := NewBuffer(nil)
	b.records = []Record{
		{Method: "GET", PathPattern: "/api/version", StatusCode: 200, DurationMs: 3},
		{Method: "GET", PathPattern: "/healthz", StatusCode: 200, DurationMs: 1},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.RunFlushLoop(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunFlushLoop did not return after context cancellation")
	}

	b.mu.Lock()
	remaining := len(b.records)
	b.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected buffer drained by the final shutdown flush, got %d records still buffered", remaining)
	}
}
