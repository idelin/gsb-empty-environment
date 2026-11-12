// Package tdreport verifies the TD report body of a TDX quote after the
// quote itself has been verified by the Quote Verification Enclave (QvE).
//
// QvE attests that a quote is authentic and well-formed, but the caller
// remains responsible for binding the report to the current TLS session
// (REPORTDATA), rejecting debug-capable TDs (TDATTRIBUTES), matching the
// launch measurements against known-good reference values (MRTD/RTMRs),
// and enforcing the platform TCB policy.
package tdreport

import (
	"bytes"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
)

// TDREPORT_STRUCT field offsets (Intel TDX Module ABI, TDREPORT 1.0/1.5).
const (
	reportDataOffset = 140 // REPORTMACSTRUCT.REPORTDATA
	reportDataLen    = 64

	tdInfoOffset       = 512 // REPORTMACSTRUCT(256) + TEE_TCB_INFO(239) + reserved(17)
	tdAttributesOffset = tdInfoOffset
	mrTDOffset         = tdInfoOffset + 16 // ATTRIBUTES(8) + XFAM(8)
	rtmrOffset         = mrTDOffset + 4*48 // MRTD + MRCONFIGID + MROWNER + MROWNERCONFIG

	// ReportLen is the full size of TDREPORT_STRUCT.
	ReportLen = 1024

	// MeasurementLen is the size of MRTD and each RTMR (SHA-384).
	MeasurementLen = 48

	// NumRTMRs is the number of runtime measurement registers.
	NumRTMRs = 4

	// tdAttrDebug is the DEBUG bit of TDATTRIBUTES: when set, the TD
	// admits a debugger and its memory is not confidential.
	tdAttrDebug uint64 = 1 << 0
)

// TCBStatus mirrors the TCB level status returned by QvE for the quote's
// TCB info (e.g. from the PCK certificate chain evaluation).
type TCBStatus string

const (
	TCBUpToDate            TCBStatus = "UpToDate"
	TCBSWHardeningNeeded   TCBStatus = "SWHardeningNeeded"
	TCBOutOfDate           TCBStatus = "OutOfDate"
	TCBConfigurationNeeded TCBStatus = "ConfigurationNeeded"
	TCBRevoked             TCBStatus = "Revoked"
)

// TDReport carries the fields of TDREPORT_STRUCT that the attesting
// service must check after QvE signature verification.
type TDReport struct {
	ReportData   [reportDataLen]byte
	TDAttributes uint64
	MRTD         [MeasurementLen]byte
	RTMRs        [NumRTMRs][MeasurementLen]byte
}

// ParseTDReport extracts the security-relevant fields from a raw
// TDREPORT_STRUCT. The buffer must be at least ReportLen bytes; a
// truncated report is rejected rather than silently zero-padded.
func ParseTDReport(raw []byte) (*TDReport, error) {
	if len(raw) < ReportLen {
		return nil, fmt.Errorf("tdreport: truncated report: got %d bytes, need %d", len(raw), ReportLen)
	}
	rep := &TDReport{}
	copy(rep.ReportData[:], raw[reportDataOffset:reportDataOffset+reportDataLen])
	rep.TDAttributes = binary.LittleEndian.Uint64(raw[tdAttributesOffset:])
	copy(rep.MRTD[:], raw[mrTDOffset:mrTDOffset+MeasurementLen])
	for i := range rep.RTMRs {
		off := rtmrOffset + i*MeasurementLen
		copy(rep.RTMRs[i][:], raw[off:off+MeasurementLen])
	}
	return rep, nil
}

// ExpectedReportData computes the REPORTDATA binding the caller must have
// placed in the TD: SHA-512(nonce || TLS public key SPKI || session ID).
// The nonce and session ID defeat replay across handshakes; the SPKI hash
// pins the report to the TLS key actually terminating the session, so a
// quote harvested from another TD cannot be replayed on a different key.
func ExpectedReportData(nonce, spkiDER, sessionID []byte) [reportDataLen]byte {
	sum := sha512.New()
	sum.Write(nonce)
	sum.Write(spkiDER)
	sum.Write(sessionID)
	var out [reportDataLen]byte
	copy(out[:], sum.Sum(nil))
	return out
}

// ReferenceValues holds the known-good launch measurements for the
// workload, provisioned out of band (e.g. from a reproducible build).
type ReferenceValues struct {
	MRTD  [MeasurementLen]byte
	RTMRs [NumRTMRs][MeasurementLen]byte
}

// Policy controls TCB status acceptance. UpToDate is always accepted.
// SWHardeningNeeded is accepted only when every advisory ID reported by
// QvE is covered by SWHardeningExemptions registered by the caller.
type Policy struct {
	SWHardeningExemptions []string
}

// Verify runs all post-QvE checks on a parsed TD report. A nil error
// means the report is bound to this TLS session, comes from a
// non-debug TD with the expected measurements, and satisfies the TCB
// policy.
func Verify(rep *TDReport, ref *ReferenceValues, nonce, spkiDER, sessionID []byte, status TCBStatus, advisoryIDs []string, pol Policy) error {
	if rep == nil {
		return errors.New("tdreport: nil report")
	}
	if ref == nil {
		return errors.New("tdreport: nil reference values")
	}
	if err := CheckReportData(rep, nonce, spkiDER, sessionID); err != nil {
		return err
	}
	if err := CheckAttributes(rep); err != nil {
		return err
	}
	if err := CheckMeasurements(rep, ref); err != nil {
		return err
	}
	return CheckTCBStatus(status, advisoryIDs, pol)
}

// CheckReportData verifies the session binding of the report.
func CheckReportData(rep *TDReport, nonce, spkiDER, sessionID []byte) error {
	want := ExpectedReportData(nonce, spkiDER, sessionID)
	if !bytes.Equal(rep.ReportData[:], want[:]) {
		return errors.New("tdreport: REPORTDATA mismatch: quote is not bound to this TLS key and session")
	}
	return nil
}

// CheckAttributes rejects TDs with the DEBUG attribute set.
func CheckAttributes(rep *TDReport) error {
	if rep.TDAttributes&tdAttrDebug != 0 {
		return fmt.Errorf("tdreport: TDATTRIBUTES DEBUG bit set (attributes=0x%016x): debuggable TD rejected", rep.TDAttributes)
	}
	return nil
}

// CheckMeasurements compares MRTD and each RTMR against the reference
// values, reporting the first mismatching register.
func CheckMeasurements(rep *TDReport, ref *ReferenceValues) error {
	if !bytes.Equal(rep.MRTD[:], ref.MRTD[:]) {
		return errors.New("tdreport: MRTD mismatch")
	}
	for i := range rep.RTMRs {
		if !bytes.Equal(rep.RTMRs[i][:], ref.RTMRs[i][:]) {
			return fmt.Errorf("tdreport: RTMR[%d] mismatch", i)
		}
	}
	return nil
}

// CheckTCBStatus enforces the TCB acceptance policy.
func CheckTCBStatus(status TCBStatus, advisoryIDs []string, pol Policy) error {
	switch status {
	case TCBUpToDate:
		return nil
	case TCBSWHardeningNeeded:
		exempt := make(map[string]struct{}, len(pol.SWHardeningExemptions))
		for _, id := range pol.SWHardeningExemptions {
			exempt[id] = struct{}{}
		}
		for _, id := range advisoryIDs {
			if _, ok := exempt[id]; !ok {
				return fmt.Errorf("tdreport: TCB status SWHardeningNeeded with non-exempt advisory %q", id)
			}
		}
		return nil
	default:
		return fmt.Errorf("tdreport: unacceptable TCB status %q", status)
	}
}
