package taskpkg_test

import (
	"testing"

	taskpkg "example.org/domainreissue/q005"
)

const (
	srcInternal taskpkg.SourceID = "internal.example"
	srcPublic   taskpkg.SourceID = "proxy.public"
	srcAlias    taskpkg.SourceID = "mirror.alias"

	signInternal taskpkg.SignerID = "key-internal"
	signPublic   taskpkg.SignerID = "key-public"
	signReleng   taskpkg.SignerID = "key-releng"

	libPath = "example.net/acme/widget"
)

var rootNode = taskpkg.NodeID{Source: srcInternal, Path: "example.net/acme/app"}

func mustV(t *testing.T, s string) taskpkg.Version {
	t.Helper()
	v, err := taskpkg.ParseVersion(s)
	if err != nil {
		t.Fatalf("parse version %q: %v", s, err)
	}
	return v
}

func mkT(t *testing.T, node taskpkg.NodeID, ver string, signer taskpkg.SignerID, digest taskpkg.Digest) taskpkg.Candidate {
	t.Helper()
	return taskpkg.Candidate{
		Node:    node,
		Version: mustV(t, ver),
		Digest:  digest,
		Proof:   taskpkg.Proof{Signer: signer, Payload: []byte("proof:" + ver)},
	}
}

func verifierFor(cands ...taskpkg.Candidate) taskpkg.MapVerifier {
	proofs := make(map[taskpkg.Digest]taskpkg.SignerID, len(cands))
	for _, c := range cands {
		proofs[c.Digest] = c.Proof.Signer
	}
	return taskpkg.MapVerifier{ValidProofs: proofs}
}

func edge(t *testing.T, from, to taskpkg.NodeID, min, max string, signers ...taskpkg.SignerID) taskpkg.Edge {
	t.Helper()
	return taskpkg.Edge{
		From: from,
		To:   to,
		Constraint: taskpkg.Constraint{
			TrustedSource:  to.Source,
			MinVersion:     mustV(t, min),
			MaxVersion:     mustV(t, max),
			AllowedSigners: signers,
		},
	}
}

func edgeWhen(t *testing.T, from, to taskpkg.NodeID, min, max string, when taskpkg.Condition, signers ...taskpkg.SignerID) taskpkg.Edge {
	t.Helper()
	e := edge(t, from, to, min, max, signers...)
	e.When = when
	return e
}
