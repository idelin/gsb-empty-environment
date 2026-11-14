package netsec_test

import (
	"encoding/json"
	"fmt"
	"testing"

	netsec "example.org/netsec/q0400"
)

func conditionPolicy(t *testing.T, condition string) *netsec.Policy {
	t.Helper()
	return mustPolicy(t, fmt.Sprintf(`{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/*",
	      "Condition": %s
	    }
	  ]
	}`, condition))
}

func TestConditionOperators(t *testing.T) {
	identity := tenantIdentity("tenant-a")

	tests := []struct {
		name       string
		condition  string
		key        string
		maxKeys    int
		values     netsec.ConditionValues
		attributes map[string][]string
		want       bool
	}{
		{
			name:      "string like",
			condition: `{"StringLike": {"s3:key": "tenant-a/*.txt"}}`,
			key:       "tenant-a/report.txt",
			want:      true,
		},
		{
			name:      "string like miss",
			condition: `{"StringLike": {"s3:key": "tenant-a/*.txt"}}`,
			key:       "tenant-a/report.json",
			want:      false,
		},
		{
			name:      "numeric greater",
			condition: `{"NumericGreaterThan": {"s3:max-keys": 10}}`,
			key:       "tenant-a/a.txt",
			maxKeys:   20,
			want:      true,
		},
		{
			name:      "numeric greater miss",
			condition: `{"NumericGreaterThan": {"s3:max-keys": 10}}`,
			key:       "tenant-a/a.txt",
			maxKeys:   5,
			want:      false,
		},
		{
			name:      "numeric not equals all values",
			condition: `{"NumericNotEquals": {"s3:max-keys": [10, 20]}}`,
			key:       "tenant-a/a.txt",
			maxKeys:   20,
			want:      false,
		},
		{
			name:      "ip address",
			condition: `{"IpAddress": {"aws:SourceIp": "10.0.0.0/8"}}`,
			key:       "tenant-a/a.txt",
			values:    netsec.ConditionValues{"aws:SourceIp": {"10.2.3.4"}},
			want:      true,
		},
		{
			name:      "ip address miss",
			condition: `{"IpAddress": {"aws:SourceIp": "10.0.0.0/8"}}`,
			key:       "tenant-a/a.txt",
			values:    netsec.ConditionValues{"aws:SourceIp": {"192.0.2.1"}},
			want:      false,
		},
		{
			name:      "bool",
			condition: `{"Bool": {"aws:SecureTransport": true}}`,
			key:       "tenant-a/a.txt",
			values:    netsec.ConditionValues{"aws:SecureTransport": {"true"}},
			want:      true,
		},
		{
			name:      "null absent",
			condition: `{"Null": {"aws:TokenIssueTime": true}}`,
			key:       "tenant-a/a.txt",
			want:      true,
		},
		{
			name:      "null present",
			condition: `{"Null": {"aws:TokenIssueTime": true}}`,
			key:       "tenant-a/a.txt",
			values:    netsec.ConditionValues{"aws:TokenIssueTime": {"now"}},
			want:      false,
		},
		{
			name:      "if exists skips missing",
			condition: `{"StringEqualsIfExists": {"aws:TokenIssueTime": "expected"}}`,
			key:       "tenant-a/a.txt",
			want:      true,
		},
		{
			name:      "all values match",
			condition: `{"ForAllValues:StringLike": {"aws:PrincipalTag/team": "eng-*"}}`,
			key:       "tenant-a/a.txt",
			attributes: map[string][]string{
				"aws:PrincipalTag/team": {"eng-a", "eng-b"},
			},
			want: true,
		},
		{
			name:      "all values miss",
			condition: `{"ForAllValues:StringLike": {"aws:PrincipalTag/team": "eng-*"}}`,
			key:       "tenant-a/a.txt",
			attributes: map[string][]string{
				"aws:PrincipalTag/team": {"eng-a", "ops"},
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := conditionPolicy(t, test.condition)
			requestIdentity := identity
			requestIdentity.Attributes = test.attributes
			decision := policy.Authorize(netsec.Request{
				Action:     netsec.ActionGetObject,
				Bucket:     bucket,
				Key:        test.key,
				MaxKeys:    test.maxKeys,
				Identity:   requestIdentity,
				Conditions: test.values,
			})
			if decision.Allowed != test.want {
				t.Fatalf("allowed = %v, want %v (%+v)", decision.Allowed, test.want, decision)
			}
		})
	}
}

func TestNotResourceAndNotAction(t *testing.T) {
	policy := mustPolicy(t, `{
	  "Statement": [
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "Action": "s3:GetObject",
	      "NotResource": "arn:aws:s3:::shared/tenant-a/private/*"
	    },
	    {
	      "Effect": "Allow",
	      "Principal": {"AWS": ["tenant-a"]},
	      "NotAction": "s3:GetObject",
	      "Resource": "arn:aws:s3:::shared/tenant-a/*"
	    }
	  ]
	}`)

	if !policy.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-a/public.txt",
		Identity: tenantIdentity("tenant-a"),
	}).Allowed {
		t.Fatal("NotResource statement did not allow public key")
	}
	if policy.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-a/private/no.txt",
		Identity: tenantIdentity("tenant-a"),
	}).Allowed {
		t.Fatal("NotResource statement allowed excluded key")
	}
}

func TestPolicyJSONRoundTrip(t *testing.T) {
	policy := mustPolicy(t, `{
	  "Version": "2012-10-17",
	  "Statement": {
	    "Effect": "Allow",
	    "Principal": {"AWS": ["tenant-a"]},
	    "Action": "s3:GetObject",
	    "Resource": "arn:aws:s3:::shared/tenant-a/*"
	  }
	}`)
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	roundTripped, err := netsec.NewPolicy(data)
	if err != nil {
		t.Fatalf("round-trip NewPolicy: %v", err)
	}
	if !roundTripped.Authorize(netsec.Request{
		Action:   netsec.ActionGetObject,
		Bucket:   bucket,
		Key:      "tenant-a/a.txt",
		Identity: tenantIdentity("tenant-a"),
	}).Allowed {
		t.Fatal("round-tripped policy lost its allow rule")
	}
}
