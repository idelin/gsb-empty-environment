package netsec_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"example.org/netsec/q0132"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")
var initialChaining = []byte("initial-chain-16")

func testMAC(counter uint32, chainingValue, apdu []byte) ([]byte, error) {
	h := hmac.New(sha256.New, testKey)
	var encodedCounter [4]byte
	binary.BigEndian.PutUint32(encodedCounter[:], counter)
	h.Write(encodedCounter[:])
	h.Write(chainingValue)
	h.Write(apdu)
	return h.Sum(nil)[:16], nil
}

func authenticate(t *testing.T, channel *netsec.Channel, level netsec.SecurityLevel, counter uint32) {
	t.Helper()

	apdu := []byte{0x80, netsec.INSExternalAuthenticate, byte(level), 0x00}
	chaining := initialChaining
	if channel.Authenticated() {
		chaining = channel.ChainingValue()
	}
	mac, err := testMAC(counter, chaining, apdu)
	if err != nil {
		t.Fatalf("compute EXTERNAL AUTHENTICATE MAC: %v", err)
	}
	if err := channel.ExternalAuthenticate(netsec.AuthRequest{
		Level:                level,
		InitialChainingValue: initialChaining,
		Counter:              counter,
		APDU:                 apdu,
		MAC:                  mac,
	}); err != nil {
		t.Fatalf("EXTERNAL AUTHENTICATE: %v", err)
	}
}

func protectedCommand(counter uint32, apdu []byte) ([]byte, []byte, error) {
	mac, err := testMAC(counter, initialChaining, apdu)
	return apdu, mac, err
}

func commandWithChain(channel *netsec.Channel, counter uint32, apdu []byte) ([]byte, error) {
	return testMAC(counter, channel.ChainingValue(), apdu)
}

func TestSecurityLevelOnlyEscalates(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	authenticate(t, channel, netsec.CMAC, 0)

	if got := channel.SecurityLevel(); got != netsec.CMAC {
		t.Fatalf("initial level = %s, want C-MAC", got)
	}

	authenticate(t, channel, netsec.LevelFull, 1)
	if got := channel.SecurityLevel(); got != netsec.LevelFull {
		t.Fatalf("escalated level = %s, want C-MAC+C-DECRYPTION", got)
	}
	if channel.NextCounter() != 2 {
		t.Fatalf("next counter = %d, want 2", channel.NextCounter())
	}

	for _, lowLevel := range []netsec.SecurityLevel{netsec.LevelNone, netsec.CMAC, netsec.CDecryption} {
		t.Run(lowLevel.String(), func(t *testing.T) {
			apdu := []byte{0x80, netsec.INSExternalAuthenticate, byte(lowLevel), 0x00}
			mac, err := commandWithChain(channel, 2, apdu)
			if err != nil {
				t.Fatalf("compute downgrade MAC: %v", err)
			}
			err = channel.ExternalAuthenticate(netsec.AuthRequest{
				Level:   lowLevel,
				Counter: 2,
				APDU:    apdu,
				MAC:     mac,
			})
			if !errors.Is(err, netsec.ErrSecurityLevelDowngrade) {
				t.Fatalf("downgrade error = %v, want ErrSecurityLevelDowngrade", err)
			}
			if channel.Invalidated() {
				t.Fatal("a rejected downgrade must not silently replace the channel state")
			}
			if got := channel.SecurityLevel(); got != netsec.LevelFull {
				t.Fatalf("level after downgrade = %s, want C-MAC+C-DECRYPTION", got)
			}
			if channel.NextCounter() != 2 {
				t.Fatalf("downgrade consumed counter: got %d, want 2", channel.NextCounter())
			}
		})
	}
}

