package pha_test

import (
	"errors"
	"fmt"

	"example.org/netsec/q0226/pha"
)

// demoConn is a minimal pha.Conn backed by a buffered channel, standing in for
// a real TLS 1.3 record-layer transport.
type demoConn struct {
	in chan []byte
}

func (d *demoConn) WriteHandshake(msg []byte) error {
	d.in <- append([]byte(nil), msg...)
	return nil
}

func (d *demoConn) WriteAlert(level, description byte) error {
	d.in <- []byte{0x15, level, description}
	return nil
}

func (d *demoConn) ReadHandshake() ([]byte, error) {
	msg, ok := <-d.in
	if !ok {
		return nil, errors.New("closed")
	}
	return msg, nil
}

func ExampleRegistry() {
	server := &demoConn{in: make(chan []byte, 8)}

	registry := pha.NewRegistry()
	registry.Register("conn-42", server, true) // ClientHello had post_handshake_auth.

	context, err := registry.CertificateRequest("conn-42", []uint16{0x0804})
	if err != nil {
		fmt.Println("request:", err)
		return
	}
	request := <-server.in

	wireContext, _, err := pha.ParseCertificateRequest(request)
	if err != nil || string(wireContext) != string(context) {
		fmt.Println("mismatch")
		return
	}

	// The client echoes the exact context inside its Certificate message.
	certificate, err := pha.MarshalCertificate(context, []byte("leaf DER"))
	if err != nil {
		fmt.Println("marshal:", err)
		return
	}
	leaf, err := registry.HandleCertificate("conn-42", certificate)
	if err != nil {
		fmt.Println("handle:", err)
		return
	}
	fmt.Printf("accepted %d-byte context, %d-byte leaf\n", len(context), len(leaf))

	// Replaying the same Certificate is rejected with illegal_parameter.
	if _, err := registry.HandleCertificate("conn-42", certificate); err != nil {
		var alert *pha.AlertError
		if errors.As(err, &alert) {
			fmt.Println("replay rejected with alert", alert.Description)
		}
	}

	// Output:
	// accepted 16-byte context, 8-byte leaf
	// replay rejected with alert 47
}
