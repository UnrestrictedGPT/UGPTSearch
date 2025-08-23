package concurrency

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPoolCreation(t *testing.T) {
	tests := []struct {
		name   string
		config WorkerPoolConfig
		want   WorkerPoolConfig
	}{
		{
			name: "default configuration",
			config: WorkerPoolConfig{
				Workers:      0,
				QueueSize:    0,
				MaxQueueSize: 0,
			},
			want: WorkerPoolConfig{
				Workers:      runtime.NumCPU(),
				QueueSize:    runtime.NumCPU() * 10,
				MaxQueueSize: runtime.NumCPU() * 20,
			},
		},
		{
			name: "custom configuration",
			config: WorkerPoolConfig{
				Workers:      5,
				QueueSize:    100,
				MaxQueueSize: 200,
			},
			want: WorkerPoolConfig{
				Workers:      5,
				QueueSize:    100,
				MaxQueueSize: 200,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wp := NewWorkerPool(tt.config)
			
			if wp.workers != tt.want.Workers {
				t.Errorf("NewWorkerPool() workers = %d, want %d", wp.workers, tt.want.Workers)
			}
			if cap(wp.taskChan) != tt.want.QueueSize {
				t.Errorf("NewWorkerPool() queue size = %d, want %d", cap(wp.taskChan), tt.want.QueueSize)
			}
			if wp.maxQueueLen != tt.want.MaxQueueSize {
				t.Errorf("NewWorkerPool() max queue size = %d, want %d", wp.maxQueueLen, tt.want.MaxQueueSize)
			}
		})
	}
}

func TestWorkerPoolStartStop(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   2,
		QueueSize: 10,
	})

	// Test start
	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Test double start
	if err := wp.Start(); err == nil {
		t.Error("Start() should fail when already started")
	}

	// Test stats after start
	stats := wp.Stats()
	if !stats.IsRunning {
		t.Error("WorkerPool should be running")
	}

	// Test stop
	if err := wp.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Test double stop
	if err := wp.Stop(1 * time.Second); err == nil {
		t.Error("Stop() should fail when already stopped")
	}

	// Test stats after stop
	stats = wp.Stats()
	if stats.IsRunning {
		t.Error("WorkerPool should not be running")
	}
}

func TestWorkerPoolTaskExecution(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   3,
		QueueSize: 10,
	})

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(5 * time.Second)

	// Test successful task
	executed := make(chan bool, 1)
	task := Task{
		ID: "test-1",
		Fn: func(ctx context.Context) error {
			executed <- true
			return nil
		},
	}

	if err := wp.Submit(task); err != nil {
		t.Fatalf("Submit() failed: %v", err)
	}

	select {
	case <-executed:
		// Task executed successfully
	case <-time.After(2 * time.Second):
		t.Error("Task was not executed within timeout")
	}

	// Test task with error
	errorReceived := make(chan error, 1)
	errorTask := Task{
		ID: "test-2",
		Fn: func(ctx context.Context) error {
			return errors.New("test error")
		},
		Callback: func(err error) {
			errorReceived <- err
		},
	}

	if err := wp.Submit(errorTask); err != nil {
		t.Fatalf("Submit() failed: %v", err)
	}

	select {
	case err := <-errorReceived:
		if err == nil || err.Error() != "test error" {
			t.Errorf("Expected 'test error', got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Error callback was not called within timeout")
	}

	// Verify stats
	stats := wp.Stats()
	if stats.TotalJobs < 2 {
		t.Errorf("Expected at least 2 total jobs, got %d", stats.TotalJobs)
	}
	if stats.FailedJobs < 1 {
		t.Errorf("Expected at least 1 failed job, got %d", stats.FailedJobs)
	}
}

func TestWorkerPoolConcurrentSubmission(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   5,
		QueueSize: 100,
	})

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(10 * time.Second)

	const numTasks = 50
	var completed int64
	var wg sync.WaitGroup

	// Submit tasks concurrently
	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		go func(taskID int) {
			defer wg.Done()
			
			task := Task{
				ID: fmt.Sprintf("task-%d", taskID),
				Fn: func(ctx context.Context) error {
					time.Sleep(10 * time.Millisecond) // Simulate work
					atomic.AddInt64(&completed, 1)
					return nil
				},
			}

			if err := wp.Submit(task); err != nil {
				t.Errorf("Submit() failed for task %d: %v", taskID, err)
			}
		}(i)
	}

	wg.Wait()

	// Wait for all tasks to complete
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt64(&completed) < numTasks && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}

	if atomic.LoadInt64(&completed) != numTasks {
		t.Errorf("Expected %d completed tasks, got %d", numTasks, atomic.LoadInt64(&completed))
	}

	stats := wp.Stats()
	if stats.TotalJobs != numTasks {
		t.Errorf("Expected %d total jobs, got %d", numTasks, stats.TotalJobs)
	}
}

func TestWorkerPoolSubmitWithTimeout(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   2,
		QueueSize: 5,
	})

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(5 * time.Second)

	// Test successful submission with timeout
	task := Task{
		ID: "timeout-test",
		Fn: func(ctx context.Context) error {
			return nil
		},
	}

	if err := wp.SubmitWithTimeout(task, 1*time.Second); err != nil {
		t.Errorf("SubmitWithTimeout() failed: %v", err)
	}

	// Fill up the queue to test timeout
	for i := 0; i < 10; i++ {
		blockingTask := Task{
			ID: fmt.Sprintf("blocking-%d", i),
			Fn: func(ctx context.Context) error {
				time.Sleep(2 * time.Second)
				return nil
			},
		}
		wp.Submit(blockingTask)
	}

	// This should timeout
	timeoutTask := Task{
		ID: "should-timeout",
		Fn: func(ctx context.Context) error {
			return nil
		},
	}

	err := wp.SubmitWithTimeout(timeoutTask, 100*time.Millisecond)
	if err == nil {
		t.Error("SubmitWithTimeout() should have timed out")
	}
}

