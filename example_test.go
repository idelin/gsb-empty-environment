package netsec_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"

	netsec "example.org/netsec/q0459"
)

func ExampleDispenser() {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)

	prescription := netsec.Prescription{
		ID:          "RX-2001",
		PatientID:   "PATIENT-A",
		PhysicianID: "DR-7",
		Diagnosis:   "J18.9",
		DrugCode:    "MED-ASPIRIN-100",
		Dosage:      100,
		Unit:        "mg",
	}
	if err := prescription.Sign(privateKey); err != nil {
		panic(err)
	}

	dispenser := netsec.NewDispenser(publicKey)
	if err := dispenser.Register(prescription); err != nil {
		panic(err)
	}
	if err := dispenser.Dispense(netsec.DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      prescription.PatientID,
		DrugCode:       prescription.DrugCode,
		Dosage:         prescription.Dosage,
	}); err != nil {
		panic(err)
	}

	fmt.Println(dispenser.IsDispensed(prescription.ID))
	err := dispenser.Dispense(netsec.DispenseRequest{
		PrescriptionID: prescription.ID,
		PatientID:      prescription.PatientID,
	})
	fmt.Println(err != nil)

	// Output:
	// true
	// true
}
