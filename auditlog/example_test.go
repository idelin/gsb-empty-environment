package auditlog_test

import (
	"context"
	"errors"
	"fmt"
	"os"

	"example.org/netsec/uid1279/auditlog"
)

func Example() {
	ctx := context.Background()

	keys, err := auditlog.GenerateKeys()
	if err != nil {
		fmt.Println("key generation failed")
		return
	}

	dir, err := os.MkdirTemp("", "auditlog-example")
	if err != nil {
		fmt.Println("temp dir failed")
		return
	}
	defer os.RemoveAll(dir)

	writer, err := auditlog.Open(ctx, dir, keys, auditlog.WithMaxSegmentRecords(1024))
	if err != nil {
		fmt.Println("open failed")
		return
	}

	seq, err := writer.Append([]byte(`{"event":"login","user":"alice"}`))
	if err != nil {
		fmt.Println("append failed")
		return
	}
	if err := writer.Close(); err != nil {
		fmt.Println("close failed")
		return
	}

	report, err := auditlog.ReadDir(ctx, dir, keys)
	if err != nil {
		var tamperErr *auditlog.TamperError
		if errors.As(err, &tamperErr) {
			fmt.Println("tampering detected:", tamperErr.Kind)
		}
		return
	}

	fmt.Println("appended sequence", seq)
	fmt.Println("verified records", len(report.Records))
	fmt.Println("torn tails", len(report.TornTails))
	// Output:
	// appended sequence 1
	// verified records 1
	// torn tails 0
}
