package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testPolicy() Policy {
	return Policy{
		Zones: map[uint8]UnitPolicy{
			1: {
				Functions: []uint8{1, 2, 3, 4, 5, 6, 15, 16, 23},
			},
			2: {
				Functions:   []uint8{5, 6, 15, 16, 23},
				AllowWrites: true,
			},
		},
	}
}

func makeADU(transactionID, protocolID, length uint16, unitID uint8, pdu ...byte) []byte {
	frame := make([]byte, MBAPHeaderSize+len(pdu))
	binary.BigEndian.PutUint16(frame[0:2], transactionID)
	binary.BigEndian.PutUint16(frame[2:4], protocolID)
	binary.BigEndian.PutUint16(frame[4:6], length)
	frame[6] = unitID
	copy(frame[MBAPHeaderSize:], pdu)
	return frame
}

func requestADU(transactionID uint16, unitID, functionCode uint8, data ...byte) []byte {
	pdu := append([]byte{functionCode}, data...)
	return makeADU(transactionID, 0, uint16(1+len(pdu)), unitID, pdu...)
}

func responseADU(transactionID uint16, unitID, functionCode uint8, data ...byte) []byte {
	return requestADU(transactionID, unitID, functionCode, data...)
}

func mustAdmitRequest(t *testing.T, a *Admission, clientID string, frame []byte) Request {
	t.Helper()
	req, err := a.AdmitRequest(clientID, frame)
	if err != nil {
		t.Fatalf("AdmitRequest() error = %v", err)
	}
	return req
}

func TestAdmitRequestMBAPValidation(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))

	t.Run("valid read request", func(t *testing.T) {
		req, err := a.AdmitRequest("client-a", requestADU(0x1234, 1, 3, 0, 0, 0, 2))
		if err != nil {
			t.Fatalf("AdmitRequest() error = %v", err)
		}
		if req.TransactionID != 0x1234 || req.UnitID != 1 || req.FunctionCode != 3 {
			t.Fatalf("unexpected request: %+v", req)
		}
	})

	t.Run("protocol identifier must be zero", func(t *testing.T) {
		frame := requestADU(1, 1, 3)
		binary.BigEndian.PutUint16(frame[2:4], 1)
		_, err := a.AdmitRequest("client-a", frame)
		if !errors.Is(err, ErrProtocolID) {
			t.Fatalf("error = %v, want %v", err, ErrProtocolID)
		}
	})

	t.Run("declared length larger than frame", func(t *testing.T) {
		frame := requestADU(2, 1, 3)
		binary.BigEndian.PutUint16(frame[4:6], uint16(len(frame)-5))
		_, err := a.AdmitRequest("client-a", frame)
		if !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrLengthMismatch)
		}
	})

	t.Run("declared length smaller than frame", func(t *testing.T) {
		frame := append(requestADU(3, 1, 3), 0x00)
		_, err := a.AdmitRequest("client-a", frame)
		if !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrLengthMismatch)
		}
	})

	t.Run("truncated frame", func(t *testing.T) {
		frame := requestADU(4, 1, 3)
		_, err := a.AdmitRequest("client-a", frame[:len(frame)-1])
		if !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrLengthMismatch)
		}
	})

	t.Run("short frame", func(t *testing.T) {
		_, err := a.AdmitRequest("client-a", []byte{0, 1, 0})
		if !errors.Is(err, ErrShortFrame) {
			t.Fatalf("error = %v, want %v", err, ErrShortFrame)
		}
	})

	t.Run("missing PDU", func(t *testing.T) {
		_, err := a.AdmitRequest("client-a", makeADU(5, 0, 1, 1))
		if !errors.Is(err, ErrMissingPDU) {
			t.Fatalf("error = %v, want %v", err, ErrMissingPDU)
		}
	})

	t.Run("request function code must not set the response bit", func(t *testing.T) {
		_, err := a.AdmitRequest("client-a", requestADU(7, 1, 0x83))
		if !errors.Is(err, ErrInvalidFunctionCode) {
			t.Fatalf("error = %v, want %v", err, ErrInvalidFunctionCode)
		}
	})

	t.Run("oversized frame", func(t *testing.T) {
		frame := makeADU(6, 0, 255, 1, make([]byte, 254)...)
		_, err := a.AdmitRequest("client-a", frame)
		if !errors.Is(err, ErrOversizedFrame) {
			t.Fatalf("error = %v, want %v", err, ErrOversizedFrame)
		}
	})
}

