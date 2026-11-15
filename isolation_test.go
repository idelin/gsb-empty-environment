package netsec

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestOtherTenantSecretsDoNotAffectOutput(t *testing.T) {
	query := bytes.Repeat([]byte("ATTACKER-QUERY-BLOCK/"), 64)

	secretA := append(bytes.Repeat([]byte("ATTACKER-QUERY-BLOCK/"), 64), bytes.Repeat([]byte("VICTIM-PRIVATE-A/"), 64)...)
	secretB := append(bytes.Repeat([]byte("ATTACKER-QUERY-BLOCK/"), 64), bytes.Repeat([]byte("VICTIM-PRIVATE-B/"), 64)...)

	control := NewStore()
	want := mustCompress(t, control, "attacker-control", Confidential, query)

	for _, tc := range []struct {
		name   string
		secret []byte
	}{
		{name: "secret A", secret: secretA},
		{name: "secret B", secret: secretB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			mustCompress(t, s, "victim", Confidential, tc.secret)
			mustCompress(t, s, "victim", Public, tc.secret)
			got := mustCompress(t, s, "attacker-fresh", Confidential, query)
			if !bytes.Equal(got.Data, want.Data) {
				t.Fatalf("attacker output changed with victim secret: got %d bytes, want %d", got.Len(), want.Len())
			}
			mustDecompress(t, s, "attacker-fresh", Confidential, got, query)
		})
	}
}

func TestCrossLabelIsolationWithinTenant(t *testing.T) {
	query := bytes.Repeat([]byte("LABEL-QUERY-BLOCK/"), 64)
	secret := append(bytes.Repeat([]byte("LABEL-QUERY-BLOCK/"), 64), bytes.Repeat([]byte("CONFIDENTIAL-SECRET/"), 64)...)

	s := NewStore()
	want := Compress(nil, query)

	mustCompress(t, s, "tenant-a", Confidential, secret)
	after := mustCompress(t, s, "tenant-a", Public, query)
	if !bytes.Equal(after.Data, want) {
		t.Fatalf("confidential commit affected public output: got %d bytes, want %d", after.Len(), len(want))
	}

	conf := mustCompress(t, s, "tenant-a", Confidential, secret)
	if conf.Version.Offset != uint64(len(secret)) {
		t.Fatalf("confidential offset = %d, want %d", conf.Version.Offset, len(secret))
	}
	if conf.Len() >= after.Len() {
		t.Fatal("same-label reuse should still compress (sanity check)")
	}
}

func TestConcurrentTenantsAndLabelsStayIsolated(t *testing.T) {
	const workers = 32
	s := NewStore()

	// Each unique tenant/label pair starts from an empty dictionary. If any
	// dictionary were shared, concurrent inserts from other workers could
	// change these byte-for-byte outputs.
	msg := bytes.Repeat([]byte("concurrent-isolation-pattern/"), 30)
	want := Compress(nil, msg)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tenant := fmt.Sprintf("tenant-%02d", i)
			label := Public
			if i%2 == 1 {
				label = Confidential
			}
			res, err := s.Compress(context.Background(), tenant, label, msg)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(res.Data, want) {
				errs <- fmt.Errorf("tenant %s/%s saw foreign dictionary bytes", tenant, label)
				return
			}
			got, err := s.Decompress(context.Background(), tenant, label, res.Version, res.Data)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, msg) {
				errs <- fmt.Errorf("tenant %s/%s decoded mismatch", tenant, label)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
