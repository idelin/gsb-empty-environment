package netsec

import (
	"net/netip"
	"sync"
	"testing"
)

func TestParseRoundTripAndMalformed(t *testing.T) {
	raw := offerPacket(addrProvider, 3600)
	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(msg.Entries) != 1 || len(msg.Options) != 1 {
		t.Fatalf("counts = %d entries, %d options", len(msg.Entries), len(msg.Options))
	}
	e := msg.Entries[0]
	if e.Type != EntryOfferService || e.ServiceID != keySvc.ServiceID ||
		e.InstanceID != keySvc.InstanceID || e.MajorVersion != keySvc.MajorVersion ||
		e.TTL != 3600 || e.MinorVersion != 1 || e.Num1 != 1 || e.Index1 != 0 {
		t.Fatalf("entry mismatch: %+v", e)
	}
	o := msg.Options[0]
	if o.Type != OptionIPv4Endpoint || o.Address != addrProvider || o.Port != 30501 || o.L4Proto != L4UDP {
		t.Fatalf("option mismatch: %+v", o)
	}
	if msg.SessionID != 1 || msg.ClientID != 0 || msg.ReturnCode != 0 {
		t.Fatalf("header mismatch: %+v", msg)
	}

	if _, err := ParseMessage(raw[:10]); err == nil {
		t.Fatal("truncated header: expected error")
	}
	tampered := append([]byte(nil), raw...)
	tampered[5] = 0xFF // break SOME/IP length consistency
	if _, err := ParseMessage(tampered); err == nil {
		t.Fatal("tampered length: expected error")
	}
	// 选项声明的长度超出剩余缓冲区时必须报错，而不是越界读取。
	truncatedOpt := append([]byte(nil), raw[:len(raw)-4]...)
	if _, err := ParseMessage(truncatedOpt); err == nil {
		t.Fatal("truncated option: expected error")
	}
	// 非 SD 报文（错误的 Message ID）直接拒绝。
	notSD := append([]byte(nil), raw...)
	notSD[2] = 0x00
	if _, err := ParseMessage(notSD); err == nil {
		t.Fatal("non-SD message: expected error")
	}
}

func TestOfferEndpointMustMatchSource(t *testing.T) {
	c := NewController()
	c.RegisterProvider(keySvc, addrProvider)

	// 合法提供者却在 Offer 里通告受害者端点：拒绝，防止借此重定向。
	verdicts, err := c.AdmitPacket(offerPacket(addrVictim, 3600), addrProvider)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonEndpointSourceMismatch {
		t.Fatalf("mismatched offer endpoint = %+v, want reject/%s", verdicts[0], ReasonEndpointSourceMismatch)
	}
	if _, ok := c.CurrentProvider(keySvc); ok {
		t.Fatal("rejected offer must not register an active provider")
	}
}

func TestUnregisterProvider(t *testing.T) {
	c := NewController()
	c.RegisterProvider(keySvc, addrProvider)
	c.UnregisterProvider(keySvc, addrProvider)

	verdicts, err := c.AdmitPacket(offerPacket(addrProvider, 3600), addrProvider)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonServiceNotRegistered {
		t.Fatalf("offer after unregister = %+v, want reject", verdicts[0])
	}
}

func TestMulticastOptionIsNotEnforcedAgainstSource(t *testing.T) {
	c := NewController()

	// 组播选项承载的是组地址而非发送者地址，不能参与源地址一致性校验。
	multicast := func(typ uint8) []byte {
		b := make([]byte, 11)
		b[1] = 0x09
		b[2] = typ
		group := netip.MustParseAddr("239.192.0.1").As4()
		copy(b[4:8], group[:])
		b[8] = L4UDP
		b[10] = 0x64
		e := entry(EntrySubscribe, 0, 1, 0, 0, keySvc, 10, 0)
		return packet([][]byte{e}, [][]byte{b})
	}

	verdicts, err := c.AdmitPacket(multicast(OptionIPv4Multicast), addrAttacker)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if verdicts[0].Verdict != VerdictAccept {
		t.Fatalf("multicast-only subscribe = %+v, want accept", verdicts[0])
	}
}

func TestAdmitRejectsInvalidSource(t *testing.T) {
	c := NewController()
	verdicts, err := c.AdmitPacket(offerPacket(addrProvider, 3600), netip.Addr{})
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if verdicts[0].Verdict != VerdictReject || verdicts[0].Reason != ReasonInvalidSource {
		t.Fatalf("zero source = %+v, want reject/%s", verdicts[0], ReasonInvalidSource)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := NewController()
	c.RegisterProvider(keySvc, addrProvider)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = c.AdmitPacket(offerPacket(addrProvider, 3600), addrProvider)
		}()
		go func() {
			defer wg.Done()
			_, _ = c.CurrentProvider(keySvc)
		}()
	}
	wg.Wait()
}
