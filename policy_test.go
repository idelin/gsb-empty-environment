package netsec_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	netsec "example.org/netsec/q0400"
)

const bucket = "shared"

func mustPolicy(t *testing.T, document string) *netsec.Policy {
	t.Helper()
	policy, err := netsec.NewPolicy([]byte(document))
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func tenantPolicy(t *testing.T) *netsec.Policy {
	t.Helper()
	return mustPolicy(t, `{
	  "Version": "2012-10-17",
	  "Statement": [
	    {
	      "Sid": "TenantAList",
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:ListBucket",
	      "Resource": "arn:aws:s3:::shared"
	    },
	    {
	      "Sid": "TenantARead",
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/*"
	    },
	    {
	      "Sid": "TenantBList",
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-b"]},
	      "Action": "s3:ListBucket",
	      "Resource": "arn:aws:s3:::shared"
	    },
	    {
	      "Sid": "TenantBRead",
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-b"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-b/*"
	    }
	  ]
	}`)
}

func tenantIdentity(id string) netsec.Identity {
	return netsec.Identity{Type: netsec.IdentityUser, ID: id, Tenant: id}
}

func listKeys(t *testing.T, policy *netsec.Policy, req netsec.ListRequest, source netsec.ObjectSource) []string {
	t.Helper()
	result, err := policy.List(context.Background(), req, source)
	if err != nil {
		t.Fatalf("List(%+v): %v", req, err)
	}
	return result.Keys()
}

func assertKeys(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
}

func testSource() *netsec.MemorySource {
	source := netsec.NewMemorySource()
	for _, key := range []string{
		"public/readme.txt",
		"tenant-a/a.txt",
		"tenant-a/nested/b.txt",
		"tenant-a-other/evil.txt",
		"tenant-b/secret.txt",
	} {
		source.AddKey(bucket, key)
	}
	return source
}

func TestCrossTenantListingReturnsOnlyAuthorizedKeys(t *testing.T) {
	policy := tenantPolicy(t)
	source := testSource()

	got := listKeys(t, policy, netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	assertKeys(t, got, []string{"tenant-a/a.txt", "tenant-a/nested/b.txt"})

	got = listKeys(t, policy, netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-b"),
	}, source)
	assertKeys(t, got, []string{"tenant-b/secret.txt"})

	decision := policy.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-b/secret.txt",
		Identity: tenantIdentity("tenant-a"),
	})
	if decision.Allowed {
		t.Fatal("tenant-a received a cross-tenant GetObject allow")
	}
}

