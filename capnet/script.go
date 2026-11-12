package capnet

// Script is the deterministic, host-provided behavior of a simulated
// endpoint. It contains no network, no goroutines and no wall-clock access.
type Script struct {
	// Inbox is the exact byte stream the simulated target delivers, chunk by
	// chunk, on reads.
	Inbox [][]byte

	// ExpectWrites asserts the exact ordered payloads the plug-in sends. A
	// mismatch makes the offending write fail with ErrUnexpectedWrite.
	ExpectWrites [][]byte

	// AutoReply appends a canned response whenever a write's payload matches a
	// key, after expectation checking.
	AutoReply map[string][]byte

	// CloseAfterScript makes the simulated target return io.EOF once Inbox is
	// exhausted instead of ErrScriptExhausted.
	CloseAfterScript bool
}

type simAddr struct {
	label string
}

func (a simAddr) Network() string { return "sim" }
func (a simAddr) String() string  { return a.label }
