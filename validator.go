package netsec

// opClass describes how an opcode touches its referenced registered file.
type opClass uint8

const (
	classNoFD  opClass = iota // no file descriptor involved (timeout, nop, ...)
	classRead                 // needs AccessRead
	classWrite                // needs AccessWrite (implies nothing about read)
)

// opcodeClass maps every opcode the validator understands to its access
// class. Opcodes absent from the map are rejected even if whitelisted.
var opcodeClass = map[uint8]opClass{
	OpNop:            classNoFD,
	OpTimeout:        classNoFD,
	OpTimeoutRemove:  classNoFD,
	OpAsyncCancel:    classNoFD,
	OpLinkTimeout:    classNoFD,
	OpProvideBuffers: classNoFD,
	OpRemoveBuffers:  classNoFD,
	OpWaitid:         classNoFD,
	OpFutexWait:      classNoFD,
	OpFutexWake:      classNoFD,
	OpFutexWaitv:     classNoFD,
	OpNapiTimeout:    classNoFD,

	OpReadv:         classRead,
	OpReadFixed:     classRead,
	OpRead:          classRead,
	OpReadMultishot: classRead,
	OpRecv:          classRead,
	OpRecvmsg:       classRead,
	OpPollAdd:       classRead,

	OpWritev:        classWrite,
	OpWriteFixed:    classWrite,
	OpWrite:         classWrite,
	OpSend:          classWrite,
	OpSendZC:        classWrite,
	OpSendmsg:       classWrite,
	OpSendmsgZC:     classWrite,
	OpSplice:        classWrite,
	OpTee:           classWrite,
	OpFsync:         classWrite,
	OpSyncFileRange: classWrite,
	OpFallocate:     classWrite,
	OpFadvise:       classWrite,
	OpConnect:       classWrite,
	OpAccept:        classWrite,
	OpShutdown:      classWrite,
}

// DefaultOpcodes is the conservative data-plane whitelist: pure data
// movement plus a few socket/fsync operations. Path-metadata, ring-control
// and fd-installing opcodes are deliberately excluded.
func DefaultOpcodes() map[uint8]bool {
	return map[uint8]bool{
		OpNop: true, OpTimeout: true,
		OpReadv: true, OpReadFixed: true, OpRead: true, OpReadMultishot: true,
		OpRecv: true, OpRecvmsg: true, OpPollAdd: true,
		OpWritev: true, OpWriteFixed: true, OpWrite: true,
		OpSend: true, OpSendZC: true, OpSendmsg: true, OpSendmsgZC: true,
		OpSplice: true, OpTee: true,
		OpFsync: true, OpSyncFileRange: true,
		OpConnect: true, OpAccept: true, OpShutdown: true,
	}
}

// Config tunes a Validator. The zero value is not usable; call DefaultConfig.
type Config struct {
	// AllowedOpcodes is the opcode whitelist. nil rejects everything.
	AllowedOpcodes map[uint8]bool
	// AllowedFlags masks acceptable IOSQE_* bits; unknown bits are rejected.
	AllowedFlags uint8
	// RequireFixedFile forces fd-class opcodes through the registered-files
	// table and forbids IOSQE_FIXED_FILE on no-fd opcodes.
	RequireFixedFile bool
	// CheckPersonality enforces sqe.personality against slot credentials.
	CheckPersonality bool
	// NoFDPersonality, when non-zero, is required on no-fd opcodes while
	// CheckPersonality is enabled.
	NoFDPersonality uint16
}

// DefaultConfig returns a config usable for an untrusted-client proxy.
func DefaultConfig() Config {
	return Config{
		AllowedOpcodes:   DefaultOpcodes(),
		AllowedFlags:     DefaultAllowedFlags,
		RequireFixedFile: true,
		CheckPersonality: true,
	}
}

// Decision is the validated result for one SQE.
type Decision struct {
	Index       int
	Opcode      uint8
	Personality uint16
	// Slots lists every registered-files index referenced by the SQE
	// (two entries for SPLICE/TEE, one for other fd-class opcodes, none
	// for no-fd opcodes).
	Slots []uint64
	// CredID is the credential identity bound to the referenced slots;
	// zero for no-fd opcodes.
	CredID uint64
}

// Validator validates SQEs against a SlotTable snapshot.
type Validator struct {
	table *SlotTable
	cfg   Config
}

