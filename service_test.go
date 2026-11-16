package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func freshCompressed(t *testing.T, tenantID string, class Classification, data []byte) []byte {
	t.Helper()
	got, err := NewCompressor().Compress(tenantID, class, data)
	if err != nil {
		t.Fatalf("fresh Compress() error: %v", err)
	}
	return got
}

func mustCompressResult(t *testing.T, svc *Service, tenantID string, class Classification, data []byte) *Result {
	t.Helper()
	result, err := svc.CompressResult(tenantID, class, data)
	if err != nil {
		t.Fatalf("CompressResult() error: %v", err)
	}
	return result
}

func TestOtherScopesDoNotAffectAttackerOutput(t *testing.T) {
	attackerMessage := []byte("attacker can choose every byte of this message")
	want := freshCompressed(t, "attacker", Public, attackerMessage)

	runExperiment := func(secret []byte) []byte {
		svc := NewCompressor()
		mustCompressResult(t, svc, "victim", Confidential, secret)
		mustCompressResult(t, svc, "victim", Public, secret)
		mustCompressResult(t, svc, "attacker", Confidential, secret)
		got, err := svc.Compress("attacker", Public, attackerMessage)
		if err != nil {
			t.Fatalf("Compress() error: %v", err)
		}
		return got
	}

	secretA := bytes.Repeat([]byte("first-other-tenant-secret/"), 64)
	secretB := bytes.Repeat([]byte("a completely different secret/"), 83)
	for name, secret := range map[string][]byte{"secret A": secretA, "secret B": secretB} {
		t.Run(name, func(t *testing.T) {
			got := runExperiment(secret)
			if !bytes.Equal(got, want) {
				t.Fatalf("attacker output changed after other scopes were populated\n got: % x\nwant: % x", got, want)
			}
		})
	}
}

func TestSameTenantReuseIsLegitimate(t *testing.T) {
	svc := NewCompressor()
	secret := []byte("same-tenant-legal-history/")
	first := mustCompressResult(t, svc, "tenant-a", Confidential, secret)
	second := mustCompressResult(t, svc, "tenant-a", Confidential, secret)

	if len(second.Data) >= len(first.Data) {
		t.Fatalf("second stream did not reuse first dictionary: first=%d second=%d", len(first.Data), len(second.Data))
	}
	if len(second.Data) != 4 || second.Data[0] != 1 {
		t.Fatalf("second stream = % x, want one match token", second.Data)
	}
	decoded, err := second.Decode()
	if err != nil {
		t.Fatalf("second Decode() error: %v", err)
	}
	if !bytes.Equal(decoded, secret) {
		t.Fatalf("second decoded = %q, want %q", decoded, secret)
	}
}

func TestDictionaryVisibleOnlyAtSubmit(t *testing.T) {
	svc := NewCompressor()
	secret := []byte("not-visible-until-successful-submission/")
	other := []byte("an unrelated concurrent stream")
	wantBefore := freshCompressed(t, "tenant-a", Public, other)

	uncommitted, err := svc.NewStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatalf("NewStream() error: %v", err)
	}
	if _, err := uncommitted.Write(secret); err != nil {
		t.Fatalf("uncommitted.Write() error: %v", err)
	}

	before, err := svc.NewStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatalf("NewStream(before) error: %v", err)
	}
	if _, err := before.Write(other); err != nil {
		t.Fatalf("before.Write() error: %v", err)
	}
	beforeResult, err := before.Submit()
	if err != nil {
		t.Fatalf("before.Submit() error: %v", err)
	}
	if !bytes.Equal(beforeResult.Data, wantBefore) {
		t.Fatalf("unsubmitted stream leaked into concurrent stream\n got: % x\nwant: % x", beforeResult.Data, wantBefore)
	}

	if _, err := uncommitted.Submit(); err != nil {
		t.Fatalf("uncommitted.Submit() error: %v", err)
	}

	after, err := svc.NewStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatalf("NewStream(after) error: %v", err)
	}
	if _, err := after.Write(secret); err != nil {
		t.Fatalf("after.Write() error: %v", err)
	}
	afterResult, err := after.Submit()
	if err != nil {
		t.Fatalf("after.Submit() error: %v", err)
	}
	if len(afterResult.Data) != 4 || afterResult.Data[0] != 1 {
		t.Fatalf("dictionary was not visible after Submit: % x", afterResult.Data)
	}
}

