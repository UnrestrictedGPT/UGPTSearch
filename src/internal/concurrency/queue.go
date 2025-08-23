package concurrency

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Priority levels for queue items
const (
	PriorityLow = iota
	PriorityNormal
	PriorityHigh
	PriorityUrgent
)

// QueueItem represents an item in the priority queue
type QueueItem struct {
	ID          string
	Priority    int
	Data        interface{}
	SubmittedAt time.Time
	ProcessedAt *time.Time
	Context     context.Context
	ResultChan  chan QueueResult
	RetryCount  int
	MaxRetries  int
}

// QueueResult represents the result of processing a queue item
type QueueResult struct {
	Item  *QueueItem
	Error error
	Data  interface{}
}

// PriorityQueue implements a priority queue using a heap
type PriorityQueue struct {
	items    []*QueueItem
	capacity int
	mu       sync.Mutex
}

// RequestQueue manages a priority queue with worker processing
type RequestQueue struct {
	queue       *PriorityQueue
	workers     *WorkerPool
	mu          sync.RWMutex
	maxSize     int
	processor   func(ctx context.Context, item *QueueItem) (interface{}, error)
	stats       *QueueStats
	deadLetterQ *DeadLetterQueue
	retryDelay  time.Duration
	running     int64
	stopped     int64
}

// QueueStats tracks queue performance metrics
type QueueStats struct {
	TotalItems      int64         `json:"total_items"`
	ProcessedItems  int64         `json:"processed_items"`
	FailedItems     int64         `json:"failed_items"`
	RetryItems      int64         `json:"retry_items"`
	QueueLength     int64         `json:"queue_length"`
	AverageWaitTime time.Duration `json:"average_wait_time"`
	ProcessingTime  time.Duration `json:"processing_time"`
	Throughput      float64       `json:"throughput"` // items per second
	lastUpdate      time.Time
}

// DeadLetterQueue handles items that repeatedly fail processing
type DeadLetterQueue struct {
	items   []*QueueItem
	maxSize int
	mu      sync.RWMutex
}

// QueueConfig holds configuration for the request queue
type QueueConfig struct {
	MaxSize       int                                                             // Maximum queue size
	WorkerPool    *WorkerPool                                                     // Worker pool to process items
	Processor     func(ctx context.Context, item *QueueItem) (interface{}, error) // Item processor function
	RetryDelay    time.Duration                                                   // Delay between retries
	DeadLetterCap int                                                             // Dead letter queue capacity
}

// Len implements heap.Interface
func (pq *PriorityQueue) Len() int {
	return len(pq.items)
}

// Less implements heap.Interface (higher priority items come first)
func (pq *PriorityQueue) Less(i, j int) bool {
	if pq.items[i].Priority == pq.items[j].Priority {
		// If same priority, earlier items come first
		return pq.items[i].SubmittedAt.Before(pq.items[j].SubmittedAt)
	}
	return pq.items[i].Priority > pq.items[j].Priority
}

// Swap implements heap.Interface
func (pq *PriorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
}

// Push implements heap.Interface
func (pq *PriorityQueue) Push(x interface{}) {
	item := x.(*QueueItem)
	pq.items = append(pq.items, item)
}

// Pop implements heap.Interface
func (pq *PriorityQueue) Pop() interface{} {
	old := pq.items
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	pq.items = old[0 : n-1]
	return item
}

// NewPriorityQueue creates a new priority queue
func NewPriorityQueue(capacity int) *PriorityQueue {
	if capacity <= 0 {
		capacity = 1000
	}

	return &PriorityQueue{
		items:    make([]*QueueItem, 0, capacity),
		capacity: capacity,
	}
}

// Push adds an item to the priority queue
func (pq *PriorityQueue) Push2(item *QueueItem) error {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if len(pq.items) >= pq.capacity {
		return fmt.Errorf("queue is at maximum capacity (%d)", pq.capacity)
	}

	heap.Push(pq, item)
	return nil
}

// Pop removes and returns the highest priority item
func (pq *PriorityQueue) Pop2() *QueueItem {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if len(pq.items) == 0 {
		return nil
	}

	return heap.Pop(pq).(*QueueItem)
}

// Size returns the current size of the queue
func (pq *PriorityQueue) Size() int {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return len(pq.items)
}

