package netsec_test

import (
	"bytes"
	"errors"
	"testing"

	netsec "example.org/netsec/q0155"
)

var (
	hostKey  = []byte("host-server-shared-secret-01")
	keyAlice = []byte("alice-inner-credential")
	keyBob   = []byte("bob-inner-credential")
)

const outerAnon = "anonymous@corp.example"

// proxy 是宿主与服务器之间不可信的中继：正常转发，但记录一切，
// 测试用它实施跨会话拼接与重放。
type proxy struct {
	srv   *netsec.Server
	reqs  []netsec.Request
	resps []netsec.Response
}

func (p *proxy) roundTrip(req netsec.Request) netsec.Response {
	p.reqs = append(p.reqs, req)
	resp := p.srv.Handle(req)
	p.resps = append(p.resps, resp)
	return resp
}

func newTestBed(t *testing.T) (*netsec.Host, *proxy) {
	t.Helper()
	srv := netsec.NewServer(hostKey)
	srv.AddCredential("alice", keyAlice)
	srv.AddCredential("bob", keyBob)
	srv.AddRealmRoute("corp.example", "corp-access-profile")
	return netsec.NewHost(srv, hostKey), &proxy{srv: srv}
}

// mustRound 推进一轮：接入端 -> 宿主 -> 服务器 -> 宿主 -> 接入端。
// 返回 nil 表示消息被宿主本地处理（密钥确认完成）。
func mustRound(t *testing.T, px *proxy, port *netsec.Port, e netsec.EAP) *netsec.EAP {
	t.Helper()
	req, err := port.ReceiveFromSupplicant(e)
	if err != nil {
		t.Fatalf("ReceiveFromSupplicant: %v", err)
	}
	if req == nil {
		return nil
	}
	resp := px.roundTrip(*req)
	out, err := port.ReceiveFromServer(resp)
	if err != nil {
		t.Fatalf("ReceiveFromServer: %v", err)
	}
	return out
}

// driveToAuthorized 完整跑通一次成功认证。
func driveToAuthorized(t *testing.T, px *proxy, port *netsec.Port, sup *netsec.Supplicant) {
	t.Helper()
	eap := sup.Identity()
	for i := 0; i < 8; i++ {
		req, err := port.ReceiveFromSupplicant(eap)
		if err != nil {
			t.Fatalf("ReceiveFromSupplicant: %v", err)
		}
		if req == nil {
			if _, ok := port.Authorized(); ok {
				return
			}
			t.Fatal("exchange ended without authorization")
		}
		resp := px.roundTrip(*req)
		out, err := port.ReceiveFromServer(resp)
		if err != nil {
			t.Fatalf("ReceiveFromServer: %v", err)
		}
		if out == nil {
			t.Fatal("host produced no EAP for supplicant")
		}
		eap, err = sup.Handle(*out)
		if err != nil {
			t.Fatalf("supplicant: %v", err)
		}
	}
	t.Fatal("too many rounds")
}

func findSuccess(t *testing.T, px *proxy) netsec.Response {
	t.Helper()
	for _, r := range px.resps {
		if r.Result == netsec.ResultSuccess {
			return r
		}
	}
	t.Fatal("no success response recorded")
	return netsec.Response{}
}

func findRequestEAP(t *testing.T, px *proxy, phase netsec.Phase) netsec.EAP {
	t.Helper()
	for _, q := range px.reqs {
		if q.EAP.Phase == phase {
			return q.EAP
		}
	}
	t.Fatalf("no request with phase %d recorded", phase)
	return netsec.EAP{}
}

// 两个并行隧道、相同外层名字：各自独立授权，互不混淆。
func TestParallelTunnelsSameOuterName(t *testing.T) {
	host, px := newTestBed(t)

	p1 := host.Attach("ge-0/0/1")
	p2 := host.Attach("ge-0/0/2")
	supA := netsec.NewSupplicant(outerAnon, "alice", keyAlice)
	supB := netsec.NewSupplicant(outerAnon, "bob", keyBob)

	a1 := supA.Identity()
	b1 := supB.Identity()
	if a1.OuterID != b1.OuterID {
		t.Fatal("precondition failed: outer names must be identical")
	}

	// 交错推进两条隧道。
	aTun := mustRound(t, px, p1, a1)
	bTun := mustRound(t, px, p2, b1)

	aInner, err := supA.Handle(*aTun)
	if err != nil {
		t.Fatal(err)
	}
	bInner, err := supB.Handle(*bTun)
	if err != nil {
		t.Fatal(err)
	}

	aConf := mustRound(t, px, p1, aInner)
	bConf := mustRound(t, px, p2, bInner)

	aMIC, err := supA.Handle(*aConf)
	if err != nil {
		t.Fatal(err)
	}
	bMIC, err := supB.Handle(*bConf)
	if err != nil {
		t.Fatal(err)
	}

	mustRound(t, px, p1, aMIC)
	mustRound(t, px, p2, bMIC)

	if id, ok := p1.Authorized(); !ok || id != "alice" {
		t.Fatalf("p1 authorized = %q,%v; want alice,true", id, ok)
	}
	if id, ok := p2.Authorized(); !ok || id != "bob" {
		t.Fatalf("p2 authorized = %q,%v; want bob,true", id, ok)
	}
}

