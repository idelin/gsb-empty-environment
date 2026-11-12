package netsec

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// LogonInfo is the authorization-relevant subset of KERB_VALIDATION_INFO
// ([MS-PAC] section 2.5).
type LogonInfo struct {
	EffectiveName string
	FullName      string
	LogonServer   string

	LogonDomainName string
	LogonDomainID   SID

	UserID            uint32
	PrimaryGroupID    uint32
	GroupIDs          []uint32
	UserFlags         uint32
	UserSID           SID
	PrimaryGroupSID   SID
	GroupSIDs         []SID
	ExtraSIDs         []SID
	ResourceGroupSIDs []SID
}

// AllGroupSIDs returns the primary group, regular group, extra, and resource
// group SIDs in one list. It includes the primary group because Windows
// authorization checks consider it a group membership.
func (l *LogonInfo) AllGroupSIDs() []SID {
	capacity := 0
	if l.PrimaryGroupSID.Valid() {
		capacity++
	}
	capacity += len(l.GroupSIDs) + len(l.ExtraSIDs) + len(l.ResourceGroupSIDs)

	out := make([]SID, 0, capacity)
	if l.PrimaryGroupSID.Valid() {
		out = append(out, l.PrimaryGroupSID)
	}
	out = append(out, l.GroupSIDs...)
	out = append(out, l.ExtraSIDs...)
	out = append(out, l.ResourceGroupSIDs...)
	return out
}

// HasGroupSID reports whether sid appears in AllGroupSIDs.
func (l *LogonInfo) HasGroupSID(sid SID) bool {
	for _, candidate := range l.AllGroupSIDs() {
		if candidate.Equal(sid) {
			return true
		}
	}
	return false
}

// KERB_VALIDATION_INFO fixed offsets. Variable data follows GroupIds.
const (
	validationGroupsCountOffset = 92
	validationGroupIDsOffset    = 96
)

func parseLogonInfo(data []byte) (*LogonInfo, error) {
	if len(data) < validationGroupIDsOffset {
		return nil, fmt.Errorf("%w: short fixed header", ErrMalformedLogonInfo)
	}

	info := &LogonInfo{}
	var err error

	if info.EffectiveName, err = parseUnicodeStringAt(data, 48); err != nil {
		return nil, err
	}
	if info.FullName, err = parseUnicodeStringAt(data, 56); err != nil {
		return nil, err
	}

	groupCount := uint64(binary.LittleEndian.Uint32(data[validationGroupsCountOffset : validationGroupsCountOffset+4]))
	groupsEnd := uint64(validationGroupIDsOffset) + 4*groupCount
	if groupsEnd > uint64(len(data)) {
		return nil, fmt.Errorf("%w: group array is out of bounds", ErrMalformedLogonInfo)
	}

	info.GroupIDs = make([]uint32, groupCount)
	for i := uint64(0); i < groupCount; i++ {
		off := uint64(validationGroupIDsOffset) + 4*i
		info.GroupIDs[i] = binary.LittleEndian.Uint32(data[off : off+4])
	}
	info.UserID = binary.LittleEndian.Uint32(data[84:88])
	info.PrimaryGroupID = binary.LittleEndian.Uint32(data[88:92])

	// The fields after GroupIds are variable-length (strings and SIDs). A
	// minimal buffer that ends at the group array is still parseable; a
	// buffer that extends into one of the fixed fields must be well formed.
	userFlagsOff := groupsEnd
	if userFlagsOff+4 <= uint64(len(data)) {
		info.UserFlags = binary.LittleEndian.Uint32(data[userFlagsOff : userFlagsOff+4])
	}

	logonServerOff := userFlagsOff + 4 + 16
	if logonServerOff+8 <= uint64(len(data)) {
		if info.LogonServer, err = parseUnicodeStringAt(data, logonServerOff); err != nil {
			return nil, err
		}
	}
	logonDomainNameOff := logonServerOff + 8
	if logonDomainNameOff+8 <= uint64(len(data)) {
		if info.LogonDomainName, err = parseUnicodeStringAt(data, logonDomainNameOff); err != nil {
			return nil, err
		}
	}

	logonDomainIDOff := logonDomainNameOff + 8
	if uint64(len(data)) > logonDomainIDOff {
		if uint64(len(data))-logonDomainIDOff < 8 {
			return nil, fmt.Errorf("%w: truncated LogonDomainId", ErrMalformedLogonInfo)
		}
		sid, size, err := parseSID(data[logonDomainIDOff:])
		if err != nil {
			return nil, err
		}
		info.LogonDomainID = sid
		sidEnd := logonDomainIDOff + uint64(size)
		info.UserSID = relativeSID(sid, info.UserID)
		info.PrimaryGroupSID = relativeSID(sid, info.PrimaryGroupID)
		info.GroupSIDs = relativeSIDs(sid, info.GroupIDs)

		if err := parseExtraSIDs(data, sidEnd, info); err != nil {
			return nil, err
		}
		if err := parseResourceGroupSIDs(data, sidEnd, info); err != nil {
			return nil, err
		}
	}

	return info, nil
}