// NewRequestQueue creates a new request queue
func NewRequestQueue(config QueueConfig) (*RequestQueue, error) {
	if config.MaxSize <= 0 {
		config.MaxSize = 10000
	}
	if config.WorkerPool == nil {
		return nil, fmt.Errorf("worker pool is required")
	}
	if config.Processor == nil {
		return nil, fmt.Errorf("processor function is required")
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = 5 * time.Second
	}
	if config.DeadLetterCap <= 0 {
		config.DeadLetterCap = 1000
	}

	return &RequestQueue{
		queue:      NewPriorityQueue(config.MaxSize),
		workers:    config.WorkerPool,
		maxSize:    config.MaxSize,
		processor:  config.Processor,
		retryDelay: config.RetryDelay,
		stats:      &QueueStats{lastUpdate: time.Now()},
		deadLetterQ: &DeadLetterQueue{
			items:   make([]*QueueItem, 0),
			maxSize: config.DeadLetterCap,
		},
	}, nil
}

// Start begins processing items in the queue
func (rq *RequestQueue) Start() error {
	if !atomic.CompareAndSwapInt64(&rq.running, 0, 1) {
		return fmt.Errorf("queue is already running")
	}

	// Start the main processing loop
	go rq.processLoop()

	return nil
}

// Stop stops processing items in the queue
func (rq *RequestQueue) Stop(timeout time.Duration) error {
	if !atomic.CompareAndSwapInt64(&rq.stopped, 0, 1) {
		return fmt.Errorf("queue is already stopped")
	}

	// Wait for current processing to finish
	start := time.Now()
	for {
		if rq.queue.Size() == 0 && atomic.LoadInt64(&rq.stats.ProcessedItems) > 0 {
			break
		}
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for queue to drain")
		}
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// Submit adds an item to the queue for processing
func (rq *RequestQueue) Submit(item *QueueItem) error {
	if atomic.LoadInt64(&rq.stopped) == 1 {
		return fmt.Errorf("queue is stopped")
	}

	if item.Context == nil {
		item.Context = context.Background()
	}
	if item.ResultChan == nil {
		item.ResultChan = make(chan QueueResult, 1)
	}
	if item.MaxRetries <= 0 {
		item.MaxRetries = 3
	}

	item.SubmittedAt = time.Now()
	err := rq.queue.Push2(item)
	if err != nil {
		return err
	}

	atomic.AddInt64(&rq.stats.TotalItems, 1)
	atomic.StoreInt64(&rq.stats.QueueLength, int64(rq.queue.Size()))
	return nil
}

// SubmitWithPriority adds an item with specified priority
func (rq *RequestQueue) SubmitWithPriority(data interface{}, priority int, ctx context.Context) (*QueueItem, error) {
	item := &QueueItem{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Priority:   priority,
		Data:       data,
		Context:    ctx,
		ResultChan: make(chan QueueResult, 1),
		MaxRetries: 3,
	}

	err := rq.Submit(item)
	return item, err
}

// processLoop is the main processing loop
func (rq *RequestQueue) processLoop() {
	for atomic.LoadInt64(&rq.stopped) == 0 {
		item := rq.queue.Pop2()
		if item == nil {
			time.Sleep(10 * time.Millisecond) // Brief pause when queue is empty
			continue
		}

		// Check if context is cancelled
		select {
		case <-item.Context.Done():
			rq.sendResult(item, nil, item.Context.Err())
			continue
		default:
		}

		// Submit to worker pool
		task := Task{
			ID: item.ID,
			Fn: rq.createProcessorTask(item),
			Callback: func(err error) {
				// This callback is handled in createProcessorTask
			},
		}

		err := rq.workers.Submit(task)
		if err != nil {
			// If worker pool is full, retry after delay
			if item.RetryCount < item.MaxRetries {
				item.RetryCount++
				atomic.AddInt64(&rq.stats.RetryItems, 1)

				// Add delay and requeue
				go func() {
					time.Sleep(rq.retryDelay)
					rq.queue.Push2(item)
				}()
			} else {
				rq.moveToDeadLetter(item)
				rq.sendResult(item, nil, fmt.Errorf("max retries exceeded: %w", err))
			}
		}

		atomic.StoreInt64(&rq.stats.QueueLength, int64(rq.queue.Size()))
	}
}

// createProcessorTask creates a task function for the worker pool
func (rq *RequestQueue) createProcessorTask(item *QueueItem) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		start := time.Now()

		// Use the item's context if it's not cancelled, otherwise use the worker context
		processCtx := item.Context
		if processCtx.Err() != nil {
			processCtx = ctx
		}

		// Process the item
		result, err := rq.processor(processCtx, item)

		// Update processing time
		processingTime := time.Since(start)
		rq.updateProcessingTime(processingTime)

		now := time.Now()
		item.ProcessedAt = &now

		if err != nil {
			// Handle retry logic
			if item.RetryCount < item.MaxRetries {
				item.RetryCount++
				atomic.AddInt64(&rq.stats.RetryItems, 1)

				// Add delay and requeue
				go func() {
					time.Sleep(rq.retryDelay)
					rq.queue.Push2(item)
				}()
				return err
			} else {
				// Max retries exceeded, move to dead letter queue
				rq.moveToDeadLetter(item)
				atomic.AddInt64(&rq.stats.FailedItems, 1)
			}
		} else {
			atomic.AddInt64(&rq.stats.ProcessedItems, 1)
		}

		// Send result
		rq.sendResult(item, result, err)

		// Update wait time statistics
		waitTime := start.Sub(item.SubmittedAt)
		rq.updateWaitTime(waitTime)

		return err
	}
}

