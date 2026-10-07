package importer

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
)

// 恢复阶段的显式错误：任一核对失败都会使导入明确停止。
var (
	// ErrSourceModified 源文件前缀摘要与检查点不符（如源中段被修改）。
	ErrSourceModified = errors.New("importer: source prefix mismatch, refusing to resume")
	// ErrTargetBehind 目标端已提交批号落后于检查点，状态不一致。
	ErrTargetBehind = errors.New("importer: sink is behind checkpoint, refusing to resume")
	// ErrReconcileFailed 回执丢失后对账失败（目标端批次与源内容对不上）。
	ErrReconcileFailed = errors.New("importer: failed to reconcile sink batches with source")
)

// Importer 从源文件按记录边界读取记录，分批提交到目标端，
// 并在每个批次确认后持久化检查点（偏移 + 前缀摘要 + 批号）。
type Importer struct {
	sourcePath string
	sink       Sink
	store      *CheckpointStore
	batchSize  int // 每批记录数
}

func NewImporter(sourcePath string, sink Sink, store *CheckpointStore, batchSize int) *Importer {
	if batchSize <= 0 {
		batchSize = 1
	}
	return &Importer{
		sourcePath: sourcePath,
		sink:       sink,
		store:      store,
		batchSize:  batchSize,
	}
}

// resumeState 是恢复核对通过后的续传起点。
type resumeState struct {
	offset int64
	batch  int64 // 已确认提交的批号
}

// recover 执行恢复核对：
//  1. 检查点必须完整（偏移 + 前缀摘要 + 批号），只存偏移直接拒绝；
//  2. 重新计算源文件前缀摘要并比对；
//  3. 与目标端批号对账：一致则续传；目标端超前（回执丢失）则逐批校验后快进；
//     目标端落后或校验不符则明确报错停止。
func (imp *Importer) recover() (resumeState, error) {
	cp, err := imp.store.Load()
	if err != nil {
		return resumeState{}, err
	}
	last, err := imp.sink.LastCommittedBatch()
	if err != nil {
		return resumeState{}, err
	}
	if cp == nil {
		if last != 0 {
			return resumeState{}, fmt.Errorf("%w: no checkpoint but sink has batch %d", ErrReconcileFailed, last)
		}
		return resumeState{offset: 0, batch: 0}, nil
	}

	digest, err := PrefixDigest(imp.sourcePath, cp.Offset)
	if err != nil {
		return resumeState{}, err
	}
	if digest != cp.PrefixDigest {
		return resumeState{}, fmt.Errorf("%w: checkpoint digest %s, source digest %s at offset %d",
			ErrSourceModified, cp.PrefixDigest, digest, cp.Offset)
	}

	switch {
	case last == cp.Batch:
		return resumeState{offset: cp.Offset, batch: cp.Batch}, nil
	case last < cp.Batch:
		return resumeState{}, fmt.Errorf("%w: checkpoint batch %d, sink batch %d", ErrTargetBehind, cp.Batch, last)
	}

	// 回执丢失路径：目标端批号超前于检查点，逐批对账后快进。
	offset := cp.Offset
	for b := cp.Batch + 1; b <= last; b++ {
		meta, err := imp.sink.BatchMetaOf(b)
		if err != nil {
			return resumeState{}, fmt.Errorf("%w: %v", ErrReconcileFailed, err)
		}
		if meta.StartOffset != offset {
			return resumeState{}, fmt.Errorf("%w: batch %d starts at %d, expected %d",
				ErrReconcileFailed, b, meta.StartOffset, offset)
		}
		digest, err := PrefixDigest(imp.sourcePath, meta.EndOffset)
		if err != nil {
			return resumeState{}, err
		}
		if digest != meta.PrefixDigest {
			return resumeState{}, fmt.Errorf("%w: batch %d digest mismatch at offset %d",
				ErrReconcileFailed, b, meta.EndOffset)
		}
		offset = meta.EndOffset
	}
	return resumeState{offset: offset, batch: last}, nil
}

// Run 先执行恢复核对，核对失败则明确停止（不导入任何数据）；
// 核对通过后从记录边界偏移继续读取、分批提交并保存检查点。
func (imp *Importer) Run() error {
	state, err := imp.recover()
	if err != nil {
		return err
	}

	f, err := os.Open(imp.sourcePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(state.offset, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReader(f)
	offset := state.offset
	batch := state.batch

	var pending [][]byte
	startOffset := offset
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		digest, err := PrefixDigest(imp.sourcePath, offset)
		if err != nil {
			return err
		}
		commit := BatchCommit{
			Batch:        batch + 1,
			StartOffset:  startOffset,
			EndOffset:    offset,
			PrefixDigest: digest,
			Records:      pending,
		}
		if err := imp.sink.CommitBatch(commit); err != nil {
			// 提交未确认（含回执丢失）：不保存检查点，明确停止，
			// 下次运行由恢复对账决定从何处续传。
			return fmt.Errorf("importer: commit batch %d not acknowledged: %w", commit.Batch, err)
		}
		batch = commit.Batch
		if err := imp.store.Save(Checkpoint{Offset: offset, PrefixDigest: digest, Batch: batch}); err != nil {
			return err
		}
		pending = nil
		startOffset = offset
		return nil
	}

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			record := line
			if record[len(record)-1] == '\n' {
				record = record[:len(record)-1]
			}
			offset += int64(len(line))
			pending = append(pending, record)
			if len(pending) >= imp.batchSize {
				if ferr := flush(); ferr != nil {
					return ferr
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return flush()
}
