package nestedmp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"sync/atomic"
)

const (
	defaultWindow = 4096
	// defaultBudget 是固定窗口加各活跃边界结构性占用的宽松默认值。
	defaultBudget = 1 << 20
)

// levelMode 区分一层当前是在等开放分隔行、读部件头还是读部件正文。
type levelMode int

const (
	modeFirstOpen levelMode = iota // 尚未见到本层第一条开放分隔行
	modeHeader                     // 开放行之后，累积部件头
	modeBody                       // 头块之后，按定界匹配器读正文
)

// openPart 是当前尚未关闭部件的流式状态。
type openPart struct {
	info      PartInfo
	want      [sha256.Size]byte
	hash      hash.Hash
	sink      io.WriteCloser // 仅附件非 nil
	container bool
}

// level 是活动栈中的一层（一个边界）。
type level struct {
	boundary string
	base     int64 // 本层流首字节在原始输入中的全局偏移
	abs      int64 // 本层已消费字节数（局部坐标）
	matcher  *delimMatcher
	mode     levelMode
	hdrBuf   []byte
	hdrRest  []byte // 头块空行后同批到达、已计数待派发的字节
	part     *openPart
}

type parser struct {
	opts     Options
	txn      StagingTxn
	stack    []*level
	window   []byte
	budget   int64
	rootAbs  int64
	result   *Result
	allParts []PartInfo
	names    map[string]bool
	closed   bool
	failed   bool
	rootDone bool
	closeErr error
	cancel   atomic.Bool
	tracer   Tracer
}

// New 创建一个流式解析器，并立即通过宿主开启暂存事务。
func New(ctx context.Context, opts Options) (Parser, error) {
	if !validBoundary(opts.RootBoundary) {
		return nil, fmt.Errorf("%w: root boundary %q", ErrBoundary, opts.RootBoundary)
	}
	if opts.Host == nil {
		return nil, errors.New("nestedmp: Options.Host is required")
	}
	window := opts.Window
	if window <= 0 {
		window = defaultWindow
	}
	budget := opts.MemoryBudget
	if budget <= 0 {
		budget = defaultBudget
	}
	txn, err := opts.Host.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("nestedmp: begin staging txn: %w", err)
	}
	p := &parser{
		opts:   opts,
		txn:    txn,
		window: make([]byte, 0, window),
		budget: budget,
		names:  make(map[string]bool),
		tracer: opts.Tracer,
	}
	p.stack = append(p.stack, &level{
		boundary: opts.RootBoundary,
		matcher:  newDelimMatcher(opts.RootBoundary),
		mode:     modeFirstOpen,
	})
	p.emit(Event{Kind: EvTxnBegin, Level: 0, Name: opts.RootBoundary})
	if p.held() > p.budget {
		_ = txn.Rollback(ctx)
		return nil, p.memLimitError()
	}
	return p, nil
}

func (p *parser) emit(ev Event) {
	if p.tracer != nil {
		p.tracer.OnEvent(ev)
	}
}

// held 估算常驻内存：固定窗口加各活跃边界匹配器与头块的结构性占用。
func (p *parser) held() int64 {
	total := int64(cap(p.window))
	for _, lv := range p.stack {
		total += int64(cap(lv.matcher.hold)) + int64(cap(lv.hdrBuf))
	}
	return total
}

func (p *parser) memLimitError() error {
	err := fmt.Errorf("%w: held %d exceeds budget %d", ErrMemoryLimit, p.held(), p.budget)
	p.emit(Event{Kind: EvMemoryLimit, Err: err})
	return err
}

func (p *parser) checkCancel(ctx context.Context) error {
	if p.cancel.Load() {
		return ErrCanceled
	}
	return ctx.Err()
}

// fail 标记失败并回滚整个事务；回滚丢弃全部暂存对象，使其永不对外可读。
func (p *parser) fail(ctx context.Context, err error) error {
	p.failed = true
	p.closed = true
	p.emit(Event{Kind: EvRollback, Err: err})
	if rbErr := p.txn.Rollback(ctx); rbErr != nil {
		return fmt.Errorf("%w; rollback also failed: %v", err, rbErr)
	}
	return err
}

func (p *parser) Write(chunk []byte) (int, error) {
	return p.WriteCtx(context.Background(), chunk)
}

func (p *parser) WriteCtx(ctx context.Context, chunk []byte) (int, error) {
	if p.closed {
		return 0, ErrClosed
	}
	if err := p.checkCancel(ctx); err != nil {
		return 0, p.fail(ctx, err)
	}
	n, err := p.feedLevel(ctx, 0, chunk)
	if err != nil {
		return n, p.fail(ctx, err)
	}
	return n, nil
}

func (p *parser) Cancel() { p.cancel.Store(true) }

