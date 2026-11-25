package nestedmp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// onBody 处理匹配器吐出的正文字节：本层哈希；附件写暂存，容器交子层。
func (p *parser) onBody(ctx context.Context, depth int, body []byte) error {
	lv := p.stack[depth]
	if lv.mode == modeFirstOpen {
		if depth == 0 && p.rootDone {
			return fmt.Errorf("%w: %d byte(s) after root close", ErrTrailing, len(body))
		}
		return fmt.Errorf("%w: %d byte(s) before first delimiter", ErrPreamble, len(body))
	}
	part := lv.part
	if part == nil {
		return fmt.Errorf("%w: body bytes without active part", ErrSyntax)
	}
	// 子层正文字节同时是直接容器父的原始正文，累加到父容器摘要。
	if err := p.accumulateContainerParent(depth, body); err != nil {
		return err
	}
	evOffset := part.info.BodyStart + part.info.Size
	if _, err := part.hash.Write(body); err != nil {
		return err
	}
	part.info.Size += int64(len(body))
	p.emit(Event{Kind: EvBodyWrite, Level: depth, Name: part.info.Name, Offset: evOffset, Length: len(body)})
	if part.container {
		// 同一批原始字节交给子层；父层哈希已在上文完成，子层不做任何重编码。
		n, err := p.feedLevel(ctx, depth+1, body)
		if err != nil {
			return err
		}
		if n != len(body) {
			return fmt.Errorf("%w: child level did not consume full body", ErrSyntax)
		}
		return nil
	}
	return p.stageBytes(ctx, part, body)
}

// accumulateContainerParent 把本层字节累加到直接父容器部件的摘要与长度。
// 容器父的正文完全由其子层字节构成（头块、正文、子定界行），每条路径
// 都经过这里或 onDelim 的定界补哈希，保证每个原始字节恰好计入一次。
func (p *parser) accumulateContainerParent(depth int, body []byte) error {
	if depth == 0 {
		return nil
	}
	// 沿活动栈向上，每一层活动容器部件都把该字节视为自身原始正文。
	// 嵌套容器因此能对完整子报文（含更深层定界符）计算摘要，
	// 且不依赖任何重新编码的子对象。
	for d := depth - 1; d >= 0; d-- {
		ancestor := p.stack[d]
		if ancestor.part == nil || !ancestor.part.container {
			break
		}
		if _, err := ancestor.part.hash.Write(body); err != nil {
			return err
		}
		ancestor.part.info.Size += int64(len(body))
	}
	return nil
}

// routeContainerByte 把容器正文的单个字节交给子层，并在父容器摘要中
// 记录同一原始字节。子匹配器识别为定界行的字节不会出现在这里（它们由
// onDelim 统一补哈希），因此父摘要恰好覆盖去除父自身定界符后的全部正文。
func (p *parser) routeContainerByte(ctx context.Context, depth int, ch byte) error {
	parent := p.stack[depth]
	part := parent.part
	if part == nil || !part.container {
		return fmt.Errorf("%w: container routing without active container", ErrSyntax)
	}
	n, err := p.feedLevel(ctx, depth+1, []byte{ch})
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: child level did not consume container byte", ErrSyntax)
	}
	return nil
}

// stageBytes 经固定窗口把附件正文推给宿主暂存写入端；阻塞即天然背压。
func (p *parser) stageBytes(ctx context.Context, part *openPart, body []byte) error {
	p.window = append(p.window, body...)
	for cap(p.window) > 0 && len(p.window) >= cap(p.window) {
		if err := p.checkCancel(ctx); err != nil {
			return err
		}
		flushed := p.window
		n, err := part.sink.Write(flushed)
		if err != nil {
			return fmt.Errorf("nestedmp: staging write: %w", err)
		}
		if n != len(flushed) {
			return fmt.Errorf("nestedmp: staging writer returned invalid count %d", n)
		}
		// 复位到固定底层数组起点，避免跨部件/多次满窗后切片漂移。
		p.window = p.window[:0]
	}
	return nil
}

// flushWindow 在附件部件结束时把窗口剩余字节排空。
func (p *parser) flushWindow(ctx context.Context, part *openPart) error {
	if len(p.window) > 0 {
		if err := p.checkCancel(ctx); err != nil {
			return err
		}
		flushed := p.window
		n, err := part.sink.Write(flushed)
		if err != nil {
			return fmt.Errorf("nestedmp: staging write: %w", err)
		}
		if n != len(flushed) {
			return fmt.Errorf("nestedmp: staging writer returned invalid count %d", n)
		}
		p.window = p.window[:0]
	}
	return nil
}

