package netsec

import (
	"errors"
	"testing"
)

func command(cla, ins, p1, p2 byte, data ...byte) []byte {
	apdu := make([]byte, 0, 5+len(data))
	apdu = append(apdu, cla, ins, p1, p2, byte(len(data)))
	return append(apdu, data...)
}

func getResponse(le byte) []byte {
	return []byte{0x00, getResponseINS, 0x00, 0x00, le}
}

func mustCheck(t *testing.T, g *APDUGuard, apdu []byte) {
	t.Helper()
	if err := g.CheckCommand(apdu); err != nil {
		t.Fatalf("CheckCommand(%X) returned %v, want nil", apdu, err)
	}
}

func mustObserve(t *testing.T, g *APDUGuard, sw1, sw2 byte) {
	t.Helper()
	if err := g.ObserveResponse([]byte{sw1, sw2}); err != nil {
		t.Fatalf("ObserveResponse(%02X%02X) returned %v, want nil", sw1, sw2, err)
	}
}

func TestConsistentChainSurvivesGetResponse(t *testing.T) {
	g := NewAPDUGuard(64)

	start := command(0x10, 0x01, 0x00, 0x00, 0xAA, 0xBB)
	mustCheck(t, g, start)
	mustObserve(t, g, 0x61, 0x08)

	// GET RESPONSE is legitimate in the middle of a T=0 command chain and
	// must not terminate or reset the chain.
	if err := g.CheckCommand(getResponse(0x08)); err != nil {
		t.Fatalf("GET RESPONSE on 61xx returned %v, want nil", err)
	}
	mustObserve(t, g, 0x90, 0x00)

	final := command(0x00, 0x01, 0x00, 0x00, 0xCC, 0xDD)
	mustCheck(t, g, final)

	if g.Blocked() {
		t.Fatal("consistent chain unexpectedly blocked the session")
	}

	// A completed chain must release the state so an unrelated standalone
	// command can be forwarded.
	mustCheck(t, g, command(0x00, 0x02, 0x00, 0x00))
}

func TestMidChainInsertionDiscardsWholeChain(t *testing.T) {
	g := NewAPDUGuard(64)

	mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00, 0xAA))

	// An attacker inserts a different instruction without the chain bit.
	inserted := command(0x00, 0x02, 0x00, 0x00, 0xBB)
	err := g.CheckCommand(inserted)
	if !errors.Is(err, ErrChainHeaderMismatch) {
		t.Fatalf("inserted command returned %v, want ErrChainHeaderMismatch", err)
	}
	if !g.Blocked() {
		t.Fatal("guard did not block the session after a chain interruption")
	}

	// Both the original continuation and an unrelated command must stay
	// rejected; the first chain was discarded rather than resumed.
	for _, apdu := range [][]byte{
		command(0x10, 0x01, 0x00, 0x00, 0xCC),
		command(0x00, 0x01, 0x00, 0x00, 0xDD),
		command(0x00, 0x02, 0x00, 0x00),
	} {
		if err := g.CheckCommand(apdu); !errors.Is(err, ErrSessionBlocked) {
			t.Fatalf("post-violation command %X returned %v, want ErrSessionBlocked", apdu, err)
		}
	}

	// Only establishing a new session clears the poison and permits a fresh
	// chain to start.
	g.ResetSession()
	mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00, 0xEE))
}

func TestCrossChainSplicingRejected(t *testing.T) {
	cases := []struct {
		name string
		next []byte
	}{
		{"different INS", command(0x10, 0x02, 0x00, 0x00, 0xAA)},
		{"different P1", command(0x10, 0x01, 0x01, 0x00, 0xAA)},
		{"different P2", command(0x10, 0x01, 0x00, 0x01, 0xAA)},
		{"different CLA below chain bit", command(0x11, 0x01, 0x00, 0x00, 0xAA)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewAPDUGuard(64)
			start := command(0x10, 0x01, 0x00, 0x00, 0xAA)
			mustCheck(t, g, start)

			err := g.CheckCommand(tc.next)
			if !errors.Is(err, ErrChainHeaderMismatch) {
				t.Fatalf("spliced segment returned %v, want ErrChainHeaderMismatch", err)
			}
			if !g.Blocked() {
				t.Fatal("guard did not block the session after cross-chain splicing")
			}

			// The matching tail of the first chain cannot be used after the
			// spliced segment poisoned the session.
			if err := g.CheckCommand(command(0x00, 0x01, 0x00, 0x00, 0xBB)); !errors.Is(err, ErrSessionBlocked) {
				t.Fatalf("matching tail returned %v, want ErrSessionBlocked", err)
			}
		})
	}

	t.Run("tail from another chain after a completed chain", func(t *testing.T) {
		g := NewAPDUGuard(64)

		mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00, 0xAA))
		mustCheck(t, g, command(0x00, 0x01, 0x00, 0x00, 0xBB))

		// Start a different operation, then try to finish it with the tail
		// belonging to the previous chain.
		mustCheck(t, g, command(0x10, 0x02, 0x00, 0x00, 0xCC))
		err := g.CheckCommand(command(0x00, 0x01, 0x00, 0x00, 0xDD))
		if !errors.Is(err, ErrChainHeaderMismatch) {
			t.Fatalf("foreign tail returned %v, want ErrChainHeaderMismatch", err)
		}
		if !g.Blocked() {
			t.Fatal("guard did not block the session after splicing a foreign tail")
		}
	})
}