func TestPrefixAndDelimiterDoNotExpandScope(t *testing.T) {
	policy := tenantPolicy(t)
	source := testSource()

	for _, prefix := range []string{
		"tenant-b/",
		"tenant-b",
		"tenant-a/../tenant-b/",
		"tenant-a/../../",
		"tenant-a/%2e%2e/tenant-b/",
		"tenant-a/%252e%252e/tenant-b/",
	} {
		got := listKeys(t, policy, netsec.ListRequest{
			Bucket:   bucket,
			Prefix:   prefix,
			MaxKeys:  100,
			Identity: tenantIdentity("tenant-a"),
		}, source)
		if len(got) != 0 {
			t.Fatalf("prefix %q expanded scope: %v", prefix, got)
		}
	}

	got := listKeys(t, policy, netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "tenant-",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	assertKeys(t, got, []string{"tenant-a/a.txt", "tenant-a/nested/b.txt"})

	result, err := policy.List(context.Background(), netsec.ListRequest{
		Bucket:    bucket,
		Prefix:    "",
		Delimiter: "/",
		MaxKeys:   100,
		Identity:  tenantIdentity("tenant-a"),
	}, source)
	if err != nil {
		t.Fatalf("List with delimiter: %v", err)
	}
	if len(result.Objects) != 0 {
		t.Fatalf("delimiter returned object keys: %v", result.Keys())
	}
	if len(result.CommonPrefixes) != 1 || result.CommonPrefixes[0] != "tenant-a/" {
		t.Fatalf("common prefixes = %v, want [tenant-a/]", result.CommonPrefixes)
	}

	for _, key := range []string{
		"tenant-a/../tenant-b/secret.txt",
		"tenant-a/%2e%2e/tenant-b/secret.txt",
	} {
		if policy.Authorize(netsec.Request{
			Action:   netsec.ActionGetObject,
			Bucket:   bucket,
			Key:      key,
			Identity: tenantIdentity("tenant-a"),
		}).Allowed {
			t.Fatalf("ambiguous key %q was authorized", key)
		}
	}
}

func TestPaginationStaysInsideAuthorizedScope(t *testing.T) {
	policy := tenantPolicy(t)
	source := netsec.NewMemorySource()
	for _, key := range []string{
		"tenant-a/01.txt",
		"tenant-a/02.txt",
		"tenant-a/03.txt",
		"tenant-a/04.txt",
		"tenant-a/05.txt",
		"tenant-b/00.txt",
		"tenant-b/01.txt",
		"tenant-b/02.txt",
		"tenant-b/03.txt",
	} {
		source.AddKey(bucket, key)
	}

	var collected []string
	var firstToken string
	token := ""
	for page := 0; page < 20; page++ {
		result, err := policy.List(context.Background(), netsec.ListRequest{
			Bucket:            bucket,
			Prefix:            "",
			MaxKeys:           2,
			ContinuationToken: token,
			Identity:          tenantIdentity("tenant-a"),
		}, source)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		collected = append(collected, result.Keys()...)
		if page == 0 {
			firstToken = result.NextContinuationToken
		}
		if !result.IsTruncated {
			break
		}
		if result.NextContinuationToken == "" {
			t.Fatal("truncated page has no continuation token")
		}
		token = result.NextContinuationToken
	}
	assertKeys(t, collected, []string{
		"tenant-a/01.txt",
		"tenant-a/02.txt",
		"tenant-a/03.txt",
		"tenant-a/04.txt",
		"tenant-a/05.txt",
	})
	if firstToken == "" {
		t.Fatal("expected a continuation token from the first page")
	}

	_, err := policy.List(context.Background(), netsec.ListRequest{
		Bucket:            bucket,
		Prefix:            "tenant-b/",
		MaxKeys:           2,
		ContinuationToken: firstToken,
		Identity:          tenantIdentity("tenant-a"),
	}, source)
	if !errors.Is(err, netsec.ErrInvalidContinuationToken) {
		t.Fatalf("token reused with another prefix: err = %v", err)
	}

	_, err = policy.List(context.Background(), netsec.ListRequest{
		Bucket:            bucket,
		Prefix:            "",
		MaxKeys:           2,
		ContinuationToken: firstToken,
		Identity:          tenantIdentity("tenant-b"),
	}, source)
	if !errors.Is(err, netsec.ErrInvalidContinuationToken) {
		t.Fatalf("token reused by another principal: err = %v", err)
	}

	tampered := "A" + firstToken[1:]
	if firstToken[0] == 'A' {
		tampered = "B" + firstToken[1:]
	}
	_, err = policy.List(context.Background(), netsec.ListRequest{
		Bucket:            bucket,
		Prefix:            "",
		MaxKeys:           2,
		ContinuationToken: tampered,
		Identity:          tenantIdentity("tenant-a"),
	}, source)
	if !errors.Is(err, netsec.ErrInvalidContinuationToken) {
		t.Fatalf("tampered token: err = %v", err)
	}
}

func TestLegitimateListingAndDelimiterPagination(t *testing.T) {
	policy := tenantPolicy(t)
	source := testSource()

	var prefixes []string
	token := ""
	for page := 0; page < 10; page++ {
		result, err := policy.List(context.Background(), netsec.ListRequest{
			Bucket:            bucket,
			Prefix:            "",
			Delimiter:         "/",
			MaxKeys:           1,
			ContinuationToken: token,
			Identity:          tenantIdentity("tenant-a"),
		}, source)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		prefixes = append(prefixes, result.CommonPrefixes...)
		if !result.IsTruncated {
			break
		}
		token = result.NextContinuationToken
	}
	if len(prefixes) != 1 || prefixes[0] != "tenant-a/" {
		t.Fatalf("common prefixes = %v, want [tenant-a/]", prefixes)
	}
}

func TestPolicyChangeRejectsOldTokensAndReturnsNewScope(t *testing.T) {
	manager, err := netsec.NewManager([]byte(`{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": ["s3:ListBucket", "s3:GetObject"],
	      "Resource": ["arn:aws:s3:::shared", "arn:aws:s3:::shared/tenant-a/*"]
	    }
	  ]
	}`))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	source := netsec.NewMemorySource()
	source.AddKey(bucket, "tenant-a/keep.txt")
	source.AddKey(bucket, "tenant-a/revoked.txt")

	first, err := manager.List(context.Background(), netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "tenant-a/",
		MaxKeys:  1,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	if err != nil {
		t.Fatalf("initial List: %v", err)
	}
	assertKeys(t, first.Keys(), []string{"tenant-a/keep.txt"})
	if first.NextContinuationToken == "" {
		t.Fatal("expected an initial continuation token")
	}
	oldFingerprint := manager.Fingerprint()

	if err := manager.SetPolicy([]byte(`{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:ListBucket",
	      "Resource": "arn:aws:s3:::shared"
	    },
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/keep.txt"
	    }
	  ]
	}`)); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if manager.Fingerprint() == oldFingerprint {
		t.Fatal("policy fingerprint did not change")
	}

	_, err = manager.List(context.Background(), netsec.ListRequest{
		Bucket:            bucket,
		Prefix:            "tenant-a/",
		MaxKeys:           1,
		ContinuationToken: first.NextContinuationToken,
		Identity:          tenantIdentity("tenant-a"),
	}, source)
	if !errors.Is(err, netsec.ErrInvalidContinuationToken) {
		t.Fatalf("old token after policy change: err = %v", err)
	}

	result, err := manager.List(context.Background(), netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "tenant-a/",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	if err != nil {
		t.Fatalf("List after policy change: %v", err)
	}
	assertKeys(t, result.Keys(), []string{"tenant-a/keep.txt"})

	if err := manager.SetPolicy([]byte(`{"Statement":`)); err == nil {
		t.Fatal("invalid policy replacement unexpectedly succeeded")
	}
	if manager.Fingerprint() == "" {
		t.Fatal("failed policy replacement removed the active policy")
	}
}

func TestAnonymousAndTemporaryCredentialsAreLeastPrivilege(t *testing.T) {
	wildcard := mustPolicy(t, `{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": "*"},
	      "Action": ["s3:ListBucket", "s3:GetObject"],
	      "Resource": ["arn:aws:s3:::shared", "arn:aws:s3:::shared/*"]
	    }
	  ]
	}`)
	source := testSource()

	if decision := wildcard.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "public/readme.txt",
		Identity: netsec.Anonymous(),
	}); decision.Allowed {
		t.Fatal("anonymous caller matched an authenticated AWS wildcard")
	}
	_, err := wildcard.List(context.Background(), netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: netsec.Anonymous(),
	}, source)
	if !errors.Is(err, netsec.ErrAccessDenied) {
		t.Fatalf("anonymous List: err = %v", err)
	}

	public := mustPolicy(t, `{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"Anonymous": "*"},
	      "Action": "s3:ListBucket",
	      "Resource": "arn:aws:s3:::shared"
	    },
	    {
	      "Effect": "Allow",
	      "Principal": {"Anonymous": "*"},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/public/*"
	    }
	  ]
	}`)
	got := listKeys(t, public, netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: netsec.Anonymous(),
	}, source)
	assertKeys(t, got, []string{"public/readme.txt"})

	session := netsec.TemporaryIdentity("tenant-a", "tenant-a", "session-1", time.Now().Add(time.Hour))
	if decision := wildcard.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-a/a.txt",
		Identity: session,
	}); decision.Allowed {
		t.Fatal("temporary credential inherited an AWS wildcard grant")
	}

	sessionPolicy := mustPolicy(t, `{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"Temporary": ["session-1"]},
	      "Action": "s3:ListBucket",
	      "Resource": "arn:aws:s3:::shared"
	    },
	    {
	      "Effect": "Allow",
	      "Principal": {"Temporary": ["session-1"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/*"
	    }
	  ]
	}`)
	got = listKeys(t, sessionPolicy, netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: session,
	}, source)
	assertKeys(t, got, []string{"tenant-a/a.txt", "tenant-a/nested/b.txt"})

	expired := netsec.TemporaryIdentity("tenant-a", "tenant-a", "session-1", time.Now().Add(-time.Minute))
	if decision := sessionPolicy.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-a/a.txt",
		Identity: expired,
	}); decision.Allowed {
		t.Fatal("expired temporary credential was allowed")
	}
}

