package concurrency

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerPool represents a pool of workers that can execute tasks concurrently
type WorkerPool struct {
	workers     int
	taskChan    chan Task
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	started     int64
	stopped     int64
	activeJobs  int64
	totalJobs   int64
	failedJobs  int64
	mu          sync.RWMutex
	maxQueueLen int
}

// Task represents a unit of work to be executed by a worker
type Task struct {
	ID       string
	Priority int // Higher values = higher priority
	Fn       func(ctx context.Context) error
	Callback func(error) // Called when task completes
	Created  time.Time
}

// WorkerPoolConfig holds configuration for the worker pool
type WorkerPoolConfig struct {
	Workers      int // Number of workers
	QueueSize    int // Size of task queue
	MaxQueueSize int // Maximum allowed queue size
}

// NewWorkerPool creates a new worker pool with the specified configuration
func NewWorkerPool(config WorkerPoolConfig) *WorkerPool {
	if config.Workers <= 0 {
		config.Workers = runtime.NumCPU()
	}
	if config.QueueSize <= 0 {
		config.QueueSize = config.Workers * 10
	}
	if config.MaxQueueSize <= 0 {
		config.MaxQueueSize = config.QueueSize * 2
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WorkerPool{
		workers:     config.Workers,
		taskChan:    make(chan Task, config.QueueSize),
		ctx:         ctx,
		cancel:      cancel,
		maxQueueLen: config.MaxQueueSize,
	}
}

// Start initializes and starts the worker pool
func (wp *WorkerPool) Start() error {
	if !atomic.CompareAndSwapInt64(&wp.started, 0, 1) {
		return fmt.Errorf("worker pool already started")
	}

	wp.wg.Add(wp.workers)

	// Start worker goroutines
	for i := 0; i < wp.workers; i++ {
		go wp.worker(i)
	}

	return nil
}

// Submit adds a task to the worker pool queue
func (wp *WorkerPool) Submit(task Task) error {
	if atomic.LoadInt64(&wp.stopped) == 1 {
		return fmt.Errorf("worker pool is stopped")
	}

	// Check queue size to prevent memory exhaustion
	if len(wp.taskChan) >= wp.maxQueueLen {
		return fmt.Errorf("queue is full, cannot accept more tasks")
	}

	task.Created = time.Now()
	atomic.AddInt64(&wp.totalJobs, 1)

	select {
	case wp.taskChan <- task:
		return nil
	case <-wp.ctx.Done():
		return fmt.Errorf("worker pool is shutting down")
	default:
		return fmt.Errorf("queue is full, try again later")
	}
}

// SubmitWithTimeout attempts to submit a task with a timeout
func (wp *WorkerPool) SubmitWithTimeout(task Task, timeout time.Duration) error {
	if atomic.LoadInt64(&wp.stopped) == 1 {
		return fmt.Errorf("worker pool is stopped")
	}

	task.Created = time.Now()
	atomic.AddInt64(&wp.totalJobs, 1)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case wp.taskChan <- task:
		return nil
	case <-ctx.Done():
		atomic.AddInt64(&wp.totalJobs, -1) // Rollback counter
		return fmt.Errorf("timeout submitting task")
	case <-wp.ctx.Done():
		atomic.AddInt64(&wp.totalJobs, -1) // Rollback counter
		return fmt.Errorf("worker pool is shutting down")
	}
}

// Stop gracefully shuts down the worker pool
func (wp *WorkerPool) Stop(timeout time.Duration) error {
	if !atomic.CompareAndSwapInt64(&wp.stopped, 0, 1) {
		return fmt.Errorf("worker pool already stopped")
	}

	wp.cancel()
	close(wp.taskChan)

	// Wait for workers to finish with timeout
	done := make(chan struct{})
	go func() {
		wp.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timeout waiting for workers to finish")
	}
}

// worker processes tasks from the queue
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	for {
		select {
		case task, ok := <-wp.taskChan:
			if !ok {
				return // Channel closed, worker should exit
			}

			atomic.AddInt64(&wp.activeJobs, 1)
			err := task.Fn(wp.ctx)
			atomic.AddInt64(&wp.activeJobs, -1)

			if err != nil {
				atomic.AddInt64(&wp.failedJobs, 1)
			}

			// Call callback if provided
			if task.Callback != nil {
				go task.Callback(err)
			}

		case <-wp.ctx.Done():
			return
		}
	}
}

// Stats returns current statistics about the worker pool
func (wp *WorkerPool) Stats() WorkerPoolStats {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	return WorkerPoolStats{
		Workers:     wp.workers,
		QueueLength: len(wp.taskChan),
		MaxQueue:    wp.maxQueueLen,
		ActiveJobs:  atomic.LoadInt64(&wp.activeJobs),
		TotalJobs:   atomic.LoadInt64(&wp.totalJobs),
		FailedJobs:  atomic.LoadInt64(&wp.failedJobs),
		IsRunning:   atomic.LoadInt64(&wp.started) == 1 && atomic.LoadInt64(&wp.stopped) == 0,
	}
}

// WorkerPoolStats contains statistics about the worker pool
type WorkerPoolStats struct {
	Workers     int   `json:"workers"`
	QueueLength int   `json:"queue_length"`
	MaxQueue    int   `json:"max_queue"`
	ActiveJobs  int64 `json:"active_jobs"`
	TotalJobs   int64 `json:"total_jobs"`
	FailedJobs  int64 `json:"failed_jobs"`
	IsRunning   bool  `json:"is_running"`
}
