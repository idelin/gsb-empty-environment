package netsec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Status is the lifecycle state of a prescription. A prescription starts
// active and can move to dispensed exactly once.
type Status string

// PrescriptionStatus is a compatibility alias for Status.
type PrescriptionStatus = Status

// State is a compatibility alias for Status.
type State = Status

const (
	// StatusActive means the prescription may still be dispensed.
	StatusActive Status = "active"
	// StatusDispensed means the one-time prescription has been consumed.
	StatusDispensed Status = "dispensed"

	// Pending and Dispensed are short aliases for the status values.
	Pending   = StatusActive
	Dispensed = StatusDispensed

	// StatusPending, StatusUsed, and StatusConsumed are compatibility aliases.
	StatusPending  = StatusActive
	StatusUsed     = StatusDispensed
	StatusConsumed = StatusDispensed
)

// DispenseRequest is the pharmacy's request to dispense a prescription.
//
// PrescriptionID and PatientID are mandatory. DrugCode and Dosage should be
// supplied by the pharmacy; when they are omitted the signed values are used.
// If they are present, they must exactly match the signed prescription.
type DispenseRequest struct {
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

	// Dosage is the amount requested for this fill. Dose and Quantity are
	// compatibility aliases, as is RequestedDose. A zero amount means the full
	// signed dosage.
	Dosage        float64
	Dose          float64
	Quantity      float64
	RequestedDose float64
}

// DispenseResult describes a successful, one-time fill.
type DispenseResult struct {
	PrescriptionID string
	PatientID      string
	PhysicianID    string
	Diagnosis      string
	DiagnosisCode  string
	DrugCode       string
	Dosage         float64
	Unit           string
	DispensedAt    time.Time
}

type prescriptionRecord struct {
	prescription Prescription
	binding      PrescriptionBinding
	payload      []byte
	status       Status
	registeredAt time.Time
	dispensedAt  time.Time
}

// Dispenser registers signed prescriptions and performs one-time dispensing.
// It is safe for concurrent use.
type Dispenser struct {
	mu sync.Mutex

	now func() time.Time

	// PublicKey is the default physician verification key. It may be set by the
	// constructor or directly on a zero-value Dispenser.
	PublicKey  ed25519.PublicKey
	physicians map[string]ed25519.PublicKey
	records    map[string]*prescriptionRecord
}

// Pharmacy, PrescriptionStore, and DispensingService are compatibility
// aliases for Dispenser.
type Pharmacy = Dispenser

// PrescriptionStore is a compatibility alias for Dispenser.
type PrescriptionStore = Dispenser

// DispensingService is a compatibility alias for Dispenser.
type DispensingService = Dispenser

// Service is a compatibility alias for Dispenser.
type Service = Dispenser

// PrescriptionBinder is a compatibility alias for Dispenser.
type PrescriptionBinder = Dispenser

// PrescriptionDispenser is a compatibility alias for Dispenser.
type PrescriptionDispenser = Dispenser

// Store is a compatibility alias for Dispenser.
type Store = Dispenser

// NewDispenser creates a dispensing service. A physician public key may be
// supplied as the default verification key. For convenience an Ed25519 private
// key or raw 32/64-byte key may also be supplied.
func NewDispenser(keys ...any) *Dispenser {
	d := &Dispenser{
		now:        time.Now,
		physicians: make(map[string]ed25519.PublicKey),
		records:    make(map[string]*prescriptionRecord),
	}
	for _, key := range keys {
		if publicKey, ok := normalizePublicKey(key); ok {
			d.PublicKey = publicKey
			break
		}
	}
	return d
}