func TestPolicyDeniesUnknownUnitsFunctionsAndWrites(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))

	t.Run("unknown unit", func(t *testing.T) {
		_, err := a.AdmitRequest("client-a", requestADU(1, 9, 3))
		if !errors.Is(err, ErrUnitDenied) {
			t.Fatalf("error = %v, want %v", err, ErrUnitDenied)
		}
	})

	t.Run("function not listed", func(t *testing.T) {
		_, err := a.AdmitRequest("client-a", requestADU(2, 1, 7))
		if !errors.Is(err, ErrFunctionDenied) {
			t.Fatalf("error = %v, want %v", err, ErrFunctionDenied)
		}
	})

	for _, functionCode := range []uint8{5, 6, 15, 16, 23} {
		t.Run("write function denied by default", func(t *testing.T) {
			_, err := a.AdmitRequest("client-a", requestADU(3, 1, functionCode))
			if !errors.Is(err, ErrWriteDenied) {
				t.Fatalf("function %d: error = %v, want %v", functionCode, err, ErrWriteDenied)
			}
		})
	}

	t.Run("explicitly enabled write is admitted", func(t *testing.T) {
		req, err := a.AdmitRequest("client-a", requestADU(4, 2, 5, 0, 1, 0xff, 0x00))
		if err != nil {
			t.Fatalf("AdmitRequest() error = %v", err)
		}
		if req.UnitID != 2 || req.FunctionCode != 5 {
			t.Fatalf("unexpected request: %+v", req)
		}
	})
}

func TestTransactionConflictAndClientIsolation(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))

	mustAdmitRequest(t, a, "client-a", requestADU(0x1234, 1, 3))

	if _, err := a.AdmitRequest("client-a", requestADU(0x1234, 1, 4)); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("duplicate transaction error = %v, want %v", err, ErrTransactionConflict)
	}

	// A different client may reuse the transaction ID.
	mustAdmitRequest(t, a, "client-b", requestADU(0x1234, 1, 3))

	if _, err := a.AdmitResponse("client-b", responseADU(0x1234, 1, 3, 2, 0, 0)); err != nil {
		t.Fatalf("client-b response error = %v", err)
	}
	if _, err := a.AdmitResponse("client-a", responseADU(0x1234, 1, 3, 2, 0, 0)); err != nil {
		t.Fatalf("client-a response error = %v", err)
	}
	if got := a.PendingCount(); got != 0 {
		t.Fatalf("PendingCount() = %d, want 0", got)
	}
}

func TestResponseMustMatchTransactionUnitAndFunction(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))
	mustAdmitRequest(t, a, "client-a", requestADU(100, 1, 3))

	t.Run("response from another client cannot race the transaction", func(t *testing.T) {
		_, err := a.AdmitResponse("client-b", responseADU(100, 1, 3, 2, 0, 0))
		if !errors.Is(err, ErrNoPendingTransaction) {
			t.Fatalf("error = %v, want %v", err, ErrNoPendingTransaction)
		}
	})

	t.Run("wrong transaction ID", func(t *testing.T) {
		_, err := a.AdmitResponse("client-a", responseADU(101, 1, 3, 2, 0, 0))
		if !errors.Is(err, ErrNoPendingTransaction) {
			t.Fatalf("error = %v, want %v", err, ErrNoPendingTransaction)
		}
	})

	t.Run("wrong unit ID", func(t *testing.T) {
		_, err := a.AdmitResponse("client-a", responseADU(100, 2, 3, 2, 0, 0))
		if !errors.Is(err, ErrResponseUnitMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrResponseUnitMismatch)
		}
	})

	t.Run("wrong function code", func(t *testing.T) {
		_, err := a.AdmitResponse("client-a", responseADU(100, 1, 4, 2, 0, 0))
		if !errors.Is(err, ErrResponseFunctionMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrResponseFunctionMismatch)
		}
	})

	// The rejected responses must not consume the legitimate pending request.
	resp, err := a.AdmitResponse("client-a", responseADU(100, 1, 3, 2, 0, 0))
	if err != nil {
		t.Fatalf("valid response error = %v", err)
	}
	if resp.Exception || resp.TransactionID != 100 || resp.UnitID != 1 || resp.FunctionCode != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	t.Run("duplicate response is rejected", func(t *testing.T) {
		_, err := a.AdmitResponse("client-a", responseADU(100, 1, 3, 2, 0, 0))
		if !errors.Is(err, ErrNoPendingTransaction) {
			t.Fatalf("error = %v, want %v", err, ErrNoPendingTransaction)
		}
	})
}

