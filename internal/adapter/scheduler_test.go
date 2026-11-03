package adapter

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitQueued(t *testing.T, b *BotLimiter, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.scheduler.mu.Lock()
		n := len(b.queue)
		b.scheduler.mu.Unlock()
		if n == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("execution did not reach bounded queue")
}

func TestSchedulerBoundsAndRoundRobinFairness(t *testing.T) {
	s := NewScheduler(1)
	defer s.Close()
	hold := s.ForBot("hold", 1, 0)
	_, release, err := hold.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, b := s.ForBot("a", 1, 3), s.ForBot("b", 1, 1)
	type admission struct {
		bot     string
		release func()
		err     error
	}
	admitted := make(chan admission, 4)
	for range 3 {
		go func() { _, done, err := a.Acquire(context.Background()); admitted <- admission{"a", done, err} }()
	}
	waitQueued(t, a, 3)
	go func() { _, done, err := b.Acquire(context.Background()); admitted <- admission{"b", done, err} }()
	waitQueued(t, b, 1)
	if _, _, err := a.Acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue overflow: %v", err)
	}
	release()
	for _, want := range []string{"a", "b", "a", "a"} {
		select {
		case got := <-admitted:
			if got.err != nil || got.bot != want {
				t.Fatalf("admission = %s, %v; want %s", got.bot, got.err, want)
			}
			got.release()
		case <-time.After(3 * time.Second):
			t.Fatal("scheduler stalled")
		}
	}
}

func TestSchedulerEnforcesPerBotConcurrencyAndCancellation(t *testing.T) {
	s := NewScheduler(2)
	defer s.Close()
	a := s.ForBot("a", 1, 1)
	_, releaseA, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, done, err := a.Acquire(ctx)
		if done != nil {
			done()
		}
		queued <- err
	}()
	waitQueued(t, a, 1)
	b := s.ForBot("b", 1, 0)
	ctxB, releaseB, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseB()
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled queue = %v", err)
	}
	if ctxB.Err() != nil {
		t.Fatal("canceling another Bot interrupted execution")
	}
}

func TestBotCloseCancelsAndWaitsWithoutStoppingOtherBots(t *testing.T) {
	s := NewScheduler(2)
	defer s.Close()
	a, b := s.ForBot("a", 1, 1), s.ForBot("b", 1, 0)
	ctxA, releaseA, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctxB, releaseB, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseB()
	queued := make(chan error, 1)
	go func() {
		_, done, err := a.Acquire(context.Background())
		if done != nil {
			done()
		}
		queued <- err
	}()
	waitQueued(t, a, 1)
	closed := make(chan struct{})
	go func() { a.Close(); close(closed) }()
	select {
	case <-ctxA.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Bot did not cancel active work")
	}
	if err := <-queued; !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("queued close = %v", err)
	}
	select {
	case <-closed:
		t.Fatal("Close returned before active execution exited")
	default:
	}
	if ctxB.Err() != nil {
		t.Fatal("Bot stop canceled another Bot")
	}
	releaseA()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not wait for release")
	}
	fresh := s.ForBot("a", 1, 0)
	if fresh == a {
		t.Fatal("restart reused closed limiter")
	}
	_, done, err := fresh.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done()
}

func TestExpiredExecutionNeverAcquires(t *testing.T) {
	s := NewScheduler(1)
	defer s.Close()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, _, err := s.ForBot("a", 1, 1).Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired execution admitted: %v", err)
	}
}
