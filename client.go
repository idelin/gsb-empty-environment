package netsec

// ClientPipe is the client-facing view of a pipe instance, implemented by
// both the secure PipeInstance and the reviewed VulnerablePipe.
type ClientPipe interface {
	Connect(Identity) (ConnectionSnapshot, error)
	WriteFromClient([]byte) error
	Disconnect()
	Listen() error
}

// Client is a controllable client. Tests and the trace search drive it to
// disconnect and reconnect at exact points: between ReadRequest and
// Impersonate, and between Impersonate and Call.
type Client struct {
	Name string
	ID   Identity
	Pipe ClientPipe
}

func NewClient(name string, id Identity, pipe ClientPipe) *Client {
	return &Client{Name: name, ID: id, Pipe: pipe}
}

// Connect attaches the client to its pipe instance.
func (c *Client) Connect() error {
	_, err := c.Pipe.Connect(c.ID)
	return Opaque(err)
}

// Send writes one complete request frame.
func (c *Client) Send(requestID uint64, payload []byte) error {
	return Opaque(c.Pipe.WriteFromClient(EncodeFrame(requestID, payload)))
}

// SendRaw writes an arbitrary fragment, used to exercise half-packet reads.
func (c *Client) SendRaw(chunk []byte) error {
	return Opaque(c.Pipe.WriteFromClient(chunk))
}

// Disconnect drops the client side of the connection.
func (c *Client) Disconnect() {
	c.Pipe.Disconnect()
}
