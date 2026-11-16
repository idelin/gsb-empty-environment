package netsec

import (
	"math"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	clientPath = netip.MustParseAddrPort("192.0.2.10:50000")
	otherPath  = netip.MustParseAddrPort("192.0.2.10:50001")
)

func TestInitialGrantsThreeTimesReceived(t *testing.T) {
	b := NewBudget()
	if !b.ReceiveInitial(clientPath, 1200) {
		t.Fatal("1200-byte Initial was not accepted")
	}

	stats, ok := b.PathStats(clientPath)
	if !ok {
		t.Fatal("path was not recorded")
	}
	if stats.Received != 1200 || stats.Limit != 3600 || stats.Remaining != 3600 {
		t.Fatalf("unexpected initial budget: %+v", stats)
	}

	if b.Reserve(clientPath, 3601, SendData) {
		t.Fatal("reservation larger than the 3600-byte allowance succeeded")
	}
	if got := b.SentBytes(clientPath); got != 0 {
		t.Fatalf("failed reservation charged %d bytes", got)
	}

	if !b.Reserve(clientPath, 3000, SendData) {
		t.Fatal("reservation within the allowance failed")
	}
	if !b.Reserve(clientPath, 600, SendData) {
		t.Fatal("reservation of the exact remaining allowance failed")
	}
	if b.Reserve(clientPath, 1, SendData) {
		t.Fatal("reservation beyond the allowance succeeded")
	}
	if got := b.SentBytes(clientPath); got != 3600 {
		t.Fatalf("sent bytes = %d, want 3600", got)
	}
	if got := b.RemainingBytes(clientPath); got != 0 {
		t.Fatalf("remaining bytes = %d, want 0", got)
	}
}

func TestPTOProbeCannotBypassBudget(t *testing.T) {
	t.Run("at the limit", func(t *testing.T) {
		b := NewBudget()
		b.ReceiveInitial(clientPath, 1200)
		if !b.Reserve(clientPath, 3599, SendData) {
			t.Fatal("ordinary reservation failed")
		}
		if !b.Reserve(clientPath, 1, SendPTOProbe) {
			t.Fatal("PTO probe within the remaining allowance failed")
		}
		if b.Reserve(clientPath, 1, SendPTOProbe) {
			t.Fatal("PTO probe exceeded the anti-amplification limit")
		}
	})

	t.Run("after ordinary data exhausted budget", func(t *testing.T) {
		b := NewBudget()
		b.ReceiveInitial(clientPath, 1200)
		if !b.Reserve(clientPath, 3600, SendData) {
			t.Fatal("ordinary reservation failed")
		}
		if b.CanSend(clientPath, 1, SendPTOProbe) {
			t.Fatal("CanSend allowed a PTO probe beyond the limit")
		}
		if b.Reserve(clientPath, 1, SendPTOProbe) {
			t.Fatal("PTO probe bypassed the exhausted budget")
		}
	})

	t.Run("with no accepted Initial", func(t *testing.T) {
		b := NewBudget()
		if b.ReceiveInitial(clientPath, 1199) {
			t.Fatal("1199-byte Initial was accepted")
		}
		if b.Reserve(clientPath, 1, SendPTOProbe) {
			t.Fatal("PTO probe created a budget without received bytes")
		}
	})
}

func TestSmallInitialIsDroppedAndNotCounted(t *testing.T) {
	b := NewBudget()

	if b.ReceiveInitial(clientPath, 1199) {
		t.Fatal("1199-byte Initial was accepted")
	}
	if _, ok := b.PathStats(clientPath); ok {
		t.Fatal("dropped Initial created path accounting")
	}
	if got := b.ReceivedBytes(clientPath); got != 0 {
		t.Fatalf("dropped Initial counted %d bytes", got)
	}
	if b.Reserve(clientPath, 1, SendData) {
		t.Fatal("dropped Initial produced sending budget")
	}

	if !b.ReceiveInitial(clientPath, 1200) {
		t.Fatal("1200-byte Initial was not accepted")
	}
	if b.ReceiveInitial(clientPath, 1) {
		t.Fatal("1-byte Initial was accepted after a valid Initial")
	}
	if got := b.ReceivedBytes(clientPath); got != 1200 {
		t.Fatalf("received bytes = %d, want 1200", got)
	}
}

func TestNonInitialDatagramBelow1200Counts(t *testing.T) {
	b := NewBudget()
	if !b.ReceiveDatagram(clientPath, 600) {
		t.Fatal("non-Initial datagram was not counted")
	}
	if got := b.RemainingBytes(clientPath); got != 1800 {
		t.Fatalf("remaining bytes = %d, want 1800", got)
	}
}

func TestAddressValidationRemovesLimit(t *testing.T) {
	tests := []struct {
		name     string
		validate func(*Budget, netip.AddrPort)
	}{
		{
			name: "decrypted Handshake",
			validate: func(b *Budget, path netip.AddrPort) {
				b.OnHandshakeDecrypted(path)
			},
		},
		{
			name: "valid Retry or NEW_TOKEN token",
			validate: func(b *Budget, path netip.AddrPort) {
				b.OnValidToken(path)
			},
		},
		{
			name: "PATH_RESPONSE",
			validate: func(b *Budget, path netip.AddrPort) {
				b.OnPathResponse(path)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBudget()
			b.ReceiveInitial(clientPath, 1200)
			if !b.Reserve(clientPath, 3600, SendData) {
				t.Fatal("initial 3600-byte allowance was unavailable")
			}
			if b.Reserve(clientPath, 1, SendData) {
				t.Fatal("limit was not enforced before validation")
			}

			tt.validate(b, clientPath)

			if !b.IsValidated(clientPath) {
				t.Fatal("path is not marked validated")
			}
			if !b.Reserve(clientPath, 1<<20, SendData) {
				t.Fatal("validated path remained amplification-limited")
			}
			if got := b.RemainingBytes(clientPath); got != math.MaxUint64 {
				t.Fatalf("validated remaining = %d, want MaxUint64", got)
			}
		})
	}
}