// New builds a Validator. A nil table is accepted and fails every fd lookup.
func New(table *SlotTable, cfg Config) *Validator {
	return &Validator{table: table, cfg: cfg}
}

// ValidateOne validates a single SQE (or one link element) against a fresh
// table snapshot. For linked submissions use ValidateBatch, which also
// enforces chain-wide identity rules and rejects dangling links.
func (v *Validator) ValidateOne(sqe *SQE) (*Decision, error) {
	snap := v.snapshot()
	return v.validateEntry(0, sqe, snap)
}

// ValidateBatch validates a submission batch. SQEs joined by IOSQE_IO_LINK /
// IOSQE_IO_HARDLINK form a chain whose members must all resolve to the same
// credential and present the same personality. The whole batch is judged
// against one table snapshot so revocation cannot split a chain.
func (v *Validator) ValidateBatch(sqes []*SQE) ([]Decision, error) {
	if len(sqes) == 0 {
		return nil, ruleErr(-1, RuleEmptyBatch, ErrEmptyBatch, "no sqes submitted")
	}
	snap := v.snapshot()
	decisions := make([]Decision, len(sqes))

	chainStart := 0
	for i := 0; i < len(sqes); i++ {
		dec, err := v.validateEntry(i, sqes[i], snap)
		if err != nil {
			return nil, err
		}
		decisions[i] = *dec

		if sqes[i].Linked() {
			if i == len(sqes)-1 {
				return nil, ruleErr(i, RuleChainLink, ErrDanglingLink,
					OpcodeName(sqes[i].Opcode)+" carries a link flag but no sqe follows")
			}
			continue
		}

		if err := v.validateChain(chainStart, i, sqes, decisions, snap); err != nil {
			return nil, err
		}
		chainStart = i + 1
	}
	return decisions, nil
}

func (v *Validator) snapshot() []Slot {
	if v.table == nil {
		return nil
	}
	return v.table.Snapshot()
}

func (v *Validator) validateEntry(pos int, sqe *SQE, snap []Slot) (*Decision, error) {
	if unknown := sqe.Flags &^ v.cfg.AllowedFlags; unknown != 0 {
		return nil, ruleErr(pos, RuleFlagAllowed, ErrFlagNotAllowed,
			"flag bits 0x"+hexByte(unknown)+" not permitted")
	}

	// FILE_INDEX_UPPER declares that the high byte is meaningful; a stray
	// high byte without the flag is a malformed/forged index encoding.
	if !sqe.HasFlag(IOSQEFileIndexUpper) && sqe.FileIndexUpper != 0 {
		return nil, ruleErr(pos, RuleIndexEncoding, ErrIndexEncoding,
			"file_index_upper is non-zero without IOSQE_FILE_INDEX_UPPER")
	}

	if !v.cfg.AllowedOpcodes[sqe.Opcode] {
		return nil, ruleErr(pos, RuleOpcodeWhitelist, ErrOpcodeNotAllowed,
			OpcodeName(sqe.Opcode)+" is not in the proxy whitelist")
	}
	class, ok := opcodeClass[sqe.Opcode]
	if !ok {
		return nil, ruleErr(pos, RuleOpcodeWhitelist, ErrOpcodeNotAllowed,
			OpcodeName(sqe.Opcode)+" has no access classification")
	}

	dec := &Decision{Index: pos, Opcode: sqe.Opcode, Personality: sqe.Personality}

	if class == classNoFD {
		if sqe.HasFlag(IOSQEFixedFile) {
			return nil, ruleErr(pos, RuleFixedFileForbidden, ErrFixedFileForbidden,
				OpcodeName(sqe.Opcode)+" cannot reference a fixed-file slot")
		}
		if v.cfg.CheckPersonality && v.cfg.NoFDPersonality != 0 &&
			sqe.Personality != v.cfg.NoFDPersonality {
			return nil, ruleErr(pos, RulePersonality, ErrPersonality,
				"nop/control sqe personality does not match the proxy persona")
		}
		return dec, nil
	}

	if v.cfg.RequireFixedFile && !sqe.HasFlag(IOSQEFixedFile) {
		return nil, ruleErr(pos, RuleFixedFileRequired, ErrFixedFileRequired,
			OpcodeName(sqe.Opcode)+" must target a registered fixed file")
	}
	if !sqe.HasFlag(IOSQEFixedFile) {
		// Fixed-file enforcement disabled: nothing else can be checked.
		return dec, nil
	}

	required := AccessRead
	if class == classWrite {
		required = AccessWrite
	}

	idx := sqe.FixedIndex()
	slot, err := checkSlot(pos, idx, required, snap)
	if err != nil {
		return nil, err
	}
	dec.Slots = append(dec.Slots, idx)
	dec.CredID = slot.Cred.CredID

	// SPLICE/TEE cannot carry a personality: sqe.personality shares its
	// storage with splice_fd_in. Their identity is pinned instead by
	// requiring both operand slots to belong to one credential below.
	if v.cfg.CheckPersonality && sqe.Opcode != OpSplice && sqe.Opcode != OpTee &&
		sqe.Personality != slot.Cred.PersonalityID {
		return nil, ruleErr(pos, RulePersonality, ErrPersonality,
			"sqe personality "+itoa(int(sqe.Personality))+
				" != slot persona "+itoa(int(slot.Cred.PersonalityID)))
	}

	// SPLICE/TEE carry a second fixed file in splice_fd_in. With
	// IOSQE_FIXED_FILE the kernel indexes both operands through the
	// registered-files table, so the source slot is validated as well.
	if sqe.Opcode == OpSplice || sqe.Opcode == OpTee {
		if sqe.SpliceFDIn < 0 {
			return nil, ruleErr(pos, RuleSlotRange, ErrSlotOutOfRange,
				"splice_fd_in cannot be a negative fixed-file index")
		}
		inIdx := uint64(sqe.SpliceFDIn)
		inSlot, err := checkSlot(pos, inIdx, AccessRead, snap)
		if err != nil {
			return nil, err
		}
		if inSlot.Cred.CredID != slot.Cred.CredID {
			return nil, ruleErr(pos, RuleChainCred, ErrChainCredMismatch,
				"splice operands belong to different credentials")
		}
		dec.Slots = append(dec.Slots, inIdx)
	}

	return dec, nil
}

