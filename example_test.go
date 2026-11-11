package netsec_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"time"

	netsec "example.org/netsec/q0091"
)

// ExampleProofChain shows how layered TLS termination hops build and verify a
// proof chain for a single connection.
func Example() {
	edgeKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	proxyKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	edgeSigner, _ := netsec.NewSigner("edge", "edge-key-1", edgeKey)
	proxySigner, _ := netsec.NewSigner("dmz-proxy", "dmz-key-1", proxyKey)

	// The target service issues a per-connection challenge and is the audience.
	audience := "svc://orders/grpc"
	challenge := "connection-nonce-7f3a"
	genesis := netsec.GenesisDigest(audience, challenge)

	// The first TLS termination point actually verifies the client certificate.
	root, err := edgeSigner.Sign(netsec.Fact{
		HopID:           "edge",
		KeyID:           "edge-key-1",
		VerifiedSubject: "CN=client-a,OU=sales",
		Audience:        audience,
		Challenge:       challenge,
		Assurance:       netsec.AssuranceValidChainAndIdentity,
		UpstreamMin:     netsec.AssuranceNone,
		NotBefore:       time.Now().Add(-time.Minute),
		NotAfter:        time.Now().Add(10 * time.Minute),
	}, 0, genesis)
	if err != nil {
		panic(err)
	}
	chain, err := netsec.NewChain(root)
	if err != nil {
		panic(err)
	}

	// The next hop appends its own fact. Its UpstreamMin is the root's
	// assurance; a weaker local observation lowers the effective level.
	tailDigest, _ := netsec.AttestationDigest(root)
	middle, err := proxySigner.Sign(netsec.Fact{
		HopID:           "dmz-proxy",
		KeyID:           "dmz-key-1",
		VerifiedSubject: "edge",
		Audience:        audience,
		Challenge:       challenge,
		Assurance:       netsec.AssuranceValidChain,
		UpstreamMin:     netsec.AssuranceValidChainAndIdentity,
		NotBefore:       time.Now().Add(-time.Minute),
		NotAfter:        time.Now().Add(10 * time.Minute),
	}, 1, tailDigest)
	if err != nil {
		panic(err)
	}
	chain, err = chain.Append(middle, proxySigner)
	if err != nil {
		panic(err)
	}

	// The target service verifies with its trusted hop key ring.
	ring := netsec.NewKeyRing()
	_ = ring.Add("edge", "edge-key-1", edgeSigner.Alg(), edgeSigner.Public())
	_ = ring.Add("dmz-proxy", "dmz-key-1", proxySigner.Alg(), proxySigner.Public())

	result, err := netsec.Verify(chain, netsec.VerifyConfig{
		KeyRing:           ring,
		ExpectedChallenge: challenge,
		ExpectedAudience:  audience,
		ExpectedClient:    "CN=client-a,OU=sales",
		ExpectedHops:      []string{"edge", "dmz-proxy"},
		MinAssurance:      netsec.AssuranceValidChain,
	})
	if err != nil {
		panic(err)
	}

	raw, _ := chain.Encode()
	fmt.Printf("client=%s firstVerifier=%s hops=%d effective=%s encoded=%t\n",
		result.ClientID, result.FirstVerifier, len(result.Hops),
		result.EffectiveAssurance, len(raw) > 0)
	// Output: client=CN=client-a,OU=sales firstVerifier=edge hops=2 effective=valid-chain encoded=true
}