func TestMigrationUsesIndependentBudgetAndChallengeFirst(t *testing.T) {
	b := NewBudget()
	b.ReceiveInitial(clientPath, 1200)
	if !b.Reserve(clientPath, 3600, SendData) {
		t.Fatal("old path allowance was unavailable")
	}

	if !b.ReceiveDatagram(otherPath, 1200) {
		t.Fatal("migration datagram was not counted")
	}
	if !b.MigrateTo(otherPath) {
		t.Fatal("migration failed")
	}

	stats, ok := b.PathStats(otherPath)
	if !ok {
		t.Fatal("new path was not recorded")
	}
	if stats.Received != 1200 || stats.Sent != 0 || stats.Remaining != 3600 {
		t.Fatalf("new path did not receive an independent budget: %+v", stats)
	}
	if got := b.SentBytes(clientPath); got != 3600 {
		t.Fatalf("old path accounting changed during migration: %d", got)
	}
	if !b.ChallengeRequired(otherPath) {
		t.Fatal("migrated path does not require a PATH_CHALLENGE")
	}

	if b.Reserve(otherPath, 1, SendData) {
		t.Fatal("ordinary data was allowed before PATH_CHALLENGE")
	}
	if b.Reserve(otherPath, 1, SendPTOProbe) {
		t.Fatal("PTO probe was allowed before PATH_CHALLENGE")
	}
	if !b.Reserve(otherPath, 24, SendPathChallenge) {
		t.Fatal("PATH_CHALLENGE was blocked by its own ordering rule")
	}
	if b.ChallengeRequired(otherPath) {
		t.Fatal("PATH_CHALLENGE did not clear the ordering requirement")
	}
	if !b.Reserve(otherPath, 3576, SendData) {
		t.Fatal("data after PATH_CHALLENGE did not use the remaining allowance")
	}
	if b.Reserve(otherPath, 1, SendData) {
		t.Fatal("new path exceeded its independent 3600-byte allowance")
	}
	if got := b.SentBytes(otherPath); got != 3600 {
		t.Fatalf("new path sent bytes = %d, want 3600", got)
	}
}

func TestValidatedMigrationNeedsNoChallenge(t *testing.T) {
	b := NewBudget()
	b.ReceiveDatagram(otherPath, 10)
	b.OnValidToken(otherPath)
	if !b.MigrateTo(otherPath) {
		t.Fatal("migration failed")
	}
	if b.ChallengeRequired(otherPath) {
		t.Fatal("validated path still requires a challenge")
	}
	if !b.Reserve(otherPath, 1<<20, SendData) {
		t.Fatal("validated migrated path was blocked")
	}
}

func TestCanSendDoesNotReserveAndRefundRestoresBudget(t *testing.T) {
	b := NewBudget()
	b.ReceiveInitial(clientPath, 1200)

	if !b.CanSend(clientPath, 3600, SendData) {
		t.Fatal("CanSend rejected the full allowance")
	}
	if got := b.SentBytes(clientPath); got != 0 {
		t.Fatalf("CanSend reserved %d bytes", got)
	}

	if !b.Reserve(clientPath, 1200, SendData) {
		t.Fatal("reservation failed")
	}
	b.Refund(clientPath, 1200, SendData)
	if got := b.SentBytes(clientPath); got != 0 {
		t.Fatalf("refund left %d sent bytes", got)
	}
	if !b.Reserve(clientPath, 3600, SendData) {
		t.Fatal("refunded budget was not reusable")
	}
}

func TestRefundingPathChallengeRestoresOrderingRequirement(t *testing.T) {
	b := NewBudget()
	b.ReceiveDatagram(otherPath, 1200)
	b.MigrateTo(otherPath)

	if !b.Reserve(otherPath, 24, SendPathChallenge) {
		t.Fatal("PATH_CHALLENGE reservation failed")
	}
	if b.ChallengeRequired(otherPath) {
		t.Fatal("PATH_CHALLENGE reservation did not clear the ordering requirement")
	}

	b.Refund(otherPath, 24, SendPathChallenge)
	if !b.ChallengeRequired(otherPath) {
		t.Fatal("refunding PATH_CHALLENGE left the path open to data")
	}
	if b.Reserve(otherPath, 1, SendData) {
		t.Fatal("data was allowed after PATH_CHALLENGE was refunded")
	}
}

func TestConcurrentReservationsDoNotOvercommit(t *testing.T) {
	b := NewBudget()
	b.ReceiveInitial(clientPath, 1200)

	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 4000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Reserve(clientPath, 1, SendData) {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 3600 {
		t.Fatalf("successful reservations = %d, want 3600", got)
	}
	if got := b.SentBytes(clientPath); got != 3600 {
		t.Fatalf("sent bytes = %d, want 3600", got)
	}
}
