package acceptance_test

import (
	"testing"

	recovery "prescreen.local/ps-4b0c066dfd"
	subrecovery "prescreen.local/ps-4b0c066dfd/recovery"
)

func TestExternalImportSurface(t *testing.T) {
	rm := recovery.New(25_000)
	r := rm.Handle(recovery.Send(recovery.SendEvent{
		Time: 0, Space: recovery.Handshake, PacketNumber: 0,
		Bytes: 1200, AckEliciting: true,
	}))
	if r.Err != nil || r.NextTimeout() != 999_000 {
		t.Fatalf("root import result=%+v", r)
	}

	sub := subrecovery.New(25_000)
	r = sub.Send(subrecovery.SendEvent{
		Time: 0, Space: subrecovery.Handshake, PacketNumber: 0,
		Bytes: 1200, AckEliciting: true,
	})
	if r.Err != nil || r.Timer.Space != subrecovery.Handshake {
		t.Fatalf("subpackage result=%+v", r)
	}
}
