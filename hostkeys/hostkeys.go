// Package hostkeys implements a verifier for the OpenSSH host-key rotation
// protocol (hostkeys-00@openssh.com / hostkeys-prove-00@openssh.com).
//
// A malicious or compromised server may advertise arbitrary public keys with a
// hostkeys-00@openssh.com global request in the hope that the client will
// blindly pin them into known_hosts. The handler in this package only accepts
// an advertised key after the server proves possession of the corresponding
// private key through a hostkeys-prove-00@openssh.com signature over the
// negotiated session identifier and the public-key blob.
//
// Security policy enforced here:
//
//   - The notification is ignored unless host authentication completed
//     successfully via the wrapped ssh.ClientConfig.HostKeyCallback.
//   - The notification is ignored unless the host already has an entry in
//     known_hosts.
//   - Every advertised key, including keys already present in known_hosts,
//     must be proved. The whole batch is discarded if any proof is missing,
//     malformed, bound to another session or fails verification.
//   - known_hosts is rewritten at most once per notification and only after
//     every proof succeeded. Stale entries are removed solely when the server
//     no longer lists them and the full batch proof succeeded.
package hostkeys

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	notifyType = "hostkeys-00@openssh.com"
	proveType  = "hostkeys-prove-00@openssh.com"

	// maxAdvertisedKeys bounds the number of keys in one rotation notification.
	maxAdvertisedKeys = 256
	// maxKeyBlob bounds a single advertised public-key blob.
	maxKeyBlob = 256 * 1024
	// maxKnownHostsSize bounds the known_hosts files this package rewrites.
	maxKnownHostsSize = 16 * 1024 * 1024
)

// Errors returned by the notification handler. Callers may use errors.Is.
var (
	// ErrNotAuthenticated means no successful HostKeyCallback was observed.
	ErrNotAuthenticated = errors.New("hostkeys: host authentication did not complete")
	// ErrNoKnownHostEntry means known_hosts has no entry for the host yet.
	ErrNoKnownHostEntry = errors.New("hostkeys: no existing known_hosts entry for host")
	// ErrMalformedNotification means the notification payload could not be parsed.
	ErrMalformedNotification = errors.New("hostkeys: malformed hostkeys notification")
	// ErrProveFailed means the server rejected or did not answer a prove request.
	ErrProveFailed = errors.New("hostkeys: hostkeys-prove request failed")
	// ErrProofRejected means a returned proof could not be verified.
	ErrProofRejected = errors.New("hostkeys: host-key proof rejected")
)

// Options configures a rotator.
type Options struct {
	// KnownHostsPath is the known_hosts file that is consulted and rewritten.
	KnownHostsPath string

	// HashHostnames causes newly added entries to use hashed hostnames,
	// matching ssh-keygen -H style entries. When false, plain host:port
	// patterns are written, matching knownhosts.Line output.
	HashHostnames bool

	// OnEvent, when non-nil, receives a short description and the error that
	// terminated processing (nil on success). It is intended for logging and
	// test synchronization and never affects the security decision.
	OnEvent func(event string, err error)
}

// Rotator wraps an ssh.ClientConfig so that connections created from it
// transparently validate host-key rotation notifications and update the
// configured known_hosts file.
type Rotator struct {
	opts Options

	mu sync.Mutex
	// authenticated records whether a HostKeyCallback returned nil at least
	// once. The remaining fields hold the latest successful parameters.
	authenticated bool
	hostname      string
	remote        net.Addr
	serverKey     ssh.PublicKey

	// processing serializes known_hosts rewrites when several notifications
	// arrive on the same connection.
	processing sync.Mutex
}

// New returns a Rotator backed by the given known_hosts file.
func New(opts Options) (*Rotator, error) {
	if opts.KnownHostsPath == "" {
		return nil, errors.New("hostkeys: KnownHostsPath is required")
	}
	return &Rotator{opts: opts}, nil
}

