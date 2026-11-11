package repartition

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
)

// workload 是一次完整的重分区输入，所有步骤都可在重启后重放且幂等。
type workload struct {
	records  []Record
	cutovers []cutoverEvent
}

func buildWorkload() workload {
	var wl workload
	for off := int64(0); off <= 2; off++ {
		wl.records = append(wl.records, Record{Partition: 0, Offset: off, Key: fmt.Sprintf("old0-%d", off)})
	}
	for off := int64(0); off <= 1; off++ {
		wl.records = append(wl.records, Record{Partition: 1, Offset: off, Key: fmt.Sprintf("old1-%d", off)})
	}
	for _, p := range []int{10, 11} {
		for off := int64(0); off < 3; off++ {
			wl.records = append(wl.records, Record{
				Topology:  true,
				Partition: p,
				Offset:    off,
				Key:       fmt.Sprintf("new%d-%d", p, off),
			})
		}
	}
	wl.cutovers = []cutoverEvent{
		{Partition: 0, Offset: 2},
		{Partition: 1, Offset: 1},
	}
	return wl
}

func expectedIDs(wl workload) []string {
	var ids []string
	for _, r := range wl.records {
		ids = append(ids, r.id())
	}
	sort.Strings(ids)
	return ids
}

// driveOnce 在单个屏障生命周期内交替“发布可见记录 / 摄入 / 通知”，
// 遇到注入的写盘崩溃时返回 errInjectedCrash，模拟进程被杀掉。
func driveOnce(t *testing.T, b *Barrier, wl workload, sink *memSink, ingested, notified *int) error {
	t.Helper()
	for {
		if r, err := b.TryNext(); err == nil {
			if err := publishAck(t, b, sink, r); err != nil {
				return err
			}
			continue
		}

		if *ingested < len(wl.records) {
			err := b.Ingest(wl.records[*ingested])
			if err != nil {
				if errors.Is(err, errInjectedCrash) || errors.Is(err, ErrClosed) {
					return errInjectedCrash
				}
				return err
			}
			*ingested++
			continue
		}

		if *notified < len(wl.cutovers) {
			ev := wl.cutovers[*notified]
			err := b.NotifyCutover(ev.Partition, ev.Offset)
			if err != nil {
				if errors.Is(err, errInjectedCrash) || errors.Is(err, ErrClosed) {
					return errInjectedCrash
				}
				return err
			}
			*notified++
			continue
		}

		r, err := b.TryNext()
		if err != nil {
			if b.FullyDrained() {
				return nil
			}
			return fmt.Errorf("驱动提前空闲但未排空: %+v", b.Snapshot())
		}
		if err := publishAck(t, b, sink, r); err != nil {
			return err
		}
	}
}

// publishAck 遵循“投递标记落盘 -> 下游发布 -> 回执落盘”协议。
// 恢复后若投递标记已存在（上次在发布前后崩溃），仍重新调用 Publish，
// 由幂等 Sink 去重，然后补回执。
func publishAck(t *testing.T, b *Barrier, sink *memSink, r Record) error {
	t.Helper()
	if err := b.MarkDelivered(r); err != nil {
		if errors.Is(err, errInjectedCrash) || errors.Is(err, ErrClosed) {
			return errInjectedCrash
		}
		return err
	}
	if err := sink.Publish(context.Background(), r); err != nil {
		t.Fatalf("下游发布失败: %v", err)
	}
	if err := b.Ack(r); err != nil {
		if errors.Is(err, errInjectedCrash) || errors.Is(err, ErrClosed) {
			return errInjectedCrash
		}
		return err
	}
	return nil
}

func runToCompletion(t *testing.T, dir string, wl workload, sink *memSink, fault *FaultConfig) {
	t.Helper()
	ingested, notified := 0, 0
	for round := 0; ; round++ {
		var opts []Option
		if fault != nil && round == 0 {
			opts = append(opts, WithFault(*fault))
		}
		b, err := Open(dir, testConfig(), opts...)
		if err != nil {
			if errors.Is(err, errInjectedCrash) && fault != nil && round == 0 {
				continue // config 帧写入即崩溃：丢弃实例并重启，故障已耗尽
			}
			t.Fatalf("第 %d 轮打开屏障失败: %v", round, err)
		}
		crashed := errors.Is(driveOnce(t, b, wl, sink, &ingested, &notified), errInjectedCrash)
		_ = b.Close()
		if !crashed {
			b2, err := Open(dir, testConfig())
			if err != nil {
				t.Fatalf("收尾轮打开屏障失败: %v", err)
			}
			if !b2.FullyDrained() {
				t.Fatalf("收尾轮仍有未完成记录: %+v", b2.Snapshot())
			}
			_ = b2.Close()
			return
		}
		if round > 100 {
			t.Fatalf("崩溃重启轮数超限")
		}
	}
}

func countWALAppends(t *testing.T, path string) int {
	t.Helper()
	n := 0
	f, err := openWAL(filepath.Dir(path), func(frame) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.close()
	return n
}
