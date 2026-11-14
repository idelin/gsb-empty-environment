package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// bindingDomain separates prescription signatures from any other use of the
	// same physician key.
	bindingDomain = "netsec/prescription-binding/v1"

	// SignatureSize is the size of an Ed25519 signature.
	SignatureSize = ed25519.SignatureSize
)

var (
	// ErrInvalidPrescription means the prescription is malformed or incomplete.
	ErrInvalidPrescription = errors.New("netsec: invalid prescription")
	// ErrInvalidSignature means the physician signature does not cover the
	// supplied prescription.
	ErrInvalidSignature = errors.New("netsec: invalid physician signature")
	// ErrMissingSigningKey means no physician key is available for verification.
	ErrMissingSigningKey = errors.New("netsec: missing physician verification key")
	// ErrExpiredPrescription means the prescription is no longer valid.
	ErrExpiredPrescription = errors.New("netsec: prescription expired")
	// ErrUnknownPrescription means the prescription was never registered.
	ErrUnknownPrescription = errors.New("netsec: unknown prescription")
	// ErrPatientMismatch means the prescription belongs to another patient.
	ErrPatientMismatch = errors.New("netsec: patient does not match prescription")
	// ErrDrugMismatch means the request names a different drug.
	ErrDrugMismatch = errors.New("netsec: drug does not match prescription")
	// ErrPhysicianMismatch means the request names a different prescriber.
	ErrPhysicianMismatch = errors.New("netsec: physician does not match prescription")
	// ErrDiagnosisMismatch means the request names a different diagnosis.
	ErrDiagnosisMismatch = errors.New("netsec: diagnosis does not match prescription")
	// ErrOverdose means the requested dose exceeds the signed maximum dose.
	ErrOverdose = errors.New("netsec: dose exceeds prescription")
	// ErrAlreadyDispensed means the one-time prescription has been used.
	ErrAlreadyDispensed = errors.New("netsec: prescription already dispensed")
	// ErrInvalidDispenseRequest means the dispensing request is ambiguous or
	// incomplete.
	ErrInvalidDispenseRequest = errors.New("netsec: invalid dispense request")
)

// Compatibility aliases for the sentinel errors above.
var (
	ErrSignatureInvalid             = ErrInvalidSignature
	ErrWrongPatient                 = ErrPatientMismatch
	ErrWrongDrug                    = ErrDrugMismatch
	ErrDoseExceeded                 = ErrOverdose
	ErrPrescriptionUsed             = ErrAlreadyDispensed
	ErrPrescriptionAlreadyDispensed = ErrAlreadyDispensed
	ErrReplay                       = ErrAlreadyDispensed
)

// Prescription is the signed binding between a patient, a prescriber, a
// diagnosis, and exactly one drug and maximum dose.
//
// The identifier fields have compatibility aliases (for example ID and
// PrescriptionID). Only one spelling may be populated, or both spellings must
// contain the same value. The aliases are resolved before validation and
// signing.
type Prescription struct {
	ID             string
	PrescriptionID string
	RxID           string

	PatientID string

	PhysicianID  string
	DoctorID     string
	PrescriberID string

	Diagnosis     string
	DiagnosisCode string

	DrugCode       string
	MedicationCode string
	DrugID         string
	Drug           string

	// Dosage is the maximum total dose that may be dispensed under this
	// prescription. Dose and MaxDose are accepted as compatibility aliases.
	Dosage  float64
	Dose    float64
	MaxDose float64

	Unit string

	IssuedAt  time.Time
	ExpiresAt time.Time

	// Signature is an Ed25519 signature by the prescriber over all other
	// binding fields. It is never part of the signed payload itself.
	Signature []byte
}

// SignedPrescription is a convenience wrapper for transports that carry the
// signature outside the prescription struct.
type SignedPrescription struct {
	Prescription
	Signature []byte
	Sig       []byte
}

// PrescriptionBinding is the canonical, effective value of a prescription.
// It is exported so services can persist exactly what was signed.
type PrescriptionBinding struct {
	ID            string
	PatientID     string
	PhysicianID   string
	Diagnosis     string
	DiagnosisCode string
	DrugCode      string
	Dosage        float64
	Unit          string
	IssuedAt      time.Time
	ExpiresAt     time.Time
}

