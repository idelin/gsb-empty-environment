package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return publicKey, privateKey
}

func testPrescription(t *testing.T, privateKey ed25519.PrivateKey) Prescription {
	t.Helper()
	prescription := Prescription{
		ID:          "RX-1001",
		PatientID:   "PATIENT-A",
		PhysicianID: "DR-7",
		Diagnosis:   "J18.9",
		DrugCode:    "MED-ASPIRIN-100",
		Dosage:      100,
		Unit:        "mg",
		IssuedAt:    time.Now().Add(-time.Minute),
	}
	if _, err := SignPrescription(&prescription, privateKey); err != nil {
		t.Fatalf("sign prescription: %v", err)
	}
	return prescription
}

func TestLegitimateDispense(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)

	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}

	result, err := dispenser.Fill(DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      prescription.PatientID,
		DrugCode:       prescription.DrugCode,
		Dosage:         50,
	})
	if err != nil {
		t.Fatalf("dispense: %v", err)
	}
	if result.PatientID != prescription.PatientID || result.DrugCode != prescription.DrugCode || result.Dosage != 50 {
		t.Fatalf("unexpected result: %+v", result)
	}

	status, err := dispenser.Status(prescription.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != StatusDispensed {
		t.Fatalf("status = %q, want %q", status, StatusDispensed)
	}
}

func TestPatientSubstitutionRejected(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}

	tampered := prescription
	tampered.PatientID = "PATIENT-B"
	if err := dispenser.Dispense(tampered, DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      tampered.PatientID,
		DrugCode:       prescription.DrugCode,
		Dosage:         100,
	}); !errors.Is(err, ErrPatientMismatch) {
		t.Fatalf("tampered patient via prescription: err = %v, want patient mismatch", err)
	}

	err := dispenser.Dispense(DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      "PATIENT-B",
		DrugCode:       prescription.DrugCode,
		Dosage:         100,
	})
	if !errors.Is(err, ErrPatientMismatch) {
		t.Fatalf("substituted patient: err = %v, want patient mismatch", err)
	}
	if dispenser.IsDispensed(prescription.ID) {
		t.Fatal("rejected request must not consume the prescription")
	}
}

func TestDrugTamperingRejected(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}

	tampered := prescription
	tampered.DrugCode = "MED-OPIOID-999"
	if err := VerifyPrescription(tampered, publicKey); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered drug signature: err = %v, want invalid signature", err)
	}

	err := dispenser.Dispense(DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      prescription.PatientID,
		DrugCode:       tampered.DrugCode,
		Dosage:         prescription.Dosage,
	})
	if !errors.Is(err, ErrDrugMismatch) {
		t.Fatalf("substituted drug: err = %v, want drug mismatch", err)
	}
	if dispenser.IsDispensed(prescription.ID) {
		t.Fatal("rejected request must not consume the prescription")
	}
}

func TestDuplicateDispenseRejected(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}

	request := DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      prescription.PatientID,
		DrugCode:       prescription.DrugCode,
		Dosage:         prescription.Dosage,
	}
	if err := dispenser.Dispense(request); err != nil {
		t.Fatalf("first dispense: %v", err)
	}
	if err := dispenser.Dispense(request); !errors.Is(err, ErrAlreadyDispensed) {
		t.Fatalf("second dispense: err = %v, want already dispensed", err)
	}
}

func TestConcurrentDispenseIsOneTime(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}

	const workers = 64
	var successes atomic.Int32
	var duplicates atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := dispenser.Dispense(DispenseRequest{
				PrescriptionID: prescription.ID,
				PatientID:      prescription.PatientID,
				DrugCode:       prescription.DrugCode,
			})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrAlreadyDispensed):
				duplicates.Add(1)
			default:
				t.Errorf("unexpected dispense error: %v", err)
			}
		}()
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("successful dispenses = %d, want 1", successes.Load())
	}
	if duplicates.Load() != workers-1 {
		t.Fatalf("duplicate errors = %d, want %d", duplicates.Load(), workers-1)
	}
}

func TestOverdoseRejected(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}

	err := dispenser.Dispense(DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      prescription.PatientID,
		DrugCode:       prescription.DrugCode,
		Dosage:         prescription.Dosage + 0.01,
	})
	if !errors.Is(err, ErrOverdose) {
		t.Fatalf("overdose: err = %v, want overdose", err)
	}
	if dispenser.IsDispensed(prescription.ID) {
		t.Fatal("overdose must not consume the prescription")
	}

	if err := dispenser.Dispense(DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      prescription.PatientID,
		DrugCode:       prescription.DrugCode,
		Dosage:         prescription.Dosage,
	}); err != nil {
		t.Fatalf("exact maximum dose: %v", err)
	}
}

func TestSignatureOnlyValidForOriginalPatient(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)

	if err := VerifyPrescriptionForPatient(prescription, prescription.PatientID, publicKey); err != nil {
		t.Fatalf("original patient: %v", err)
	}
	if err := VerifyPrescriptionForPatient(prescription, "PATIENT-B", publicKey); !errors.Is(err, ErrPatientMismatch) {
		t.Fatalf("other patient: err = %v, want patient mismatch", err)
	}

	otherPublicKey, _ := testKeys(t)
	if err := VerifyPrescription(prescription, otherPublicKey); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("other key: err = %v, want invalid signature", err)
	}
}