// onDelim 处理一层匹配到的开放/结束定界行。
func (p *parser) onDelim(ctx context.Context, depth int, event delimEvent, delimStart int64, span int) error {
	lv := p.stack[depth]
	// 容器父层的摘要绑定包含子层定界行在内的原始正文。子匹配器识别
	// 定界行时这些字节不走正文回调，因此在这里向父容器补算哈希。
	if depth > 0 {
		line := append([]byte(nil), lv.matcher.lastDelim...)
		for d := depth - 1; d >= 0; d-- {
			ancestor := p.stack[d]
			if ancestor.part == nil || !ancestor.part.container {
				break
			}
			if _, err := ancestor.part.hash.Write(line); err != nil {
				return err
			}
			ancestor.part.info.Size += int64(len(line))
			p.emit(Event{
				Kind:   EvBodyWrite,
				Level:  d,
				Name:   ancestor.part.info.Name,
				Offset: delimStart,
				Length: len(line),
			})
		}
	}
	switch event {
	case evOpen:
		if lv.mode == modeBody && lv.part != nil {
			if err := p.finishPart(ctx, depth, delimStart, span); err != nil {
				return err
			}
		}
		lv.mode = modeHeader
		return nil
	case evClose:
		if lv.mode != modeBody {
			return fmt.Errorf("%w: close delimiter without open part at level %d", ErrSyntax, depth)
		}
		if depth < len(p.stack)-1 {
			return fmt.Errorf("%w: parent closed while child boundary %q is active",
				ErrSyntax, p.stack[depth+1].boundary)
		}
		if lv.part != nil {
			if err := p.finishPart(ctx, depth, delimStart, span); err != nil {
				return err
			}
		}
		lv.mode = modeFirstOpen
		if depth > 0 {
			// 子层结束：弹出本层。直接父容器的原始正文随该子 close 行结束，
			// 结束父容器部件，使父层恢复用自身匹配器扫描后续同层定界行。
			// 更深的容器在各自直接子层 close 时已经逐级结束，无需跨级上溯。
			// delimStart 已是子 close 行的全局字节位置；容器原始正文
			// 恰好结束于该子 close 行起点，因此父容器直接复用该全局位置。
			// 子 close 行（含紧邻其前的 CRLF）是父容器原始正文的结尾，
			// 用匹配器记录的完整定界行长度计算父容器的结束位置。
			childLineLen := len(lv.matcher.lastDelim)
			childLineEnd := delimStart + int64(childLineLen)
			// 容器原始正文包含整个子 close 行（含前导 CRLF），因此其
			// 排他终点 BodyEnd 与 EndOffset 都是子 close 行之后的位置。
			parentBodyEnd := childLineEnd
			parentEndOffset := childLineEnd
			parent := p.stack[depth-1]
			p.emit(Event{Kind: EvPopBoundary, Level: depth, Name: lv.boundary, Offset: delimStart})
			p.stack = p.stack[:depth]
			if parent.part == nil || !parent.part.container {
				return fmt.Errorf("%w: child boundary %q has no enclosing container",
					ErrSyntax, lv.boundary)
			}
			if err := p.finishContainerAt(ctx, depth-1, parentBodyEnd, parentEndOffset); err != nil {
				return err
			}
			// 父容器正文已结束，但该层可能还有后续同层部件或本层 close，
			// 因此回到正文模式：part 为 nil，匹配器从 mScan 等待下一定界行。
			parent.mode = modeBody
			return nil
		}
		p.rootDone = true
		p.result = &Result{Parts: append([]PartInfo(nil), p.allParts...)}
		return nil
	}
	return fmt.Errorf("%w: unknown delimiter event", ErrSyntax)
}

