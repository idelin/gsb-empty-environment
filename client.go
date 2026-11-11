package sshchannel

import (
	"errors"
	"io"
	"sync"
)

// Channel is one multiplexed logical connection as seen by the client.
type Channel interface {
	io.ReadWriteCloser
	// CloseWrite shuts down only the writing side, propagating half-close
	// through the host to the upstream target.
	CloseWrite() error
	ID() uint32
}

// Client multiplexes channels over one session from the dialing side.
type Client struct {
	reader *frameReader
	writer *frameWriter
	closer io.Closer

	mu       sync.Mutex
	channels map[uint32]*clientChannel
	nextID   uint32
	freeIDs  []uint32
	closed   bool

	closeOnce sync.Once
}

// NewClient wraps a session transport and starts the read loop.
func NewClient(session io.ReadWriter) *Client {
	c := &Client{
		reader:   newFrameReader(session),
		writer:   newFrameWriter(session),
		channels: make(map[uint32]*clientChannel),
	}
	if cl, ok := session.(io.Closer); ok {
		c.closer = cl
	}
	go c.readLoop()
	return c
}

// Open requests a new channel and waits for the host's verdict.
func (c *Client) Open(host string, port uint16) (Channel, error) {
	id, ok := c.allocID()
	if !ok {
		return nil, ErrSessionClosed
	}
	ch := newClientChannel(c, id)

	c.mu.Lock()
	c.channels[id] = ch
	c.mu.Unlock()

	if err := c.writer.write(frameOpen, encodeOpen(id, host, port)); err != nil {
		ch.releaseIDOnce()
		c.removeChannel(ch)
		return nil, err
	}

	if err := ch.waitReady(); err != nil {
		c.releaseID(id)
		return nil, err
	}
	return ch, nil
}

// Close tears down all channels and closes the session transport.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		chs := make([]*clientChannel, 0, len(c.channels))
		for _, ch := range c.channels {
			chs = append(chs, ch)
		}
		c.mu.Unlock()

		for _, ch := range chs {
			ch.fail(ErrSessionClosed)
		}
		if c.closer != nil {
			err = c.closer.Close()
		}
	})
	return err
}

func (c *Client) allocID() (uint32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, false
	}
	if n := len(c.freeIDs); n > 0 {
		id := c.freeIDs[n-1]
		c.freeIDs = c.freeIDs[:n-1]
		return id, true
	}
	id := c.nextID
	c.nextID++
	return id, true
}

func (c *Client) releaseID(id uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.freeIDs = append(c.freeIDs, id)
}

func (c *Client) removeChannel(ch *clientChannel) {
	c.mu.Lock()
	if cur := c.channels[ch.id]; cur == ch {
		delete(c.channels, ch.id)
	}
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	for {
		typ, payload, err := c.reader.readFrame()
		if err != nil {
			c.failAll(err)
			return
		}
		switch typ {
		case frameData:
			id, data, perr := parseData(payload)
			if perr != nil {
				continue
			}
			if ch := c.lookup(id); ch != nil {
				ch.deliver(data)
			}
		case frameOK:
			if ch := c.lookup(decodeID(payload)); ch != nil {
				ch.ready()
			}
		case frameFail:
			id, msg := parseFail(payload)
			if ch := c.lookup(id); ch != nil {
				ch.fail(errors.New(msg))
				c.removeChannel(ch)
				ch.releaseIDOnce()
			}
		case frameHalf:
			if ch := c.lookup(decodeID(payload)); ch != nil {
				ch.remoteHalf()
			}
		case frameClose:
			if ch := c.lookup(decodeID(payload)); ch != nil {
				ch.fail(io.EOF)
				c.removeChannel(ch)
				ch.releaseIDOnce()
			}
		}
	}
}

func (c *Client) lookup(id uint32) *clientChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channels[id]
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	c.closed = true
	chs := make([]*clientChannel, 0, len(c.channels))
	for _, ch := range c.channels {
		chs = append(chs, ch)
	}
	c.mu.Unlock()
	for _, ch := range chs {
		ch.fail(err)
	}
}