func (p *parser) CloseCtx(ctx context.Context) (*Result, error) {
	if p.closed {
		return nil, ErrClosed
	}
	if err := p.checkCancel(ctx); err != nil {
		return nil, p.fail(ctx, err)
	}
	// EOF 处各层未决候选项一律属于正文，自最深层向上吐出。
	for d := len(p.stack) - 1; d >= 0; d-- {
		lv := p.stack[d]
		lv.matcher.flushEOF(func(body []byte) {
			if p.closeErr == nil {
				p.closeErr = p.onBody(ctx, d, body)
			}
		})
		if p.closeErr != nil {
			err := p.closeErr
			p.closeErr = nil
			return nil, p.fail(ctx, err)
		}
	}
	if !p.rootDone || p.result == nil || len(p.stack) != 1 {
		return nil, p.fail(ctx, fmt.Errorf("%w: stream ended before complete close delimiter", ErrEOF))
	}
	if err := p.checkCancel(ctx); err != nil {
		return nil, p.fail(ctx, err)
	}
	p.result.TotalBytes = p.rootAbs
	p.emit(Event{Kind: EvCommit, Offset: p.rootAbs})
	// 提交回调可能迟到（阻塞）；在 Commit 成功返回前暂存内容保持不可读。
	if err := p.txn.Commit(ctx); err != nil {
		return nil, p.fail(ctx, fmt.Errorf("nestedmp: commit: %w", err))
	}
	p.closed = true
	return p.result, nil
}

// feedLevel 把 chunk 喂给指定层，返回该层消费的字节数。
// 每层维护自己的局部流位置；子层消费的字节同时也是父层容器正文，
// 已在父层路径参与父层摘要，不存在重新编码。
func (p *parser) feedLevel(ctx context.Context, depth int, chunk []byte) (int, error) {
	lv := p.stack[depth]
	consumed := 0
	// 上一轮头块空行后同批携带的字节已经计过数，先派发且不重复计数。
	if len(lv.hdrRest) > 0 {
		rest := lv.hdrRest
		lv.hdrRest = nil
		for _, ch := range rest {
			if err := p.checkCancel(ctx); err != nil {
				return consumed, err
			}
			if err := p.dispatch(ctx, depth, ch); err != nil {
				return consumed, err
			}
		}
	}
	for i := 0; i < len(chunk); i++ {
		if err := p.checkCancel(ctx); err != nil {
			return consumed, err
		}
		ch := chunk[i]
		lv.abs++
		if depth == 0 {
			p.rootAbs++
		}
		consumed++
		if err := p.dispatch(ctx, depth, ch); err != nil {
			return consumed, err
		}
		// dispatch 可能从头块里解析出同批剩余字节，就地排空。
		if len(lv.hdrRest) > 0 {
			rest := lv.hdrRest
			lv.hdrRest = nil
			for _, rc := range rest {
				if err := p.checkCancel(ctx); err != nil {
					return consumed, err
				}
				if err := p.dispatch(ctx, depth, rc); err != nil {
					return consumed, err
				}
			}
		}
	}
	return consumed, nil
}

// dispatch 处理一个已经计入流位置的字节，按当前模式路由到头部累积或
// 定界匹配器。头块完成时同批剩余字节仍由外层循环逐个派发，因此模式在
// 开放/结束行之间切换时不会错配。
func (p *parser) dispatch(ctx context.Context, depth int, ch byte) error {
	lv := p.stack[depth]
	if lv.mode == modeHeader {
		if err := p.accumulateContainerParent(depth, []byte{ch}); err != nil {
			return err
		}
		lv.hdrBuf = append(lv.hdrBuf, ch)
		if len(lv.hdrBuf) > maxHeaderSize {
			return fmt.Errorf("%w: header exceeds %d bytes", ErrHeader, maxHeaderSize)
		}
		if bytes.Index(lv.hdrBuf, []byte("\r\n\r\n")) < 0 {
			return nil
		}
		h, total, err := parseHeaderBlock(lv.hdrBuf)
		if err != nil {
			return err
		}
		headerStart := lv.base + lv.abs - int64(len(lv.hdrBuf))
		rest := lv.hdrBuf[total:]
		lv.hdrBuf = lv.hdrBuf[:0]
		if err := p.startPart(ctx, depth, h, headerStart, total); err != nil {
			return err
		}
		lv.mode = modeBody
		// 匹配器识别开放分隔行时已 reset 到 mScan，部件正文因此天然
		// 从普通扫描态开始；新建子层则保留 mInit 以接受流首开放行。
		// 这些字节与头块同批到达、已计入流位置；交还 feedLevel 立即派发，
		// 保证空行后的正文或下一定界行不会丢失或被重复计数。
		lv.hdrRest = append(lv.hdrRest, rest...)
		return nil
	}

	// 容器部件的正文完全归子层解析：本层匹配器不得扫描其中的字节，
	// 否则互为前缀的父子边界会被本层匹配器误判并切碎子层字节流。
	// 本层只对同一原始字节做摘要，然后整段透传给子层。
	if lv.part != nil && lv.part.container {
		return p.routeContainerByte(ctx, depth, ch)
	}

	var callErr error
	event, span := lv.matcher.feed(ch, func(body []byte) {
		if callErr == nil {
			callErr = p.onBody(ctx, depth, body)
		}
	})
	if callErr != nil {
		return callErr
	}
	if event != evNone {
		delimStart := lv.base + lv.abs - int64(span)
		return p.onDelim(ctx, depth, event, delimStart, span)
	}
	return nil
}