func TestRestrictedCommandsRequireBothProtections(t *testing.T) {
	for _, ins := range []byte{netsec.INSInstall, netsec.INSLoad, netsec.INSDelete} {
		t.Run(fmt.Sprintf("INS_%02X", ins), func(t *testing.T) {
			channel := netsec.NewChannel(testMAC)
			authenticate(t, channel, netsec.CMAC, 0)

			apdu := []byte{0x80, ins, 0x00, 0x00}
			mac, err := commandWithChain(channel, 1, apdu)
			if err != nil {
				t.Fatalf("compute command MAC: %v", err)
			}
			if err := channel.VerifyCommand(1, apdu, mac); !errors.Is(err, netsec.ErrInsufficientSecurity) {
				t.Fatalf("verify at C-MAC-only = %v, want ErrInsufficientSecurity", err)
			}
			if channel.Invalidated() {
				t.Fatal("a validly protected policy denial should not destroy the channel")
			}
			if channel.NextCounter() != 2 {
				t.Fatalf("validly protected denied command did not advance counter: got %d", channel.NextCounter())
			}

			authenticate(t, channel, netsec.LevelFull, 2)
			apdu = []byte{0x80, ins, 0x00, 0x00}
			mac, err = commandWithChain(channel, 3, apdu)
			if err != nil {
				t.Fatalf("compute escalated command MAC: %v", err)
			}
			if err := channel.VerifyCommand(3, apdu, mac); err != nil {
				t.Fatalf("restricted command after escalation: %v", err)
			}
		})
	}
}

func TestReplayInvalidatesChannel(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	authenticate(t, channel, netsec.LevelFull, 0)

	apdu := []byte{0x80, 0xE6, 0x00, 0x00}
	mac, err := commandWithChain(channel, 1, apdu)
	if err != nil {
		t.Fatalf("compute command MAC: %v", err)
	}
	if err := channel.VerifyCommand(1, apdu, mac); err != nil {
		t.Fatalf("first command: %v", err)
	}

	if err := channel.VerifyCommand(1, apdu, mac); !errors.Is(err, netsec.ErrReplay) {
		t.Fatalf("replay error = %v, want ErrReplay", err)
	}
	if !channel.Invalidated() {
		t.Fatal("replay did not invalidate the channel")
	}

	freshAPDU := []byte{0x80, 0xE6, 0x00, 0x00}
	freshMAC, err := commandWithChain(channel, 2, freshAPDU)
	if err != nil {
		t.Fatalf("compute fresh command MAC: %v", err)
	}
	if err := channel.VerifyCommand(2, freshAPDU, freshMAC); !errors.Is(err, netsec.ErrChannelInvalid) {
		t.Fatalf("command after replay = %v, want ErrChannelInvalid", err)
	}
}

func TestCounterRollbackInvalidatesChannel(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	authenticate(t, channel, netsec.LevelFull, 0)

	firstAPDU := []byte{0x80, 0xCA, 0x00, 0x00}
	firstMAC, err := commandWithChain(channel, 1, firstAPDU)
	if err != nil {
		t.Fatalf("compute first command MAC: %v", err)
	}
	if err := channel.VerifyCommand(1, firstAPDU, firstMAC); err != nil {
		t.Fatalf("first command: %v", err)
	}

	secondAPDU := []byte{0x80, 0xCA, 0x00, 0x01}
	secondMAC, err := commandWithChain(channel, 2, secondAPDU)
	if err != nil {
		t.Fatalf("compute second command MAC: %v", err)
	}
	if err := channel.VerifyCommand(2, secondAPDU, secondMAC); err != nil {
		t.Fatalf("second command: %v", err)
	}

	err = channel.VerifyCommand(1, firstAPDU, firstMAC)
	if !errors.Is(err, netsec.ErrCounterRollback) {
		t.Fatalf("rollback error = %v, want ErrCounterRollback", err)
	}
	if !errors.Is(err, netsec.ErrReplay) {
		t.Fatalf("rollback error = %v, want it to match ErrReplay", err)
	}
	if !errors.Is(err, netsec.ErrChannelInvalid) {
		t.Fatalf("rollback error = %v, want it to match ErrChannelInvalid", err)
	}
	if !channel.Invalidated() {
		t.Fatal("counter rollback did not invalidate the channel")
	}
}