func TestAllBoundFieldsAreCoveredBySignature(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	base := testPrescription(t, privateKey)

	mutations := map[string]func(*Prescription){
		"id":            func(p *Prescription) { p.ID = "RX-9999" },
		"patient":       func(p *Prescription) { p.PatientID = "PATIENT-B" },
		"physician":     func(p *Prescription) { p.PhysicianID = "DR-8" },
		"diagnosis":     func(p *Prescription) { p.Diagnosis = "I10" },
		"diagnosisCode": func(p *Prescription) { p.DiagnosisCode = "Z00" },
		"drug":          func(p *Prescription) { p.DrugCode = "MED-OTHER" },
		"dosage":        func(p *Prescription) { p.Dosage = 101 },
		"unit":          func(p *Prescription) { p.Unit = "g" },
		"issuedAt":      func(p *Prescription) { p.IssuedAt = p.IssuedAt.Add(time.Second) },
		"expiresAt":     func(p *Prescription) { p.ExpiresAt = p.IssuedAt.Add(time.Hour) },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			tampered := base
			mutate(&tampered)
			if err := VerifyPrescription(tampered, publicKey); !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("err = %v, want invalid signature", err)
			}
		})
	}
}

func TestReRegisterCannotReplaceBinding(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}

	replacement := prescription
	replacement.PatientID = "PATIENT-B"
	if err := dispenser.Register(replacement); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("unsigned replacement: err = %v, want invalid signature", err)
	}
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("idempotent re-register: %v", err)
	}
}

func TestExpiredPrescriptionRejected(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	prescription.ExpiresAt = time.Now().Add(-time.Second)
	if _, err := SignPrescription(&prescription, privateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); !errors.Is(err, ErrExpiredPrescription) {
		t.Fatalf("register expired: err = %v, want expired", err)
	}
}

func TestAliasFieldsResolveBeforeSigning(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := Prescription{
		PrescriptionID: "RX-ALIAS",
		PatientID:      "PATIENT-A",
		DoctorID:       "DR-9",
		DiagnosisCode:  "J18.9",
		MedicationCode: "MED-1",
		Dose:           25,
	}
	if _, err := SignPrescription(prescription, privateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	signature, err := SignPrescription(prescription, privateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	prescription.Signature = signature

	if err := VerifyPrescription(prescription, publicKey); err != nil {
		t.Fatalf("verify alias prescription: %v", err)
	}
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register alias prescription: %v", err)
	}
	if err := dispenser.Dispense("RX-ALIAS", "PATIENT-A", "MED-1", 25.0); err != nil {
		t.Fatalf("dispense alias prescription: %v", err)
	}
}

func TestZeroValueDispenserWithPublicKey(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)

	dispenser := &Dispenser{PublicKey: publicKey}
	if err := dispenser.Register(prescription); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := dispenser.Dispense(prescription.ID, prescription.PatientID, prescription.DrugCode, int(100)); err != nil {
		t.Fatalf("dispense: %v", err)
	}
	if !dispenser.IsDispensed(prescription.ID) {
		t.Fatal("prescription should be dispensed")
	}
}

func TestExternalSignatureAndDefaultedFields(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	prescription := testPrescription(t, privateKey)
	signature := prescription.Signature
	prescription.Signature = nil

	signed := SignedPrescription{Prescription: prescription, Signature: signature}
	if err := VerifyPrescription(signed, publicKey); err != nil {
		t.Fatalf("verify external signature: %v", err)
	}

	dispenser := NewDispenser()
	if err := dispenser.AddPhysician(prescription.PhysicianID, publicKey); err != nil {
		t.Fatalf("add physician: %v", err)
	}
	if err := dispenser.Register(signed); err != nil {
		t.Fatalf("register signed prescription: %v", err)
	}

	// Drug code and dose are optional in a request; both default to the signed
	// values. This still cannot substitute another patient.
	if err := dispenser.Dispense(DispenseRequest{
		ID:        prescription.ID,
		PatientID: prescription.PatientID,
	}); err != nil {
		t.Fatalf("dispense with defaults: %v", err)
	}
}

func TestDispenseWithPrescriptionArgument(t *testing.T) {
	publicKey, privateKey := testKeys(t)
	first := testPrescription(t, privateKey)
	dispenser := NewDispenser(publicKey)
	if err := dispenser.Register(first); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := dispenser.Dispense(&first, first.PatientID, first.DrugCode, float64(40)); err != nil {
		t.Fatalf("dispense with signed prescription: %v", err)
	}

	second := testPrescription(t, privateKey)
	second.ID = "RX-1002"
	if _, err := SignPrescription(&second, privateKey); err != nil {
		t.Fatalf("sign second: %v", err)
	}
	if err := dispenser.Register(second); err != nil {
		t.Fatalf("register second: %v", err)
	}
	if err := dispenser.Dispense(second); err != nil {
		t.Fatalf("dispense whole signed prescription: %v", err)
	}
}
