package netsec

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentWriterReader 使用真实 goroutine 并发：写入方持续发布新记录，
// 读取方在 seqlock 保护下持续采样。任何被接受的记录都必须与某条已发布
// 记录逐字节相等；读取器要么拿到真实记录，要么明确返回不确定。
func TestConcurrentWriterReader(t *testing.T) {
	const capacity = 512
	page := NewMemPage(PageSize(capacity))
	writer, err := NewWriter(page, capacity)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	reader := NewReader(page, ReaderConfig{MaxAttempts: 16, MaxPayload: capacity})

	var (
		mu            sync.Mutex
		everPublished = make(map[[digestSize]byte]struct{})
		stop          atomic.Bool
		publishedN    atomic.Int64
		acceptedN     atomic.Int64
		uncertainN    atomic.Int64
	)

	register := func(payload []byte) {
		sum := sha256.Sum256(payload)
		mu.Lock()
		everPublished[sum] = struct{}{}
		mu.Unlock()
	}

	// 登记初始页面状态：零字段空记录（4 字节零）以及首条业务记录。
	empty, err := (Record{}).Encoded()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	register(empty)
	initial, err := (Record{Fields: []Field{{ID: 1, Type: FieldUint64, Data: uint64Bytes(0)}}}).Encoded()
	if err != nil {
		t.Fatalf("encode initial: %v", err)
	}
	register(initial)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(1); !stop.Load(); i++ {
			rec := Record{Fields: []Field{
				{ID: 1, Type: FieldUint64, Data: uint64Bytes(i)},
				{ID: 2, Type: FieldBytes, Data: []byte(time.Now().Format(time.RFC3339Nano))},
			}}
			payload, encErr := rec.Encoded()
			if encErr != nil {
				panic(encErr)
			}
			// 先登记再发布：保证记录一旦对读取器可见，就已在真实集合中。
			register(payload)
			if _, _, pubErr := writer.Publish(rec); pubErr != nil {
				panic(pubErr)
			}
			publishedN.Add(1)
		}
	}()

	deadline := time.Now().Add(300 * time.Millisecond)
	ctx := context.Background()
	for time.Now().Before(deadline) {
		rec, readErr := reader.Read(ctx)
		if readErr != nil {
			if errors.Is(readErr, ErrUncertain) {
				uncertainN.Add(1)
				continue
			}
			stop.Store(true)
			wg.Wait()
			t.Fatalf("unexpected read error: %v", readErr)
		}
		payload, encErr := rec.Encoded()
		if encErr != nil {
			t.Fatalf("encode accepted: %v", encErr)
		}
		sum := sha256.Sum256(payload)
		mu.Lock()
		_, ok := everPublished[sum]
		mu.Unlock()
		if !ok {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("fabricated record under concurrency: %x", payload)
		}
		acceptedN.Add(1)
	}

	stop.Store(true)
	wg.Wait()
	if publishedN.Load() == 0 || acceptedN.Load() == 0 {
		t.Fatalf("expected both publishes and accepts, got published=%d accepted=%d",
			publishedN.Load(), acceptedN.Load())
	}
	t.Logf("published=%d accepted=%d uncertain=%d",
		publishedN.Load(), acceptedN.Load(), uncertainN.Load())
}
