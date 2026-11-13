package netsec_test

import (
	"fmt"
	"time"

	netsec "example.org/netsec/q0079"
)

func ExampleCache() {
	cache, err := netsec.NewCache("example.")
	if err != nil {
		panic(err)
	}

	expires := time.Now().Add(time.Hour)
	record := netsec.NSECRecord{
		Owner:            "example.",
		Next:             "www.example.",
		Types:            netsec.TypeBitmap{netsec.TypeNS, netsec.TypeSOA, netsec.TypeNSEC, netsec.TypeRRSIG},
		TTL:              300,
		SignatureExpires: expires,
		ReceiptID:        "validation-42",
		ZoneSerial:       7,
	}
	receipt := netsec.Receipt{
		ID:               "validation-42",
		Zone:             "example.",
		Owner:            "example.",
		Next:             "www.example.",
		Types:            record.Types,
		SignatureExpires: expires,
		ZoneSerial:       7,
	}
	if err := cache.Add(record, receipt); err != nil {
		panic(err)
	}

	answer, err := cache.Query("absent.example.", netsec.TypeA)
	if err != nil {
		panic(err)
	}
	fmt.Println(answer.Status == netsec.NXDomain, len(answer.Proof))
	// Output: true 1
}