// WrapCallback returns a HostKeyCallback that runs inner first and records a
// successful authentication. Most callers should pass the result of
// knownhosts.New(path); inner may be nil for callers that gate authentication
// through another acceptance path.
func (r *Rotator) WrapCallback(inner ssh.HostKeyCallback) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if inner != nil {
			if err := inner(hostname, remote, key); err != nil {
				return err
			}
		}
		r.mu.Lock()
		r.authenticated = true
		r.hostname = hostname
		r.remote = remote
		r.serverKey = key
		r.mu.Unlock()
		return nil
	}
}

// Config returns a shallow copy of cfg whose HostKeyCallback records
// successful host authentication for the rotator. The original config is not
// modified.
func (r *Rotator) Config(cfg *ssh.ClientConfig) *ssh.ClientConfig {
	wrapped := *cfg
	wrapped.HostKeyCallback = r.WrapCallback(cfg.HostKeyCallback)
	return &wrapped
}

// NewClientConn performs an SSH handshake like ssh.NewClientConn while
// installing the rotation handler on the connection's global-request channel.
func (r *Rotator) NewClientConn(c net.Conn, addr string, cfg *ssh.ClientConfig) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	conn, chans, reqs, err := ssh.NewClientConn(c, addr, r.Config(cfg))
	if err != nil {
		return nil, nil, nil, err
	}
	filtered := make(chan *ssh.Request, 16)
	go r.intercept(conn, reqs, filtered)
	return conn, chans, filtered, nil
}

// Dial connects to the given network address with the same overall semantics
// as ssh.Dial while applying the rotation handler.
func (r *Rotator) Dial(network, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	conn, err := net.DialTimeout(network, addr, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	sshConn, chans, reqs, err := r.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// intercept drains the raw global-request channel. Host-key notifications are
// handled locally; every other request is forwarded untouched to the SSH
// client's mux.
func (r *Rotator) intercept(conn ssh.Conn, raw <-chan *ssh.Request, out chan<- *ssh.Request) {
	for req := range raw {
		if req == nil {
			close(out)
			return
		}
		if req.Type == notifyType {
			// hostkeys notifications are one-way in OpenSSH and carry
			// WantReply=false. Refuse any reply-bearing variant defensively.
			if req.WantReply && req.Reply != nil {
				_ = req.Reply(false, nil)
			}
			go r.handleNotification(conn, req.Payload)
			continue
		}
		out <- req
	}
	close(out)
}

func (r *Rotator) emit(event string, err error) {
	if r.opts.OnEvent != nil {
		r.opts.OnEvent(event, err)
	}
}

// handleNotification is the entry point for one hostkeys-00 request.
func (r *Rotator) handleNotification(conn ssh.Conn, payload []byte) {
	// Notifications on one connection are fully serialized so that two
	// batches can never interleave their reads and rewrites of known_hosts.
	r.processing.Lock()
	defer r.processing.Unlock()

	r.emit("notify", nil)

	r.mu.Lock()
	authenticated := r.authenticated
	hostname := r.hostname
	remote := r.remote
	serverKey := r.serverKey
	r.mu.Unlock()
	if !authenticated {
		r.emit("ignored", ErrNotAuthenticated)
		return
	}

	if !r.hostIsKnown(hostname, remote, serverKey) {
		r.emit("ignored", ErrNoKnownHostEntry)
		return
	}

	advertised, err := parseNotification(payload)
	if err != nil {
		r.emit("abort", err)
		return
	}

	proved, err := r.requestProofs(conn, conn.SessionID(), advertised)
	if err != nil {
		r.emit("abort", err)
		return
	}

	if err := r.rewriteKnownHosts(hostname, remote, proved); err != nil {
		r.emit("abort", err)
		return
	}
	r.emit("applied", nil)
}

// hostIsKnown reopens the known_hosts file through the standard library
// callback, which implements all hostname/port/hash matching rules.
func (r *Rotator) hostIsKnown(hostname string, remote net.Addr, key ssh.PublicKey) bool {
	if key == nil {
		return false
	}
	cb, err := knownhosts.New(r.opts.KnownHostsPath)
	if err != nil {
		// A missing file simply means the host cannot be known yet.
		return false
	}
	return cb(hostname, remote, key) == nil
}

// parseNotification decodes the string-list payload of hostkeys-00.
func parseNotification(payload []byte) ([][]byte, error) {
	var blobs [][]byte
	for len(payload) > 0 {
		if len(blobs) >= maxAdvertisedKeys {
			return nil, fmt.Errorf("%w: too many advertised keys", ErrMalformedNotification)
		}
		var blob []byte
		if err := ssh.Unmarshal(payload, &blob); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedNotification, err)
		}
		if len(blob) == 0 {
			return nil, fmt.Errorf("%w: empty key blob", ErrMalformedNotification)
		}
		if len(blob) > maxKeyBlob {
			return nil, fmt.Errorf("%w: key blob too large", ErrMalformedNotification)
		}
		blobs = append(blobs, blob)
		payload = payload[4+len(blob):]
	}
	if len(blobs) == 0 {
		return nil, fmt.Errorf("%w: no advertised keys", ErrMalformedNotification)
	}
	return blobs, nil
}

