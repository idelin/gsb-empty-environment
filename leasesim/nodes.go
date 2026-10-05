package leasesim

import "fmt"

// ---------------------------------------------------------------------------
// Voter
// ---------------------------------------------------------------------------

// Lease is a granted half-open interval [Start, End) persisted by a voter.
type Lease struct {
	Owner string
	Epoch int
	Start int
	End   int
}

// VoterDisk is the voter's durable state: the maximum epoch ever seen and
// every lease promise not yet known to be irrelevant. It survives restarts.
type VoterDisk struct {
	MaxEpoch int
	Leases   []Lease
}

// Voter grants leases. It has no volatile state besides transport.
type Voter struct {
	node
	id   string
	disk *VoterDisk
}

// conflictingLease returns an existing still-valid lease owned by someone
// other than owner, which overlaps any interval starting at now. Leases are
// half-open and always start at their grant tick, so an existing lease
// overlaps [now, now+dur) iff its End > now.
func (v *Voter) conflictingLease(owner string, now int) *Lease {
	for i := range v.disk.Leases {
		l := &v.disk.Leases[i]
		if l.Owner != owner && l.End > now {
			return l
		}
	}
	return nil
}

// prune drops fully expired leases; their promises no longer constrain
// anything. Remaining promises stay on disk across restarts.
func (v *Voter) prune(now int) {
	kept := v.disk.Leases[:0]
	for _, l := range v.disk.Leases {
		if l.End > now {
			kept = append(kept, l)
		}
	}
	v.disk.Leases = kept
}

func (v *Voter) onGrantReq(s *Sim, e *envelope) {
	resp := &envelope{
		From: v.id, To: e.From, Kind: msgGrantResp,
		CampaignID: e.CampaignID, Epoch: e.Epoch, Owner: e.Owner,
	}
	grant := func() {
		resp.Granted = true
		resp.Start = s.now
		resp.End = s.now + e.Duration
		v.disk.MaxEpoch = e.Epoch
		v.prune(s.now)
		v.disk.Leases = append(v.disk.Leases, Lease{Owner: e.Owner, Epoch: e.Epoch, Start: resp.Start, End: resp.End})
		s.logf("voter %s grants %s epoch=%d lease [%d,%d)", v.id, e.Owner, e.Epoch, resp.Start, resp.End)
	}
	switch {
	case e.Epoch < v.disk.MaxEpoch:
		resp.Reason = fmt.Sprintf("stale epoch %d < persisted max epoch %d", e.Epoch, v.disk.MaxEpoch)
		s.logf("voter %s rejects %s epoch=%d: %s", v.id, e.Owner, e.Epoch, resp.Reason)
	default:
		if c := v.conflictingLease(e.Owner, s.now); c != nil {
			resp.Reason = fmt.Sprintf("conflicting lease held by %s until tick %d", c.Owner, c.End)
			s.logf("voter %s rejects %s epoch=%d: %s", v.id, e.Owner, e.Epoch, resp.Reason)
		} else {
			grant()
		}
	}
	s.send(&v.node, resp)
}

// ---------------------------------------------------------------------------
// Leader (candidate primary)
// ---------------------------------------------------------------------------

// Leader is a candidate primary. All of its state is volatile: a restart
// forces a fresh campaign.
type Leader struct {
	node
	id           string
	campaignID   string
	epoch        int
	grants       []Grant
	cert         *Cert
	certCampaign string
}