func checkSlot(pos int, idx uint64, required Access, snap []Slot) (Slot, error) {
	if idx >= uint64(len(snap)) {
		return Slot{}, ruleErr(pos, RuleSlotRange, ErrSlotOutOfRange,
			"slot index "+u64toa(idx)+" >= table size "+itoa(len(snap)))
	}
	slot := snap[idx]
	if slot.State != SlotActive {
		state := "empty"
		if slot.State == SlotRevoked {
			state = "revoked"
		}
		return Slot{}, ruleErr(pos, RuleSlotState, ErrSlotNotActive,
			"slot "+u64toa(idx)+" is "+state)
	}
	if !slot.Access.Can(required) {
		return Slot{}, ruleErr(pos, RuleSlotAccess, ErrSlotAccessDenied,
			"slot "+u64toa(idx)+" lacks required access "+accessName(required))
	}
	return slot, nil
}

// validateChain enforces that one already-bounded link chain never mixes
// credentials or personalities across its SQEs.
func (v *Validator) validateChain(start, end int, sqes []*SQE, decisions []Decision, snap []Slot) error {
	if start == end {
		return nil // single, unlinked SQE
	}

	var chainCred uint64
	haveCred := false
	var chainPersona uint16
	havePersona := false

	for j := start; j <= end; j++ {
		// SPLICE/TEE have no readable personality (union with splice_fd_in).
		if v.cfg.CheckPersonality && sqes[j].Opcode != OpSplice && sqes[j].Opcode != OpTee {
			if !havePersona {
				chainPersona = sqes[j].Personality
				havePersona = true
			} else if sqes[j].Personality != chainPersona {
				return ruleErr(j, RuleChainPersonality, ErrChainPersMismatch,
					"chain personality changed inside a linked chain")
			}
		}
		for _, idx := range decisions[j].Slots {
			slot := snap[idx]
			if !haveCred {
				chainCred = slot.Cred.CredID
				haveCred = true
				continue
			}
			if slot.Cred.CredID != chainCred {
				return ruleErr(j, RuleChainCred, ErrChainCredMismatch,
					"linked chain switches from credential "+u64toa(chainCred)+
						" to "+u64toa(slot.Cred.CredID))
			}
		}
	}
	return nil
}

func accessName(a Access) string {
	switch a {
	case AccessRead:
		return "read"
	case AccessWrite:
		return "write"
	case AccessRead | AccessWrite:
		return "read-write"
	default:
		return "none"
	}
}

func u64toa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
