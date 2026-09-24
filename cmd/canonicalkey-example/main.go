package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	canonicalkey "canonicalkey"
)

func main() {
	if len(os.Args) > 1 {
		log.Fatal("this sample takes JSON on standard input; run with no arguments for built-in samples")
	}
	inputs := [][]byte{
		[]byte(`{"tenant":"tenant-a","parameter_type":"search","config_version":"cfg-3","params":1}`),
		[]byte(`{"tenant":"tenant-a","parameter_type":"search","config_version":"cfg-3","params":"1"}`),
		[]byte(`{"tenant":"tenant-a","parameter_type":"search","config_version":"cfg-4","params":1}`),
	}
	catalog := canonicalkey.NewMemoryCatalog(canonicalkey.DefaultHash)
	clock := func() time.Time { return time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC) }
	service := canonicalkey.NewService(catalog, clock, canonicalkey.DefaultLimits())
	for _, input := range inputs {
		source := canonicalkey.SourceFunc(func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(input)), nil
		})
		result, err := service.Resolve(context.Background(), source)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("input=%s\ncanonical=%s\nkey=%s created=%v\n\n", input, result.Canonical, result.Key, result.Created)
	}
}