func pickAlias(field string, values ...string) (string, error) {
	var selected string
	for _, value := range values {
		if value == "" {
			continue
		}
		if selected != "" && selected != value {
			return "", fmt.Errorf("%w: conflicting %s values", ErrInvalidPrescription, field)
		}
		selected = value
	}
	return selected, nil
}

func pickNumber(field string, values ...float64) (float64, error) {
	var selected float64
	for _, value := range values {
		if value == 0 {
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("%w: %s must be finite", ErrInvalidPrescription, field)
		}
		if selected != 0 && selected != value {
			return 0, fmt.Errorf("%w: conflicting %s values", ErrInvalidPrescription, field)
		}
		selected = value
	}
	return selected, nil
}

func (p Prescription) normalized() (PrescriptionBinding, error) {
	var out PrescriptionBinding
	var err error

	if out.ID, err = pickAlias("prescription identifier", p.ID, p.PrescriptionID, p.RxID); err != nil {
		return out, err
	}
	out.PatientID = strings.TrimSpace(p.PatientID)

	if out.PhysicianID, err = pickAlias("physician identifier", p.PhysicianID, p.DoctorID, p.PrescriberID); err != nil {
		return out, err
	}

	out.Diagnosis = strings.TrimSpace(p.Diagnosis)
	out.DiagnosisCode = strings.TrimSpace(p.DiagnosisCode)

	if out.DrugCode, err = pickAlias("drug code", p.DrugCode, p.MedicationCode, p.DrugID, p.Drug); err != nil {
		return out, err
	}
	out.DrugCode = strings.TrimSpace(out.DrugCode)

	if out.Dosage, err = pickNumber("dosage", p.Dosage, p.Dose, p.MaxDose); err != nil {
		return out, err
	}

	out.Unit = strings.TrimSpace(p.Unit)
	out.IssuedAt = p.IssuedAt
	out.ExpiresAt = p.ExpiresAt

	if out.ID == "" {
		return out, fmt.Errorf("%w: missing prescription identifier", ErrInvalidPrescription)
	}
	if out.PatientID == "" {
		return out, fmt.Errorf("%w: missing patient identifier", ErrInvalidPrescription)
	}
	if out.PhysicianID == "" {
		return out, fmt.Errorf("%w: missing physician identifier", ErrInvalidPrescription)
	}
	if out.Diagnosis == "" && out.DiagnosisCode == "" {
		return out, fmt.Errorf("%w: missing diagnosis", ErrInvalidPrescription)
	}
	if out.DrugCode == "" {
		return out, fmt.Errorf("%w: missing drug code", ErrInvalidPrescription)
	}
	if out.Dosage <= 0 || math.IsNaN(out.Dosage) || math.IsInf(out.Dosage, 0) {
		return out, fmt.Errorf("%w: dosage must be a positive finite number", ErrInvalidPrescription)
	}
	if !out.ExpiresAt.IsZero() && !out.IssuedAt.IsZero() && out.ExpiresAt.Before(out.IssuedAt) {
		return out, fmt.Errorf("%w: expiry precedes issue time", ErrInvalidPrescription)
	}

	return out, nil
}

// Binding returns the canonical effective fields that a signature covers.
func (p Prescription) Binding() (PrescriptionBinding, error) {
	return p.normalized()
}

func writeString(buf *bytes.Buffer, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	buf.Write(size[:])
	buf.WriteString(value)
}

func writeFloat(buf *bytes.Buffer, value float64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], math.Float64bits(value))
	buf.Write(raw[:])
}

func writeTime(buf *bytes.Buffer, value time.Time) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(value.UnixNano()))
	buf.Write(raw[:])
}