func TestCounterGapInvalidatesChannel(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	authenticate(t, channel, netsec.LevelFull, 0)

	apdu := []byte{0x80, 0xCA, 0x00, 0x00}
	mac, err := commandWithChain(channel, 2, apdu)
	if err != nil {
		t.Fatalf("compute command MAC: %v", err)
	}
	err = channel.VerifyCommand(2, apdu, mac)
	if !errors.Is(err, netsec.ErrCounterGap) {
		t.Fatalf("counter gap error = %v, want ErrCounterGap", err)
	}
	if !errors.Is(err, netsec.ErrCounterMismatch) {
		t.Fatalf("counter gap error = %v, want it to match ErrCounterMismatch", err)
	}
	if !channel.Invalidated() {
		t.Fatal("counter gap did not invalidate the channel")
	}
}

func TestMACCallbackFailureInvalidatesChannel(t *testing.T) {
	callbackErr := errors.New("HSM unavailable")
	channel := netsec.NewChannel(func(counter uint32, chainingValue, apdu []byte) ([]byte, error) {
		if counter > 0 {
			return nil, callbackErr
		}
		return testMAC(counter, chainingValue, apdu)
	})
	authenticate(t, channel, netsec.LevelFull, 0)

	apdu := []byte{0x80, 0xCA, 0x00, 0x00}
	err := channel.VerifyCommand(1, apdu, []byte("mac"))
	if !errors.Is(err, netsec.ErrMACGeneration) {
		t.Fatalf("callback failure error = %v, want ErrMACGeneration", err)
	}
	if !errors.Is(err, callbackErr) {
		t.Fatalf("callback failure error = %v, want callback cause", err)
	}
	if !channel.Invalidated() {
		t.Fatal("MAC callback failure did not invalidate the channel")
	}
}

func TestMACChainingAdvancesInOrder(t *testing.T) {
	var calls []struct {
		counter  uint32
		chaining []byte
		apdu     []byte
	}
	channel := netsec.NewChannel(func(counter uint32, chainingValue, apdu []byte) ([]byte, error) {
		calls = append(calls, struct {
			counter  uint32
			chaining []byte
			apdu     []byte
		}{
			counter:  counter,
			chaining: append([]byte(nil), chainingValue...),
			apdu:     append([]byte(nil), apdu...),
		})
		mac := []byte(fmt.Sprintf("mac-%d", counter))
		return mac, nil
	})

	authAPDU := []byte{0x80, netsec.INSExternalAuthenticate, 0x03, 0x00}
	if err := channel.ExternalAuthenticate(netsec.AuthRequest{
		Level:                netsec.LevelFull,
		InitialChainingValue: initialChaining,
		Counter:              0,
		APDU:                 authAPDU,
		MAC:                  []byte("mac-0"),
	}); err != nil {
		t.Fatalf("EXTERNAL AUTHENTICATE: %v", err)
	}

	for i := uint32(1); i <= 2; i++ {
		apdu := []byte{0x80, 0xCA, 0x00, byte(i)}
		mac := []byte(fmt.Sprintf("mac-%d", i))
		if err := channel.VerifyCommand(i, apdu, mac); err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
	}

	if len(calls) != 3 {
		t.Fatalf("MAC callback called %d times, want 3", len(calls))
	}
	if string(calls[0].chaining) != string(initialChaining) {
		t.Fatalf("auth chaining = %q, want %q", calls[0].chaining, initialChaining)
	}
	if string(calls[1].chaining) != "mac-0" {
		t.Fatalf("command 1 chaining = %q, want mac-0", calls[1].chaining)
	}
	if string(calls[2].chaining) != "mac-1" {
		t.Fatalf("command 2 chaining = %q, want mac-1", calls[2].chaining)
	}
	for i, call := range calls {
		if call.counter != uint32(i) {
			t.Fatalf("callback counter %d = %d, want %d", i, call.counter, i)
		}
	}
	if channel.LastCounter() != 2 || channel.NextCounter() != 3 {
		t.Fatalf("counter state = last %d next %d, want last 2 next 3", channel.LastCounter(), channel.NextCounter())
	}
	if string(channel.ChainingValue()) != "mac-2" {
		t.Fatalf("final chaining value = %q, want mac-2", channel.ChainingValue())
	}
}

