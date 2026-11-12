package netsec

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReadCommandArray(t *testing.T) {
	frame := "*3\r\n$6\r\nSELECT\r\n$1\r\n0\r\n$3\r\nfoo\r\n"
	r := bufio.NewReader(strings.NewReader(frame))

	args, err := ReadCommand(r)
	if err != nil {
		t.Fatalf("ReadCommand failed: %v", err)
	}
	want := []string{"SELECT", "0", "foo"}
	if len(args) != len(want) {
		t.Fatalf("expected %d args, got %d", len(want), len(args))
	}
	for i := range want {
		if string(args[i]) != want[i] {
			t.Fatalf("arg %d: want %q, got %q", i, want[i], args[i])
		}
	}
}

func TestReadCommandInline(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("select 1\r\nPING\r\n"))

	first, err := ReadCommand(r)
	if err != nil {
		t.Fatalf("inline read failed: %v", err)
	}
	if len(first) != 2 || string(first[0]) != "select" || string(first[1]) != "1" {
		t.Fatalf("unexpected inline command: %v", first)
	}

	second, err := ReadCommand(r)
	if err != nil {
		t.Fatalf("second inline read failed: %v", err)
	}
	if len(second) != 1 || string(second[0]) != "PING" {
		t.Fatalf("unexpected second command: %v", second)
	}
}

func TestReadCommandMalformed(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("*2\r\n$3\r\nGET\r\nnot-a-bulk\r\n"))
	if _, err := ReadCommand(r); err == nil {
		t.Fatal("malformed frame must return an error")
	} else if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("expected *ProtocolError, got %T", err)
	}
}

func TestWriteError(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	gateErr := deniedDB("SELECT to DB %d is not allowed", 1)
	if err := WriteError(w, gateErr); err != nil {
		t.Fatalf("WriteError failed: %v", err)
	}
	if got, want := buf.String(), "-NOPERM SELECT to DB 1 is not allowed\r\n"; got != want {
		t.Fatalf("reply mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestPipelineOverRESP feeds a full pipelined RESP buffer through the gate,
// exactly as a proxy would: every frame is parsed, inspected and either
// accepted (forwarded marker) or answered with an error reply.
func TestPipelineOverRESP(t *testing.T) {
	client := strings.NewReader(strings.Join([]string{
		"*3\r\n$3\r\nSET\r\n$1\r\na\r\n$1\r\n1\r\n",
		"*2\r\n$6\r\nSELECT\r\n$1\r\n1\r\n",
		"*2\r\n$3\r\nGET\r\n$1\r\na\r\n",
	}, ""))
	r := bufio.NewReader(client)
	var replies bytes.Buffer
	w := bufio.NewWriter(&replies)
	gate := newTestGate()
	conn := gate.Open()

	forwarded := 0
	for {
		args, err := ReadCommand(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected frame error: %v", err)
		}
		if gateErr := gate.Check(conn, args); gateErr != nil {
			if err := WriteError(w, gateErr); err != nil {
				t.Fatalf("write failed: %v", err)
			}
			continue
		}
		forwarded++
	}
	w.Flush()

	if forwarded != 2 {
		t.Fatalf("expected 2 forwarded commands, got %d", forwarded)
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("connection must stay on DB 0, got %d", gate.DB(conn))
	}
	replyText := replies.String()
	if !strings.Contains(replyText, "-NOPERM") || strings.Count(replyText, "\r\n") != 1 {
		t.Fatalf("expected exactly one NOPERM error reply, got %q", replyText)
	}
}

// TestTransactionOverRESP verifies that SELECT inside MULTI/EXEC is gated,
// the denial does not tear down the transaction and EXEC clears the state.
func TestTransactionOverRESP(t *testing.T) {
	client := strings.NewReader(strings.Join([]string{
		"*1\r\n$5\r\nMULTI\r\n",
		"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n",
		"*2\r\n$6\r\nSELECT\r\n$1\r\n2\r\n",
		"*2\r\n$4\r\nEVAL\r\n$1\r\n0\r\n",
		"*1\r\n$4\r\nEXEC\r\n",
	}, ""))
	r := bufio.NewReader(client)
	var replies bytes.Buffer
	w := bufio.NewWriter(&replies)
	gate := newTestGate()
	conn := gate.Open()

	denied := 0
	for {
		args, err := ReadCommand(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected frame error: %v", err)
		}
		if gateErr := gate.Check(conn, args); gateErr != nil {
			denied++
			if err := WriteError(w, gateErr); err != nil {
				t.Fatalf("write failed: %v", err)
			}
		}
	}

	if denied != 2 {
		t.Fatalf("expected 2 denials (SELECT 2 and EVAL), got %d", denied)
	}
	if gate.InMulti(conn) {
		t.Fatal("EXEC must leave the connection outside of MULTI")
	}
	if gate.DB(conn) != 0 {
		t.Fatalf("transaction must not move the connection off DB 0, got %d", gate.DB(conn))
	}
}