// NewPharmacy is an alias for NewDispenser.
func NewPharmacy(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewPrescriptionStore is an alias for NewDispenser.
func NewPrescriptionStore(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewService is an alias for NewDispenser.
func NewService(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewDispensingService is an alias for NewDispenser.
func NewDispensingService(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewPrescriptionService is an alias for NewDispenser.
func NewPrescriptionService(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewPrescriptionBinder is an alias for NewDispenser.
func NewPrescriptionBinder(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewPrescriptionDispenser is an alias for NewDispenser.
func NewPrescriptionDispenser(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewStore is an alias for NewDispenser.
func NewStore(keys ...any) *Dispenser { return NewDispenser(keys...) }

// NewDispenserWithPublicKey creates a service with an explicit physician key.
func NewDispenserWithPublicKey(publicKey ed25519.PublicKey) *Dispenser {
	return NewDispenser(publicKey)
}

func normalizePublicKey(key any) (ed25519.PublicKey, bool) {
	switch typed := key.(type) {
	case ed25519.PublicKey:
		if len(typed) == ed25519.PublicKeySize {
			return append(ed25519.PublicKey(nil), typed...), true
		}
	case *ed25519.PublicKey:
		if typed != nil && len(*typed) == ed25519.PublicKeySize {
			return append(ed25519.PublicKey(nil), (*typed)...), true
		}
	case ed25519.PrivateKey:
		if len(typed) == ed25519.PrivateKeySize {
			publicKey, ok := typed.Public().(ed25519.PublicKey)
			if ok {
				return append(ed25519.PublicKey(nil), publicKey...), true
			}
		}
	case *ed25519.PrivateKey:
		if typed != nil && len(*typed) == ed25519.PrivateKeySize {
			publicKey, ok := (*typed).Public().(ed25519.PublicKey)
			if ok {
				return append(ed25519.PublicKey(nil), publicKey...), true
			}
		}
	case []byte:
		switch len(typed) {
		case ed25519.PublicKeySize:
			return append(ed25519.PublicKey(nil), typed...), true
		case ed25519.PrivateKeySize:
			privateKey := ed25519.PrivateKey(append([]byte(nil), typed...))
			publicKey, ok := privateKey.Public().(ed25519.PublicKey)
			if ok {
				return append(ed25519.PublicKey(nil), publicKey...), true
			}
		}
	case string:
		decoded, err := hex.DecodeString(strings.TrimSpace(typed))
		if err == nil {
			return normalizePublicKey(decoded)
		}
	}
	return nil, false
}

// SetPublicKey sets the default physician verification key.
func (d *Dispenser) SetPublicKey(publicKey ed25519.PublicKey) error {
	normalized, ok := normalizePublicKey(publicKey)
	if !ok {
		return ErrMissingSigningKey
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.PublicKey = normalized
	return nil
}

// AddPhysician authorizes a physician-specific verification key. When a key is
// registered for a physician, it takes precedence over the default key.
func (d *Dispenser) AddPhysician(physicianID string, publicKey ed25519.PublicKey) error {
	physicianID = strings.TrimSpace(physicianID)
	if physicianID == "" {
		return fmt.Errorf("%w: missing physician identifier", ErrInvalidPrescription)
	}
	normalized, ok := normalizePublicKey(publicKey)
	if !ok {
		return ErrMissingSigningKey
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()
	d.physicians[physicianID] = normalized
	return nil
}

// RegisterPhysician is an alias for AddPhysician.
func (d *Dispenser) RegisterPhysician(physicianID string, publicKey ed25519.PublicKey) error {
	return d.AddPhysician(physicianID, publicKey)
}

func (d *Dispenser) verificationKey(physicianID string) (ed25519.PublicKey, bool) {
	if key, ok := d.physicians[physicianID]; ok {
		return key, true
	}
	if len(d.PublicKey) == ed25519.PublicKeySize {
		return d.PublicKey, true
	}
	return nil, false
}

func (d *Dispenser) initLocked() {
	if d.now == nil {
		d.now = time.Now
	}
	if d.physicians == nil {
		d.physicians = make(map[string]ed25519.PublicKey)
	}
	if d.records == nil {
		d.records = make(map[string]*prescriptionRecord)
	}
}

func (d *Dispenser) currentTime() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// Register verifies and stores a signed prescription. Re-registering the same
// signed prescription is idempotent; trying to replace a prescription ID with
// different contents is rejected.
//
// Accepted forms are Register(prescription), Register(&prescription),
// Register(signedPrescription), or Register(prescription, signature).
func (d *Dispenser) Register(args ...any) error {
	p, signature, err := parseRegisterArgs(args)
	if err != nil {
		return err
	}
	if len(signature) != 0 {
		p.Signature = append([]byte(nil), signature...)
	}

	binding, err := p.normalized()
	if err != nil {
		return err
	}

	payload, err := p.SigningPayload()
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()

	publicKey, ok := d.verificationKey(binding.PhysicianID)
	if !ok {
		return ErrMissingSigningKey
	}
	if err := p.Verify(publicKey); err != nil {
		return err
	}
	if p.IsExpired(d.currentTime()) {
		return ErrExpiredPrescription
	}

	if existing, found := d.records[binding.ID]; found {
		if !bytes.Equal(existing.payload, payload) || !bytes.Equal(existing.prescription.Signature, p.Signature) {
			return fmt.Errorf("%w: prescription identifier already bound to different contents", ErrInvalidPrescription)
		}
		return nil
	}

	d.records[binding.ID] = &prescriptionRecord{
		prescription: clonePrescription(p),
		binding:      binding,
		payload:      append([]byte(nil), payload...),
		status:       StatusActive,
		registeredAt: d.currentTime(),
	}
	return nil
}

// RegisterPrescription is an alias for Register.
func (d *Dispenser) RegisterPrescription(prescription any) error {
	return d.Register(prescription)
}

// AddPrescription is an alias for Register.
func (d *Dispenser) AddPrescription(prescription any) error {
	return d.Register(prescription)
}

// StorePrescription is an alias for Register.
func (d *Dispenser) StorePrescription(prescription any) error {
	return d.Register(prescription)
}

// ImportPrescription is an alias for Register.
func (d *Dispenser) ImportPrescription(prescription any) error {
	return d.Register(prescription)
}

// LoadPrescription is an alias for Register.
func (d *Dispenser) LoadPrescription(prescription any) error {
	return d.Register(prescription)
}

func parseRegisterArgs(args []any) (Prescription, []byte, error) {
	var p Prescription
	var found bool
	var signature []byte

	for _, arg := range args {
		if arg == nil {
			continue
		}
		switch typed := arg.(type) {
		case context.Context:
			continue
		case Prescription, *Prescription, SignedPrescription, *SignedPrescription:
			if found {
				return Prescription{}, nil, fmt.Errorf("%w: multiple prescriptions supplied", ErrInvalidPrescription)
			}
			value, sig, err := signatureFromValue(typed)
			if err != nil {
				return Prescription{}, nil, err
			}
			p = value
			signature = sig
			found = true
		case []byte:
			signature = append([]byte(nil), typed...)
		case ed25519.PublicKey:
			// Physician verification keys are configured on the service rather
			// than accepted per registration call.
		case string:
			if found && len(signature) == 0 {
				decoded, err := hex.DecodeString(strings.TrimSpace(typed))
				if err == nil {
					signature = decoded
				}
			}
		default:
			return Prescription{}, nil, fmt.Errorf("%w: unsupported registration argument %T", ErrInvalidPrescription, arg)
		}
	}
	if !found {
		return Prescription{}, nil, fmt.Errorf("%w: missing prescription", ErrInvalidPrescription)
	}
	return p, signature, nil
}

// Dispense performs the pre-dispensing checks and atomically consumes the
// prescription. It accepts a DispenseRequest, or the common service form
// Dispense(prescriptionID, patientID, drugCode, dosage). A Prescription value
// may also be supplied so the caller can prove it holds the exact signed
// source object.
func (d *Dispenser) Dispense(args ...any) error {
	_, err := d.dispense(args...)
	return err
}

// Fill is the typed form of Dispense and returns a fill receipt.
func (d *Dispenser) Fill(request DispenseRequest) (DispenseResult, error) {
	return d.dispense(request)
}

// DispenseWithReceipt is an alias for Fill.
func (d *Dispenser) DispenseWithReceipt(request DispenseRequest) (DispenseResult, error) {
	return d.Fill(request)
}

// DispensePrescription is an alias for Dispense that makes the optional
// prescription argument explicit in call sites.
func (d *Dispenser) DispensePrescription(args ...any) error {
	return d.Dispense(args...)
}

func (d *Dispenser) dispense(args ...any) (DispenseResult, error) {
	request, suppliedPrescription, err := parseDispenseArgs(args)
	if err != nil {
		return DispenseResult{}, err
	}
	request, err = normalizeDispenseRequest(request)
	if err != nil {
		return DispenseResult{}, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()

	record, ok := d.records[request.ID]
	if !ok {
		return DispenseResult{}, ErrUnknownPrescription
	}
	if record.status == StatusDispensed {
		return DispenseResult{}, ErrAlreadyDispensed
	}

	publicKey, ok := d.verificationKey(record.binding.PhysicianID)
	if !ok {
		return DispenseResult{}, ErrMissingSigningKey
	}
	if err := record.prescription.Verify(publicKey); err != nil {
		return DispenseResult{}, err
	}
	if record.prescription.IsExpired(d.currentTime()) {
		return DispenseResult{}, ErrExpiredPrescription
	}

	if suppliedPrescription != nil {
		if err := d.checkSuppliedPrescription(record, *suppliedPrescription); err != nil {
			return DispenseResult{}, err
		}
	}

	if !sameIdentifier(record.binding.PatientID, request.PatientID) {
		return DispenseResult{}, ErrPatientMismatch
	}
	if request.PhysicianID != "" && !sameIdentifier(record.binding.PhysicianID, request.PhysicianID) {
		return DispenseResult{}, ErrPhysicianMismatch
	}
	if request.Diagnosis != "" && !sameIdentifier(record.binding.Diagnosis, request.Diagnosis) {
		return DispenseResult{}, ErrDiagnosisMismatch
	}
	if request.DiagnosisCode != "" && !sameIdentifier(record.binding.DiagnosisCode, request.DiagnosisCode) {
		return DispenseResult{}, ErrDiagnosisMismatch
	}

	drugCode := request.DrugCode
	if drugCode == "" {
		drugCode = record.binding.DrugCode
	}
	if !sameIdentifier(record.binding.DrugCode, drugCode) {
		return DispenseResult{}, ErrDrugMismatch
	}

	dosage := request.Dosage
	if dosage == 0 {
		dosage = record.binding.Dosage
	}
	if dosage <= 0 || math.IsNaN(dosage) || math.IsInf(dosage, 0) {
		return DispenseResult{}, fmt.Errorf("%w: dosage must be positive and finite", ErrInvalidDispenseRequest)
	}
	if dosage > record.binding.Dosage && !nearlyEqual(dosage, record.binding.Dosage) {
		return DispenseResult{}, ErrOverdose
	}

	dispensedAt := d.currentTime()
	record.status = StatusDispensed
	record.dispensedAt = dispensedAt

	return DispenseResult{
		PrescriptionID: record.binding.ID,
		PatientID:      record.binding.PatientID,
		PhysicianID:    record.binding.PhysicianID,
		Diagnosis:      record.binding.Diagnosis,
		DiagnosisCode:  record.binding.DiagnosisCode,
		DrugCode:       record.binding.DrugCode,
		Dosage:         dosage,
		Unit:           record.binding.Unit,
		DispensedAt:    dispensedAt,
	}, nil
}

func (d *Dispenser) checkSuppliedPrescription(record *prescriptionRecord, supplied Prescription) error {
	binding, err := supplied.normalized()
	if err != nil {
		return err
	}
	if !sameIdentifier(record.binding.ID, binding.ID) {
		return fmt.Errorf("%w: supplied prescription has a different identifier", ErrInvalidPrescription)
	}
	if !sameIdentifier(record.binding.PatientID, binding.PatientID) {
		return ErrPatientMismatch
	}
	if !sameIdentifier(record.binding.PhysicianID, binding.PhysicianID) {
		return ErrPhysicianMismatch
	}
	if !sameIdentifier(record.binding.Diagnosis, binding.Diagnosis) ||
		!sameIdentifier(record.binding.DiagnosisCode, binding.DiagnosisCode) {
		return ErrDiagnosisMismatch
	}
	if !sameIdentifier(record.binding.DrugCode, binding.DrugCode) {
		return ErrDrugMismatch
	}
	if binding.Dosage > record.binding.Dosage && !nearlyEqual(binding.Dosage, record.binding.Dosage) {
		return ErrOverdose
	}
	payload, err := supplied.SigningPayload()
	if err != nil {
		return err
	}
	if !bytes.Equal(record.payload, payload) {
		return fmt.Errorf("%w: supplied prescription differs from the registered binding", ErrInvalidSignature)
	}
	publicKey, ok := d.verificationKey(binding.PhysicianID)
	if !ok {
		return ErrMissingSigningKey
	}
	return supplied.Verify(publicKey)
}

func nearlyEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

func normalizeDispenseRequest(request DispenseRequest) (DispenseRequest, error) {
	var err error
	if request.ID, err = pickAlias("prescription identifier", request.ID, request.PrescriptionID, request.RxID); err != nil {
		return request, fmt.Errorf("%w: %v", ErrInvalidDispenseRequest, err)
	}
	request.ID = strings.TrimSpace(request.ID)
	request.PatientID = strings.TrimSpace(request.PatientID)

	if request.PhysicianID, err = pickAlias("physician identifier", request.PhysicianID, request.DoctorID, request.PrescriberID); err != nil {
		return request, fmt.Errorf("%w: %v", ErrInvalidDispenseRequest, err)
	}
	request.Diagnosis = strings.TrimSpace(request.Diagnosis)
	request.DiagnosisCode = strings.TrimSpace(request.DiagnosisCode)

	if request.DrugCode, err = pickAlias("drug code", request.DrugCode, request.MedicationCode, request.DrugID, request.Drug); err != nil {
		return request, fmt.Errorf("%w: %v", ErrInvalidDispenseRequest, err)
	}
	request.DrugCode = strings.TrimSpace(request.DrugCode)

	if request.Dosage, err = pickAliasNumber("dosage", request.Dosage, request.Dose, request.Quantity, request.RequestedDose); err != nil {
		return request, fmt.Errorf("%w: %v", ErrInvalidDispenseRequest, err)
	}
	if request.Dosage < 0 || math.IsNaN(request.Dosage) || math.IsInf(request.Dosage, 0) {
		return request, fmt.Errorf("%w: dosage must be non-negative and finite", ErrInvalidDispenseRequest)
	}

	if request.ID == "" {
		return request, fmt.Errorf("%w: missing prescription identifier", ErrInvalidDispenseRequest)
	}
	if request.PatientID == "" {
		return request, fmt.Errorf("%w: missing patient identifier", ErrInvalidDispenseRequest)
	}
	return request, nil
}

func pickAliasNumber(field string, values ...float64) (float64, error) {
	var selected float64
	for _, value := range values {
		if value == 0 {
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("%s must be finite", field)
		}
		if selected != 0 && selected != value {
			return 0, fmt.Errorf("conflicting %s values", field)
		}
		selected = value
	}
	return selected, nil
}

func parseDispenseArgs(args []any) (DispenseRequest, *Prescription, error) {
	var request DispenseRequest
	var haveRequest bool
	var supplied *Prescription
	var stringsSeen []string
	var numbersSeen []float64
	var separateSignature []byte

	for _, arg := range args {
		if arg == nil {
			continue
		}
		switch typed := arg.(type) {
		case context.Context:
			continue
		case DispenseRequest:
			if haveRequest {
				return request, nil, fmt.Errorf("%w: multiple requests supplied", ErrInvalidDispenseRequest)
			}
			request = typed
			haveRequest = true
		case *DispenseRequest:
			if typed == nil {
				return request, nil, fmt.Errorf("%w: nil request", ErrInvalidDispenseRequest)
			}
			if haveRequest {
				return request, nil, fmt.Errorf("%w: multiple requests supplied", ErrInvalidDispenseRequest)
			}
			request = *typed
			haveRequest = true
		case Prescription:
			if supplied != nil {
				return request, nil, fmt.Errorf("%w: multiple prescriptions supplied", ErrInvalidDispenseRequest)
			}
			copy := typed
			supplied = &copy
		case *Prescription:
			if typed == nil {
				return request, nil, fmt.Errorf("%w: nil prescription", ErrInvalidDispenseRequest)
			}
			if supplied != nil {
				return request, nil, fmt.Errorf("%w: multiple prescriptions supplied", ErrInvalidDispenseRequest)
			}
			copy := *typed
			supplied = &copy
		case SignedPrescription:
			if supplied != nil {
				return request, nil, fmt.Errorf("%w: multiple prescriptions supplied", ErrInvalidDispenseRequest)
			}
			copy := typed.Prescription
			if len(copy.Signature) == 0 {
				copy.Signature = typed.Signature
			}
			if len(copy.Signature) == 0 {
				copy.Signature = typed.Sig
			}
			supplied = &copy
		case *SignedPrescription:
			if typed == nil {
				return request, nil, fmt.Errorf("%w: nil prescription", ErrInvalidDispenseRequest)
			}
			if supplied != nil {
				return request, nil, fmt.Errorf("%w: multiple prescriptions supplied", ErrInvalidDispenseRequest)
			}
			copy := typed.Prescription
			if len(copy.Signature) == 0 {
				copy.Signature = typed.Signature
			}
			if len(copy.Signature) == 0 {
				copy.Signature = typed.Sig
			}
			supplied = &copy
		case string:
			stringsSeen = append(stringsSeen, typed)
		case float64:
			numbersSeen = append(numbersSeen, typed)
		case float32:
			numbersSeen = append(numbersSeen, float64(typed))
		case int:
			numbersSeen = append(numbersSeen, float64(typed))
		case int64:
			numbersSeen = append(numbersSeen, float64(typed))
		case ed25519.PublicKey:
			// A verification key in a dispense call is not part of the request.
			// The service already owns its trusted key set.
		case []byte:
			separateSignature = append([]byte(nil), typed...)
		default:
			return request, nil, fmt.Errorf("%w: unsupported dispense argument %T", ErrInvalidDispenseRequest, arg)
		}
	}

	if supplied != nil && len(separateSignature) != 0 && len(supplied.Signature) == 0 {
		supplied.Signature = separateSignature
	}

	if supplied != nil {
		setRequestFieldsFromPrescription(&request, supplied, stringsSeen, numbersSeen)
	} else if len(stringsSeen) > 0 || len(numbersSeen) > 0 {
		setRequestFields(&request, stringsSeen, numbersSeen)
	}
	if !haveRequest {
		if request.ID == "" && len(stringsSeen) == 0 {
			return request, nil, fmt.Errorf("%w: missing dispensing input", ErrInvalidDispenseRequest)
		}
	}
	return request, supplied, nil
}

func setRequestFieldsFromPrescription(request *DispenseRequest, prescription *Prescription, values []string, numbers []float64) {
	binding, err := prescription.normalized()
	if err != nil {
		return
	}
	if request.ID == "" {
		request.ID = binding.ID
	}
	if request.PatientID == "" {
		request.PatientID = binding.PatientID
	}
	if len(values) > 0 {
		request.PatientID = values[0]
	}
	if len(values) > 1 {
		request.DrugCode = values[1]
	}
	if len(values) > 2 {
		request.PhysicianID = values[2]
	}
	if len(values) > 3 {
		request.Diagnosis = values[3]
	}
	if request.DrugCode == "" {
		request.DrugCode = binding.DrugCode
	}
	if len(numbers) > 0 {
		request.Dosage = numbers[0]
	}
	if request.Dosage == 0 {
		request.Dosage = binding.Dosage
	}
}

func setRequestFields(request *DispenseRequest, values []string, numbers []float64) {
	if len(values) > 0 {
		request.ID = values[0]
	}
	if len(values) > 1 {
		request.PatientID = values[1]
	}
	if len(values) > 2 {
		request.DrugCode = values[2]
	}
	if len(values) > 3 {
		request.PhysicianID = values[3]
	}
	if len(values) > 4 {
		request.Diagnosis = values[4]
	}
	if len(numbers) > 0 {
		request.Dosage = numbers[0]
	}
}

// Status returns the current lifecycle state of a prescription.
func (d *Dispenser) Status(prescriptionID string) (Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[strings.TrimSpace(prescriptionID)]
	if !ok {
		return "", ErrUnknownPrescription
	}
	return record.status, nil
}

// LookupStatus returns the status and whether the prescription is registered.
func (d *Dispenser) LookupStatus(prescriptionID string) (Status, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[strings.TrimSpace(prescriptionID)]
	if !ok {
		return "", false
	}
	return record.status, true
}

// IsDispensed reports whether a registered prescription has been consumed.
func (d *Dispenser) IsDispensed(prescriptionID string) bool {
	status, ok := d.LookupStatus(prescriptionID)
	return ok && status == StatusDispensed
}

// Prescription returns a defensive copy of a registered prescription.
func (d *Dispenser) Prescription(prescriptionID string) (Prescription, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[strings.TrimSpace(prescriptionID)]
	if !ok {
		return Prescription{}, false
	}
	return clonePrescription(record.prescription), true
}

// VerifyPrescription checks that a prescription supplied to the service is
// registered, unmodified, and signed by its physician.
func (d *Dispenser) VerifyPrescription(prescription any) error {
	p, signature, err := signatureFromValue(prescription)
	if err != nil {
		return err
	}
	p.Signature = signature
	binding, err := p.normalized()
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[binding.ID]
	if !ok {
		return ErrUnknownPrescription
	}
	if err := d.checkSuppliedPrescription(record, p); err != nil {
		return err
	}
	return record.prescription.checkNotExpired(d.currentTime())
}

// Verify is an alias for VerifyPrescription.
func (d *Dispenser) Verify(prescription any) error {
	return d.VerifyPrescription(prescription)
}
