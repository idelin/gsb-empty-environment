package netsec

// Envelope is an SMTP transaction destined for a next hop. RequireTLS maps
// to the RFC 8689 REQUIRETLS MAIL FROM parameter.
type Envelope struct {
	MailFrom   string
	RcptTo     []string
	Data       []byte
	RequireTLS bool
}

// InboundMessage is a message accepted from an SMTP client. TLS reports
// whether the session had completed STARTTLS before MAIL FROM was issued.
type InboundMessage struct {
	Envelope
	TLS        bool
	RemoteAddr string
}

// InboundHandler is called only after the transaction has passed SMTP and
// TLS-Required header policy checks. A returned error causes a 451 reply.
type InboundHandler func(*InboundMessage) error