func TestExceptionResponseMatching(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))
	mustAdmitRequest(t, a, "client-a", requestADU(200, 1, 3))

	t.Run("malformed exception response keeps request pending", func(t *testing.T) {
		_, err := a.AdmitResponse("client-a", responseADU(200, 1, 0x83))
		if !errors.Is(err, ErrMalformedException) {
			t.Fatalf("error = %v, want %v", err, ErrMalformedException)
		}
		if got := a.PendingCount(); got != 1 {
			t.Fatalf("PendingCount() = %d, want 1", got)
		}
	})

	resp, err := a.AdmitResponse("client-a", responseADU(200, 1, 0x83, 0x02))
	if err != nil {
		t.Fatalf("exception response error = %v", err)
	}
	if !resp.IsException() || resp.ExceptionCode != 0x02 {
		t.Fatalf("unexpected exception response: %+v", resp)
	}
	if resp.FunctionCode != 0x83 {
		t.Fatalf("FunctionCode = 0x%02x, want 0x83", resp.FunctionCode)
	}
	if got := a.PendingCount(); got != 0 {
		t.Fatalf("PendingCount() = %d, want 0", got)
	}
}

func TestMatchResponseResolvesClient(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))
	mustAdmitRequest(t, a, "client-a", requestADU(700, 1, 3))
	mustAdmitRequest(t, a, "client-b", requestADU(701, 1, 3))

	resp, err := a.MatchResponse(responseADU(700, 1, 3, 2, 0, 0))
	if err != nil {
		t.Fatalf("MatchResponse() error = %v", err)
	}
	if resp.ClientID != "client-a" {
		t.Fatalf("ClientID = %q, want client-a", resp.ClientID)
	}
	if got := a.PendingCount(); got != 1 {
		t.Fatalf("PendingCount() = %d, want 1", got)
	}

	resp, err = a.MatchResponse(responseADU(701, 1, 3, 2, 0, 0))
	if err != nil {
		t.Fatalf("MatchResponse() error = %v", err)
	}
	if resp.ClientID != "client-b" {
		t.Fatalf("ClientID = %q, want client-b", resp.ClientID)
	}
}

func TestMatchResponseRejectsAmbiguousBinding(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))
	mustAdmitRequest(t, a, "client-a", requestADU(800, 1, 3))
	mustAdmitRequest(t, a, "client-b", requestADU(800, 1, 3))

	if _, err := a.MatchResponse(responseADU(800, 1, 3, 2, 0, 0)); !errors.Is(err, ErrAmbiguousResponse) {
		t.Fatalf("error = %v, want %v", err, ErrAmbiguousResponse)
	}
	if got := a.PendingCount(); got != 2 {
		t.Fatalf("ambiguous response consumed pending requests: PendingCount() = %d, want 2", got)
	}
}

func TestMatchResponseDisambiguatesByUnitAndFunction(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))
	mustAdmitRequest(t, a, "client-a", requestADU(850, 1, 3))
	mustAdmitRequest(t, a, "client-b", requestADU(850, 2, 5, 0, 1, 0xff, 0x00))

	resp, err := a.MatchResponse(responseADU(850, 2, 5, 0, 1, 0xff, 0x00))
	if err != nil {
		t.Fatalf("unit-2 response error = %v", err)
	}
	if resp.ClientID != "client-b" {
		t.Fatalf("ClientID = %q, want client-b", resp.ClientID)
	}

	resp, err = a.MatchResponse(responseADU(850, 1, 3, 2, 0, 0))
	if err != nil {
		t.Fatalf("unit-1 response error = %v", err)
	}
	if resp.ClientID != "client-a" {
		t.Fatalf("ClientID = %q, want client-a", resp.ClientID)
	}
}

