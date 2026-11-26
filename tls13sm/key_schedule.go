package tls13sm

import (
	"crypto/hmac"
	"hash"
)

// Secret labels from RFC 8446 section 7.1.
const (
	labelDerived             = "derived"
	labelEarlyExporter       = "e exp master"
	labelClientEarlyTraffic  = "c e traffic"
	labelClientHandshake     = "c hs traffic"
	labelServerHandshake     = "s hs traffic"
	labelClientAppTraffic    = "c ap traffic"
	labelServerAppTraffic    = "s ap traffic"
	labelExporterMaster      = "exp master"
	labelResumptionMaster    = "res master"
	labelTrafficKey          = "key"
	labelTrafficIV           = "iv"
	labelResumption          = "resumption"
	labelExternalPSKBinder   = "ext binder"
	labelResumptionPSKBinder = "res binder"
)

// trafficKeys is the key/IV pair of one traffic direction.
type trafficKeys struct {
	Key []byte
	IV  []byte
}

// trafficState bundles the secret, AEAD material and per-direction sequence
// number needed for record protection.
type trafficState struct {
	Secret []byte
	cipher *recordCipher
}

// keySchedule is the RFC 8446 section 7.1 key schedule for one connection. It
// stores every named secret so that tests can assert the deterministic
// vectors of Appendix A and so that both state machines share one
// implementation of the derivation chain.
type keySchedule struct {
	suite *CipherSuite

	zeroPSK       []byte
	earlySecret   []byte
	binderKeyExt  []byte
	binderKeyRes  []byte
	earlyExporter []byte
	clientEarly   []byte

	handshakeSecret []byte
	clientHandshake []byte
	serverHandshake []byte

	masterSecret     []byte
	clientApp        []byte
	serverApp        []byte
	exporterMaster   []byte
	resumptionMaster []byte
}

func newKeySchedule(suite *CipherSuite) *keySchedule {
	return &keySchedule{
		suite:   suite,
		zeroPSK: make([]byte, suite.HashLen),
	}
}

func (k *keySchedule) hashFunc() func() hash.Hash { return k.suite.NewHash }

// initEarlySecret derives Early Secret = HKDF-Extract(salt=0, IKM=PSK) and the
// binder keys. With a zero-length PSK the IKM is a string of Hash.length zero
// bytes, which is exactly the full-handshake Appendix A.1 case.
func (k *keySchedule) initEarlySecret(psk []byte) {
	if len(psk) == 0 {
		psk = k.zeroPSK
	}
	k.earlySecret = hkdfExtract(k.hashFunc(), nil, psk)
	emptyHash := k.hashFunc()().Sum(nil)
	k.binderKeyExt = deriveSecret(k.hashFunc(), k.earlySecret, labelExternalPSKBinder, emptyHash)
	k.binderKeyRes = deriveSecret(k.hashFunc(), k.earlySecret, labelResumptionPSKBinder, emptyHash)
}

// deriveEarlyTraffic computes the 0-RTT client_early_traffic_secret and the
// early exporter master secret. The transcript hash is Hash(ClientHello) with
// the binders truncated to zero length per RFC 8446 4.2.11.2.
func (k *keySchedule) deriveEarlyTraffic(clientHelloTruncatedHash []byte) {
	k.clientEarly = deriveSecret(k.hashFunc(), k.earlySecret, labelClientEarlyTraffic, clientHelloTruncatedHash)
	k.earlyExporter = deriveSecret(k.hashFunc(), k.earlySecret, labelEarlyExporter, clientHelloTruncatedHash)
}

// deriveHandshake computes Handshake Secret =
// HKDF-Extract(Derive-Secret(ES, "derived", ""), (EC)DHE) and the two
// handshake traffic secrets.
func (k *keySchedule) deriveHandshake(ecdheShared, transcriptHashSH []byte) {
	emptyHash := k.hashFunc()().Sum(nil)
	salt := deriveSecret(k.hashFunc(), k.earlySecret, labelDerived, emptyHash)
	k.handshakeSecret = hkdfExtract(k.hashFunc(), salt, ecdheShared)
	k.clientHandshake = deriveSecret(k.hashFunc(), k.handshakeSecret, labelClientHandshake, transcriptHashSH)
	k.serverHandshake = deriveSecret(k.hashFunc(), k.handshakeSecret, labelServerHandshake, transcriptHashSH)
}

// deriveApplication computes the Master Secret and the application/exporter
// traffic secrets. transcriptHashSF is the hash through the server Finished.
func (k *keySchedule) deriveApplication(transcriptHashSF []byte) {
	emptyHash := k.hashFunc()().Sum(nil)
	salt := deriveSecret(k.hashFunc(), k.handshakeSecret, labelDerived, emptyHash)
	k.masterSecret = hkdfExtract(k.hashFunc(), salt, k.zeroPSK)
	k.clientApp = deriveSecret(k.hashFunc(), k.masterSecret, labelClientAppTraffic, transcriptHashSF)
	k.serverApp = deriveSecret(k.hashFunc(), k.masterSecret, labelServerAppTraffic, transcriptHashSF)
	k.exporterMaster = deriveSecret(k.hashFunc(), k.masterSecret, labelExporterMaster, transcriptHashSF)
	k.resumptionMaster = deriveSecret(k.hashFunc(), k.masterSecret, labelResumptionMaster, transcriptHashSF)
}

// trafficKeyMaterial expands a traffic secret into the "key" and "iv"
// variables per RFC 8446 section 7.3.
func (k *keySchedule) trafficKeyMaterial(secret []byte) (trafficKeys, error) {
	key := hkdfExpandLabel(k.hashFunc(), secret, labelTrafficKey, nil, k.suite.KeyLen)
	iv := hkdfExpandLabel(k.hashFunc(), secret, labelTrafficIV, nil, k.suite.IVLen)
	return trafficKeys{Key: key, IV: iv}, nil
}

func (k *keySchedule) newTrafficState(secret []byte) (*trafficState, error) {
	keys, err := k.trafficKeyMaterial(secret)
	if err != nil {
		return nil, err
	}
	cipher, err := newRecordCipher(k.suite, keys.Key, keys.IV)
	if err != nil {
		return nil, err
	}
	return &trafficState{Secret: append([]byte(nil), secret...), cipher: cipher}, nil
}

// applicationTrafficSecret returns the next-generation application traffic
// secret for a KeyUpdate: HKDF-Expand-Label(secret, "traffic_upd", "", HashLen).
func (k *keySchedule) applicationTrafficSecret(secret []byte) []byte {
	return hkdfExpandLabel(k.hashFunc(), secret, "traffic upd", nil, k.suite.HashLen)
}

// resumptionTicket derives the resumption PSK for a NewSessionTicket:
// HKDF-Expand-Label(resumption_master_secret, "resumption",
// ticket_nonce, Hash.length).
func (k *keySchedule) resumptionTicket(ticketNonce []byte) []byte {
	return hkdfExpandLabel(k.hashFunc(), k.resumptionMaster, labelResumption, ticketNonce, k.suite.HashLen)
}

// binderCompute produces the PSK binder value for ClientHello.
// transcriptForBinder is Hash(ClientHello.truncate(binders)).
func (k *keySchedule) binderCompute(resumption bool, transcriptForBinder []byte) []byte {
	base := k.binderKeyExt
	if resumption {
		base = k.binderKeyRes
	}
	mac := hmac.New(k.hashFunc(), finishedKey(k.hashFunc(), base))
	mac.Write(transcriptForBinder)
	return mac.Sum(nil)
}
