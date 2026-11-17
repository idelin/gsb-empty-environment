package netsec

import (
	"errors"
	"net"
	"testing"
)

func TestRelayLoopAdmissionUnderAttack(t *testing.T) {
	control, peer := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{IP: net.IPv4zero}, Policy{})

	target := &capturePacketConn{}
	relay := func(src *net.UDPAddr, packet []byte) error {
		gotTarget, payload, err := association.Admit(src, packet)
		if err != nil {
			return err
		}
		_, err = target.WriteTo(payload, &net.UDPAddr{
			IP:   net.ParseIP(gotTarget.Host),
			Port: int(gotTarget.Port),
		})
		return err
	}

	legit := udpSource("127.0.0.1", 46000)
	attacker := udpSource("127.0.0.1", 46001)
	drifted := udpSource("127.0.0.1", 46002)
	valid := mustDatagram(t, Address{Host: "192.0.2.70", Port: 53}, "legitimate")

	if err := relay(legit, valid); err != nil {
		t.Fatalf("legitimate datagram rejected: %v", err)
	}
	if writes := target.snapshot(); len(writes) != 1 || string(writes[0].packet) != "legitimate" {
		t.Fatalf("target writes after legitimate datagram = %#v", writes)
	}

	// A different Internet source sent to the public relay port must not be
	// forwarded, even though its SOCKS5 header is otherwise valid.
	if err := relay(attacker, valid); !errors.Is(err, ErrSourcePort) {
		t.Fatalf("forged source error = %v, want ErrSourcePort", err)
	}
	if writes := target.snapshot(); len(writes) != 1 {
		t.Fatalf("forged source was forwarded: %#v", writes)
	}

	// UDP source-port drift from the legitimate IP must also be rejected.
	if err := relay(drifted, valid); !errors.Is(err, ErrSourcePort) {
		t.Fatalf("port drift error = %v, want ErrSourcePort", err)
	}
	if writes := target.snapshot(); len(writes) != 1 {
		t.Fatalf("port drift was forwarded: %#v", writes)
	}

	fragmented := []byte{0, 0, 1, atypIPv4, 192, 0, 2, 1, 0, 53}
	if err := relay(legit, fragmented); !errors.Is(err, ErrFragmented) {
		t.Fatalf("FRAG error = %v, want ErrFragmented", err)
	}
	if writes := target.snapshot(); len(writes) != 1 {
		t.Fatalf("FRAG packet was forwarded: %#v", writes)
	}

	// Once the TCP control connection disappears the relay may still receive
	// packets, but the association must not admit or forward them.
	if err := peer.Close(); err != nil {
		t.Fatalf("close control: %v", err)
	}
	waitAssociationDone(t, association)
	if err := relay(legit, valid); !errors.Is(err, ErrAssociationClosed) {
		t.Fatalf("post-close error = %v, want ErrAssociationClosed", err)
	}
	if writes := target.snapshot(); len(writes) != 1 {
		t.Fatalf("post-close datagram was forwarded: %#v", writes)
	}
}
