package netsec

import (
	"errors"
	"io"
	"testing"
)

// fakeSend 描述分块替身的一次逻辑消息：正文被切成若干块送达，
// 末尾附带控制面信息（传入句柄、内核凭据、截断标志）。
type fakeSend struct {
	chunks       [][]byte
	fds          []int
	peer         *Creds
	controlCreds []Creds
	truncated    bool
}

// chunkedFake 是分块传输替身：模拟正文分块到达、控制面随末块
// 送达的传输层，Recv 时把块重新拼装成完整正文。
type chunkedFake struct {
	sends []fakeSend
}

func (f *chunkedFake) Recv() (*RawMessage, error) {
	if len(f.sends) == 0 {
		return nil, io.EOF
	}
	s := f.sends[0]
	f.sends = f.sends[1:]
	var payload []byte
	for _, c := range s.chunks {
		payload = append(payload, c...)
	}
	return &RawMessage{
		Payload:          payload,
		FDs:              append([]int(nil), s.fds...),
		PeerCreds:        s.peer,
		ControlCreds:     s.controlCreds,
		ControlTruncated: s.truncated,
	}, nil
}

type closeRecorder struct {
	closed []int
}

func (c *closeRecorder) close(fd int) error {
	c.closed = append(c.closed, fd)
	return nil
}

func newTestReceiver(fake *chunkedFake) (*Receiver, *closeRecorder) {
	rec := &closeRecorder{}
	r := NewReceiver(fake)
	r.CloseFD = rec.close
	return r, rec
}

func creds(pid int, uid uint32, gen uint64) *Creds {
	return &Creds{PID: pid, UID: uid, Generation: gen}
}

func equalFDs(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 正文完整但凭据缺失：即使分块全部到齐、正文可完整拼装，
// 没有内核凭据也必须拒绝，并关闭随消息传入的句柄。
func TestBodyCompleteButCredsMissing(t *testing.T) {
	fake := &chunkedFake{sends: []fakeSend{{
		chunks: [][]byte{[]byte(`{"op":"write","`), []byte(`data":"hello"}`)},
		fds:    []int{9001, 9002},
		peer:   nil,
	}}}
	r, rec := newTestReceiver(fake)

	msg, err := r.Recv()
	if !errors.Is(err, ErrMissingCreds) {
		t.Fatalf("err = %v, want ErrMissingCreds", err)
	}
	if msg != nil {
		t.Fatalf("msg = %+v, want nil", msg)
	}
	if !equalFDs(rec.closed, []int{9001, 9002}) {
		t.Fatalf("closed fds = %v, want [9001 9002]", rec.closed)
	}
}

// 多个凭据冲突：同一消息上控制面出现多份凭据且互相矛盾，
// 至少一份与内核凭据不符，整条拒绝。
func TestConflictingCredentials(t *testing.T) {
	peer := creds(4242, 501, 7)
	fake := &chunkedFake{sends: []fakeSend{{
		chunks: [][]byte{[]byte("ping")},
		fds:    []int{9003},
		peer:   peer,
		controlCreds: []Creds{
			{PID: 4242, UID: 501, Generation: 7},
			{PID: 4242, UID: 0, Generation: 7}, // 自称 root，与内核凭据冲突
		},
	}}}
	r, rec := newTestReceiver(fake)

	msg, err := r.Recv()
	if !errors.Is(err, ErrConflictingCreds) {
		t.Fatalf("err = %v, want ErrConflictingCreds", err)
	}
	if msg != nil {
		t.Fatalf("msg = %+v, want nil", msg)
	}
	if !equalFDs(rec.closed, []int{9003}) {
		t.Fatalf("closed fds = %v, want [9003]", rec.closed)
	}
}

// 发送者退出后进程号复用：首条消息确立连接身份，随后进程号
// 相同但代次不同的消息说明对端已换成另一个进程，必须拒绝。
func TestPIDReuseAfterSenderExit(t *testing.T) {
	fake := &chunkedFake{sends: []fakeSend{
		{
			chunks: [][]byte{[]byte("first")},
			peer:   creds(4242, 501, 7),
		},
		{
			chunks: [][]byte{[]byte("second")},
			fds:    []int{9004},
			peer:   creds(4242, 501, 8), // 进程号复用：代次已变
		},
	}}
	r, rec := newTestReceiver(fake)

	msg, err := r.Recv()
	if err != nil {
		t.Fatalf("first Recv err = %v", err)
	}
	if got := msg.Creds; got.PID != 4242 || got.Generation != 7 {
		t.Fatalf("first msg creds = %v", got)
	}

	msg, err = r.Recv()
	if !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("err = %v, want ErrIdentityChanged", err)
	}
	if msg != nil {
		t.Fatalf("msg = %+v, want nil", msg)
	}
	if !equalFDs(rec.closed, []int{9004}) {
		t.Fatalf("closed fds = %v, want [9004]", rec.closed)
	}
}

