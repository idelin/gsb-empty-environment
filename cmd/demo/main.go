// Demo of the hedged fetcher: a cache miss fans out to a slow, stale
// primary and, after the hedge delay, to a fast fresh secondary.
package main

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"time"

	"hedged"
)

func mkResult(s string, ver uint64) hedged.Result {
	b := []byte(s)
	return hedged.Result{Value: b, Version: ver, Checksum: crc32.ChecksumIEEE(b)}
}

func main() {
	// Scenario 1: primary is slow AND stale (version 1 < 2); the hedged
	// secondary wins with a fresh version 2 payload.
	f, err := hedged.New(hedged.Config{
		MinVersion:     2,
		HedgeDelay:     50 * time.Millisecond,
		MaxSpeculative: 1,
		Primary: func(ctx context.Context) (hedged.Result, error) {
			select {
			case <-time.After(200 * time.Millisecond):
				return mkResult("stale-primary-payload", 1), nil
			case <-ctx.Done():
				return hedged.Result{}, ctx.Err()
			}
		},
		Secondary: func(ctx context.Context) (hedged.Result, error) {
			select {
			case <-time.After(30 * time.Millisecond):
				return mkResult("fresh-secondary-payload", 2), nil
			case <-ctx.Done():
				return hedged.Result{}, ctx.Err()
			}
		},
	})
	if err != nil {
		panic(err)
	}
	start := time.Now()
	res, err := f.Fetch(context.Background())
	fmt.Printf("scenario1: value=%q version=%d elapsed=%s err=%v\n",
		res.Value, res.Version, time.Since(start).Round(time.Millisecond), err)
	f.Close()

	// Scenario 2: both sources fail; the error aggregates both reasons.
	f2, err := hedged.New(hedged.Config{
		MinVersion: 1,
		HedgeDelay: 20 * time.Millisecond,
		Primary: func(context.Context) (hedged.Result, error) {
			return hedged.Result{}, errors.New("connection refused")
		},
		Secondary: func(context.Context) (hedged.Result, error) {
			return hedged.Result{}, errors.New("deadline exceeded")
		},
	})
	if err != nil {
		panic(err)
	}
	_, err = f2.Fetch(context.Background())
	fmt.Printf("scenario2: err=%v\n", err)
	f2.Close()
}
