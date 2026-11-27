package oci

import "io"

// Common camel-case aliases for Linux capability constants.
const (
	CapCHOWN             = CAP_CHOWN
	CapDACOverride       = CAP_DAC_OVERRIDE
	CapDACReadSearch     = CAP_DAC_READ_SEARCH
	CapFOWNER            = CAP_FOWNER
	CapFSETID            = CAP_FSETID
	CapKILL              = CAP_KILL
	CapSETGID            = CAP_SETGID
	CapSETUID            = CAP_SETUID
	CapSETPCAP           = CAP_SETPCAP
	CapLINUXIMMUTABLE    = CAP_LINUX_IMMUTABLE
	CapNETBINDSERVICE    = CAP_NET_BIND_SERVICE
	CapNETBROADCAST      = CAP_NET_BROADCAST
	CapNETADMIN          = CAP_NET_ADMIN
	CapNETRAW            = CAP_NET_RAW
	CapIPCLOCK           = CAP_IPC_LOCK
	CapIPCOWNER          = CAP_IPC_OWNER
	CapSYSMODULE         = CAP_SYS_MODULE
	CapSYSRAWIO          = CAP_SYS_RAWIO
	CapSYSCHROOT         = CAP_SYS_CHROOT
	CapSYSPTRACE         = CAP_SYS_PTRACE
	CapSYSPACCT          = CAP_SYS_PACCT
	CapSYSADMIN          = CAP_SYS_ADMIN
	CapSYSBOOT           = CAP_SYS_BOOT
	CapSYSNICE           = CAP_SYS_NICE
	CapSYSRESOURCE       = CAP_SYS_RESOURCE
	CapSYSTIME           = CAP_SYS_TIME
	CapSYSTTYCONFIG      = CAP_SYS_TTY_CONFIG
	CapMKNOD             = CAP_MKNOD
	CapLEASE             = CAP_LEASE
	CapAUDITWRITE        = CAP_AUDIT_WRITE
	CapAUDITCONTROL      = CAP_AUDIT_CONTROL
	CapSETFCAP           = CAP_SETFCAP
	CapMACOVERRIDE       = CAP_MAC_OVERRIDE
	CapMACADMIN          = CAP_MAC_ADMIN
	CapSYSLOG            = CAP_SYSLOG
	CapWAKEALARM         = CAP_WAKE_ALARM
	CapBLOCKSUSPEND      = CAP_BLOCK_SUSPEND
	CapAUDITREAD         = CAP_AUDIT_READ
	CapPERFMON           = CAP_PERFMON
	CapBPF               = CAP_BPF
	CapCHECKPOINTRESTORE = CAP_CHECKPOINT_RESTORE
)

// Additional decoded-capability type names used by hosts.
type FileCapability = CapabilitySet
type VFSCapability = CapabilitySet
type CapabilityPayload = CapabilitySet
type CapabilityData = CapabilitySet

// DecodeSecurityCapability is the fully descriptive decoder alias.
func DecodeSecurityCapability(data []byte) (*CapabilitySet, error) {
	return DecodeCapability(data)
}

// ParseCapability is an alias for DecodeCapability.
func ParseCapability(data []byte) (*CapabilitySet, error) { return DecodeCapability(data) }

// ParseCapabilityPayload decodes either raw bytes or a tar/PAX string value.
func ParseCapabilityPayload(value any) (*CapabilitySet, error) {
	return DecodeCapabilityValue(value)
}

// NewPolicy returns the strict default policy.
func NewPolicy() *Policy {
	policy := DefaultPolicy()
	return &policy
}

// NewWhitelist returns an empty whitelist.
func NewWhitelist() *Whitelist { return &Whitelist{} }

// DefaultWhitelist returns an empty strict whitelist. The name is retained for
// hosts that expect a constructor; image-specific distribution exceptions
// should be supplied explicitly.
func DefaultWhitelist() Whitelist { return Whitelist{} }

// MergeLayer applies one layer to a new or existing merger.
func MergeLayer(layer Layer, mergers ...*Merger) (*FileSystem, error) {
	var merger *Merger
	if len(mergers) > 0 && mergers[0] != nil {
		merger = mergers[0]
	} else {
		merger = NewMerger()
	}
	return merger.Apply(layer)
}

// ComposeLayers is the slice-form descriptive merge API.
func ComposeLayers(layers []Layer) (*FileSystem, error) { return MergeLayers(layers) }

// AnalyzeLayers merges and validates only the final rootfs.
func AnalyzeLayers(policy any, layers ...Layer) (*Report, *FileSystem, error) {
	fs, err := Merge(layers...)
	if err != nil {
		return nil, nil, err
	}
	report, err := Validate(fs, policy)
	return report, fs, err
}

// ScanFileSystem validates an already merged file system.
func ScanFileSystem(fs *FileSystem, policy any) (*Report, error) { return Validate(fs, policy) }

// Audit and Inspect are descriptive Validate aliases.
func Audit(fs *FileSystem, policies ...any) (*Report, error) {
	return Validate(fs, policies...)
}

func Inspect(fs *FileSystem, policies ...any) (*Report, error) {
	return Validate(fs, policies...)
}

// Evaluate returns the report and error like Validate.
func Evaluate(fs *FileSystem, policies ...any) (*Report, error) {
	return Validate(fs, policies...)
}

// CheckPrivileges returns only findings for an already merged snapshot.
func CheckPrivileges(fs *FileSystem, policies ...any) []Finding {
	return Check(fs, policies...)
}

// ScanLayerTar is an alias for ScanTarLayers with one reader.
func ScanLayerTar(policy any, reader io.Reader) (*Report, *MergeResult, error) {
	return ScanTarLayers(policy, reader)
}