func parseExtraSIDs(data []byte, sidEnd uint64, info *LogonInfo) error {
	// Reserved1 (8), UserAccountControl (4), SubAuthStatus (4),
	// LastSuccessfulLogon (8), LastFailedLogon (8), FailedPasswordCount (4),
	// SidCount (4), ExtraSids (4).
	const sidCountDelta = 36
	const extraSidsDelta = 40

	if uint64(len(data)) < sidEnd+sidCountDelta+4 {
		return nil
	}
	sidCount := uint64(binary.LittleEndian.Uint32(data[sidEnd+sidCountDelta : sidEnd+sidCountDelta+4]))
	if sidCount == 0 {
		return nil
	}
	if uint64(len(data)) < sidEnd+extraSidsDelta+4 {
		return fmt.Errorf("%w: truncated ExtraSids pointer", ErrMalformedLogonInfo)
	}
	extraSidsOff := uint64(binary.LittleEndian.Uint32(data[sidEnd+extraSidsDelta : sidEnd+extraSidsDelta+4]))
	if extraSidsOff == 0 {
		return fmt.Errorf("%w: nil ExtraSids with count %d", ErrMalformedLogonInfo, sidCount)
	}
	if sidCount > uint64(len(data))/8 {
		return fmt.Errorf("%w: ExtraSids count is out of bounds", ErrMalformedLogonInfo)
	}

	info.ExtraSIDs = make([]SID, 0, sidCount)
	for i := uint64(0); i < sidCount; i++ {
		entry := extraSidsOff + 8*i
		if entry > uint64(len(data)) || uint64(len(data))-entry < 8 {
			return fmt.Errorf("%w: truncated ExtraSids entry", ErrMalformedLogonInfo)
		}
		sidOff := binary.LittleEndian.Uint32(data[entry : entry+4])
		if sidOff == 0 {
			return fmt.Errorf("%w: nil ExtraSids SID", ErrMalformedLogonInfo)
		}
		sid, err := parseSIDAt(data, sidOff)
		if err != nil {
			return err
		}
		info.ExtraSIDs = append(info.ExtraSIDs, sid)
	}
	return nil
}

func parseResourceGroupSIDs(data []byte, sidEnd uint64, info *LogonInfo) error {
	// ResourceGroupDomainSid (4), ResourceGroupCount (4), ResourceGroupIds (4).
	const resourceDomainDelta = 44
	const resourceCountDelta = 48
	const resourceIDsDelta = 52

	if uint64(len(data)) < sidEnd+resourceIDsDelta+4 {
		return nil
	}
	count := uint64(binary.LittleEndian.Uint32(data[sidEnd+resourceCountDelta : sidEnd+resourceCountDelta+4]))
	if count == 0 {
		return nil
	}
	domainOff := binary.LittleEndian.Uint32(data[sidEnd+resourceDomainDelta : sidEnd+resourceDomainDelta+4])
	if domainOff == 0 {
		return fmt.Errorf("%w: nil ResourceGroupDomainSid", ErrMalformedLogonInfo)
	}
	domain, err := parseSIDAt(data, domainOff)
	if err != nil {
		return err
	}
	idsOff := uint64(binary.LittleEndian.Uint32(data[sidEnd+resourceIDsDelta : sidEnd+resourceIDsDelta+4]))
	if idsOff == 0 {
		return fmt.Errorf("%w: nil ResourceGroupIds with count %d", ErrMalformedLogonInfo, count)
	}
	if count > uint64(len(data))/4 {
		return fmt.Errorf("%w: ResourceGroupIds count is out of bounds", ErrMalformedLogonInfo)
	}

	info.ResourceGroupSIDs = make([]SID, 0, count)
	for i := uint64(0); i < count; i++ {
		off := idsOff + 4*i
		if off > uint64(len(data)) || uint64(len(data))-off < 4 {
			return fmt.Errorf("%w: truncated ResourceGroupIds", ErrMalformedLogonInfo)
		}
		rid := binary.LittleEndian.Uint32(data[off : off+4])
		info.ResourceGroupSIDs = append(info.ResourceGroupSIDs, relativeSID(domain, rid))
	}
	return nil
}

func parseUnicodeStringAt(data []byte, offset uint64) (string, error) {
	if offset > uint64(len(data)) || uint64(len(data))-offset < 8 {
		return "", fmt.Errorf("%w: truncated RPC_UNICODE_STRING", ErrMalformedLogonInfo)
	}
	length := binary.LittleEndian.Uint16(data[offset : offset+2])
	// MaximumLength is not needed for parsing.
	bufferOff := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
	if length == 0 {
		return "", nil
	}
	if length%2 != 0 {
		return "", fmt.Errorf("%w: odd UTF-16 length %d", ErrMalformedLogonInfo, length)
	}
	if bufferOff > uint64(len(data)) || uint64(length) > uint64(len(data))-bufferOff {
		return "", fmt.Errorf("%w: string data is out of bounds", ErrMalformedLogonInfo)
	}

	utf16Bytes := data[bufferOff : bufferOff+uint64(length)]
	codeUnits := make([]uint16, len(utf16Bytes)/2)
	for i := range codeUnits {
		codeUnits[i] = binary.LittleEndian.Uint16(utf16Bytes[2*i:])
	}
	return string(utf16.Decode(codeUnits)), nil
}

func relativeSID(domain SID, rid uint32) SID {
	if !domain.Valid() {
		return SID{}
	}
	subs := make([]uint32, 0, len(domain.SubAuthorities)+1)
	subs = append(subs, domain.SubAuthorities...)
	subs = append(subs, rid)
	return SID{
		Revision:       domain.Revision,
		Authority:      domain.Authority,
		SubAuthorities: subs,
	}
}

func relativeSIDs(domain SID, rids []uint32) []SID {
	if !domain.Valid() {
		return nil
	}
	out := make([]SID, len(rids))
	for i, rid := range rids {
		out[i] = relativeSID(domain, rid)
	}
	return out
}