func TestWriteChunkingDoesNotChangeEncoding(t *testing.T) {
	cases := [][]byte{
		[]byte("abcdXabcYabcd"),
		bytes.Repeat([]byte("overlap-aaaa-"), 300),
	}
	for _, input := range cases {
		encode := func(chunkSize int) *Result {
			svc := NewCompressor()
			stream, err := svc.NewStream(context.Background(), "tenant-a", Public)
			if err != nil {
				t.Fatalf("NewStream() error: %v", err)
			}
			for start := 0; start < len(input); start += chunkSize {
				end := start + chunkSize
				if end > len(input) {
					end = len(input)
				}
				if _, err := stream.Write(input[start:end]); err != nil {
					t.Fatalf("Write() error: %v", err)
				}
			}
			result, err := stream.Submit()
			if err != nil {
				t.Fatalf("Submit() error: %v", err)
			}
			return result
		}

		oneWrite := encode(len(input))
		chunked := encode(7)
		want := Compress(nil, input)
		if !bytes.Equal(oneWrite.Data, want) {
			t.Fatalf("one-write encoding = % x, want % x", oneWrite.Data, want)
		}
		if !bytes.Equal(chunked.Data, want) {
			t.Fatalf("chunked encoding = % x, want % x", chunked.Data, want)
		}
	}
}

func TestCancelAndResetDiscardWorkingDictionary(t *testing.T) {
	svc := NewCompressor()
	secret := []byte("cancelled-stream-secret-must-not-leak/")
	want := freshCompressed(t, "tenant-a", Public, secret)

	cancelled, err := svc.NewStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatalf("NewStream() error: %v", err)
	}
	if _, err := cancelled.Write(secret); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	cancelled.Cancel()
	if _, err := cancelled.Submit(); !errors.Is(err, ErrStreamCancelled) {
		t.Fatalf("Submit() after Cancel error = %v, want ErrStreamCancelled", err)
	}

	got, err := svc.Compress("tenant-a", Public, secret)
	if err != nil {
		t.Fatalf("Compress() after cancel error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cancelled bytes affected later stream\n got: % x\nwant: % x", got, want)
	}

	reset, err := svc.NewStream(context.Background(), "tenant-a", Public)
	if err != nil {
		t.Fatalf("NewStream(reset) error: %v", err)
	}
	if _, err := reset.Write(secret); err != nil {
		t.Fatalf("reset.Write() error: %v", err)
	}
	reset.Reset()
	if _, err := reset.Submit(); !errors.Is(err, ErrStreamCancelled) {
		t.Fatalf("Submit() after Reset error = %v, want ErrStreamCancelled", err)
	}
}

func TestStreamErrorDiscardsWorkingDictionary(t *testing.T) {
	svc := NewCompressor()
	secret := []byte("failed-stream-secret-must-not-leak/")
	want := freshCompressed(t, "tenant-a", Public, secret)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := svc.NewStream(ctx, "tenant-a", Public)
	if err != nil {
		t.Fatalf("NewStream() error: %v", err)
	}
	if _, err := stream.Write(secret); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	cancel()
	if _, err := stream.Submit(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit() after cancellation = %v, want context.Canceled", err)
	}

	got, err := svc.Compress("tenant-a", Public, secret)
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("failed bytes affected later stream\n got: % x\nwant: % x", got, want)
	}
}

func TestTenantIDReuseStartsWithEmptyDictionary(t *testing.T) {
	svc := NewCompressor()
	secret := []byte("old-tenant-secret-that-must-be-cleared/")
	want := freshCompressed(t, "reused-tenant", Public, secret)
	mustCompressResult(t, svc, "reused-tenant", Public, secret)

	stale, err := svc.NewStream(context.Background(), "reused-tenant", Public)
	if err != nil {
		t.Fatalf("NewStream(stale) error: %v", err)
	}
	if _, err := stale.Write([]byte("stale-stream")); err != nil {
		t.Fatalf("stale.Write() error: %v", err)
	}

	if err := svc.ReleaseTenant("reused-tenant"); err != nil {
		t.Fatalf("ReleaseTenant() error: %v", err)
	}
	if _, err := stale.Submit(); !errors.Is(err, ErrStaleDictionary) {
		t.Fatalf("stale Submit() error = %v, want ErrStaleDictionary", err)
	}

	got, err := svc.Compress("reused-tenant", Public, secret)
	if err != nil {
		t.Fatalf("Compress() after tenant reuse error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("old dictionary leaked across tenant reuse\n got: % x\nwant: % x", got, want)
	}
}

func TestResetScopeRejectsOldStream(t *testing.T) {
	svc := NewCompressor()
	secret := []byte("reset-scope-secret/")
	want := freshCompressed(t, "tenant-a", Confidential, secret)
	mustCompressResult(t, svc, "tenant-a", Confidential, secret)

	stale, err := svc.NewStream(context.Background(), "tenant-a", Confidential)
	if err != nil {
		t.Fatalf("NewStream() error: %v", err)
	}
	if _, err := stale.Write(secret); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err := svc.ResetScope("tenant-a", Confidential); err != nil {
		t.Fatalf("ResetScope() error: %v", err)
	}
	if _, err := stale.Submit(); !errors.Is(err, ErrStaleDictionary) {
		t.Fatalf("stale Submit() error = %v, want ErrStaleDictionary", err)
	}

	got, err := svc.Compress("tenant-a", Confidential, secret)
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("reset dictionary affected later stream\n got: % x\nwant: % x", got, want)
	}
}

