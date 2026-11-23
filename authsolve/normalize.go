package authsolve

// normalizeProblem folds the compatibility field spellings into the
// canonical fields used by the solver.  It is deliberately a value
// transformation: the caller's Problem is never mutated.
func normalizeProblem(p Problem) Problem {
	if isZeroDigest(p.Artifact) && !isZeroDigest(p.ArtifactDigest) {
		p.Artifact = p.ArtifactDigest
	}
	if len(p.Windows) == 0 {
		p.Windows = p.AllowedWindows
	}
	if len(p.Clock) == 0 {
		p.Clock = p.ClockEvidence
	}
	if p.ReferenceDevice == "" {
		switch {
		case p.RefDevice != "":
			p.ReferenceDevice = p.RefDevice
		case p.Reference != "":
			p.ReferenceDevice = p.Reference
		}
	}
	if len(p.RoleLevels) == 0 && len(p.RoleOrder) > 0 {
		p.RoleLevels = make(map[string]int, len(p.RoleOrder))
		for i, role := range p.RoleOrder {
			p.RoleLevels[role] = len(p.RoleOrder) - i
		}
	}

	p.Credentials = append([]Credential(nil), p.Credentials...)
	for i := range p.Credentials {
		c := &p.Credentials[i]
		if c.ID == "" {
			c.ID = c.CredentialID
		}
		if c.Identity == "" {
			switch {
			case c.PermanentID != "":
				c.Identity = c.PermanentID
			case c.SignerID != "":
				c.Identity = c.SignerID
			case c.Signer != "":
				c.Identity = c.Signer
			}
		}
		if c.Device == "" {
			c.Device = c.DeviceID
		}
		if isZeroDigest(c.Artifact) && !isZeroDigest(c.ArtifactDigest) {
			c.Artifact = c.ArtifactDigest
		}
		if c.NotBefore == 0 && c.ValidFrom != 0 {
			c.NotBefore = c.ValidFrom
		}
		if c.NotAfter == 0 && c.ValidUntil != 0 {
			c.NotAfter = c.ValidUntil
		}
	}
	p.Policy = clonePolicy(p.Policy)
	normalizePolicy(&p.Policy)
	return p
}

func clonePolicy(n PolicyNode) PolicyNode {
	out := n
	if n.Children != nil {
		out.Children = make([]PolicyNode, len(n.Children))
		for i := range n.Children {
			out.Children[i] = clonePolicy(n.Children[i])
		}
	}
	return out
}

func normalizePolicy(n *PolicyNode) {
	if n == nil {
		return
	}
	if n.Role == "" {
		n.Role = n.RequiredRole
	}
	if n.Identity == "" {
		n.Identity = n.PermanentID
	}
	if n.Device == "" {
		n.Device = n.DeviceID
	}
	if n.Credential == "" {
		n.Credential = n.CredentialID
	}
	for i := range n.Children {
		normalizePolicy(&n.Children[i])
	}
}

func isZeroDigest(d Digest) bool {
	for _, b := range d {
		if b != 0 {
			return false
		}
	}
	return true
}