// SigningPayload returns the exact bytes covered by the physician signature.
// It contains a domain separator and length-delimited fields to avoid
// concatenation ambiguity.
func (p Prescription) SigningPayload() ([]byte, error) {
	binding, err := p.normalized()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writeString(&buf, bindingDomain)
	writeString(&buf, binding.ID)
	writeString(&buf, binding.PatientID)
	writeString(&buf, binding.PhysicianID)
	writeString(&buf, binding.Diagnosis)
	writeString(&buf, binding.DiagnosisCode)
	writeString(&buf, binding.DrugCode)
	writeFloat(&buf, binding.Dosage)
	writeString(&buf, binding.Unit)
	writeTime(&buf, binding.IssuedAt)
	writeTime(&buf, binding.ExpiresAt)
	return buf.Bytes(), nil
}

// CanonicalBytes is an alias for SigningPayload.
func (p Prescription) CanonicalBytes() ([]byte, error) {
	return p.SigningPayload()
}

// Bytes is an alias for SigningPayload.
func (p Prescription) Bytes() ([]byte, error) {
	return p.SigningPayload()
}

// Sign fills p.Signature with an Ed25519 signature over the complete binding.
func (p *Prescription) Sign(privateKey ed25519.PrivateKey) error {
	if p == nil {
		return fmt.Errorf("%w: nil prescription", ErrInvalidPrescription)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidSignature)
	}
	payload, err := p.SigningPayload()
	if err != nil {
		return err
	}
	p.Signature = ed25519.Sign(privateKey, payload)
	return nil
}

// SignPrescription signs a prescription value or pointer. When a pointer is
// supplied its Signature field is populated in addition to the returned
// signature.
func SignPrescription(prescription any, privateKey ed25519.PrivateKey) ([]byte, error) {
	p, pointer, err := prescriptionValue(prescription)
	if err != nil {
		return nil, err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidSignature)
	}
	payload, err := p.SigningPayload()
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(privateKey, payload)
	if pointer != nil {
		pointer.Signature = append(pointer.Signature[:0], signature...)
	}
	if signed, ok := prescription.(*SignedPrescription); ok && signed != nil {
		signed.Signature = append(signed.Signature[:0], signature...)
		signed.Sig = append(signed.Sig[:0], signature...)
	}
	return signature, nil
}

// Sign is an alias for SignPrescription.
func Sign(prescription any, privateKey ed25519.PrivateKey) ([]byte, error) {
	return SignPrescription(prescription, privateKey)
}

func prescriptionValue(value any) (Prescription, *Prescription, error) {
	switch typed := value.(type) {
	case Prescription:
		return typed, nil, nil
	case *Prescription:
		if typed == nil {
			return Prescription{}, nil, fmt.Errorf("%w: nil prescription", ErrInvalidPrescription)
		}
		return *typed, typed, nil
	case SignedPrescription:
		return typed.Prescription, nil, nil
	case *SignedPrescription:
		if typed == nil {
			return Prescription{}, nil, fmt.Errorf("%w: nil prescription", ErrInvalidPrescription)
		}
		return typed.Prescription, &typed.Prescription, nil
	default:
		return Prescription{}, nil, fmt.Errorf("%w: unsupported prescription type %T", ErrInvalidPrescription, value)
	}
}

func signatureFromValue(value any) (Prescription, []byte, error) {
	switch typed := value.(type) {
	case Prescription:
		return typed, typed.Signature, nil
	case *Prescription:
		if typed == nil {
			return Prescription{}, nil, fmt.Errorf("%w: nil prescription", ErrInvalidPrescription)
		}
		return *typed, typed.Signature, nil
	case SignedPrescription:
		signature := typed.Signature
		if len(signature) == 0 {
			signature = typed.Sig
		}
		if len(signature) == 0 {
			signature = typed.Prescription.Signature
		}
		return typed.Prescription, signature, nil
	case *SignedPrescription:
		if typed == nil {
			return Prescription{}, nil, fmt.Errorf("%w: nil prescription", ErrInvalidPrescription)
		}
		signature := typed.Signature
		if len(signature) == 0 {
			signature = typed.Sig
		}
		if len(signature) == 0 {
			signature = typed.Prescription.Signature
		}
		return typed.Prescription, signature, nil
	default:
		return Prescription{}, nil, fmt.Errorf("%w: unsupported prescription type %T", ErrInvalidPrescription, value)
	}
}