func TestResultDecodeUsesCapturedSnapshot(t *testing.T) {
	svc := NewCompressor()
	secret := []byte("snapshot-secret/")
	first := mustCompressResult(t, svc, "tenant-a", Public, secret)
	second := mustCompressResult(t, svc, "tenant-a", Public, secret)

	for i := 0; i < 4; i++ {
		mustCompressResult(t, svc, "tenant-a", Public, bytes.Repeat([]byte(fmt.Sprintf("later-%d/", i)), 800))
	}

	decoded, err := svc.DecompressResult(context.Background(), first)
	if err != nil {
		t.Fatalf("DecompressResult(first) error: %v", err)
	}
	if !bytes.Equal(decoded, secret) {
		t.Fatalf("first decoded = %q, want %q", decoded, secret)
	}
	decoded, err = second.Decode()
	if err != nil {
		t.Fatalf("second.Decode() error: %v", err)
	}
	if !bytes.Equal(decoded, secret) {
		t.Fatalf("second decoded = %q, want %q", decoded, secret)
	}
}

func TestConcurrentStreamsAndDecoding(t *testing.T) {
	const streams = 24
	svc := NewCompressor()
	inputs := make([][]byte, streams)
	results := make([]*Result, streams)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		inputs[i] = bytes.Repeat([]byte(fmt.Sprintf("stream-%02d-secret/", i)), 80)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stream, err := svc.NewStream(context.Background(), "tenant-a", Public)
			if err != nil {
				t.Errorf("stream %d NewStream: %v", i, err)
				return
			}
			<-start
			if _, err := stream.Write(inputs[i]); err != nil {
				t.Errorf("stream %d Write: %v", i, err)
				return
			}
			result, err := stream.Submit()
			if err != nil {
				t.Errorf("stream %d Submit: %v", i, err)
				return
			}
			results[i] = result
		}(i)
	}
	close(start)
	wg.Wait()

	var decodeWG sync.WaitGroup
	for i, result := range results {
		if result == nil {
			continue
		}
		decodeWG.Add(1)
		go func(i int, result *Result) {
			defer decodeWG.Done()
			decoded, err := result.Decode()
			if err != nil {
				t.Errorf("stream %d Decode: %v", i, err)
				return
			}
			if !bytes.Equal(decoded, inputs[i]) {
				t.Errorf("stream %d decoded mismatch", i)
			}
		}(i, result)
	}
	decodeWG.Wait()

	if _, revision, err := svc.Dictionary("tenant-a", Public); err != nil {
		t.Fatalf("Dictionary() error: %v", err)
	} else if revision != streams {
		t.Fatalf("revision = %d, want %d", revision, streams)
	}
}

func TestInvalidScope(t *testing.T) {
	svc := NewCompressor()
	if _, err := svc.Compress("", Public, []byte("x")); !errors.Is(err, ErrInvalidTenant) {
		t.Fatalf("empty tenant error = %v, want ErrInvalidTenant", err)
	}
	if _, err := svc.Compress("tenant-a", Classification("secret"), []byte("x")); !errors.Is(err, ErrInvalidClassification) {
		t.Fatalf("invalid classification error = %v, want ErrInvalidClassification", err)
	}
}

func TestZeroValueService(t *testing.T) {
	var svc Service
	result := mustCompressResult(t, &svc, "tenant-a", Public, []byte("zero-value"))
	decoded, err := result.Decode()
	if err != nil {
		t.Fatalf("Decode() error: %v", err)
	}
	if string(decoded) != "zero-value" {
		t.Fatalf("decoded = %q", decoded)
	}
}
