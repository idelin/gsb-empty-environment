// rcvd is the access-control receiver firmware service. It reads
// hex-encoded rolling-code frames (one per line) from stdin and prints a
// verdict per line. Unlock events go to stdout with their idempotent
// operation number.
//
// Usage:
//
//	rcvd <wal-path>
//
// Environment:
//
//	RCVD_KEYS  comma-separated "<deviceIDhex>=<keyhex>" entries
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"example.org/offlinecli/receiver"
)

func loadKeys() (receiver.MapKeyStore, error) {
	keys := receiver.MapKeyStore{}
	env := os.Getenv("RCVD_KEYS")
	if env == "" {
		return nil, fmt.Errorf("RCVD_KEYS is not set")
	}
	for _, entry := range strings.Split(env, ",") {
		idHex, keyHex, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("bad RCVD_KEYS entry %q", entry)
		}
		id, err := strconv.ParseUint(idHex, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("bad device id %q: %w", idHex, err)
		}
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, fmt.Errorf("bad key for %q: %w", idHex, err)
		}
		keys[id] = key
	}
	return keys, nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: rcvd <wal-path>")
		os.Exit(2)
	}
	keys, err := loadKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rcvd:", err)
		os.Exit(2)
	}
	unlock := func(_ context.Context, op receiver.UnlockOp) error {
		fmt.Printf("UNLOCK op=%s device=%016x counter=%d\n", op.OpID, op.DeviceID, op.Counter)
		return nil
	}
	r, err := receiver.Open(context.Background(), os.Args[1], keys, unlock, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rcvd:", err)
		os.Exit(1)
	}
	defer r.Close()

	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		raw, err := hex.DecodeString(line)
		if err != nil {
			fmt.Println("BAD_FRAME")
			continue
		}
		frame, err := receiver.ParseFrame(raw)
		if err != nil {
			fmt.Println("BAD_FRAME")
			continue
		}
		res, err := r.Submit(context.Background(), frame)
		if err != nil {
			fmt.Println("ERROR:", err)
			continue
		}
		fmt.Println(res)
	}
}