// 跨会话拼接：把会话 A 的内层成功套到会话 B 的端口上，必须失败。
func TestCrossSessionSpliceRejected(t *testing.T) {
	host, px := newTestBed(t)

	p1 := host.Attach("ge-0/0/1")
	supA := netsec.NewSupplicant(outerAnon, "alice", keyAlice)
	driveToAuthorized(t, px, p1, supA)

	successA := findSuccess(t, px)
	innerA := findRequestEAP(t, px, netsec.PhaseInner)

	// 会话 B：同一外层名字、另一端口，进行到隧道建立之后。
	p2 := host.Attach("ge-0/0/2")
	supB := netsec.NewSupplicant(outerAnon, "bob", keyBob)
	if out := mustRound(t, px, p2, supB.Identity()); out == nil {
		t.Fatal("expected tunnel challenge for session B")
	}

	// 拼接 1：把 A 的成功响应直接塞给 B 的端口会话。
	if _, err := p2.ReceiveFromServer(successA); err == nil {
		t.Fatal("spliced success response was accepted")
	}
	if _, ok := p2.Authorized(); ok {
		t.Fatal("port authorized via spliced success")
	}

	// 拼接 2：把 A 的内层成功消息塞进 B 的服务器会话。
	req, err := p2.ReceiveFromSupplicant(innerA)
	if err != nil {
		t.Fatalf("relay spliced inner: %v", err)
	}
	resp := px.roundTrip(*req)
	if _, err := p2.ReceiveFromServer(resp); !errors.Is(err, netsec.ErrRejected) {
		t.Fatalf("spliced inner: got %v, want ErrRejected", err)
	}
	if _, ok := p2.Authorized(); ok {
		t.Fatal("port authorized via spliced inner")
	}
	if got := p2.State(); got != netsec.StateHeld {
		t.Fatalf("port state = %v, want StateHeld", got)
	}
}

// 端口重接：旧会话的成功绑定立即失效，重放不能恢复授权。
func TestPortReconnectInvalidatesBinding(t *testing.T) {
	host, px := newTestBed(t)

	port := host.Attach("ge-0/0/1")
	sup := netsec.NewSupplicant(outerAnon, "alice", keyAlice)
	driveToAuthorized(t, px, port, sup)
	if _, ok := port.Authorized(); !ok {
		t.Fatal("precondition: port not authorized")
	}

	successOld := findSuccess(t, px)
	identityOld := findRequestEAP(t, px, netsec.PhaseIdentity)
	innerOld := findRequestEAP(t, px, netsec.PhaseInner)

	// 物理重接：同一端口 ID，全新端口实例。
	port2 := host.Attach("ge-0/0/1")
	if host.Port("ge-0/0/1") != port2 {
		t.Fatal("host did not replace port instance on reconnect")
	}

	// 攻击者在新实例上重放旧交换：服务器给出全新挑战。
	if out := mustRound(t, px, port2, identityOld); out == nil {
		t.Fatal("expected tunnel challenge on replayed identity")
	}

	// 重放旧成功响应：端口实例与交换摘要均不匹配。
	if _, err := port2.ReceiveFromServer(successOld); err == nil {
		t.Fatal("replayed success accepted after reconnect")
	}

	// 重放旧内层响应：挑战与隧道密钥已变，服务器拒绝。
	req, err := port2.ReceiveFromSupplicant(innerOld)
	if err != nil {
		t.Fatalf("relay replayed inner: %v", err)
	}
	resp := px.roundTrip(*req)
	if _, err := port2.ReceiveFromServer(resp); !errors.Is(err, netsec.ErrRejected) {
		t.Fatalf("replayed inner: got %v, want ErrRejected", err)
	}
	if _, ok := port2.Authorized(); ok {
		t.Fatal("port authorized via replay after reconnect")
	}
}