func TestConditionsAndExplicitDeny(t *testing.T) {
	policy := mustPolicy(t, `{
	  "Statement": [
	    {
	      "Sid": "TenantAList",
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:ListBucket",
	      "Resource": "arn:aws:s3:::shared",
	      "Condition": {"StringLike": {"s3:prefix": ["tenant-a/*"]}}
	    },
	    {
	      "Sid": "TenantARead",
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/*",
	      "Condition": {"StringEquals": {"aws:PrincipalTenant": "tenant-a"}}
	    },
	    {
	      "Sid": "DenyPrivate",
	      "Effect": "Deny",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/private/*"
	    }
	  ]
	}`)
	source := netsec.NewMemorySource()
	for _, key := range []string{
		"tenant-a/ok.txt",
		"tenant-a/private/no.txt",
		"tenant-b/no.txt",
	} {
		source.AddKey(bucket, key)
	}

	got := listKeys(t, policy, netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "tenant-a/",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	assertKeys(t, got, []string{"tenant-a/ok.txt"})

	_, err := policy.List(context.Background(), netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	if !errors.Is(err, netsec.ErrAccessDenied) {
		t.Fatalf("list with an unauthorized prefix: err = %v", err)
	}

	decision := policy.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-a/private/no.txt",
		Identity: tenantIdentity("tenant-a"),
	})
	if decision.Allowed || decision.Sid != "DenyPrivate" {
		t.Fatalf("explicit deny did not win: %+v", decision)
	}
}

