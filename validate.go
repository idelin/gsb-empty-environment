package netsec

import "fmt"

func validate(in Input) error {
	if len(in.Organizations) == 0 {
		return fmt.Errorf("%w: no organizations declared", ErrEmptyInput)
	}

	orgs := make(map[string]struct{}, len(in.Organizations))
	for _, org := range in.Organizations {
		if org.ID == "" {
			return fmt.Errorf("%w: organization with empty id", ErrUnknownReference)
		}
		if _, dup := orgs[org.ID]; dup {
			return fmt.Errorf("%w: duplicate organization id %q", ErrUnknownReference, org.ID)
		}
		orgs[org.ID] = struct{}{}
	}

	certs := make(map[string]struct{}, len(in.Certificates))
	for _, cert := range in.Certificates {
		if cert.ID == "" {
			return fmt.Errorf("%w: certificate with empty id", ErrUnknownReference)
		}
		if _, dup := certs[cert.ID]; dup {
			return fmt.Errorf("%w: duplicate certificate id %q", ErrUnknownReference, cert.ID)
		}
		certs[cert.ID] = struct{}{}
		if _, ok := orgs[cert.Holder]; !ok {
			return fmt.Errorf("%w: certificate %q held by unknown organization %q",
				ErrUnknownReference, cert.ID, cert.Holder)
		}
	}

	for _, edge := range in.ControlEdges {
		if _, ok := orgs[edge.From]; !ok {
			return fmt.Errorf("%w: control edge from unknown organization %q",
				ErrUnknownReference, edge.From)
		}
		if _, ok := orgs[edge.To]; !ok {
			return fmt.Errorf("%w: control edge to unknown organization %q",
				ErrUnknownReference, edge.To)
		}
	}

	if len(in.Signatures) == 0 {
		return fmt.Errorf("%w: no witness signatures", ErrEmptyInput)
	}

	seen := make(map[string]struct{}, len(in.Signatures))
	for _, sig := range in.Signatures {
		if sig.SignatureID == "" {
			return fmt.Errorf("%w: signature with empty id", ErrUnknownReference)
		}
		if _, dup := seen[sig.SignatureID]; dup {
			return fmt.Errorf("%w: %s", ErrDuplicateSignature, sig.SignatureID)
		}
		seen[sig.SignatureID] = struct{}{}
		if _, ok := certs[sig.CertificateID]; !ok {
			return fmt.Errorf("%w: signature %q uses unknown certificate %q",
				ErrUnknownReference, sig.SignatureID, sig.CertificateID)
		}
	}

	if in.MaxWitnesses > hardWitnessCap {
		return fmt.Errorf("%w: MaxWitnesses %d exceeds protocol cap %d",
			ErrNoQualifiedSet, in.MaxWitnesses, hardWitnessCap)
	}
	if in.MaxWitnesses < 0 {
		return fmt.Errorf("%w: negative MaxWitnesses", ErrNoQualifiedSet)
	}

	if in.Thresholds.MinWitnesses < 0 ||
		in.Thresholds.MinRegions < 0 ||
		in.Thresholds.MinCategories < 0 {
		return fmt.Errorf("%w: negative threshold value", ErrNoQualifiedSet)
	}
	for pair, want := range in.Thresholds.MinPairs {
		region, category, ok := splitPair(pair)
		if !ok || region == "" || category == "" || want < 0 {
			return fmt.Errorf("%w: malformed pair key %q", ErrUnknownReference, pair)
		}
	}
	return nil
}

func splitPair(pair string) (region, category string, ok bool) {
	for i := 0; i < len(pair); i++ {
		if pair[i] == '|' {
			return pair[:i], pair[i+1:], true
		}
	}
	return "", "", false
}

// indexInputs builds lookup maps for organizations and certificates.
func indexInputs(in Input) (map[string]Organization, map[string]Certificate) {
	orgs := make(map[string]Organization, len(in.Organizations))
	for _, org := range in.Organizations {
		orgs[org.ID] = org
	}
	certs := make(map[string]Certificate, len(in.Certificates))
	for _, cert := range in.Certificates {
		certs[cert.ID] = cert
	}
	return orgs, certs
}