func TestBadMACInvalidatesChannel(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	authenticate(t, channel, netsec.LevelFull, 0)

	apdu, mac, err := protectedCommand(1, []byte{0x80, 0xCA, 0x00, 0x00})
	if err != nil {
		t.Fatalf("compute command MAC: %v", err)
	}
	err = channel.VerifyCommand(1, apdu, mac)
	if !errors.Is(err, netsec.ErrMACMismatch) {
		t.Fatalf("bad MAC error = %v, want ErrMACMismatch", err)
	}
	if !channel.Invalidated() {
		t.Fatal("bad MAC did not invalidate the channel")
	}
}

func TestBadAuthMACInvalidatesChannel(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	apdu := []byte{0x80, netsec.INSExternalAuthenticate, 0x03, 0x00}
	err := channel.ExternalAuthenticate(netsec.AuthRequest{
		Level:                netsec.LevelFull,
		InitialChainingValue: initialChaining,
		Counter:              0,
		APDU:                 apdu,
		MAC:                  []byte("not-a-mac"),
	})
	if !errors.Is(err, netsec.ErrMACMismatch) {
		t.Fatalf("bad auth MAC error = %v, want ErrMACMismatch", err)
	}
	if !channel.Invalidated() {
		t.Fatal("bad auth MAC did not invalidate the channel")
	}
}

func TestDispatchHandlerFailureInvalidatesChannel(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	authenticate(t, channel, netsec.LevelFull, 0)

	apdu := []byte{0x80, 0xCA, 0x00, 0x00}
	mac, err := commandWithChain(channel, 1, apdu)
	if err != nil {
		t.Fatalf("compute command MAC: %v", err)
	}
	handlerErr := errors.New("load failed")
	err = channel.Dispatch(1, apdu, mac, func() error {
		return handlerErr
	})
	if !errors.Is(err, handlerErr) {
		t.Fatalf("dispatch error = %v, want handler error", err)
	}
	if !errors.Is(err, netsec.ErrChannelInvalid) {
		t.Fatalf("dispatch error = %v, want ErrChannelInvalid", err)
	}
	if !channel.Invalidated() {
		t.Fatal("handler failure did not invalidate the channel")
	}
}

func TestUnauthenticatedAndUnsupportedLevelsRejected(t *testing.T) {
	channel := netsec.NewChannel(testMAC)
	if err := channel.VerifyCommand(0, []byte{0x80, 0xCA, 0x00, 0x00}, []byte("mac")); !errors.Is(err, netsec.ErrNotAuthenticated) {
		t.Fatalf("unauthenticated command error = %v, want ErrNotAuthenticated", err)
	}
	if channel.Invalidated() {
		t.Fatal("unauthenticated command should fail closed without changing unopened state")
	}

	err := channel.ExternalAuthenticate(netsec.AuthRequest{
		Level:                netsec.SecurityLevel(0x04),
		InitialChainingValue: initialChaining,
		APDU:                 []byte{0x80, netsec.INSExternalAuthenticate, 0x04, 0x00},
		MAC:                  []byte("mac"),
	})
	if !errors.Is(err, netsec.ErrInvalidSecurityLevel) {
		t.Fatalf("invalid level error = %v, want ErrInvalidSecurityLevel", err)
	}
}
