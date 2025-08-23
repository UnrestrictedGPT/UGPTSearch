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

func TestPriorityQueueCreation(t *testing.T) {
	pq := NewPriorityQueue(100)
	
	if pq.capacity != 100 {
		t.Errorf("Expected capacity 100, got %d", pq.capacity)
	}
	if pq.Size() != 0 {
		t.Errorf("Expected initial size 0, got %d", pq.Size())
	}
}

func TestPriorityQueueOperations(t *testing.T) {
	pq := NewPriorityQueue(10)
	
	// Test Push
	items := []*QueueItem{
		{ID: "low", Priority: PriorityLow, SubmittedAt: time.Now()},
		{ID: "high", Priority: PriorityHigh, SubmittedAt: time.Now()},
		{ID: "normal", Priority: PriorityNormal, SubmittedAt: time.Now()},
		{ID: "urgent", Priority: PriorityUrgent, SubmittedAt: time.Now()},
	}

	for _, item := range items {
		err := pq.Push2(item)
		if err != nil {
			t.Errorf("Push2 failed: %v", err)
		}
	}

	if pq.Size() != len(items) {
		t.Errorf("Expected size %d, got %d", len(items), pq.Size())
	}

	// Test Pop - should return highest priority first
	expectedOrder := []string{"urgent", "high", "normal", "low"}
	for i, expected := range expectedOrder {
		item := pq.Pop2()
		if item == nil {
			t.Fatalf("Pop2 returned nil at index %d", i)
		}
		if item.ID != expected {
			t.Errorf("Expected ID %s, got %s", expected, item.ID)
		}
	}

	if pq.Size() != 0 {
		t.Errorf("Expected size 0 after popping all items, got %d", pq.Size())
	}
}

func TestPriorityQueueCapacity(t *testing.T) {
	pq := NewPriorityQueue(2)
	
	// Fill to capacity
	err := pq.Push2(&QueueItem{ID: "1", Priority: PriorityNormal, SubmittedAt: time.Now()})
	if err != nil {
		t.Errorf("First push should succeed: %v", err)
	}
	
	err = pq.Push2(&QueueItem{ID: "2", Priority: PriorityNormal, SubmittedAt: time.Now()})
	if err != nil {
		t.Errorf("Second push should succeed: %v", err)
	}
	
	// Should fail when at capacity
	err = pq.Push2(&QueueItem{ID: "3", Priority: PriorityNormal, SubmittedAt: time.Now()})
	if err == nil {
		t.Error("Push should fail when at capacity")
	}
}

func TestPriorityQueueTimeOrdering(t *testing.T) {
	pq := NewPriorityQueue(10)
	
	baseTime := time.Now()
	
	// Add items with same priority but different times
	items := []*QueueItem{
		{ID: "third", Priority: PriorityNormal, SubmittedAt: baseTime.Add(2 * time.Second)},
		{ID: "first", Priority: PriorityNormal, SubmittedAt: baseTime},
		{ID: "second", Priority: PriorityNormal, SubmittedAt: baseTime.Add(1 * time.Second)},
	}

	for _, item := range items {
		pq.Push2(item)
	}

	// Should come out in time order when priorities are equal
	expectedOrder := []string{"first", "second", "third"}
	for i, expected := range expectedOrder {
		item := pq.Pop2()
		if item == nil {
			t.Fatalf("Pop2 returned nil at index %d", i)
		}
		if item.ID != expected {
			t.Errorf("Expected ID %s, got %s", expected, item.ID)
		}
	}
}

func TestRequestQueueCreation(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 2, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	tests := []struct {
		name   string
		config QueueConfig
		wantErr bool
	}{
		{
			name: "valid configuration",
			config: QueueConfig{
				MaxSize: 100,
				WorkerPool: wp,
				Processor: func(ctx context.Context, item *QueueItem) (interface{}, error) {
					return "result", nil
				},
				RetryDelay: 1 * time.Second,
				DeadLetterCap: 50,
			},
			wantErr: false,
		},
		{
			name: "missing worker pool",
			config: QueueConfig{
				MaxSize: 100,
				Processor: func(ctx context.Context, item *QueueItem) (interface{}, error) {
					return "result", nil
				},
			},
			wantErr: true,
		},
		{
			name: "missing processor",
			config: QueueConfig{
				MaxSize: 100,
				WorkerPool: wp,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rq, err := NewRequestQueue(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRequestQueue() error = %v, wantErr %v", err, tt.wantErr)
			}
			if rq != nil {
				defer func() {
					rq.Stop(1 * time.Second)
				}()
			}
		})
	}
}