func TestForgedGetResponseRejected(t *testing.T) {
	t.Run("without preceding 61xx", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustObserve(t, g, 0x90, 0x00)

		err := g.CheckCommand(getResponse(0x01))
		if !errors.Is(err, ErrUnexpectedGetResponse) {
			t.Fatalf("GET RESPONSE after 9000 returned %v, want ErrUnexpectedGetResponse", err)
		}
		if !g.Blocked() {
			t.Fatal("forged GET RESPONSE did not block the session")
		}
	})

	t.Run("before any card response", func(t *testing.T) {
		g := NewAPDUGuard(64)

		err := g.CheckCommand(getResponse(0x01))
		if !errors.Is(err, ErrUnexpectedGetResponse) {
			t.Fatalf("initial GET RESPONSE returned %v, want ErrUnexpectedGetResponse", err)
		}
	})

	t.Run("Le exceeds SW2", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustObserve(t, g, 0x61, 0x05)

		err := g.CheckCommand(getResponse(0x06))
		if !errors.Is(err, ErrGetResponseTooLarge) {
			t.Fatalf("oversized GET RESPONSE returned %v, want ErrGetResponseTooLarge", err)
		}
		if !g.Blocked() {
			t.Fatal("oversized GET RESPONSE did not block the session")
		}
	})

	t.Run("replayed GET RESPONSE", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustObserve(t, g, 0x61, 0x05)

		mustCheck(t, g, getResponse(0x05))
		err := g.CheckCommand(getResponse(0x05))
		if !errors.Is(err, ErrUnexpectedGetResponse) {
			t.Fatalf("replayed GET RESPONSE returned %v, want ErrUnexpectedGetResponse", err)
		}
	})

	t.Run("stale 61xx after another command", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustObserve(t, g, 0x61, 0x05)
		mustCheck(t, g, command(0x00, 0x02, 0x00, 0x00))

		err := g.CheckCommand(getResponse(0x05))
		if !errors.Is(err, ErrUnexpectedGetResponse) {
			t.Fatalf("GET RESPONSE using stale 61xx returned %v, want ErrUnexpectedGetResponse", err)
		}
	})

	t.Run("forged P1/P2", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustObserve(t, g, 0x61, 0x05)

		err := g.CheckCommand([]byte{0x00, getResponseINS, 0x01, 0x00, 0x05})
		if !errors.Is(err, ErrUnexpectedGetResponse) {
			t.Fatalf("GET RESPONSE with P1=01 returned %v, want ErrUnexpectedGetResponse", err)
		}
	})

	t.Run("during an active chain", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00, 0xAA))

		err := g.CheckCommand(getResponse(0x01))
		if !errors.Is(err, ErrUnexpectedGetResponse) {
			t.Fatalf("forged in-chain GET RESPONSE returned %v, want ErrUnexpectedGetResponse", err)
		}
		if err := g.CheckCommand(command(0x10, 0x01, 0x00, 0x00, 0xBB)); !errors.Is(err, ErrSessionBlocked) {
			t.Fatalf("continuation after forged GET RESPONSE returned %v, want ErrSessionBlocked", err)
		}
	})

	t.Run("SW2 zero means 256 bytes", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustObserve(t, g, 0x61, 0x00)
		mustCheck(t, g, getResponse(0x00))
	})

	t.Run("extended Le is checked", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustObserve(t, g, 0x61, 0x20)

		mustCheck(t, g, []byte{0x00, getResponseINS, 0x00, 0x00, 0x00, 0x00, 0x20})
	})
}