func TestWorkerPoolQueueFull(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:      1,
		QueueSize:    2,
		MaxQueueSize: 2,
	})

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(5 * time.Second)

	// Fill the queue
	for i := 0; i < 3; i++ {
		task := Task{
			ID: fmt.Sprintf("fill-%d", i),
			Fn: func(ctx context.Context) error {
				time.Sleep(1 * time.Second) // Block to fill queue
				return nil
			},
		}
		wp.Submit(task)
	}

	// This should fail due to full queue
	overflowTask := Task{
		ID: "overflow",
		Fn: func(ctx context.Context) error {
			return nil
		},
	}

	err := wp.Submit(overflowTask)
	if err == nil {
		t.Error("Submit() should fail when queue is full")
	}
}

func TestWorkerPoolContextCancellation(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   2,
		QueueSize: 10,
	})

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(5 * time.Second)

	cancelled := make(chan bool, 1)
	task := Task{
		ID: "context-test",
		Fn: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				cancelled <- true
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return errors.New("task should have been cancelled")
			}
		},
	}

	if err := wp.Submit(task); err != nil {
		t.Fatalf("Submit() failed: %v", err)
	}

	// Stop the worker pool to trigger context cancellation
	go func() {
		time.Sleep(100 * time.Millisecond)
		wp.Stop(5 * time.Second)
	}()

	select {
	case <-cancelled:
		// Task was cancelled as expected
	case <-time.After(3 * time.Second):
		t.Error("Task was not cancelled within timeout")
	}
}

func TestWorkerPoolStats(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   3,
		QueueSize: 20,
	})

	stats := wp.Stats()
	if stats.IsRunning {
		t.Error("WorkerPool should not be running initially")
	}

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(5 * time.Second)

	// Submit some tasks
	const numTasks = 10
	for i := 0; i < numTasks; i++ {
		task := Task{
			ID: fmt.Sprintf("stats-test-%d", i),
			Fn: func(ctx context.Context) error {
				if i%3 == 0 { // Fail every 3rd task
					return errors.New("intentional failure")
				}
				return nil
			},
		}
		wp.Submit(task)
	}

	// Wait for tasks to complete
	time.Sleep(1 * time.Second)

	stats = wp.Stats()
	if stats.Workers != 3 {
		t.Errorf("Expected 3 workers, got %d", stats.Workers)
	}
	if stats.TotalJobs != numTasks {
		t.Errorf("Expected %d total jobs, got %d", numTasks, stats.TotalJobs)
	}
	if stats.FailedJobs == 0 {
		t.Error("Expected some failed jobs")
	}
	if !stats.IsRunning {
		t.Error("WorkerPool should be running")
	}
}

func TestWorkerPoolGracefulShutdown(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   2,
		QueueSize: 5,
	})

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Submit long-running tasks
	var completed int64
	for i := 0; i < 3; i++ {
		task := Task{
			ID: fmt.Sprintf("long-task-%d", i),
			Fn: func(ctx context.Context) error {
				time.Sleep(500 * time.Millisecond)
				atomic.AddInt64(&completed, 1)
				return nil
			},
		}
		wp.Submit(task)
	}

	start := time.Now()
	if err := wp.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	elapsed := time.Since(start)

	// Should complete within reasonable time
	if elapsed > 3*time.Second {
		t.Errorf("Stop took too long: %v", elapsed)
	}

	// All tasks should have completed
	if atomic.LoadInt64(&completed) != 3 {
		t.Errorf("Expected 3 completed tasks, got %d", atomic.LoadInt64(&completed))
	}
}

func TestWorkerPoolSubmitAfterStop(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   2,
		QueueSize: 5,
	})

	if err := wp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if err := wp.Stop(1 * time.Second); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Should not accept tasks after stop
	task := Task{
		ID: "after-stop",
		Fn: func(ctx context.Context) error {
			return nil
		},
	}

	err := wp.Submit(task)
	if err == nil {
		t.Error("Submit() should fail after worker pool is stopped")
	}
}

func BenchmarkWorkerPoolSubmit(b *testing.B) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   runtime.NumCPU(),
		QueueSize: 10000,
	})

	if err := wp.Start(); err != nil {
		b.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(10 * time.Second)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			task := Task{
				ID: "bench-task",
				Fn: func(ctx context.Context) error {
					// Minimal work to test submission overhead
					return nil
				},
			}
			wp.Submit(task)
		}
	})
}

func BenchmarkWorkerPoolExecution(b *testing.B) {
	wp := NewWorkerPool(WorkerPoolConfig{
		Workers:   runtime.NumCPU(),
		QueueSize: 10000,
	})

	if err := wp.Start(); err != nil {
		b.Fatalf("Start() failed: %v", err)
	}
	defer wp.Stop(10 * time.Second)

	var completed int64
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		task := Task{
			ID: fmt.Sprintf("bench-task-%d", i),
			Fn: func(ctx context.Context) error {
				atomic.AddInt64(&completed, 1)
				return nil
			},
		}
		wp.Submit(task)
	}

	// Wait for all tasks to complete
	for atomic.LoadInt64(&completed) < int64(b.N) {
		time.Sleep(time.Millisecond)
	}
}