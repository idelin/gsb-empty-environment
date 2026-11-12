// Package openauth implements authorization for the NFSv4.1 OPEN
// operation, including the CREATE variants of OPEN.
//
// It is transport and wire-format agnostic: callers translate an incoming
// OPEN request into an OpenRequest and implement the small Backend
// interface against their file-handle layer. The package enforces:
//
//   - AUTH_SYS credential mapping through a caller supplied root squashing
//     rule (see UIDMapper);
//   - owner/group attribute confinement at create time, so a caller cannot
//     create a file owned by another identity (NFS4ERR_PERM);
//   - GUARDED and EXCLUSIVE4_1 create semantics, including verifier based
//     retry detection (NFS4ERR_EXIST);
//   - share reservation access checks and conflict detection
//     (NFS4ERR_ACCES / NFS4ERR_SHARE_DENIED);
//   - rollback of a freshly created file when the OPEN subsequently fails.
package openauth

import "fmt"

// NFS4Status is the numeric NFSv4 error code space defined by RFC 8881.
type NFS4Status uint32

// NFSv4.1 status codes used by this package. NFS4_OK is zero; every other
// value matches the wire values in RFC 8881 appendix A.
const (
	NFS4_OK              NFS4Status = 0
	NFS4ERR_PERM         NFS4Status = 1
	NFS4ERR_NOENT        NFS4Status = 2
	NFS4ERR_IO           NFS4Status = 5
	NFS4ERR_ACCES        NFS4Status = 13
	NFS4ERR_EXIST        NFS4Status = 17
	NFS4ERR_INVAL        NFS4Status = 22
	NFS4ERR_FBIG         NFS4Status = 27
	NFS4ERR_NOSPC        NFS4Status = 28
	NFS4ERR_ROFS         NFS4Status = 30
	NFS4ERR_DQUOT        NFS4Status = 69
	NFS4ERR_GRACE        NFS4Status = 53
	NFS4ERR_BADHANDLE    NFS4Status = 10001
	NFS4ERR_BAD_COOKIE   NFS4Status = 10003
	NFS4ERR_NOTSUPP      NFS4Status = 10004
	NFS4ERR_TOOSMALL     NFS4Status = 10005
	NFS4ERR_SERVERFAULT  NFS4Status = 10006
	NFS4ERR_BADTYPE      NFS4Status = 10007
	NFS4ERR_DELAY        NFS4Status = 10008
	NFS4ERR_FHEXPIRED    NFS4Status = 10010
	NFS4ERR_SHARE_DENIED NFS4Status = 10011
	NFS4ERR_NO_GRACE     NFS4Status = 10012
	NFS4ERR_BAD_STATEID  NFS4Status = 10026
	NFS4ERR_OLD_STATEID  NFS4Status = 10025
	NFS4ERR_BAD_SEQID    NFS4Status = 10027
	NFS4ERR_NOT_SAME     NFS4Status = 10028
	NFS4ERR_EXPIRED      NFS4Status = 10039
	NFS4ERR_BADOWNER     NFS4Status = 10036
	NFS4ERR_BADVERF      NFS4Status = 10067
)

// NFSError wraps an NFS4Status so it can be returned as a Go error.
type NFSError struct {
	Status NFS4Status
	Text   string
}

// NewNFSError builds an error carrying the given NFSv4 status code.
func NewNFSError(status NFS4Status, text string) *NFSError {
	return &NFSError{Status: status, Text: text}
}

func (e *NFSError) Error() string {
	if e.Text == "" {
		return fmt.Sprintf("nfs4 status %d", e.Status)
	}
	return fmt.Sprintf("nfs4 status %d: %s", e.Status, e.Text)
}

// Is reports whether the target error wraps the same NFSv4 status code.
// It allows errors.Is(err, NewNFSError(NFS4ERR_EXIST, "")) style checks.
func (e *NFSError) Is(target error) bool {
	other, ok := target.(*NFSError)
	return ok && other.Status == e.Status
}

// StatusOf extracts the NFSv4 status code carried by err. Unknown errors
// map to NFS4ERR_SERVERFAULT; a nil error maps to NFS4_OK.
func StatusOf(err error) NFS4Status {
	if err == nil {
		return NFS4_OK
	}
	if nerr, ok := err.(*NFSError); ok {
		return nerr.Status
	}
	return NFS4ERR_SERVERFAULT
}