func TestChainLengthLimit(t *testing.T) {
	t.Run("exact limit accepted", func(t *testing.T) {
		g := NewAPDUGuard(10)
		mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00))
		mustCheck(t, g, command(0x00, 0x01, 0x00, 0x00))
	})

	t.Run("limit exceeded discards chain", func(t *testing.T) {
		g := NewAPDUGuard(10)
		mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00))

		err := g.CheckCommand(command(0x00, 0x01, 0x00, 0x00, 0xAA))
		if !errors.Is(err, ErrChainTooLong) {
			t.Fatalf("oversized chain returned %v, want ErrChainTooLong", err)
		}
		if !g.Blocked() {
			t.Fatal("oversized chain did not block the session")
		}
		if err := g.CheckCommand(command(0x10, 0x01, 0x00, 0x00)); !errors.Is(err, ErrSessionBlocked) {
			t.Fatalf("command after oversized chain returned %v, want ErrSessionBlocked", err)
		}
	})

	t.Run("first segment alone exceeds limit", func(t *testing.T) {
		g := NewAPDUGuard(4)
		err := g.CheckCommand(command(0x10, 0x01, 0x00, 0x00, 0xAA))
		if !errors.Is(err, ErrChainTooLong) {
			t.Fatalf("oversized first segment returned %v, want ErrChainTooLong", err)
		}
	})
}

func TestNonPositiveLimitUsesDefault(t *testing.T) {
	g := NewAPDUGuard(0)
	if g.maxChainBytes != DefaultMaxChainBytes {
		t.Fatalf("maxChainBytes = %d, want default %d", g.maxChainBytes, DefaultMaxChainBytes)
	}
}

func TestFinalSegmentHeaderMismatch(t *testing.T) {
	g := NewAPDUGuard(64)
	mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00, 0xAA))

	err := g.CheckCommand(command(0x00, 0x01, 0x00, 0x01, 0xBB))
	if !errors.Is(err, ErrChainHeaderMismatch) {
		t.Fatalf("mismatched final segment returned %v, want ErrChainHeaderMismatch", err)
	}
}

func TestMalformedCommands(t *testing.T) {
	short := []byte{0x00, 0x01, 0x00}

	t.Run("standalone malformed command is rejected without poisoning", func(t *testing.T) {
		g := NewAPDUGuard(64)
		if err := g.CheckCommand(short); !errors.Is(err, ErrMalformedAPDU) {
			t.Fatalf("short command returned %v, want ErrMalformedAPDU", err)
		}
		if g.Blocked() {
			t.Fatal("standalone malformed command poisoned the session")
		}
		mustCheck(t, g, command(0x00, 0x01, 0x00, 0x00))
	})

	t.Run("malformed command in chain discards it", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00, 0xAA))

		if err := g.CheckCommand(short); !errors.Is(err, ErrMalformedAPDU) {
			t.Fatalf("short in-chain command returned %v, want ErrMalformedAPDU", err)
		}
		if !g.Blocked() {
			t.Fatal("malformed in-chain command did not poison the session")
		}
	})

	t.Run("malformed GET RESPONSE discards chain", func(t *testing.T) {
		g := NewAPDUGuard(64)
		mustCheck(t, g, command(0x10, 0x01, 0x00, 0x00, 0xAA))
		mustObserve(t, g, 0x61, 0x05)

		if err := g.CheckCommand([]byte{0x00, getResponseINS, 0x00, 0x00, 0x00, 0x05}); !errors.Is(err, ErrMalformedAPDU) {
			t.Fatalf("malformed GET RESPONSE returned %v, want ErrMalformedAPDU", err)
		}
		if !g.Blocked() {
			t.Fatal("malformed GET RESPONSE did not poison the session")
		}
	})
}

func TestObserveResponseUsesTrailingStatusWord(t *testing.T) {
	g := NewAPDUGuard(64)
	if err := g.ObserveResponse([]byte{0xAA, 0xBB, 0x61, 0x05}); err != nil {
		t.Fatalf("response with data returned %v, want nil", err)
	}
	mustCheck(t, g, getResponse(0x05))
}

func TestMalformedResponseCannotLeaveGetResponseAuthorized(t *testing.T) {
	g := NewAPDUGuard(64)
	mustObserve(t, g, 0x61, 0x05)

	if err := g.ObserveResponse([]byte{0x61}); !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("short response returned %v, want ErrMalformedResponse", err)
	}
	if err := g.CheckCommand(getResponse(0x05)); !errors.Is(err, ErrUnexpectedGetResponse) {
		t.Fatalf("GET RESPONSE after malformed response returned %v, want ErrUnexpectedGetResponse", err)
	}
}
