package netsec

// Key is an opaque key material handle owned by the transport host. The
// receiver treats it as an immutable byte slice and never inspects its
// contents; the host Authenticator maps a generation to its own key state.
type Key []byte
