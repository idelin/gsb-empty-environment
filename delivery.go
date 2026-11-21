package roomdelivery

import "strings"

func (h *Host) isRetractedLocked(receiptID ReceiptID) bool {
	for i := len(h.events) - 1; i >= 0; i-- {
		event := h.events[i]
		if event.Kind != EventRetracted {
			continue
		}
		for _, id := range event.ReceiptIDs {
			if id == receiptID {
				return true
			}
		}
	}
	return false
}

func (h *Host) latestCorrectionLocked(receipt Receipt) (Event, bool) {
	var latest Event
	found := false
	for _, event := range h.events {
		if event.Kind != EventCorrected || event.Member != receipt.Author {
			continue
		}
		for _, id := range event.ReceiptIDs {
			if id != receipt.ID {
				continue
			}
			if !found || event.Seq > latest.Seq {
				latest = event
				found = true
			}
			break
		}
	}
	return latest, found
}

func (h *Host) messageEventLocked(receipt Receipt) (Event, bool) {
	if receipt.MessageEventSeq != 0 {
		for _, event := range h.events {
			if event.Seq == receipt.MessageEventSeq && event.Kind == EventMessage {
				return event, true
			}
		}
		return Event{}, false
	}
	for _, event := range h.events {
		if event.Kind != EventMessage {
			continue
		}
		for _, id := range event.ReceiptIDs {
			if id == receipt.ID {
				return event, true
			}
		}
	}
	return Event{}, false
}

func (h *Host) disclosedGrantsLocked(receipt *Receipt) map[IdentityKey]map[RevealID]struct{} {
	grants := make(map[IdentityKey]map[RevealID]struct{})
	h.addDisclosedGrantsLocked(grants, receipt, make(map[ReceiptID]bool))
	return grants
}

func (h *Host) addDisclosedGrantsLocked(grants map[IdentityKey]map[RevealID]struct{}, receipt *Receipt, seen map[ReceiptID]bool) {
	if receipt == nil || seen[receipt.ID] {
		return
	}
	seen[receipt.ID] = true
	defer delete(seen, receipt.ID)

	h.addVisibleMemberLocked(grants, receipt.Author, receipt.Policy)
	root, ok := h.messageEventLocked(*receipt)
	if !ok {
		return
	}
	h.addBodyKeysLocked(grants, root.Body, receipt.Policy)
	h.addFailureKeysLocked(grants, root.Failure, receipt.Policy)
	if latest, found := h.latestCorrectionLocked(*receipt); found {
		h.addBodyKeysLocked(grants, latest.Body, receipt.Policy)
	}
	if receipt.QuoteReceipt == "" {
		return
	}
	quoted := h.receipts[receipt.QuoteReceipt]
	if quoted == nil || quoted.Target != receipt.Target || quoted.Status != ReceiptPublished {
		return
	}
	h.addVisibleMemberLocked(grants, quoted.Author, quoted.Policy)
	quotedRoot, ok := h.messageEventLocked(*quoted)
	if !ok {
		return
	}
	h.addBodyKeysLocked(grants, quotedRoot.Body, quoted.Policy)
	h.addFailureKeysLocked(grants, quotedRoot.Failure, quoted.Policy)
	if latest, found := h.latestCorrectionLocked(*quoted); found {
		h.addBodyKeysLocked(grants, latest.Body, quoted.Policy)
	}
	h.addDisclosedGrantsLocked(grants, h.receipts[quoted.QuoteReceipt], seen)
}

func (h *Host) addVisibleMemberLocked(grants map[IdentityKey]map[RevealID]struct{}, ref MembershipRef, policy IdentityPolicy) {
	if !policy.AllowsKey(ref.identityKey()) {
		return
	}
	member := h.members[ref.Instance]
	if member == nil || member.realJID == "" {
		return
	}
	addPolicyIdentity(grants, policy, ref.identityKey())
}

func (h *Host) addBodyKeysLocked(grants map[IdentityKey]map[RevealID]struct{}, body Body, policy IdentityPolicy) {
	h.addTextKeysLocked(grants, body.Text, policy)
	for _, attribute := range body.Attributes {
		if attribute.Identity != nil {
			if member, key, ok := h.resolveIdentityRefLocked(*attribute.Identity); ok &&
				policy.AllowsKey(key) && member.realJID != "" {
				addPolicyIdentity(grants, policy, key)
			}
		}
		h.addTextKeysLocked(grants, attribute.Value, policy)
	}
}

func (h *Host) addFailureKeysLocked(grants map[IdentityKey]map[RevealID]struct{}, failure *Failure, policy IdentityPolicy) {
	if failure == nil {
		return
	}
	h.addTextKeysLocked(grants, failure.Text, policy)
	if failure.Subject == nil {
		return
	}
	if member, key, ok := h.resolveIdentityRefLocked(*failure.Subject); ok &&
		policy.AllowsKey(key) && member.realJID != "" {
		addPolicyIdentity(grants, policy, key)
	}
}

func (h *Host) addTextKeysLocked(grants map[IdentityKey]map[RevealID]struct{}, text string, policy IdentityPolicy) {
	if text == "" {
		return
	}
	for _, member := range h.members {
		if member.realJID == "" || !policy.AllowsKey(member.ref.identityKey()) {
			continue
		}
		if strings.Contains(text, string(member.realJID)) {
			addPolicyIdentity(grants, policy, member.ref.identityKey())
		}
	}
}

func addPolicyIdentity(grants map[IdentityKey]map[RevealID]struct{}, policy IdentityPolicy, key IdentityKey) {
	revealIDs := policy.RevealIDs(key)
	if len(revealIDs) == 0 {
		return
	}
	if grants[key] == nil {
		grants[key] = make(map[RevealID]struct{}, len(revealIDs))
	}
	for _, id := range revealIDs {
		grants[key][id] = struct{}{}
	}
}

func (h *Host) memberByKeyLocked(key IdentityKey) *memberState {
	for _, member := range h.members {
		if member.ref.identityKey() == key {
			return member
		}
	}
	return nil
}

func (h *Host) resolveIdentityRefLocked(ref IdentityRef) (*memberState, IdentityKey, bool) {
	if ref.Instance != "" {
		if member, ok := h.members[ref.Instance]; ok {
			return member, member.ref.identityKey(), true
		}
	}
	if ref.Principal != "" && ref.Generation != 0 {
		key := ref.Key()
		if member := h.memberByKeyLocked(key); member != nil {
			return member, key, true
		}
	}
	return nil, IdentityKey{}, false
}
