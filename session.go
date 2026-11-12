package netsec

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
)

// Session is one RRQ or WRQ transfer. It is bound to the client's IP address
// and UDP source port (TID) at creation time.
type Session struct {
	mu sync.Mutex

	client net.Addr
	remote netip.AddrPort
	file   *os.File
	path   string

	blockSize int
	isWrite   bool
	finished  bool

	// Read state.
	oackPending bool
	readSeq     uint64
	lastWire    uint16
	lastPacket  []byte
	lastFinal   bool

	// Write state.
	writeSeq uint64
}

// RemoteAddr returns the client address the session is bound to.
func (t *Session) RemoteAddr() net.Addr { return t.client }

// TID returns the bound client IP address and port as a canonical value.
func (t *Session) TID() netip.AddrPort { return t.remote }

// BlockSize returns the negotiated block size.
func (t *Session) BlockSize() int { return t.blockSize }

// Path returns the normalized path relative to the server root.
func (t *Session) Path() string { return t.path }

// Done reports whether the transfer has completed.
func (t *Session) Done() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.finished
}

// Close releases the open file. It is safe to call more than once.
func (t *Session) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finished = true
	if t.file == nil {
		return nil
	}
	err := t.file.Close()
	t.file = nil
	return err
}

// HandlePacket processes one UDP datagram from from and returns an outgoing
// TFTP packet, or nil when no response is needed.
//
// Packets from any address other than the bound client address and port receive
// ERROR 5 (Unknown TID). Such packets are ignored for session-state purposes:
// the transfer continues normally for the legitimate client.
func (t *Session) HandlePacket(packet []byte, from net.Addr) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()

	remote, err := canonicalAddrPort(from)
	if err != nil || remote != t.remote {
		return ErrorPacket(ErrorUnknownTID, "Unknown transfer ID")
	}
	if t.finished {
		return nil
	}
	if t.isWrite {
		return t.handleWriteLocked(packet)
	}
	return t.handleReadLocked(packet)
}

// Handle is an alias for HandlePacket.
func (t *Session) Handle(packet []byte, from net.Addr) []byte {
	return t.HandlePacket(packet, from)
}

func (t *Session) handleReadLocked(packet []byte) []byte {
	block, ok := parseACK(packet)
	if !ok {
		return ErrorPacket(ErrorIllegalOperation, "expected ACK")
	}
	if t.oackPending {
		if block != 0 {
			return nil
		}
		t.oackPending = false
		response, err := t.sendReadBlockLocked(1)
		if err != nil {
			t.finishLocked()
			return ErrorPacket(ErrorNotDefined, "read failed")
		}
		return response
	}
	if t.readSeq == 0 {
		return nil
	}
	if block == t.lastWire {
		if t.lastFinal {
			t.finishLocked()
			return nil
		}
		response, err := t.sendReadBlockLocked(t.readSeq + 1)
		if err != nil {
			t.finishLocked()
			return ErrorPacket(ErrorNotDefined, "read failed")
		}
		return response
	}
	if t.readSeq >= 1 && block == wireBlock(t.readSeq-1) {
		return t.lastPacket
	}
	return nil
}

func (t *Session) handleWriteLocked(packet []byte) []byte {
	block, data, ok := parseDATA(packet)
	if !ok {
		return ErrorPacket(ErrorIllegalOperation, "expected DATA")
	}
	if len(data) > t.blockSize {
		return ErrorPacket(ErrorIllegalOperation, "data exceeds negotiated block size")
	}
	if block == wireBlock(t.writeSeq) {
		if t.file == nil {
			return ErrorPacket(ErrorIllegalOperation, "transfer is closed")
		}
		if _, err := t.file.Write(data); err != nil {
			t.finishLocked()
			return ErrorPacket(ErrorDiskFull, "write failed")
		}
		if len(data) < t.blockSize {
			t.finishLocked()
		} else {
			t.writeSeq++
		}
		return ACKPacket(block)
	}
	if t.writeSeq > 1 && block == wireBlock(t.writeSeq-1) {
		return ACKPacket(block)
	}
	return ErrorPacket(ErrorIllegalOperation, "unexpected block number")
}

func (t *Session) sendReadBlockLocked(seq uint64) ([]byte, error) {
	if t.file == nil {
		return nil, errors.New("netsec: file is closed")
	}
	offset := (seq - 1) * uint64(t.blockSize)
	if offset/uint64(t.blockSize) != seq-1 || offset > uint64(^uint64(0)>>1) {
		return nil, errors.New("netsec: file is too large")
	}
	buf := make([]byte, t.blockSize)
	n, err := t.file.ReadAt(buf, int64(offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:n]
	wire := wireBlock(seq)
	packet := DataPacket(wire, buf)
	t.readSeq = seq
	t.lastWire = wire
	t.lastPacket = packet
	t.lastFinal = n < t.blockSize
	return packet, nil
}

func (t *Session) finishLocked() {
	t.finished = true
	if t.file != nil {
		_ = t.file.Close()
		t.file = nil
	}
}

func wireBlock(seq uint64) uint16 {
	return uint16(seq & 0xffff)
}
