package wireguard_test

import (
	"net/netip"
	"testing"

	wireguard "prescreen.local/ps-394659603d"
)

func TestExternalPackageAPI(t *testing.T) {
	aKeys, err := wireguard.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bKeys, err := wireguard.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	a, err := wireguard.NewPeer(wireguard.Config{
		LocalPrivateKey: aKeys.Private,
		RemotePublicKey: bKeys.Public,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := wireguard.NewPeer(wireguard.Config{
		LocalPrivateKey: bKeys.Private,
		RemotePublicKey: aKeys.Public,
	})
	if err != nil {
		t.Fatal(err)
	}

	initiation, err := a.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	response, err := b.HandlePacket(initiation, netip.MustParseAddrPort("192.0.2.1:1000"))
	if err != nil || response.Action != wireguard.ActionHandshakeResponse {
		t.Fatalf("response: %+v %v", response, err)
	}
	if _, err := a.HandlePacket(response.Reply, netip.MustParseAddrPort("192.0.2.2:2000")); err != nil {
		t.Fatal(err)
	}

	packet, err := a.Encrypt([]byte("external API"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.HandlePacket(packet, netip.MustParseAddrPort("192.0.2.1:1000"))
	if err != nil || string(result.Payload) != "external API" || !result.Confirmed {
		t.Fatalf("transport: %+v %v", result, err)
	}
}