func TestPolicyParsingForms(t *testing.T) {
	policy := mustPolicy(t, `{
	  "Version": "2012-10-17",
	  "Statement": {
	    "Effect": "Allow",
	    "Principal": "tenant-a",
	    "Action": "GetObject",
	    "Resource": "s3://shared/tenant-a/*",
	    "Condition": {"StringEquals": {"aws:PrincipalTenant": "tenant-a"}}
	  }
	}`)
	if !policy.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-a/file.txt",
		Identity: tenantIdentity("tenant-a"),
	}).Allowed {
		t.Fatal("simple policy form did not authorize the tenant key")
	}
	if policy.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-b/file.txt",
		Identity: tenantIdentity("tenant-a"),
	}).Allowed {
		t.Fatal("simple policy form authorized a cross-tenant key")
	}
}

func TestInvalidPoliciesAreRejected(t *testing.T) {
	tests := []string{
		`{`,
		`{"Statement": [{"Effect": "Allow", "Principal": {"AWS": "*"}, "Action": "s3:GetObject"}]}`,
		`{"Statement": [{"Effect": "Maybe", "Principal": {"AWS": "*"}, "Action": "s3:GetObject", "Resource": "*"}]}`,
		`{"Statement": [{"Effect": "Allow", "Principal": {}, "Action": "s3:GetObject", "Resource": "*"}]}`,
		`{"Statement": [{"Effect": "Allow", "Principal": {"AWS": "*"}, "Action": "s3:GetObject", "Resource": "arn:aws:sqs:us-east-1:1:q"}]}`,
		`{"Statement": [{"Effect": "Allow", "Principal": {"AWS": "*"}, "Action": "s3:GetObject", "Resource": "*", "Condition": {"Bogus": {"s3:prefix": "x"}}}]}`,
	}
	for _, document := range tests {
		if _, err := netsec.NewPolicy([]byte(document)); err == nil {
			t.Fatalf("invalid policy accepted: %s", document)
		}
	}
}

func TestListRequiresBothListAndRead(t *testing.T) {
	onlyList := mustPolicy(t, `{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:ListBucket",
	      "Resource": "arn:aws:s3:::shared"
	    }
	  ]
	}`)
	source := testSource()
	got := listKeys(t, onlyList, netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	if len(got) != 0 {
		t.Fatalf("ListBucket without GetObject leaked names: %v", got)
	}

	onlyRead := mustPolicy(t, `{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/*"
	    }
	  ]
	}`)
	_, err := onlyRead.List(context.Background(), netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	if !errors.Is(err, netsec.ErrAccessDenied) {
		t.Fatalf("GetObject without ListBucket allowed listing: %v", err)
	}
}

func TestSourceContractViolationIsRejected(t *testing.T) {
	policy := tenantPolicy(t)
	source := &badSource{objects: []netsec.Object{
		{Key: "tenant-a/z.txt"},
		{Key: "tenant-a/a.txt"},
	}}
	_, err := policy.List(context.Background(), netsec.ListRequest{
		Bucket:   bucket,
		Prefix:   "",
		MaxKeys:  100,
		Identity: tenantIdentity("tenant-a"),
	}, source)
	if !errors.Is(err, netsec.ErrSourceContract) {
		t.Fatalf("unordered source: err = %v", err)
	}
}

type badSource struct {
	objects []netsec.Object
}

func (s *badSource) ListObjects(ctx context.Context, bucket, prefix, after string, limit int) ([]netsec.Object, error) {
	out := make([]netsec.Object, 0, len(s.objects))
	for _, object := range s.objects {
		if strings.HasPrefix(object.Key, prefix) && object.Key > after {
			out = append(out, object)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