// startPart 在头块解析完成后建立部件；容器会同时推入子层。
func (p *parser) startPart(ctx context.Context, depth int, h header, headerStart int64, headerLen int) error {
	if p.names[h.name] {
		return fmt.Errorf("%w: duplicate part name %q", ErrHeader, h.name)
	}
	lv := p.stack[depth]
	bodyStart := headerStart + int64(headerLen)
	part := &openPart{
		info: PartInfo{
			Level:       depth,
			Type:        h.typ,
			Name:        h.name,
			Boundary:    lv.boundary,
			HeaderStart: headerStart,
			BodyStart:   bodyStart,
		},
		want:      h.digest,
		hash:      sha256.New(),
		container: h.typ == TypeContainer,
	}
	lv.part = part
	if !part.container {
		sink, err := p.txn.Put(ctx, h.name)
		if err != nil {
			return fmt.Errorf("nestedmp: staging put %q: %w", h.name, err)
		}
		part.sink = sink
	}
	p.names[h.name] = true
	p.allParts = append(p.allParts, part.info)
	p.emit(Event{Kind: EvPartOpen, Level: depth, Name: h.name, Offset: headerStart})

	if part.container {
		for _, other := range p.stack {
			if other.boundary == h.child {
				return fmt.Errorf("%w: child boundary %q duplicates active boundary", ErrBoundary, h.child)
			}
		}
		child := &level{
			boundary: h.child,
			base:     bodyStart,
			matcher:  newDelimMatcher(h.child),
			mode:     modeFirstOpen,
		}
		p.stack = append(p.stack, child)
		p.emit(Event{Kind: EvPushBoundary, Level: depth + 1, Name: h.child, Offset: bodyStart})
		if p.held() > p.budget {
			return p.memLimitError()
		}
	}
	return nil
}

// finishPart 关闭部件：排空窗口、校验摘要、关闭暂存写入端并固化位置信息。
func (p *parser) finishPart(ctx context.Context, depth int, delimStart int64, span int) error {
	lv := p.stack[depth]
	part := lv.part
	if part == nil {
		return fmt.Errorf("%w: delimiter without active part", ErrSyntax)
	}
	if !part.container {
		if err := p.flushWindow(ctx, part); err != nil {
			return err
		}
	}
	sum := part.hash.Sum(nil)
	if !bytes.Equal(sum, part.want[:]) {
		return fmt.Errorf("%w: part %q want %x got %x", ErrDigest, part.info.Name, part.want, sum)
	}
	copy(part.info.Digest[:], sum)
	p.emit(Event{
		Kind:   EvDigestOK,
		Level:  depth,
		Name:   part.info.Name,
		Offset: delimStart,
		Digest: hex.EncodeToString(sum),
	})
	part.info.BodyEnd = delimStart
	part.info.EndOffset = delimStart + int64(span)
	if part.sink != nil {
		if err := part.sink.Close(); err != nil {
			return fmt.Errorf("nestedmp: staging close %q: %w", part.info.Name, err)
		}
	}
	p.emit(Event{Kind: EvPartClose, Level: depth, Name: part.info.Name, Offset: part.info.EndOffset})
	lv.part = nil
	p.replacePartInfo(part.info)
	return nil
}

func (p *parser) replacePartInfo(info PartInfo) {
	for i := range p.allParts {
		if p.allParts[i].Name == info.Name {
			p.allParts[i] = info
			return
		}
	}
	p.allParts = append(p.allParts, info)
}

// finishContainerAt 结束容器部件，使用子 close 行界定的显式字节位置，
// 校验摘要后关闭并固化 PartInfo（容器没有暂存写入端）。
func (p *parser) finishContainerAt(ctx context.Context, depth int, bodyEnd, endOffset int64) error {
	lv := p.stack[depth]
	part := lv.part
	if part == nil || !part.container {
		return fmt.Errorf("%w: expected active container at level %d", ErrSyntax, depth)
	}
	sum := part.hash.Sum(nil)
	if !bytes.Equal(sum, part.want[:]) {
		return fmt.Errorf("%w: container %q want %x got %x", ErrDigest, part.info.Name, part.want, sum)
	}
	copy(part.info.Digest[:], sum)
	p.emit(Event{
		Kind:   EvDigestOK,
		Level:  depth,
		Name:   part.info.Name,
		Offset: bodyEnd,
		Digest: hex.EncodeToString(sum),
	})
	part.info.BodyEnd = bodyEnd
	part.info.EndOffset = endOffset
	p.emit(Event{Kind: EvPartClose, Level: depth, Name: part.info.Name, Offset: endOffset})
	lv.part = nil
	p.replacePartInfo(part.info)
	return nil
}