// 控制消息截断：无法确认句柄集合完整，整条拒绝并关闭
// 已收到的句柄，防止泄漏。
func TestControlTruncatedRejectsAndClosesFDs(t *testing.T) {
	fake := &chunkedFake{sends: []fakeSend{{
		chunks:    [][]byte{[]byte("data")},
		fds:       []int{9005, 9006},
		peer:      creds(4242, 501, 7),
		truncated: true,
	}}}
	r, rec := newTestReceiver(fake)

	msg, err := r.Recv()
	if !errors.Is(err, ErrControlTruncated) {
		t.Fatalf("err = %v, want ErrControlTruncated", err)
	}
	if msg != nil {
		t.Fatalf("msg = %+v, want nil", msg)
	}
	if !equalFDs(rec.closed, []int{9005, 9006}) {
		t.Fatalf("closed fds = %v, want [9005 9006]", rec.closed)
	}
}

// 载荷自报身份不可信：正文里自称 root，接收方必须绑定内核
// 凭据（uid 501），且正文、凭据、句柄落在同一消息上。
func TestKernelCredsOverrideSelfReported(t *testing.T) {
	body := `{"uid":0,"user":"root","op":"read"}` // 载荷自报 root
	fake := &chunkedFake{sends: []fakeSend{{
		chunks: [][]byte{[]byte(body[:10]), []byte(body[10:])},
		fds:    []int{9007},
		peer:   creds(4242, 501, 7),
	}}}
	r, rec := newTestReceiver(fake)

	msg, err := r.Recv()
	if err != nil {
		t.Fatalf("Recv err = %v", err)
	}
	if string(msg.Payload) != body {
		t.Fatalf("payload = %q, want %q", msg.Payload, body)
	}
	if msg.Creds.UID != 501 || msg.Creds.PID != 4242 || msg.Creds.Generation != 7 {
		t.Fatalf("creds = %v, want kernel-attached uid=501", msg.Creds)
	}
	if !equalFDs(msg.FDs, []int{9007}) {
		t.Fatalf("fds = %v, want [9007]", msg.FDs)
	}
	if err := msg.Close(); err != nil {
		t.Fatalf("Close err = %v", err)
	}
	if !equalFDs(rec.closed, []int{9007}) {
		t.Fatalf("closed fds = %v, want [9007]", rec.closed)
	}
}

// 同连接上身份保持一致时消息连续通过。
func TestStableIdentityAccepted(t *testing.T) {
	fake := &chunkedFake{sends: []fakeSend{
		{chunks: [][]byte{[]byte("m1")}, peer: creds(4242, 501, 7)},
		{chunks: [][]byte{[]byte("m2")}, peer: creds(4242, 501, 7)},
	}}
	r, _ := newTestReceiver(fake)
	for i, want := range []string{"m1", "m2"} {
		msg, err := r.Recv()
		if err != nil {
			t.Fatalf("Recv %d err = %v", i, err)
		}
		if string(msg.Payload) != want {
			t.Fatalf("payload %d = %q, want %q", i, msg.Payload, want)
		}
	}
}