func TestMatchResponseReportsMismatchWithoutConsuming(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))
	mustAdmitRequest(t, a, "client-a", requestADU(900, 1, 3))

	if _, err := a.MatchResponse(responseADU(900, 2, 3, 2, 0, 0)); !errors.Is(err, ErrResponseUnitMismatch) {
		t.Fatalf("unit mismatch error = %v, want %v", err, ErrResponseUnitMismatch)
	}
	if _, err := a.MatchResponse(responseADU(900, 1, 4, 2, 0, 0)); !errors.Is(err, ErrResponseFunctionMismatch) {
		t.Fatalf("function mismatch error = %v, want %v", err, ErrResponseFunctionMismatch)
	}
	if got := a.PendingCount(); got != 1 {
		t.Fatalf("mismatched responses consumed pending request: PendingCount() = %d, want 1", got)
	}

	if _, err := a.MatchResponse(responseADU(900, 1, 0x83)); !errors.Is(err, ErrMalformedException) {
		t.Fatalf("malformed exception error = %v, want %v", err, ErrMalformedException)
	}
	if got := a.PendingCount(); got != 1 {
		t.Fatalf("malformed exception consumed pending request: PendingCount() = %d, want 1", got)
	}

	resp, err := a.MatchResponse(responseADU(900, 1, 0x83, 0x02))
	if err != nil {
		t.Fatalf("exception response error = %v", err)
	}
	if resp.ClientID != "client-a" || !resp.IsException() || resp.ExceptionCode != 0x02 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestResponseEnvelopeRevalidation(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))
	mustAdmitRequest(t, a, "client-a", requestADU(300, 1, 3))

	t.Run("wrong protocol ID", func(t *testing.T) {
		frame := responseADU(300, 1, 3, 2, 0, 0)
		binary.BigEndian.PutUint16(frame[2:4], 1)
		_, err := a.AdmitResponse("client-a", frame)
		if !errors.Is(err, ErrProtocolID) {
			t.Fatalf("error = %v, want %v", err, ErrProtocolID)
		}
	})

	t.Run("wrong length", func(t *testing.T) {
		frame := responseADU(300, 1, 3, 2, 0, 0)
		binary.BigEndian.PutUint16(frame[4:6], uint16(len(frame)-6+1))
		_, err := a.AdmitResponse("client-a", frame)
		if !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrLengthMismatch)
		}
	})

	if _, err := a.AdmitResponse("client-a", responseADU(300, 1, 3, 2, 0, 0)); err != nil {
		t.Fatalf("valid response after malformed ones error = %v", err)
	}
}

func TestRejectedFramesAreLogged(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := NewAdmission(testPolicy(), WithLogger(logger))

	mustAdmitRequest(t, a, "client-a", requestADU(400, 1, 3))
	if _, err := a.AdmitResponse("client-a", responseADU(400, 2, 3, 2, 0, 0)); !errors.Is(err, ErrResponseUnitMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrResponseUnitMismatch)
	}
	if _, err := a.AdmitRequest("client-a", requestADU(401, 1, 5)); !errors.Is(err, ErrWriteDenied) {
		t.Fatalf("error = %v, want %v", err, ErrWriteDenied)
	}

	output := logs.String()
	if !strings.Contains(output, "modbus response rejected") {
		t.Fatalf("response rejection was not logged: %s", output)
	}
	if !strings.Contains(output, "expected_unit_id=1") {
		t.Fatalf("expected unit was not logged: %s", output)
	}
	if !strings.Contains(output, "modbus request rejected") || !strings.Contains(output, "write function code") {
		t.Fatalf("write rejection was not logged: %s", output)
	}
}

func TestPendingRequestExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := NewAdmission(
		testPolicy(),
		WithLogger(discardLogger()),
		WithTimeout(time.Second),
		WithClock(func() time.Time { return now }),
	)

	mustAdmitRequest(t, a, "client-a", requestADU(500, 1, 3))
	now = now.Add(2 * time.Second)

	if _, err := a.AdmitResponse("client-a", responseADU(500, 1, 3, 2, 0, 0)); !errors.Is(err, ErrNoPendingTransaction) {
		t.Fatalf("stale response error = %v, want %v", err, ErrNoPendingTransaction)
	}

	mustAdmitRequest(t, a, "client-a", requestADU(500, 1, 4))
	if _, err := a.AdmitResponse("client-a", responseADU(500, 1, 4, 2, 0, 0)); err != nil {
		t.Fatalf("response for new request error = %v", err)
	}
}

func TestConcurrentTransactionConflict(t *testing.T) {
	a := NewAdmission(testPolicy(), WithLogger(discardLogger()))

	const workers = 32
	var admitted atomic.Int32
	var conflicts atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := a.AdmitRequest("client-a", requestADU(600, 1, 3))
			switch {
			case err == nil:
				admitted.Add(1)
			case errors.Is(err, ErrTransactionConflict):
				conflicts.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := admitted.Load(); got != 1 {
		t.Fatalf("admitted = %d, want 1", got)
	}
	if got := conflicts.Load(); got != workers-1 {
		t.Fatalf("conflicts = %d, want %d", got, workers-1)
	}
}

func TestPendingLimitAndRelease(t *testing.T) {
	a := NewAdmission(
		testPolicy(),
		WithLogger(discardLogger()),
		WithMaxPending(1),
	)

	mustAdmitRequest(t, a, "client-a", requestADU(1000, 1, 3))
	if _, err := a.AdmitRequest("client-a", requestADU(1001, 1, 3)); !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("error = %v, want %v", err, ErrPendingLimit)
	}

	if _, err := a.AdmitResponse("client-a", responseADU(1000, 1, 3, 2, 0, 0)); err != nil {
		t.Fatalf("response error = %v", err)
	}
	mustAdmitRequest(t, a, "client-a", requestADU(1001, 1, 3))
}
