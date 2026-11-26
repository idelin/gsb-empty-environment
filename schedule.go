package tls13

// KeySchedule chains the Early, Handshake, and Master stages while retaining
// the typed stage objects.
type KeySchedule struct {
	Suite     CipherSuite
	PSK       []byte
	Early     *EarlySecret
	Handshake *HandshakeSecret
	Master    *MasterSecret
}

// NewKeySchedule starts the schedule with an optional PSK. A nil/empty PSK
// selects the standard non-PSK zero input.
func NewKeySchedule(suite CipherSuite, psk []byte) *KeySchedule {
	early := NewEarlySecret(suite, psk)
	return &KeySchedule{Suite: suite, PSK: early.PSK, Early: early}
}

// SetSharedSecret mixes in the (EC)DHE shared secret and creates the Handshake
// and Master stages.
func (k *KeySchedule) SetSharedSecret(sharedSecret []byte) *KeySchedule {
	k.Handshake = k.Early.HandshakeSecret(sharedSecret)
	k.Master = k.Handshake.MasterSecret()
	return k
}

// HandshakeTrafficSecrets returns client and server handshake traffic secrets.
func (k *KeySchedule) HandshakeTrafficSecrets(transcriptHash []byte) (client, server []byte, err error) {
	client, err = k.Handshake.ClientHandshakeTrafficSecret(transcriptHash)
	if err != nil {
		return nil, nil, err
	}
	server, err = k.Handshake.ServerHandshakeTrafficSecret(transcriptHash)
	if err != nil {
		return nil, nil, err
	}
	return client, server, nil
}

// ApplicationTrafficSecrets returns client and server application secrets.
func (k *KeySchedule) ApplicationTrafficSecrets(transcriptHash []byte) (client, server []byte, err error) {
	client, err = k.Master.ClientApplicationTrafficSecret(transcriptHash)
	if err != nil {
		return nil, nil, err
	}
	server, err = k.Master.ServerApplicationTrafficSecret(transcriptHash)
	if err != nil {
		return nil, nil, err
	}
	return client, server, nil
}
