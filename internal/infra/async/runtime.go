// Package async provides the async runtime for Garurda.
package async

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents an async task.
type Task struct {
	id       uint64
	fn       func() (interface{}, error)
	result   chan Result
	ctx      context.Context
	cancel   context.CancelFunc
	created  time.Time
	started  time.Time
	finished time.Time
}

// Result represents the result of a task.
type Result struct {
	Value interface{}
	Err   error
}

// Scheduler manages task execution.
type Scheduler struct {
	tasks       map[uint64]*Task
	workers     int
	taskQueue   chan *Task
	workerQueue chan *Task
	wg          sync.WaitGroup
	mu          sync.RWMutex
	nextID      uint64
	running     bool
	shutdown    chan struct{}
}

// NewScheduler creates a new scheduler.
func NewScheduler(workers int) *Scheduler {
	if workers <= 0 {
		workers = 4
	}
	return &Scheduler{
		tasks:       make(map[uint64]*Task),
		workers:     workers,
		taskQueue:   make(chan *Task, 1000),
		workerQueue: make(chan *Task, 1000),
		shutdown:    make(chan struct{}),
	}
}

// Start starts the scheduler.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return
	}
	s.running = true

	// Start workers
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}

	// Start task dispatcher
	s.wg.Add(1)
	go s.dispatcher()
}

// Stop stops the scheduler gracefully.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}
	s.running = false
	close(s.shutdown)
	s.wg.Wait()
}

// Submit submits a new task to the scheduler.
func (s *Scheduler) Submit(fn func() (interface{}, error)) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	id := atomic.AddUint64(&s.nextID, 1)
	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{
		id:     id,
		fn:     fn,
		result: make(chan Result, 1),
		ctx:    ctx,
		cancel: cancel,
		created: time.Now(),
	}

	s.tasks[id] = task
	s.taskQueue <- task
	return task
}

// Await waits for a task to complete and returns its result.
func (s *Scheduler) Await(task *Task) (interface{}, error) {
	result := <-task.result
	return result.Value, result.Err
}

// Cancel cancels a task.
func (s *Scheduler) Cancel(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return false
	}
	task.cancel()
	return true
}

// GetTask returns a task by ID.
func (s *Scheduler) GetTask(id uint64) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	return task, ok
}

// dispatcher distributes tasks to workers.
func (s *Scheduler) dispatcher() {
	defer s.wg.Done()

	for {
		select {
		case task := <-s.taskQueue:
			select {
			case s.workerQueue <- task:
			case <-s.shutdown:
				return
			}
		case <-s.shutdown:
			return
		}
	}
}

// worker processes tasks from the worker queue.
func (s *Scheduler) worker() {
	defer s.wg.Done()

	for {
		select {
		case task := <-s.workerQueue:
			s.executeTask(task)
		case <-s.shutdown:
			return
		}
	}
}

// executeTask executes a single task.
func (s *Scheduler) executeTask(task *Task) {
	task.started = time.Now()
	defer func() {
		task.finished = time.Now()
		if r := recover(); r != nil {
			task.result <- Result{Err: errors.New("panic: " + r.(string))}
		}
		close(task.result)
	}()

	result, err := task.fn()
	select {
	case task.result <- Result{Value: result, Err: err}:
	case <-task.ctx.Done():
		task.result <- Result{Err: task.ctx.Err()}
	}
}

// AwaitAll waits for all tasks to complete.
func (s *Scheduler) AwaitAll(tasks []*Task) []Result {
	results := make([]Result, len(tasks))
	for i, task := range tasks {
		results[i] = <-task.result
	}
	return results
}

// Gather runs multiple functions concurrently and returns their results.
func Gather(fns ...func() (interface{}, error)) []Result {
	scheduler := NewScheduler(len(fns))
	scheduler.Start()
	defer scheduler.Stop()

	tasks := make([]*Task, len(fns))
	for i, fn := range fns {
		tasks[i] = scheduler.Submit(fn)
	}
	return scheduler.AwaitAll(tasks)
}

// Promise represents a future value.
type Promise struct {
	result chan Result
	done   bool
	mu     sync.Mutex
}

// NewPromise creates a new promise.
func NewPromise() *Promise {
	return &Promise{
		result: make(chan Result, 1),
	}
}

// Resolve resolves the promise with a value.
func (p *Promise) Resolve(value interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		p.done = true
		p.result <- Result{Value: value}
		close(p.result)
	}
}

// Reject rejects the promise with an error.
func (p *Promise) Reject(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		p.done = true
		p.result <- Result{Err: err}
		close(p.result)
	}
}

// Await waits for the promise to resolve.
func (p *Promise) Await() (interface{}, error) {
	result := <-p.result
	return result.Value, result.Err
}

// Timeout returns a new promise that resolves after the given duration.
func Timeout(d time.Duration) *Promise {
	p := NewPromise()
	go func() {
		time.Sleep(d)
		p.Reject(errors.New("timeout"))
	}()
	return p
}

// Race returns a promise that settles as soon as one of the promises settles.
func Race(promises ...*Promise) *Promise {
	p := NewPromise()
	for _, pr := range promises {
		go func(pr *Promise) {
			v, err := pr.Await()
			// Resolve/Reject sudah mengunci p.mu sendiri dan mengabaikan
			// panggilan kedua lewat flag done — jangan dikunci dua kali.
			if err != nil {
				p.Reject(err)
			} else {
				p.Resolve(v)
			}
		}(pr)
	}
	return p
}

// All returns a promise that resolves when all promises resolve.
func All(promises ...*Promise) *Promise {
	p := NewPromise()
	go func() {
		results := make([]interface{}, len(promises))
		for i, pr := range promises {
			v, err := pr.Await()
			if err != nil {
				p.Reject(err)
				return
			}
			results[i] = v
		}
		p.Resolve(results)
	}()
	return p
}

// Any returns a promise that resolves as soon as one promise resolves. It
// rejects only after every promise has rejected (Promise.any semantics);
// the last rejection reason is used.
func Any(promises ...*Promise) *Promise {
	p := NewPromise()
	if len(promises) == 0 {
		p.Reject(errors.New("any: no promises"))
		return p
	}
	var mu sync.Mutex
	remaining := len(promises)
	for _, pr := range promises {
		go func(pr *Promise) {
			v, err := pr.Await()
			if err == nil {
				p.Resolve(v) // penyelesai pertama menang (dijaga flag done).
				return
			}
			mu.Lock()
			remaining--
			last := remaining == 0
			mu.Unlock()
			if last {
				p.Reject(err)
			}
		}(pr)
	}
	return p
}