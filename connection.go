package netsec

// Connection is an authenticated transport connection. Its generation is
// advanced whenever the transport is reused for a new request; that single
// number invalidates every token and task handle minted for the old request.
type Connection struct {
	host *Host

	id         string
	generation uint64
	open       bool
	request    *Request
}