// Verify checks the physician signature over every bound field.
func (p Prescription) Verify(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: invalid Ed25519 public key", ErrMissingSigningKey)
	}
	if len(p.Signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	payload, err := p.SigningPayload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, payload, p.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyForPatient verifies the signature and proves the prescription belongs
// to patientID.
func (p Prescription) VerifyForPatient(patientID string, publicKey ed25519.PublicKey) error {
	if err := p.Verify(publicKey); err != nil {
		return err
	}
	binding, err := p.normalized()
	if err != nil {
		return err
	}
	if !sameIdentifier(binding.PatientID, strings.TrimSpace(patientID)) {
		return ErrPatientMismatch
	}
	return nil
}

// VerifyPrescription checks a prescription value or pointer against a
// physician public key.
func VerifyPrescription(prescription any, publicKey ed25519.PublicKey) error {
	p, signature, err := signatureFromValue(prescription)
	if err != nil {
		return err
	}
	p.Signature = signature
	return p.Verify(publicKey)
}

// VerifyPrescriptionWithSignature verifies a prescription whose signature is
// transported separately from the bound fields.
func VerifyPrescriptionWithSignature(prescription any, signature []byte, publicKey ed25519.PublicKey) error {
	p, _, err := signatureFromValue(prescription)
	if err != nil {
		return err
	}
	p.Signature = signature
	return p.Verify(publicKey)
}

// VerifyPrescriptionForPatient verifies the signature and additionally proves
// that the prescription was issued to patientID.
func VerifyPrescriptionForPatient(prescription any, patientID string, publicKey ed25519.PublicKey) error {
	if err := VerifyPrescription(prescription, publicKey); err != nil {
		return err
	}
	p, _, err := signatureFromValue(prescription)
	if err != nil {
		return err
	}
	binding, err := p.normalized()
	if err != nil {
		return err
	}
	if !sameIdentifier(binding.PatientID, strings.TrimSpace(patientID)) {
		return ErrPatientMismatch
	}
	return nil
}

// VerifyPatient is an alias for VerifyPrescriptionForPatient.
func VerifyPatient(prescription any, patientID string, publicKey ed25519.PublicKey) error {
	return VerifyPrescriptionForPatient(prescription, patientID, publicKey)
}

// IsValidPrescription is the boolean form of VerifyPrescription.
func IsValidPrescription(prescription any, publicKey ed25519.PublicKey) bool {
	return VerifyPrescription(prescription, publicKey) == nil
}

// VerifySignature is the boolean form of VerifyPrescription.
func VerifySignature(prescription any, publicKey ed25519.PublicKey) bool {
	return IsValidPrescription(prescription, publicKey)
}

// ValidatePrescription checks the structural requirements of a prescription
// without verifying its signature.
func ValidatePrescription(prescription any) error {
	p, _, err := prescriptionValue(prescription)
	if err != nil {
		return err
	}
	_, err = p.normalized()
	return err
}

// NewSignedPrescription signs a copy and returns a transport-friendly wrapper.
func NewSignedPrescription(prescription any, privateKey ed25519.PrivateKey) (SignedPrescription, error) {
	p, _, err := prescriptionValue(prescription)
	if err != nil {
		return SignedPrescription{}, err
	}
	signature, err := SignPrescription(&p, privateKey)
	if err != nil {
		return SignedPrescription{}, err
	}
	return SignedPrescription{Prescription: p, Signature: signature, Sig: signature}, nil
}

// IsExpired reports whether the prescription has an expiry and it has passed.
func (p Prescription) IsExpired(now time.Time) bool {
	if p.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(p.ExpiresAt)
}

func (p Prescription) checkNotExpired(now time.Time) error {
	if p.IsExpired(now) {
		return ErrExpiredPrescription
	}
	return nil
}

func sameIdentifier(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func clonePrescription(p Prescription) Prescription {
	clone := p
	clone.Signature = append([]byte(nil), p.Signature...)
	return clone
}
