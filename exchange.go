package netsec

import (
	"crypto/subtle"
	"math/big"
)

// HandleNegotiation processes the first AUTH_ELS of an exchange: it
// negotiates hash and DH group under the local policy, validates the peer DH
// public value against the small-subgroup range [2, p-2], records the peer
// challenge and builds the reply carrying the local public value and
// challenge. Any failure terminates the transaction.
func (t *Transaction) HandleNegotiation(msg AuthMessage) (AuthMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == StateFailed {
		return AuthMessage{}, ErrTransactionFailed
	}
	if t.state != StateInit {
		t.fail()
		return AuthMessage{}, ErrInvalidMessage
	}
	if msg.Command != CmdAuthET || msg.Status != StatusInProgress {
		t.fail()
		return AuthMessage{}, ErrInvalidMessage
	}

	proto, err := requireOnce(msg, ParamAuthProto)
	if err != nil || len(proto.Value) != 1 || proto.Value[0] != ProtoDHCHAP {
		t.fail()
		return AuthMessage{}, ErrInvalidMessage
	}

	hashTLV, err := requireOnce(msg, ParamHashFunction)
	if err != nil {
		t.fail()
		return AuthMessage{}, err
	}
	groupTLV, err := requireOnce(msg, ParamDHGroup)
	if err != nil {
		t.fail()
		return AuthMessage{}, err
	}

	// Negotiation failures are terminal and must never degrade to plain CHAP.
	selectedHash, err := NegotiateHash(t.policy, bytesToHashIDs(hashTLV.Value))
	if err != nil {
		t.fail()
		return AuthMessage{}, err
	}
	selectedGroup, err := NegotiateDHGroup(t.policy, bytesToGroupIDs(groupTLV.Value))
	if err != nil {
		t.fail()
		return AuthMessage{}, err
	}

	challengeTLV, err := requireOnce(msg, ParamChallenge)
	if err != nil {
		t.fail()
		return AuthMessage{}, err
	}
	if err := t.observeChallenge(challengeTLV.Value); err != nil {
		t.fail()
		return AuthMessage{}, err
	}
	t.peerChallenge = append([]byte(nil), challengeTLV.Value...)

	if id, ok := msg.Param(ParamCHAPIdentifier); ok {
		if msg.count(ParamCHAPIdentifier) > 1 || len(id.Value) == 0 {
			t.fail()
			return AuthMessage{}, ErrInvalidMessage
		}
		t.peerChapID = append([]byte(nil), id.Value...)
	}
	if name, ok := msg.Param(ParamLocalName); ok {
		t.peerName = string(name.Value)
	}

	reply := AuthMessage{Command: CmdAuthET, Status: StatusInProgress, Proto: ProtoDHCHAP}
	reply.Params = append(reply.Params,
		Param{Type: ParamHashFunction, Value: []byte{byte(selectedHash)}},
		Param{Type: ParamDHGroup, Value: []byte{byte(selectedGroup)}},
	)

	if selectedGroup == DHGroupNull {
		if err := requireAbsent(msg, ParamDHPublicValue); err != nil {
			t.fail()
			return AuthMessage{}, err
		}
		t.nullDH = true
	} else {
		pubTLV, err := requireOnce(msg, ParamDHPublicValue)
		if err != nil {
			t.fail()
			return AuthMessage{}, err
		}
		params, err := Group(selectedGroup)
		if err != nil {
			t.fail()
			return AuthMessage{}, err
		}
		peerY := new(big.Int).SetBytes(pubTLV.Value)
		if err := params.ValidatePublicValue(peerY); err != nil {
			t.fail()
			return AuthMessage{}, err
		}
		keys, err := params.GenerateKeyPair()
		if err != nil {
			t.fail()
			return AuthMessage{}, err
		}
		shared, err := params.ComputeSharedSecret(keys.Private, peerY)
		if err != nil {
			t.fail()
			return AuthMessage{}, err
		}
		t.group = params
		t.localKeys = keys
		t.peerPublic = peerY
		t.rawShared = shared
		reply.Params = append(reply.Params, Param{
			Type:  ParamDHPublicValue,
			Value: append([]byte(nil), keys.Public.Bytes()...),
		})
	}

	localChallenge, err := t.generateChallengeLocked()
	if err != nil {
		t.fail()
		return AuthMessage{}, err
	}
	t.localChallenge = localChallenge
	t.localChapID = []byte{randomByte()}

	reply.Params = append(reply.Params,
		Param{Type: ParamChallenge, Value: t.localChallenge},
		Param{Type: ParamCHAPIdentifier, Value: t.localChapID},
	)
	if t.localName != "" {
		reply.Params = append(reply.Params, Param{
			Type:  ParamLocalName,
			Value: []byte(t.localName),
		})
	}

	t.hashID = selectedHash
	t.groupID = selectedGroup
	t.state = StateNegotiated
	return reply, nil
}