// requestProofs sends a hostkeys-prove-00 request and verifies every returned
// signature. No file mutation happens before this function succeeds.
func (r *Rotator) requestProofs(conn ssh.Conn, sessionID []byte, blobs [][]byte) (map[string]ssh.PublicKey, error) {
	ok, reply, err := conn.SendRequest(proveType, true, marshalStringList(blobs))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProveFailed, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: server returned failure", ErrProveFailed)
	}

	var signatures [][]byte
	if err := ssh.Unmarshal(reply, &signatures); err != nil {
		return nil, fmt.Errorf("%w: malformed proof reply: %v", ErrProofRejected, err)
	}
	if len(signatures) != len(blobs) {
		return nil, fmt.Errorf("%w: expected %d signatures, got %d", ErrProofRejected, len(blobs), len(signatures))
	}

	proved := make(map[string]ssh.PublicKey, len(blobs))
	for i, blob := range blobs {
		key, err := ssh.ParsePublicKey(blob)
		if err != nil {
			return nil, fmt.Errorf("%w: unparseable advertised key: %v", ErrProofRejected, err)
		}

		signedData := make([]byte, 0, len(sessionID)+len(blob))
		signedData = append(signedData, sessionID...)
		signedData = append(signedData, blob...)

		var sig ssh.Signature
		if err := ssh.Unmarshal(signatures[i], &sig); err != nil {
			return nil, fmt.Errorf("%w: key %d (%s): malformed signature: %v", ErrProofRejected, i, key.Type(), err)
		}
		if err := key.Verify(signedData, &sig); err != nil {
			return nil, fmt.Errorf("%w: key %d (%s): %v", ErrProofRejected, i, key.Type(), err)
		}

		// A duplicate blob must not produce conflicting entries.
		if existing, dup := proved[string(blob)]; dup {
			if existing.Type() != key.Type() || !bytes.Equal(existing.Marshal(), key.Marshal()) {
				return nil, fmt.Errorf("%w: conflicting duplicate key blob", ErrProofRejected)
			}
			continue
		}
		proved[string(blob)] = key
	}
	return proved, nil
}

// marshalStringList encodes [][]byte as an SSH wire list of strings.
func marshalStringList(items [][]byte) []byte {
	total := 0
	for _, item := range items {
		total += 4 + len(item)
	}
	out := make([]byte, 0, total)
	for _, item := range items {
		out = append(out, byte(len(item)>>24), byte(len(item)>>16), byte(len(item)>>8), byte(len(item)))
		out = append(out, item...)
	}
	return out
}
