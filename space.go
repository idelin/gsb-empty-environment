package rfc9002

// PacketNumberSpace identifies one of QUIC's three independent packet-number
// spaces (RFC 9000 Section 12.3): Initial, Handshake and Application Data.
//
// Packet numbers are independent per space, so loss thresholds and ACK
// ranges never cross space boundaries.
type PacketNumberSpace uint8

// Space is a short alias for PacketNumberSpace.
type Space = PacketNumberSpace

const (
	// Initial is the Initial packet number space.
	Initial PacketNumberSpace = iota
	// Handshake is the Handshake packet number space.
	Handshake
	// ApplicationData is the Application Data (1-RTT) packet number space.
	ApplicationData
)

// NumSpaces is the number of QUIC packet number spaces.
const NumSpaces = 3

// RFC-prefixed / descriptive aliases so callers can use whichever naming
// style they are most familiar with.
const (
	SpaceInitial         = Initial
	InitialSpace         = Initial
	PNInitial            = Initial
	PNSpaceInitial       = Initial
	EncryptionInitial    = Initial
	SpaceHandshake       = Handshake
	HandshakeSpace       = Handshake
	PNHandshake          = Handshake
	PNSpaceHandshake     = Handshake
	EncryptionHandshake  = Handshake
	SpaceApplicationData = ApplicationData
	ApplicationDataSpace = ApplicationData
	Application          = ApplicationData
	ApplicationSpace     = ApplicationData
	AppData              = ApplicationData
	AppDataSpace         = ApplicationData
	PNApplicationData    = ApplicationData
	PNSpaceApplication   = ApplicationData
	Encryption1RTT       = ApplicationData
)

// AllSpaces lists the spaces in RFC order.
var AllSpaces = [NumSpaces]Space{Initial, Handshake, ApplicationData}

// String returns the RFC name of the space.
func (s PacketNumberSpace) String() string {
	switch s {
	case Initial:
		return "initial"
	case Handshake:
		return "handshake"
	case ApplicationData:
		return "application_data"
	default:
		return "unknown"
	}
}

// Valid reports whether s is one of the three defined spaces.
func (s PacketNumberSpace) Valid() bool {
	return s <= ApplicationData
}