type clientChannel struct {
	client *Client
	id     uint32

	mu        sync.Mutex
	cond      *sync.Cond
	buf       []byte
	opened    bool
	half      bool
	terminal  bool
	readErr   error
	readyCh   chan struct{}
	readyOnce sync.Once

	sendCloseOnce sync.Once
	releaseOnce   sync.Once
}

func newClientChannel(client *Client, id uint32) *clientChannel {
	ch := &clientChannel{
		client:  client,
		id:      id,
		readyCh: make(chan struct{}),
	}
	ch.cond = sync.NewCond(&ch.mu)
	return ch
}

func (ch *clientChannel) ID() uint32 { return ch.id }

func (ch *clientChannel) waitReady() error {
	<-ch.readyCh
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if !ch.opened {
		if ch.readErr != nil {
			return ch.readErr
		}
		return errors.New("sshchannel: open rejected")
	}
	return nil
}

func (ch *clientChannel) ready() {
	ch.readyOnce.Do(func() {
		ch.mu.Lock()
		ch.opened = true
		ch.mu.Unlock()
		close(ch.readyCh)
	})
}

func (ch *clientChannel) fail(err error) {
	ch.readyOnce.Do(func() {
		ch.mu.Lock()
		ch.opened = false
		ch.readErr = err
		ch.mu.Unlock()
		close(ch.readyCh)
	})
	ch.mu.Lock()
	if !ch.terminal {
		ch.terminal = true
		if ch.readErr == nil {
			ch.readErr = err
		}
	}
	ch.mu.Unlock()
	ch.cond.Broadcast()
}

func (ch *clientChannel) deliver(data []byte) {
	ch.mu.Lock()
	if !ch.terminal {
		ch.buf = append(ch.buf, data...)
	}
	ch.mu.Unlock()
	ch.cond.Broadcast()
}

func (ch *clientChannel) remoteHalf() {
	ch.mu.Lock()
	ch.half = true
	ch.mu.Unlock()
	ch.cond.Broadcast()
}

func (ch *clientChannel) Read(p []byte) (int, error) {
	ch.mu.Lock()
	for len(ch.buf) == 0 {
		if ch.terminal {
			err := ch.readErr
			ch.mu.Unlock()
			if err == nil {
				return 0, io.EOF
			}
			return 0, err
		}
		if ch.half {
			ch.mu.Unlock()
			return 0, io.EOF
		}
		ch.cond.Wait()
	}
	n := copy(p, ch.buf)
	ch.buf = ch.buf[n:]
	ch.mu.Unlock()
	return n, nil
}

func (ch *clientChannel) Write(p []byte) (int, error) {
	if err := ch.client.writer.write(frameData, encodeData(ch.id, p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (ch *clientChannel) CloseWrite() error {
	return ch.client.writer.write(frameHalf, encodeID(ch.id))
}

func (ch *clientChannel) Close() error {
	var err error
	ch.sendCloseOnce.Do(func() {
		err = ch.client.writer.write(frameClose, encodeID(ch.id))
	})
	// If the channel was still awaiting the host verdict, unblock Open now.
	ch.readyOnce.Do(func() {
		ch.mu.Lock()
		ch.opened = false
		if ch.readErr == nil {
			ch.readErr = ErrChannelClosed
		}
		ch.mu.Unlock()
		close(ch.readyCh)
	})
	ch.client.removeChannel(ch)
	ch.releaseOnce.Do(func() {
		ch.client.releaseID(ch.id)
	})
	ch.mu.Lock()
	ch.terminal = true
	if ch.readErr == nil {
		ch.readErr = io.EOF
	}
	ch.mu.Unlock()
	ch.cond.Broadcast()
	return err
}

// releaseIDOnce returns the numeric id to the client's reuse pool exactly
// once, regardless of how the channel terminates.
func (ch *clientChannel) releaseIDOnce() {
	ch.releaseOnce.Do(func() {
		ch.client.releaseID(ch.id)
	})
}