// HandleReply processes the third AUTH_ELS message carrying the initiator's
// CHAP response. It validates the exchange state but does not itself know the
// pre-shared secret; call VerifyPeerCHAPResponse afterwards.
func (t *Transaction) HandleReply(msg AuthMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == StateFailed {
		return ErrTransactionFailed
	}
	if t.state != StateNegotiated {
		t.fail()
		return ErrInvalidMessage
	}
	if msg.Command != CmdAuthET ||
		(msg.Status != StatusInProgress && msg.Status != StatusSuccess) {
		t.fail()
		return ErrInvalidMessage
	}
	// A second DH value or fresh negotiation TLV would amount to re-running
	// the unauthenticated negotiation; reject it.
	if err := requireAbsent(msg, ParamDHPublicValue); err != nil {
		t.fail()
		return err
	}
	if err := requireAbsent(msg, ParamHashFunction); err != nil {
		t.fail()
		return err
	}
	if err := requireAbsent(msg, ParamDHGroup); err != nil {
		t.fail()
		return err
	}

	if ch, ok := msg.Param(ParamChallenge); ok {
		if msg.count(ParamChallenge) > 1 {
			t.fail()
			return ErrInvalidMessage
		}
		if err := t.observeChallenge(ch.Value); err != nil {
			t.fail()
			return err
		}
	}

	resp, err := requireOnce(msg, ParamCHAPResponse)
	if err != nil {
		t.fail()
		return err
	}
	if name, ok := msg.Param(ParamLocalName); ok {
		t.peerName = string(name.Value)
	}
	t.peerResponse = append([]byte(nil), resp.Value...)
	return nil
}

// VerifyPeerCHAPResponse checks the stored peer response against the local
// challenge and CHAP identifier. A mismatch terminates the transaction.
func (t *Transaction) VerifyPeerCHAPResponse(secret []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == StateFailed {
		return ErrTransactionFailed
	}
	if t.state != StateNegotiated || len(t.peerResponse) == 0 {
		t.fail()
		return ErrInvalidMessage
	}
	expected, err := CHAPResponse(t.hashID, t.localChapID, t.localChallenge, secret)
	if err != nil {
		t.fail()
		return err
	}
	if subtle.ConstantTimeCompare(expected, t.peerResponse) != 1 {
		t.fail()
		return ErrBadCredentials
	}
	t.state = StateVerified
	return nil
}

// ComputeLocalCHAPResponse produces this side's CHAP response to the peer's
// challenge, for mutual authentication in the final AUTH_DONE message.
func (t *Transaction) ComputeLocalCHAPResponse(secret []byte) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != StateNegotiated && t.state != StateVerified {
		return nil, ErrInvalidMessage
	}
	return CHAPResponse(t.hashID, t.peerChapID, t.peerChallenge, secret)
}

// DoneMessage builds the terminal AUTH_DONE frame.
func (t *Transaction) DoneMessage(success bool) AuthMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := StatusFailure
	if success {
		status = StatusSuccess
	}
	return AuthMessage{Command: CmdAuthDone, Status: uint8(status), Proto: ProtoDHCHAP}
}

// RawSharedSecret returns the big-endian DH shared value, or nil when null DH
// was negotiated.
func (t *Transaction) RawSharedSecret() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rawShared == nil {
		return nil
	}
	return append([]byte(nil), t.rawShared.Bytes()...)
}
