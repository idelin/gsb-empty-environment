package netsec

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// lengthBody 严格按 Content-Length 读取；提前 EOF 即截断。
type lengthBody struct {
	br        *bufio.Reader
	owner     *Reader
	remaining int64
	done      bool
}

func (b *lengthBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > b.remaining {
		p = p[:int(b.remaining)]
	}
	n, err := b.br.Read(p)
	b.remaining -= int64(n)
	if b.remaining == 0 {
		b.done = true
		b.owner.bodyComplete = true
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		b.done = true
		b.owner.bodyComplete = true
		b.owner.reusable = false
		return n, ErrTruncated
	}
	return n, err
}

func (b *lengthBody) Close() error {
	if !b.done {
		// 未读完就放弃：剩余字节归属不明，连接不能复用。
		b.owner.reusable = false
	}
	b.done = true
	b.owner.bodyComplete = true
	return nil
}

// closeBody 用于无长度字段的响应：读到连接关闭为止，之后连接禁止复用。
type closeBody struct {
	br    *bufio.Reader
	owner *Reader
	done  bool
}

func (b *closeBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	n, err := b.br.Read(p)
	if errors.Is(err, io.EOF) {
		b.done = true
		b.owner.bodyComplete = true
		if n > 0 {
			return n, nil
		}
		return 0, io.EOF
	}
	return n, err
}

func (b *closeBody) Close() error {
	b.done = true
	b.owner.bodyComplete = true
	return nil
}

// tunnelBody 只是透传隧道字节；真正的切换点在 Reader 标记连接不可复用。
type tunnelBody struct {
	br *bufio.Reader
}

func (b *tunnelBody) Read(p []byte) (int, error) {
	return b.br.Read(p)
}

func (b *tunnelBody) Close() error { return nil }

// chunkedBody 实现 chunked 传输编码的状态机，块与尾部之外的字节不会被消费。
type chunkedBody struct {
	br     *bufio.Reader
	owner  *Reader
	state  chunkState
	remain int64
}

type chunkState int

const (
	chunkSize chunkState = iota
	chunkData
	chunkCRLF
	chunkTrailer
	chunkDone
)

func (b *chunkedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		switch b.state {
		case chunkSize:
			line, err := b.br.ReadString('\n')
			if err != nil {
				return 0, b.fail(mapEOF(err))
			}
			sizeLine := strings.TrimSpace(line)
			if i := strings.IndexByte(sizeLine, ';'); i >= 0 {
				sizeLine = sizeLine[:i]
			}
			size, perr := strconv.ParseUint(sizeLine, 16, 64)
			if perr != nil {
				return 0, b.fail(ErrProtocol)
			}
			if size == 0 {
				b.state = chunkTrailer
				continue
			}
			b.remain = int64(size)
			b.state = chunkData
		case chunkData:
			if int64(len(p)) > b.remain {
				p = p[:int(b.remain)]
			}
			n, err := b.br.Read(p)
			b.remain -= int64(n)
			if b.remain == 0 {
				b.state = chunkCRLF
				if n > 0 {
					return n, nil
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return n, b.fail(ErrTruncated)
				}
				return n, err
			}
			if n > 0 {
				return n, nil
			}
		case chunkCRLF:
			if err := b.expectCRLF(); err != nil {
				return 0, b.fail(err)
			}
			b.state = chunkSize
		case chunkTrailer:
			line, err := b.br.ReadString('\n')
			if err != nil {
				return 0, b.fail(mapEOF(err))
			}
			if line == "\r\n" || line == "\n" {
				b.state = chunkDone
				b.owner.bodyComplete = true
				return 0, io.EOF
			}
			// 其余行为尾部字段，继续读直到空行。
		case chunkDone:
			return 0, io.EOF
		}
	}
}

func (b *chunkedBody) expectCRLF() error {
	c, err := b.br.ReadByte()
	if err != nil {
		return mapEOF(err)
	}
	if c != '\r' {
		return ErrProtocol
	}
	c, err = b.br.ReadByte()
	if err != nil {
		return mapEOF(err)
	}
	if c != '\n' {
		return ErrProtocol
	}
	return nil
}

func (b *chunkedBody) Close() error {
	if b.state != chunkDone {
		// 未读完即放弃，后续字节归属不明，连接不能复用。
		b.owner.reusable = false
	}
	b.state = chunkDone
	b.owner.bodyComplete = true
	return nil
}

func (b *chunkedBody) fail(err error) error {
	b.state = chunkDone
	b.owner.bodyComplete = true
	b.owner.reusable = false
	return err
}

func mapEOF(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrTruncated
	}
	return err
}
