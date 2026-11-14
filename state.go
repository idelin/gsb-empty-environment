package netsec

import "crypto/x509"

type bundleState struct {
	generation Generation
	roots      []*x509.Certificate
}

type policyState struct {
	generation  Generation
	initialized bool
	policies    map[string]compiledConnectionPolicy
}

type state struct {
	bundles     map[TrustDomain]bundleState
	revocations map[TrustDomain]revocationState
	rotations   rotationState
	policies    policyState
}

func newState() *state {
	return &state{
		bundles:     make(map[TrustDomain]bundleState),
		revocations: make(map[TrustDomain]revocationState),
		rotations: rotationState{
			records: make(map[SPIFFEID]RotationRecord),
		},
		policies: policyState{
			policies: make(map[string]compiledConnectionPolicy),
		},
	}
}

func (s *state) clone() *state {
	out := &state{
		bundles:     make(map[TrustDomain]bundleState, len(s.bundles)),
		revocations: make(map[TrustDomain]revocationState, len(s.revocations)),
		rotations: rotationState{
			generation:  s.rotations.generation,
			initialized: s.rotations.initialized,
			records:     make(map[SPIFFEID]RotationRecord, len(s.rotations.records)),
		},
		policies: policyState{
			generation:  s.policies.generation,
			initialized: s.policies.initialized,
			policies:    make(map[string]compiledConnectionPolicy, len(s.policies.policies)),
		},
	}
	for td, bundle := range s.bundles {
		bundle.roots = cloneCertificates(bundle.roots)
		out.bundles[td] = bundle
	}
	for td, revocations := range s.revocations {
		entries := make(map[revocationKey]Revocation, len(revocations.entries))
		for key, entry := range revocations.entries {
			entry.Issuer = append([]byte(nil), entry.Issuer...)
			entries[key] = entry
		}
		out.revocations[td] = revocationState{generation: revocations.generation, entries: entries}
	}
	for workload, record := range s.rotations.records {
		active := make([]CertificateID, len(record.Active))
		for i, id := range record.Active {
			id.Issuer = append([]byte(nil), id.Issuer...)
			active[i] = id
		}
		record.Active = active
		out.rotations.records[workload] = record
	}
	for name, policy := range s.policies.policies {
		out.policies.policies[name] = policy
	}
	return out
}

func cloneCertificates(in []*x509.Certificate) []*x509.Certificate {
	out := make([]*x509.Certificate, len(in))
	for i, cert := range in {
		if cert != nil {
			out[i] = cloneCertificate(cert)
		}
	}
	return out
}

func cloneCertificate(cert *x509.Certificate) *x509.Certificate {
	if cert == nil {
		return nil
	}
	if len(cert.Raw) != 0 {
		if parsed, err := x509.ParseCertificate(cert.Raw); err == nil {
			return parsed
		}
	}
	cloned := *cert
	cloned.Raw = append([]byte(nil), cert.Raw...)
	cloned.RawIssuer = append([]byte(nil), cert.RawIssuer...)
	cloned.RawSubject = append([]byte(nil), cert.RawSubject...)
	return &cloned
}

// Generations reports the versions that were active when a decision was made.
type Generations struct {
	Bundles     map[TrustDomain]Generation
	Revocations map[TrustDomain]Generation
	Rotation    Generation
	Policies    Generation
}

func (s *state) generations() Generations {
	out := Generations{
		Bundles:     make(map[TrustDomain]Generation, len(s.bundles)),
		Revocations: make(map[TrustDomain]Generation, len(s.revocations)),
		Rotation:    s.rotations.generation,
		Policies:    s.policies.generation,
	}
	for td, bundle := range s.bundles {
		out.Bundles[td] = bundle.generation
	}
	for td, revocations := range s.revocations {
		out.Revocations[td] = revocations.generation
	}
	return out
}
