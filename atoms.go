package netsec

import "sort"

// buildAtoms turns signatures into counted witnesses. Signatures that resolve
// to the same (control domain, organization, region, category, certificate)
// equivalence class are certificate aliases: only one atom survives, and the
// lexicographically smallest SignatureID is kept for deterministic output.
func buildAtoms(signatures []Signature, certs map[string]Certificate,
	orgs map[string]Organization, domainOf map[string]string) []Witness {

	atoms := make([]Witness, 0, len(signatures))
	seen := map[string]struct{}{}

	for _, sig := range signatures {
		cert := certs[sig.CertificateID]
		org := orgs[cert.Holder]
		w := Witness{
			SignatureID:   sig.SignatureID,
			CertificateID: cert.ID,
			Organization:  org.ID,
			Region:        org.Region,
			Category:      org.Category,
			ControlDomain: domainOf[org.ID],
		}
		key := w.ControlDomain + "|" + w.Organization + "|" +
			w.Region + "|" + w.Category + "|" + w.CertificateID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		atoms = append(atoms, w)
	}

	sort.Slice(atoms, func(i, j int) bool {
		return atoms[i].SignatureID < atoms[j].SignatureID
	})
	return atoms
}

// domainOption is the single witness a control domain can contribute. Since a
// domain may only ever cast one vote, only the deterministically smallest
// available atom (by SignatureID) is retained per domain.
type domainOption struct {
	domain string
	atom   Witness
}

func groupDomains(atoms []Witness) []domainOption {
	best := map[string]Witness{}
	for _, atom := range atoms {
		cur, ok := best[atom.ControlDomain]
		if !ok || atom.SignatureID < cur.SignatureID {
			best[atom.ControlDomain] = atom
		}
	}

	out := make([]domainOption, 0, len(best))
	for domain, atom := range best {
		out = append(out, domainOption{domain: domain, atom: atom})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].domain != out[j].domain {
			return out[i].domain < out[j].domain
		}
		return out[i].atom.SignatureID < out[j].atom.SignatureID
	})
	return out
}

func pairKey(region, category string) string {
	return region + "|" + category
}
