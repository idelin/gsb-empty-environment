package roaring

import (
	"sync"
	"testing"
)

func runTransfer(t *testing.T, fi *FaultInjector, b *Bitmap) *Bitmap {
	t.Helper()
	link := NewLossyLink(fi)
	s, r := link.Ends()
	var wg sync.WaitGroup
	wg.Add(2)
	var recv *Bitmap
	var serr, rerr error
	go func() {
		defer wg.Done()
		recv, rerr = ReceiveBitmap(r)
	}()
	go func() {
		defer wg.Done()
		serr = SendBitmap(b, s)
	}()
	wg.Wait()
	if serr != nil {
		t.Fatalf("SendBitmap: %v", serr)
	}
	if rerr != nil {
		t.Fatalf("ReceiveBitmap: %v", rerr)
	}
	return recv
}

func transferSample() *Bitmap {
	b := New()
	for i := uint64(0); i < 100000; i++ {
		b.Add(i * 13)
	}
	b.AddRange(5000000, 5600000)
	return b
}

func TestTransferPerfectLink(t *testing.T) {
	want := transferSample()
	got := runTransfer(t, NewFaultInjector(), want)
	if !got.Equal(want) {
		t.Fatal("received bitmap differs from sent bitmap")
	}
}

func TestTransferPacketLoss(t *testing.T) {
	want := transferSample()
	fi := NewFaultInjector()
	fi.PacketLossRate = 0.30 // 30% of packets vanish
	got := runTransfer(t, fi, want)
	if !got.Equal(want) {
		t.Fatal("bitmap corrupted despite retransmission")
	}
}

func TestTransferDeterministicDrops(t *testing.T) {
	want := transferSample()
	fi := NewFaultInjector()
	fi.DropFirstPackets = 100 // first 100 packets always lost
	got := runTransfer(t, fi, want)
	if !got.Equal(want) {
		t.Fatal("bitmap corrupted despite deterministic drops")
	}
}

func TestTransferPacketCorruption(t *testing.T) {
	want := transferSample()
	fi := NewFaultInjector()
	fi.PacketCorruptRate = 0.25 // 25% of packets arrive bit-flipped
	got := runTransfer(t, fi, want)
	if !got.Equal(want) {
		t.Fatal("bitmap corrupted despite CRC retransmission")
	}
}

func TestTransferHeavyChaos(t *testing.T) {
	want := transferSample()
	fi := NewFaultInjector()
	fi.PacketLossRate = 0.40
	fi.PacketCorruptRate = 0.30
	fi.DropFirstPackets = 20
	got := runTransfer(t, fi, want)
	if !got.Equal(want) {
		t.Fatal("bitmap corrupted under combined loss+corruption")
	}
}

func TestTransferEmptyBitmap(t *testing.T) {
	want := New()
	got := runTransfer(t, NewFaultInjector(), want)
	if got.Cardinality() != 0 {
		t.Fatal("empty bitmap did not survive transfer")
	}
}