// 外层身份只用于路由：即使外层谎称他人，授权主体仍是内层身份。
func TestOuterIdentityNotAuthorizationSubject(t *testing.T) {
	host, px := newTestBed(t)

	port := host.Attach("ge-0/0/3")
	// 外层声称 alice，内层真实认证为 bob。
	sup := netsec.NewSupplicant("alice@corp.example", "bob", keyBob)
	driveToAuthorized(t, px, port, sup)

	id, ok := port.Authorized()
	if !ok || id != "bob" {
		t.Fatalf("authorized subject = %q,%v; want bob,true (inner identity)", id, ok)
	}
}

// 成功绑定的每个字段被篡改都必须被拒绝，未篡改响应仍可完成。
func TestSuccessBindingTamperRejected(t *testing.T) {
	host, px := newTestBed(t)

	port := host.Attach("ge-0/0/1")
	sup := netsec.NewSupplicant(outerAnon, "alice", keyAlice)

	tun := mustRound(t, px, port, sup.Identity())
	inner, err := sup.Handle(*tun)
	if err != nil {
		t.Fatal(err)
	}
	req, err := port.ReceiveFromSupplicant(inner)
	if err != nil {
		t.Fatal(err)
	}
	resp := px.roundTrip(*req)
	if resp.Result != netsec.ResultSuccess {
		t.Fatal("expected success response")
	}

	clone := func(r netsec.Response) netsec.Response {
		r.State = bytes.Clone(r.State)
		r.Exporter = bytes.Clone(r.Exporter)
		r.Digest = bytes.Clone(r.Digest)
		r.PortInst = bytes.Clone(r.PortInst)
		r.AuthMAC = bytes.Clone(r.AuthMAC)
		return r
	}
	tampers := []struct {
		name   string
		mutate func(*netsec.Response)
	}{
		{"exporter", func(r *netsec.Response) { r.Exporter[0] ^= 1 }},
		{"digest", func(r *netsec.Response) { r.Digest[0] ^= 1 }},
		{"port-instance", func(r *netsec.Response) { r.PortInst[0] ^= 1 }},
		{"inner-id", func(r *netsec.Response) { r.InnerID = "mallory" }},
		{"state-cookie", func(r *netsec.Response) { r.State[0] ^= 1 }},
	}
	for _, tc := range tampers {
		bad := clone(resp)
		tc.mutate(&bad)
		if _, err := port.ReceiveFromServer(bad); err == nil {
			t.Fatalf("tampered %s was accepted", tc.name)
		}
		if _, ok := port.Authorized(); ok {
			t.Fatalf("authorized after tampered %s", tc.name)
		}
	}

	// 原始响应仍然有效，交换可正常完成。
	out, err := port.ReceiveFromServer(resp)
	if err != nil {
		t.Fatalf("clean response rejected: %v", err)
	}
	mic, err := sup.Handle(*out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := port.ReceiveFromSupplicant(mic); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if id, ok := port.Authorized(); !ok || id != "alice" {
		t.Fatalf("authorized = %q,%v; want alice,true", id, ok)
	}
}

// 没有隧道导出值的攻击者无法完成密钥确认。
func TestConfirmRequiresTunnelExporter(t *testing.T) {
	host, px := newTestBed(t)

	port := host.Attach("ge-0/0/1")
	sup := netsec.NewSupplicant(outerAnon, "alice", keyAlice)

	tun := mustRound(t, px, port, sup.Identity())
	inner, err := sup.Handle(*tun)
	if err != nil {
		t.Fatal(err)
	}
	req, err := port.ReceiveFromSupplicant(inner)
	if err != nil {
		t.Fatal(err)
	}
	resp := px.roundTrip(*req)
	if _, err := port.ReceiveFromServer(resp); err != nil {
		t.Fatalf("ReceiveFromServer: %v", err)
	}

	forged := netsec.EAP{Phase: netsec.PhaseConfirm, Body: bytes.Repeat([]byte{0x42}, 32)}
	if _, err := port.ReceiveFromSupplicant(forged); !errors.Is(err, netsec.ErrConfirmFailed) {
		t.Fatalf("forged confirm: got %v, want ErrConfirmFailed", err)
	}
	if _, ok := port.Authorized(); ok {
		t.Fatal("authorized without tunnel exporter knowledge")
	}
}
