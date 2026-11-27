package zipguard_test

import (
	"errors"
	"fmt"

	"formal.local/w28-539424a87d/zipguard"
)

func ExampleVerifyBytes() {
	archive := []byte("not-an-archive")
	_, err := zipguard.VerifyBytes(archive, zipguard.DefaultPolicy())
	switch {
	case errors.Is(err, zipguard.ErrEOCDNotFound):
		fmt.Println("rejected: no EOCD")
	case err != nil:
		fmt.Println("rejected:", err)
	default:
		fmt.Println("accepted")
	}
	// Output:
	// rejected: no EOCD
}