func TestRequestQueueBasicProcessing(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 2, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	processed := make(chan string, 10)
	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		data := item.Data.(string)
		processed <- data
		return fmt.Sprintf("processed-%s", data), nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit items
	testData := []string{"item1", "item2", "item3"}
	for i, data := range testData {
		item := &QueueItem{
			ID: fmt.Sprintf("test-%d", i),
			Priority: PriorityNormal,
			Data: data,
			Context: context.Background(),
			ResultChan: make(chan QueueResult, 1),
		}

		err := rq.Submit(item)
		if err != nil {
			t.Errorf("Submit failed for %s: %v", data, err)
			continue
		}

		// Wait for result
		select {
		case result := <-item.ResultChan:
			if result.Error != nil {
				t.Errorf("Processing failed for %s: %v", data, result.Error)
			} else {
				expected := fmt.Sprintf("processed-%s", data)
				if result.Data != expected {
					t.Errorf("Expected result %s, got %v", expected, result.Data)
				}
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Timeout waiting for result for %s", data)
		}
	}

	// Verify all items were processed
	processedItems := make(map[string]bool)
	timeout := time.After(2 * time.Second)
	
	for len(processedItems) < len(testData) {
		select {
		case item := <-processed:
			processedItems[item] = true
		case <-timeout:
			t.Error("Timeout waiting for all items to be processed")
			break
		}
	}

	for _, data := range testData {
		if !processedItems[data] {
			t.Errorf("Item %s was not processed", data)
		}
	}
}

func TestRequestQueuePriorityProcessing(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 1, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	processOrder := make([]string, 0)
	var mu sync.Mutex
	
	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		mu.Lock()
		processOrder = append(processOrder, item.ID)
		mu.Unlock()
		return nil, nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit items with different priorities
	items := []*QueueItem{
		{ID: "low", Priority: PriorityLow, Data: "low-data", Context: context.Background(), ResultChan: make(chan QueueResult, 1)},
		{ID: "urgent", Priority: PriorityUrgent, Data: "urgent-data", Context: context.Background(), ResultChan: make(chan QueueResult, 1)},
		{ID: "normal", Priority: PriorityNormal, Data: "normal-data", Context: context.Background(), ResultChan: make(chan QueueResult, 1)},
		{ID: "high", Priority: PriorityHigh, Data: "high-data", Context: context.Background(), ResultChan: make(chan QueueResult, 1)},
	}

	for _, item := range items {
		err := rq.Submit(item)
		if err != nil {
			t.Errorf("Submit failed for %s: %v", item.ID, err)
		}
	}

	// Wait for all to be processed
	for _, item := range items {
		select {
		case <-item.ResultChan:
			// Item processed
		case <-time.After(3 * time.Second):
			t.Errorf("Timeout waiting for %s to be processed", item.ID)
		}
	}

	// Check processing order
	mu.Lock()
	defer mu.Unlock()
	
	if len(processOrder) != len(items) {
		t.Fatalf("Expected %d processed items, got %d", len(items), len(processOrder))
	}

	// Should be processed in priority order: urgent, high, normal, low
	expectedOrder := []string{"urgent", "high", "normal", "low"}
	for i, expected := range expectedOrder {
		if processOrder[i] != expected {
			t.Errorf("Expected processing order %v, got %v", expectedOrder, processOrder)
			break
		}
	}
}

func TestRequestQueueRetryLogic(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 1, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	attemptCount := int64(0)
	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		count := atomic.AddInt64(&attemptCount, 1)
		if count <= 2 { // Fail first 2 attempts
			return nil, errors.New("simulated failure")
		}
		return "success", nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
		RetryDelay: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit item that will fail initially
	item := &QueueItem{
		ID: "retry-test",
		Priority: PriorityNormal,
		Data: "test-data",
		Context: context.Background(),
		ResultChan: make(chan QueueResult, 1),
		MaxRetries: 3,
	}

	err = rq.Submit(item)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for final result
	select {
	case result := <-item.ResultChan:
		if result.Error != nil {
			t.Errorf("Expected success after retries, got error: %v", result.Error)
		}
		if result.Data != "success" {
			t.Errorf("Expected 'success', got %v", result.Data)
		}
	case <-time.After(5 * time.Second):
		t.Error("Timeout waiting for retry to succeed")
	}

	// Should have attempted 3 times
	if atomic.LoadInt64(&attemptCount) != 3 {
		t.Errorf("Expected 3 attempts, got %d", atomic.LoadInt64(&attemptCount))
	}

	stats := rq.GetStats()
	if stats.RetryItems == 0 {
		t.Error("Expected some retry items in stats")
	}
}

func TestRequestQueueDeadLetter(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 1, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		return nil, errors.New("always fail")
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
		RetryDelay: 50 * time.Millisecond,
		DeadLetterCap: 10,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit item that will always fail
	item := &QueueItem{
		ID: "dead-letter-test",
		Priority: PriorityNormal,
		Data: "test-data",
		Context: context.Background(),
		ResultChan: make(chan QueueResult, 1),
		MaxRetries: 2,
	}

	err = rq.Submit(item)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for final failure
	select {
	case result := <-item.ResultChan:
		if result.Error == nil {
			t.Error("Expected error after max retries")
		}
	case <-time.After(3 * time.Second):
		t.Error("Timeout waiting for final failure")
	}

	// Check dead letter queue
	deadLetterItems := rq.GetDeadLetterItems()
	if len(deadLetterItems) != 1 {
		t.Errorf("Expected 1 dead letter item, got %d", len(deadLetterItems))
	}
	if len(deadLetterItems) > 0 && deadLetterItems[0].ID != "dead-letter-test" {
		t.Errorf("Expected dead letter item ID 'dead-letter-test', got %s", deadLetterItems[0].ID)
	}

	stats := rq.GetStats()
	if stats.FailedItems == 0 {
		t.Error("Expected some failed items in stats")
	}
}

func TestRequestQueueRequeue(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 1, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	attemptCount := int64(0)
	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		count := atomic.AddInt64(&attemptCount, 1)
		if item.ID == "requeue-test" && count == 1 {
			return nil, errors.New("initial failure")
		}
		return "success", nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
		RetryDelay: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit item that will fail initially
	item := &QueueItem{
		ID: "requeue-test",
		Priority: PriorityNormal,
		Data: "test-data",
		Context: context.Background(),
		ResultChan: make(chan QueueResult, 1),
		MaxRetries: 0, // No retries, should go to dead letter
	}

	err = rq.Submit(item)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for failure
	select {
	case result := <-item.ResultChan:
		if result.Error == nil {
			t.Error("Expected error on first attempt")
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for initial failure")
	}

	// Should be in dead letter queue
	deadLetterItems := rq.GetDeadLetterItems()
	if len(deadLetterItems) != 1 {
		t.Fatalf("Expected 1 dead letter item, got %d", len(deadLetterItems))
	}

	// Requeue the item
	err = rq.RequeueDeadLetterItem("requeue-test")
	if err != nil {
		t.Fatalf("Requeue failed: %v", err)
	}

	// Should succeed on requeue
	select {
	case result := <-item.ResultChan:
		if result.Error != nil {
			t.Errorf("Expected success after requeue, got error: %v", result.Error)
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for requeue result")
	}

	// Should no longer be in dead letter queue
	deadLetterItems = rq.GetDeadLetterItems()
	if len(deadLetterItems) != 0 {
		t.Errorf("Expected 0 dead letter items after requeue, got %d", len(deadLetterItems))
	}
}

func TestRequestQueueSubmitWithPriority(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 1, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	processOrder := make([]string, 0)
	var mu sync.Mutex
	
	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		mu.Lock()
		processOrder = append(processOrder, item.Data.(string))
		mu.Unlock()
		return nil, nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit items using SubmitWithPriority
	testData := []struct{
		data string
		priority int
	}{
		{"low", PriorityLow},
		{"urgent", PriorityUrgent},
		{"normal", PriorityNormal},
	}

	items := make([]*QueueItem, 0)
	for _, td := range testData {
		item, err := rq.SubmitWithPriority(td.data, td.priority, context.Background())
		if err != nil {
			t.Errorf("SubmitWithPriority failed for %s: %v", td.data, err)
			continue
		}
		items = append(items, item)
	}

	// Wait for all to be processed
	for _, item := range items {
		select {
		case <-item.ResultChan:
			// Item processed
		case <-time.After(3 * time.Second):
			t.Error("Timeout waiting for items to be processed")
		}
	}

	// Check processing order
	mu.Lock()
	defer mu.Unlock()
	
	expectedOrder := []string{"urgent", "normal", "low"}
	if len(processOrder) != len(expectedOrder) {
		t.Fatalf("Expected %d processed items, got %d", len(expectedOrder), len(processOrder))
	}

	for i, expected := range expectedOrder {
		if processOrder[i] != expected {
			t.Errorf("Expected processing order %v, got %v", expectedOrder, processOrder)
			break
		}
	}
}

func TestRequestQueueContextCancellation(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 1, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return "completed", nil
		}
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit item with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	item := &QueueItem{
		ID: "cancel-test",
		Priority: PriorityNormal,
		Data: "test-data",
		Context: ctx,
		ResultChan: make(chan QueueResult, 1),
	}

	err = rq.Submit(item)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Cancel context immediately
	cancel()

	// Should get cancellation error
	select {
	case result := <-item.ResultChan:
		if result.Error == nil {
			t.Error("Expected cancellation error")
		}
		if result.Error != context.Canceled {
			t.Errorf("Expected context.Canceled, got %v", result.Error)
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for cancellation result")
	}
}

func TestRequestQueueConcurrentSubmissions(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 5, QueueSize: 100})
	wp.Start()
	defer wp.Stop(10 * time.Second)

	var processed int64
	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		atomic.AddInt64(&processed, 1)
		time.Sleep(time.Millisecond) // Simulate some work
		return fmt.Sprintf("result-%s", item.ID), nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 1000,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(10 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	const numGoroutines = 10
	const itemsPerGoroutine = 20
	var wg sync.WaitGroup
	var submitErrors int64

	// Submit items concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < itemsPerGoroutine; j++ {
				item := &QueueItem{
					ID: fmt.Sprintf("item-%d-%d", goroutineID, j),
					Priority: PriorityNormal,
					Data: fmt.Sprintf("data-%d-%d", goroutineID, j),
					Context: context.Background(),
					ResultChan: make(chan QueueResult, 1),
				}

				err := rq.Submit(item)
				if err != nil {
					atomic.AddInt64(&submitErrors, 1)
					continue
				}

				// Don't wait for result to avoid blocking
				go func() {
					select {
					case <-item.ResultChan:
						// Result received
					case <-time.After(10 * time.Second):
						// Timeout, but don't fail test - high load scenario
					}
				}()
			}
		}(i)
	}

	wg.Wait()

	// Wait for processing to complete
	totalExpected := int64(numGoroutines * itemsPerGoroutine)
	timeout := time.After(15 * time.Second)
	
	for atomic.LoadInt64(&processed) < totalExpected-atomic.LoadInt64(&submitErrors) {
		select {
		case <-timeout:
			break // Timeout reached
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	processedCount := atomic.LoadInt64(&processed)
	submitErrorCount := atomic.LoadInt64(&submitErrors)

	if processedCount == 0 {
		t.Error("No items were processed")
	}

	t.Logf("Processed %d items with %d submit errors", processedCount, submitErrorCount)

	stats := rq.GetStats()
	if stats.TotalItems == 0 {
		t.Error("Expected some total items in stats")
	}
}

func TestRequestQueueStats(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 2, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		if item.ID == "fail-item" {
			return nil, errors.New("intentional failure")
		}
		return "success", nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit various items
	items := []struct{
		id string
		shouldFail bool
	}{
		{"success-1", false},
		{"success-2", false},
		{"fail-item", true},
		{"success-3", false},
	}

	for _, item := range items {
		queueItem := &QueueItem{
			ID: item.id,
			Priority: PriorityNormal,
			Data: "test-data",
			Context: context.Background(),
			ResultChan: make(chan QueueResult, 1),
			MaxRetries: 0, // No retries to make stats predictable
		}

		err := rq.Submit(queueItem)
		if err != nil {
			t.Errorf("Submit failed for %s: %v", item.id, err)
			continue
		}

		// Wait for result
		select {
		case result := <-queueItem.ResultChan:
			if item.shouldFail && result.Error == nil {
				t.Errorf("Expected %s to fail", item.id)
			} else if !item.shouldFail && result.Error != nil {
				t.Errorf("Expected %s to succeed, got error: %v", item.id, result.Error)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("Timeout waiting for %s", item.id)
		}
	}

	stats := rq.GetStats()
	if stats.TotalItems != int64(len(items)) {
		t.Errorf("Expected %d total items, got %d", len(items), stats.TotalItems)
	}
	if stats.ProcessedItems != 3 {
		t.Errorf("Expected 3 processed items, got %d", stats.ProcessedItems)
	}
	if stats.FailedItems != 1 {
		t.Errorf("Expected 1 failed item, got %d", stats.FailedItems)
	}
}

func TestRequestQueueClear(t *testing.T) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 1, QueueSize: 10})
	wp.Start()
	defer wp.Stop(5 * time.Second)

	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		time.Sleep(100 * time.Millisecond) // Slow processing
		return "result", nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		t.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(5 * time.Second)

	err = rq.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Submit several items
	for i := 0; i < 5; i++ {
		item := &QueueItem{
			ID: fmt.Sprintf("item-%d", i),
			Priority: PriorityNormal,
			Data: "test-data",
			Context: context.Background(),
			ResultChan: make(chan QueueResult, 1),
		}
		rq.Submit(item)
	}

	// Check queue has items
	stats := rq.GetStats()
	if stats.QueueLength == 0 {
		t.Error("Expected items in queue before clear")
	}

	// Clear the queue
	rq.Clear()

	// Check queue is empty
	stats = rq.GetStats()
	if stats.QueueLength != 0 {
		t.Errorf("Expected empty queue after clear, got %d items", stats.QueueLength)
	}
}

