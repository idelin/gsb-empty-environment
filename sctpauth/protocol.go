package sctpauth

import "time"

// SCTP chunk type identifiers relevant to the state machine (RFC 9260 /
// RFC 5061).
const (
	ChunkData         uint8 = 0   // RFC 9260 DATA
	ChunkInit         uint8 = 1   // INIT
	ChunkInitAck      uint8 = 2   // INIT-ACK
	ChunkSack         uint8 = 3   // SACK
	ChunkHeartbeat    uint8 = 4   // HEARTBEAT
	ChunkHeartbeatAck uint8 = 5   // HEARTBEAT-ACK
	ChunkAbort        uint8 = 6   // ABORT
	ChunkShutdown     uint8 = 7   // SHUTDOWN
	ChunkCookieEcho   uint8 = 10  // COOKIE-ECHO
	ChunkCookieAck    uint8 = 11  // COOKIE-ACK
	ChunkASCONF       uint8 = 193 // RFC 5061 AS-CONFIG
)

// Chunk is the minimal transport-independent representation of an SCTP chunk.
// The embedding host maps its real wire chunks onto these fields.
type Chunk struct {
	Type       uint8
	TSN        uint32
	Nonce      [nonceSize]byte
	HasNonce   bool
	AddAddress string
	HasAddIP   bool
}

// Packet is an inbound SCTP packet stripped to the fields the security state
// machine needs.
type Packet struct {
	SourceAddr      string
	VerificationTag uint32
	Chunks          []Chunk
}

// InitRequest models the security-relevant contents of an inbound INIT.
type InitRequest struct {
	SourceAddr     string
	InitiateTag    uint32
	InitiateTSN    uint32
	AnnouncedAddrs []string
}

// InitAckResponse is what the host places into its INIT-ACK.
type InitAckResponse struct {
	MyTag       uint32
	MyTSN       uint32
	Cookie      []byte
	CookieState *StateCookie
}

// EstablishedResult reports the negotiated tags of a fresh association.
type EstablishedResult struct {
	PeerTag         uint32
	LocalTag        uint32
	PeerInitialTSN  uint32
	LocalInitialTSN uint32
	PrimaryAddr     string
}

// Association is an established SCTP association with its exact Verification
// Tag, a strict TSN receive window and the multihoming path gate.
type Association struct {
	localTag  uint32
	peerTag   uint32
	localTSN  uint32
	peerTSN   uint32
	paths     *PathManager
	window    *ReceiveWindow
	createdAt time.Time
}

// PeerTag returns the Verification Tag every inbound chunk must carry.
func (a *Association) PeerTag() uint32 { return a.peerTag }

// LocalTag returns the tag the host places on every outbound chunk.
func (a *Association) LocalTag() uint32 { return a.localTag }

// PeerInitialTSN returns the peer's initial TSN.
func (a *Association) PeerInitialTSN() uint32 { return a.peerTSN }

// Paths exposes the multihoming gating state machine.
func (a *Association) Paths() *PathManager { return a.paths }

// Window exposes the sliding TSN receive window.
func (a *Association) Window() *ReceiveWindow { return a.window }

// checkTag enforces the RFC 9260 rule that every chunk on an established
// association carries the exact Verification Tag of the peer.
func (a *Association) checkTag(tag uint32) error {
	if tag == 0 {
		return ErrForbiddenTag
	}
	if tag != a.peerTag {
		return ErrVerificationTag
	}
	return nil
}

// HandlePacket validates every chunk in an inbound established-association
// packet. Validation is all-or-nothing for the DATA chunks: if any TSN is
// outside the window or duplicated, the packet is rejected before any state is
// mutated.
func (a *Association) HandlePacket(packet Packet) error {
	if err := a.checkTag(packet.VerificationTag); err != nil {
		return err
	}

	for _, chunk := range packet.Chunks {
		if chunk.Type == ChunkData {
			if err := a.window.Check(chunk.TSN); err != nil {
				return err
			}
		}
	}

	for _, chunk := range packet.Chunks {
		switch chunk.Type {
		case ChunkData:
			if err := a.window.Accept(chunk.TSN); err != nil {
				return err
			}
		case ChunkHeartbeatAck:
			if !chunk.HasNonce {
				return ErrInvalidChunk
			}
			if err := a.paths.HandleAck(packet.SourceAddr, chunk.Nonce); err != nil {
				return err
			}
		case ChunkASCONF:
			if !chunk.HasAddIP {
				return ErrInvalidChunk
			}
			if _, err := a.paths.Add(chunk.AddAddress); err != nil {
				return err
			}
		default:
			return ErrInvalidChunk
		}
	}
	return nil
}

// SendData authorizes an outbound DATA chunk toward dst. It succeeds only when
// the destination path is CONFIRMED, guaranteeing that user traffic is never
// reflected onto an unvalidated third-party address.
func (a *Association) SendData(dst string, _ uint32) error {
	return a.paths.RequireConfirmed(dst)
}

// StartPathValidation issues the independent random HEARTBEAT nonce that must
// be sent to dst. Heartbeats are permitted toward UNCONFIRMED addresses; user
// data is not.
func (a *Association) StartPathValidation(dst string) ([nonceSize]byte, error) {
	return a.paths.NewChallenge(dst)
}
