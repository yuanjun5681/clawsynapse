package adapter

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrQueueFull       = errors.New("bot execution queue is full")
	ErrSchedulerClosed = errors.New("execution scheduler is closed")
)

// Scheduler admits one waiting execution per eligible Bot per round. Waiting
// requests occupy bounded Bot queues, not goroutines owned by the scheduler.
type Scheduler struct {
	mu           sync.Mutex
	max, running int
	closed       bool
	bots         map[string]*BotLimiter
	ready        []*BotLimiter
}

type BotLimiter struct {
	scheduler               *Scheduler
	id                      string
	max, maxQueued, running int
	closed, ready           bool
	queue                   []*executionSlot
	active                  map[*executionSlot]struct{}
	done                    chan struct{}
}

type executionSlot struct {
	ctx     context.Context
	cancel  context.CancelFunc
	ready   chan struct{}
	granted bool
	err     error
}

func NewScheduler(maxConcurrent int) *Scheduler {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Scheduler{max: maxConcurrent, bots: make(map[string]*BotLimiter)}
}

// ForBot returns the live limiter for a Bot; after Close a fresh limiter may be
// created for the same Bot. Limits are fixed for the lifetime of that limiter.
func (s *Scheduler) ForBot(botID string, maxConcurrent, maxQueued int) *BotLimiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.bots[botID]; b != nil {
		return b
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if maxQueued < 0 {
		maxQueued = 0
	}
	b := &BotLimiter{scheduler: s, id: botID, max: maxConcurrent, maxQueued: maxQueued, active: make(map[*executionSlot]struct{}), done: make(chan struct{})}
	if s.closed {
		b.closed = true
		close(b.done)
	} else {
		s.bots[botID] = b
	}
	return b
}

// Capacity bounds accepted commands including running work and durable queued
// work. A receiver can leave excess commands on its durable transport.
func (b *BotLimiter) Capacity() int { return b.max + b.maxQueued }

// Snapshot reports currently running executions and waiting queue depth.
func (b *BotLimiter) Snapshot() (running, queued int) {
	b.scheduler.mu.Lock()
	defer b.scheduler.mu.Unlock()
	return b.running, len(b.queue)
}

// Acquire returns a context canceled by either the caller or this Bot's Close.
// The caller must release every acquired slot, after the child has exited.
func (b *BotLimiter) Acquire(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s := b.scheduler
	s.mu.Lock()
	if b.closed || s.closed {
		s.mu.Unlock()
		return nil, nil, ErrSchedulerClosed
	}
	// An immediately available execution does not consume a queue slot.
	immediate := s.running < s.max && b.running < b.max && len(b.queue) == 0 && len(s.ready) == 0
	if !immediate && len(b.queue) >= b.maxQueued {
		s.mu.Unlock()
		return nil, nil, ErrQueueFull
	}
	runCtx, cancel := context.WithCancel(ctx)
	slot := &executionSlot{ctx: runCtx, cancel: cancel, ready: make(chan struct{})}
	b.queue = append(b.queue, slot)
	s.markReady(b)
	s.dispatch()
	s.mu.Unlock()
	select {
	case <-slot.ready:
	case <-ctx.Done():
		s.mu.Lock()
		if !slot.granted && slot.err == nil {
			b.remove(slot)
			slot.err = ctx.Err()
			cancel()
		}
		s.dispatch()
		s.mu.Unlock()
	}
	s.mu.Lock()
	granted, err := slot.granted, slot.err
	s.mu.Unlock()
	if !granted {
		cancel()
		return nil, nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			delete(b.active, slot)
			b.running--
			s.running--
			s.markReady(b)
			b.finishClose()
			s.dispatch()
			s.mu.Unlock()
		})
	}
	if err := runCtx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	return runCtx, release, nil
}

func (b *BotLimiter) remove(slot *executionSlot) {
	for i, queued := range b.queue {
		if queued == slot {
			copy(b.queue[i:], b.queue[i+1:])
			b.queue[len(b.queue)-1] = nil
			b.queue = b.queue[:len(b.queue)-1]
			return
		}
	}
}

func (s *Scheduler) markReady(b *BotLimiter) {
	if !b.ready && !b.closed && len(b.queue) > 0 && b.running < b.max {
		b.ready = true
		s.ready = append(s.ready, b)
	}
}

func (s *Scheduler) dispatch() {
	for !s.closed && s.running < s.max && len(s.ready) > 0 {
		b := s.ready[0]
		s.ready[0] = nil
		s.ready = s.ready[1:]
		b.ready = false
		if b.closed || b.running >= b.max || len(b.queue) == 0 {
			continue
		}
		slot := b.queue[0]
		b.remove(slot)
		if err := slot.ctx.Err(); err != nil {
			slot.err = err
			slot.cancel()
			close(slot.ready)
		} else {
			slot.granted = true
			b.active[slot] = struct{}{}
			b.running++
			s.running++
			close(slot.ready)
		}
		s.markReady(b)
	}
}

func (b *BotLimiter) finishClose() {
	if b.closed && b.running == 0 {
		if b.scheduler.bots[b.id] == b {
			delete(b.scheduler.bots, b.id)
		}
		select {
		case <-b.done:
		default:
			close(b.done)
		}
	}
}

func (b *BotLimiter) closeLocked() {
	if b.closed {
		return
	}
	b.closed = true
	for _, slot := range b.queue {
		slot.err = ErrSchedulerClosed
		slot.cancel()
		close(slot.ready)
	}
	b.queue = nil
	for slot := range b.active {
		slot.cancel()
	}
	if b.ready {
		for i, ready := range b.scheduler.ready {
			if ready == b {
				copy(b.scheduler.ready[i:], b.scheduler.ready[i+1:])
				b.scheduler.ready[len(b.scheduler.ready)-1] = nil
				b.scheduler.ready = b.scheduler.ready[:len(b.scheduler.ready)-1]
				break
			}
		}
		b.ready = false
	}
	b.finishClose()
}

// Close cancels this Bot alone and waits until all acquired slots are released.
func (b *BotLimiter) Close() {
	s := b.scheduler
	s.mu.Lock()
	b.closeLocked()
	s.dispatch()
	s.mu.Unlock()
	<-b.done
}

func (s *Scheduler) Close() {
	s.mu.Lock()
	s.closed = true
	bots := make([]*BotLimiter, 0, len(s.bots))
	for _, b := range s.bots {
		bots = append(bots, b)
		b.closeLocked()
	}
	s.ready = nil
	s.mu.Unlock()
	for _, b := range bots {
		<-b.done
	}
}