// sendResult sends the result to the item's result channel
func (rq *RequestQueue) sendResult(item *QueueItem, result interface{}, err error) {
	select {
	case item.ResultChan <- QueueResult{Item: item, Data: result, Error: err}:
	default:
		// Channel is full or closed, ignore
	}
}

// moveToDeadLetter moves an item to the dead letter queue
func (rq *RequestQueue) moveToDeadLetter(item *QueueItem) {
	rq.deadLetterQ.mu.Lock()
	defer rq.deadLetterQ.mu.Unlock()

	if len(rq.deadLetterQ.items) >= rq.deadLetterQ.maxSize {
		// Remove oldest item if at capacity
		rq.deadLetterQ.items = rq.deadLetterQ.items[1:]
	}

	rq.deadLetterQ.items = append(rq.deadLetterQ.items, item)
}

// updateWaitTime updates the average wait time statistic
func (rq *RequestQueue) updateWaitTime(waitTime time.Duration) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	// Simple moving average
	if rq.stats.AverageWaitTime == 0 {
		rq.stats.AverageWaitTime = waitTime
	} else {
		rq.stats.AverageWaitTime = (rq.stats.AverageWaitTime + waitTime) / 2
	}
}

// updateProcessingTime updates the average processing time statistic
func (rq *RequestQueue) updateProcessingTime(processingTime time.Duration) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	// Simple moving average
	if rq.stats.ProcessingTime == 0 {
		rq.stats.ProcessingTime = processingTime
	} else {
		rq.stats.ProcessingTime = (rq.stats.ProcessingTime + processingTime) / 2
	}

	// Update throughput
	elapsed := time.Since(rq.stats.lastUpdate)
	if elapsed >= time.Second {
		processed := atomic.LoadInt64(&rq.stats.ProcessedItems)
		rq.stats.Throughput = float64(processed) / elapsed.Seconds()
		rq.stats.lastUpdate = time.Now()
	}
}

// GetStats returns current queue statistics
func (rq *RequestQueue) GetStats() *QueueStats {
	rq.mu.RLock()
	defer rq.mu.RUnlock()

	return &QueueStats{
		TotalItems:      atomic.LoadInt64(&rq.stats.TotalItems),
		ProcessedItems:  atomic.LoadInt64(&rq.stats.ProcessedItems),
		FailedItems:     atomic.LoadInt64(&rq.stats.FailedItems),
		RetryItems:      atomic.LoadInt64(&rq.stats.RetryItems),
		QueueLength:     atomic.LoadInt64(&rq.stats.QueueLength),
		AverageWaitTime: rq.stats.AverageWaitTime,
		ProcessingTime:  rq.stats.ProcessingTime,
		Throughput:      rq.stats.Throughput,
	}
}

// GetDeadLetterItems returns items in the dead letter queue
func (rq *RequestQueue) GetDeadLetterItems() []*QueueItem {
	rq.deadLetterQ.mu.RLock()
	defer rq.deadLetterQ.mu.RUnlock()

	items := make([]*QueueItem, len(rq.deadLetterQ.items))
	copy(items, rq.deadLetterQ.items)
	return items
}

// RequeueDeadLetterItem moves an item from dead letter queue back to main queue
func (rq *RequestQueue) RequeueDeadLetterItem(itemID string) error {
	rq.deadLetterQ.mu.Lock()
	defer rq.deadLetterQ.mu.Unlock()

	for i, item := range rq.deadLetterQ.items {
		if item.ID == itemID {
			// Reset retry count and requeue
			item.RetryCount = 0
			item.SubmittedAt = time.Now()

			err := rq.Submit(item)
			if err != nil {
				return err
			}

			// Remove from dead letter queue
			rq.deadLetterQ.items = append(rq.deadLetterQ.items[:i], rq.deadLetterQ.items[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("item with ID %s not found in dead letter queue", itemID)
}

// Clear empties the queue
func (rq *RequestQueue) Clear() {
	rq.queue.mu.Lock()
	defer rq.queue.mu.Unlock()

	rq.queue.items = rq.queue.items[:0]
	atomic.StoreInt64(&rq.stats.QueueLength, 0)
}