func (l *Leader) onGrantResp(s *Sim, e *envelope) {
	if e.CampaignID != l.campaignID {
		s.logf("leader %s ignores stale grant response from %s (old campaign %s)", l.id, e.From, e.CampaignID)
		return
	}
	if !e.Granted {
		s.logf("leader %s: voter %s denied grant: %s", l.id, e.From, e.Reason)
		return
	}
	for _, g := range l.grants {
		if g.Voter == e.From {
			s.logf("leader %s ignores duplicate grant from %s", l.id, e.From)
			return
		}
	}
	l.grants = append(l.grants, Grant{Voter: e.From, Epoch: e.Epoch, Owner: e.Owner, Start: e.Start, End: e.End})
	s.logf("leader %s collects grant from %s (%d/%d) valid [%d,%d)",
		l.id, e.From, len(l.grants), Quorum, e.Start, e.End)
	if len(l.grants) >= Quorum && l.certCampaign != l.campaignID {
		// Authorization deadline: the minimum End of the three grants that
		// formed the quorum. A partially successful renewal never reaches
		// this point, so an existing certificate keeps its old expiry.
		expiry := l.grants[0].End
		for _, g := range l.grants[1:Quorum] {
			if g.End < expiry {
				expiry = g.End
			}
		}
		l.cert = &Cert{
			Epoch:  e.Epoch,
			Owner:  l.id,
			Expiry: expiry,
			Grants: append([]Grant(nil), l.grants[:Quorum]...),
		}
		l.certCampaign = l.campaignID
		s.logf("leader %s certified epoch=%d expiry=%d", l.id, e.Epoch, expiry)
	}
}

func (l *Leader) onWriteResp(s *Sim, e *envelope) {
	if e.OK {
		s.logf("leader %s: write id=%s acked by storage", l.id, e.ReqID)
	} else {
		s.logf("leader %s: write id=%s rejected by storage: %s", l.id, e.ReqID, e.Reason)
	}
}

// ---------------------------------------------------------------------------
// Storage
// ---------------------------------------------------------------------------

// StorageDisk is the storage node's durable state: the fencing token
// (maximum epoch ever seen), the committed write sequence, and the
// idempotency table keyed by request ID.
type StorageDisk struct {
	MaxEpoch int
	Writes   []string
	ByID     map[string]string
}

// Storage is the protected store. All of its state is durable.
type Storage struct {
	node
	disk *StorageDisk
}

// validCert checks structural integrity: at least a quorum of distinct
// voters, all grants matching the certificate's epoch and owner, and Expiry
// equal to the minimum grant End.
func validCert(c *Cert) bool {
	if len(c.Grants) < Quorum {
		return false
	}
	seen := map[string]bool{}
	expiry := c.Grants[0].End
	for _, g := range c.Grants {
		if g.Epoch != c.Epoch || g.Owner != c.Owner || seen[g.Voter] {
			return false
		}
		seen[g.Voter] = true
		if g.End < expiry {
			expiry = g.End
		}
	}
	return c.Expiry == expiry
}

func (st *Storage) onWrite(s *Sim, e *envelope) {
	d := st.disk
	resp := &envelope{From: StorageName, To: e.From, Kind: msgWriteResp, ReqID: e.ReqID}

	// Idempotent retry: a previously committed request ID short-circuits
	// all other checks. Same ID with different content is an error.
	if prev, ok := d.ByID[e.ReqID]; ok {
		if prev == e.Value {
			resp.OK = true
			s.logf("storage dedups retry id=%s value=%q (already committed)", e.ReqID, e.Value)
		} else {
			resp.Reason = fmt.Sprintf("request id %s already committed with different value %q", e.ReqID, prev)
			s.logf("storage rejects id=%s value=%q from %s: %s", e.ReqID, e.Value, e.From, resp.Reason)
		}
		s.send(&st.node, resp)
		return
	}

	switch {
	case e.Cert == nil || !validCert(e.Cert):
		resp.Reason = "invalid certificate"
	case e.Cert.Epoch < d.MaxEpoch:
		resp.Reason = fmt.Sprintf("fenced: cert epoch %d < storage max epoch %d", e.Cert.Epoch, d.MaxEpoch)
	case s.now >= e.Cert.Expiry:
		resp.Reason = fmt.Sprintf("expired certificate: tick %d >= expiry %d", s.now, e.Cert.Expiry)
	default:
		resp.OK = true
	}

	if resp.OK {
		d.Writes = append(d.Writes, e.Value)
		d.ByID[e.ReqID] = e.Value
		if e.Cert.Epoch > d.MaxEpoch {
			d.MaxEpoch = e.Cert.Epoch
		}
		s.logf("storage commits id=%s value=%q (epoch=%d)", e.ReqID, e.Value, e.Cert.Epoch)
	} else {
		s.logf("storage rejects id=%s value=%q from %s: %s", e.ReqID, e.Value, e.From, resp.Reason)
	}
	s.send(&st.node, resp)
}
