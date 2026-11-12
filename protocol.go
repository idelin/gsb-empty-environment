package netsec

// SMB2/3 wire constants needed for negotiation and encryption enforcement.
// Only the subset used by this decision package is declared; offsets are
// relative to the start of the SMB2 header unless noted otherwise.

const (
	smb2HeaderLen       = 64
	negotiateReqBaseLen = 36
	negotiateCtxHdrLen  = 8
	treeConnectReqLen   = 8
	transformHeaderLen  = 52
)

// Protocol identifiers placed in the first four bytes of a message.
const (
	protocolSMB2      uint32 = 0x424d53fe // "\xfeSMB"
	protocolTransform uint32 = 0x424d53fd // "\xfdSMB"
)

// SMB2 command codes.
const (
	cmdNegotiate    uint16 = 0x0000
	cmdSessionSetup uint16 = 0x0001
	cmdTreeConnect  uint16 = 0x0003
)

// SMB2 dialect revisions.
const (
	DialectSMB202 uint16 = 0x0202
	DialectSMB210 uint16 = 0x0210
	DialectSMB300 uint16 = 0x0300
	DialectSMB302 uint16 = 0x0302
	DialectSMB311 uint16 = 0x0311
)

// Capability flags (SMB2 NEGOTIATE request body).
const (
	capEncryption uint32 = 0x00000001
)

// Negotiate context types (SMB 3.1.1).
const (
	ctxPreauthIntegrity uint16 = 0x0001
	ctxEncryption       uint16 = 0x0002
)

// Preauth integrity hash algorithm identifiers.
const (
	preauthSHA512 uint16 = 0x0001
)

// Encryption cipher identifiers (negotiate context 0x0002).
const (
	CipherAES128GCM uint16 = 0x0001
	CipherAES128CCM uint16 = 0x0002
	CipherAES256GCM uint16 = 0x0003
)

// Offsets inside the SMB2 NEGOTIATE request body (header + body start).
const (
	negDialectCountOff  = 4  // uint16
	negSecurityModeOff  = 6  // uint16
	negCapabilitiesOff  = 12 // uint32
	negCtxOffsetOff     = 28 // uint32 (offset from header start)
	negCtxCountOff      = 32 // uint16
	negDialectsStartOff = 36 // dialect array begins here within the body
)

// Offsets and sizes inside an SMB2 NEGOTIATE_CONTEXT.
const (
	ctxTypeOff    = 0
	ctxDataLenOff = 2
	ctxDataOff    = 4
)

// Offsets inside the SMB2 TREE_CONNECT request body.
const (
	treeFlagsOff   = 2
	treePathOffOff = 4  // uint32 buffer offset from the SMB2 header start
	treePathLenOff = 8  // uint32 buffer length in bytes
	treePathStart  = 12 // fixed part is 12 bytes including 2 bytes of reserved space
)