func BenchmarkRequestQueueSubmit(b *testing.B) {
	wp := NewWorkerPool(WorkerPoolConfig{Workers: runtime.NumCPU(), QueueSize: 10000})
	wp.Start()
	defer wp.Stop(10 * time.Second)

	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		return nil, nil
	}

	rq, err := NewRequestQueue(QueueConfig{
		MaxSize: 100000,
		WorkerPool: wp,
		Processor: processor,
	})
	if err != nil {
		b.Fatalf("NewRequestQueue failed: %v", err)
	}
	defer rq.Stop(10 * time.Second)

	rq.Start()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			item := &QueueItem{
				ID: "bench-item",
				Priority: PriorityNormal,
				Data: "bench-data",
				Context: context.Background(),
				ResultChan: make(chan QueueResult, 1),
			}
			rq.Submit(item)
		}
	})
}

func BenchmarkPriorityQueueOperations(b *testing.B) {
	pq := NewPriorityQueue(10000)

	// Pre-populate with items
	for i := 0; i < 1000; i++ {
		item := &QueueItem{
			ID: fmt.Sprintf("item-%d", i),
			Priority: i % 4, // Mix of priorities
			SubmittedAt: time.Now(),
		}
		pq.Push2(item)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Alternate between push and pop
		if i%2 == 0 && pq.Size() < 5000 {
			item := &QueueItem{
				ID: fmt.Sprintf("bench-item-%d", i),
				Priority: i % 4,
				SubmittedAt: time.Now(),
			}
			pq.Push2(item)
		} else if pq.Size() > 0 {
			pq.Pop2()
		}
	}
}