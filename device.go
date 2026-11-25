package displaycommit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// RandomSessionNonce returns an unpredictable device/session nonce while the
// separate PersistentState sequence supplies monotonicity.
func RandomSessionNonce() ([32]byte, error) {
	var n [32]byte
	_, err := rand.Read(n[:])
	return n, err
}

// PageReceipt is a device-authenticated paging token. It is bound to the full
// request hash and raw message root, not merely the visible preview.
type PageReceipt struct {
	RequestHash Hash   `json:"request_hash"`
	MessageRoot Hash   `json:"message_root"`
	PagesRoot   Hash   `json:"pages_root"`
	PageIndex   int    `json:"page_index"`
	NextIndex   int    `json:"next_index"`
	Sequence    uint64 `json:"sequence"`
	PageHash    Hash   `json:"page_hash"`
	MAC         []byte `json:"mac,omitempty"`
}

func (r PageReceipt) macInput() []byte {
	req := r.RequestHash
	msg := r.MessageRoot
	pages := r.PagesRoot
	page := r.PageHash
	h := DomainHash("displaycommit/receipt/v1",
		req[:],
		msg[:],
		pages[:],
		int64Bytes(int64(r.PageIndex)),
		int64Bytes(int64(r.NextIndex)),
		uint64Bytes(r.Sequence),
		page[:],
	)
	return h[:]
}

func (r PageReceipt) verifyMAC(secret []byte) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write(r.macInput())
	want := mac.Sum(nil)
	return hmac.Equal(want, r.MAC)
}

// PersistentState is the small monotonic/secret state expected to survive a
// power loss. The current page itself can be supplied as a PagePacket.
type PersistentState struct {
	Secret       []byte `json:"secret"`
	NextSequence uint64 `json:"next_sequence"`
	HighWater    uint64 `json:"high_water"`
}

type Device struct {
	state PersistentState
	used  map[Hash]bool
}

func NewDevice(secret []byte) *Device {
	if len(secret) == 0 {
		secret = []byte("displaycommit-default-test-device-secret")
	}
	return &Device{state: PersistentState{Secret: append([]byte(nil), secret...), NextSequence: 1}, used: map[Hash]bool{}}
}

// NextNonce returns and persists the next monotonic device sequence. The
// returned value can serve as the monotonic half of a session nonce.
func (d *Device) NextNonce() uint64 {
	n := d.state.NextSequence
	d.state.NextSequence++
	return n
}

func (d *Device) State() PersistentState {
	return PersistentState{
		Secret:       append([]byte(nil), d.state.Secret...),
		NextSequence: d.state.NextSequence,
		HighWater:    d.state.HighWater,
	}
}

func (d *Device) MarshalState() ([]byte, error) {
	return json.Marshal(d.State())
}

func RestoreDevice(data []byte) (*Device, error) {
	var st PersistentState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	if st.NextSequence == 0 {
		st.NextSequence = 1
	}
	return &Device{state: st, used: map[Hash]bool{}}, nil
}

// PowerCycle simulates loss of volatile session state while retaining the
// persistent monotonic counter and device secret.
func (d *Device) PowerCycle() (*Device, error) {
	data, err := d.MarshalState()
	if err != nil {
		return nil, err
	}
	return RestoreDevice(data)
}

// IssueReceipt authenticates a displayed page packet. The optional trusted
// request hash is the hash previously approved/verified by the device.
func (d *Device) IssueReceipt(packet *PagePacket, trusted ...Hash) (PageReceipt, error) {
	if packet == nil {
		return PageReceipt{}, fmt.Errorf("displaycommit: nil page packet")
	}
	reqHash, ok := packet.Verify()
	if !ok {
		return PageReceipt{}, fmt.Errorf("displaycommit: invalid page proof")
	}
	if len(trusted) > 0 && trusted[0] != ZeroHash && trusted[0] != reqHash {
		return PageReceipt{}, fmt.Errorf("displaycommit: packet does not match trusted request")
	}
	seq := d.state.NextSequence
	d.state.NextSequence++
	r := PageReceipt{
		RequestHash: reqHash,
		MessageRoot: packet.Binding.MessageRoot,
		PagesRoot:   packet.Binding.PagesRoot,
		PageIndex:   packet.Page.Index,
		NextIndex:   packet.Page.Index + 1,
		Sequence:    seq,
		PageHash:    packet.Proof.LeafHash,
	}
	mac := hmac.New(sha256.New, d.state.Secret)
	mac.Write(r.macInput())
	r.MAC = mac.Sum(nil)
	return r, nil
}

// AcceptReceipt checks a next-page packet against a prior receipt. It rejects
// malformed proofs, wrong transactions/messages, wrong page order, replays,
// and monotonic-counter rollback, including after a simulated power cycle.
func (d *Device) AcceptReceipt(next *PagePacket, receipt PageReceipt) error {
	if next == nil {
		return fmt.Errorf("displaycommit: nil next page")
	}
	reqHash, ok := next.Verify()
	if !ok {
		return fmt.Errorf("displaycommit: invalid next-page proof")
	}
	if !receipt.verifyMAC(d.state.Secret) {
		return fmt.Errorf("displaycommit: receipt MAC invalid")
	}
	if receipt.RequestHash != reqHash {
		return fmt.Errorf("displaycommit: receipt belongs to another signing request")
	}
	if receipt.MessageRoot != next.Binding.MessageRoot {
		return fmt.Errorf("displaycommit: receipt message root mismatch")
	}
	if receipt.PagesRoot != next.Binding.PagesRoot {
		return fmt.Errorf("displaycommit: receipt page tree mismatch")
	}
	if receipt.NextIndex != next.Page.Index {
		return fmt.Errorf("displaycommit: page order mismatch: receipt expects %d, got %d", receipt.NextIndex, next.Page.Index)
	}
	rh := Sum256(receipt.macInput())
	if d.used[rh] {
		return fmt.Errorf("displaycommit: receipt replay detected")
	}
	if receipt.Sequence <= d.state.HighWater {
		return fmt.Errorf("displaycommit: stale receipt sequence %d <= %d", receipt.Sequence, d.state.HighWater)
	}
	d.used[rh] = true
	d.state.HighWater = receipt.Sequence
	if receipt.Sequence >= d.state.NextSequence {
		d.state.NextSequence = receipt.Sequence + 1
	}
	return nil
}

// VerifyReceipt is a stateless MAC/binding check. Stateful callers should use
// AcceptReceipt to obtain replay and monotonic protection.
func VerifyReceipt(secret []byte, receipt PageReceipt) bool {
	return receipt.verifyMAC(secret)
}
