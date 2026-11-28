package wireguard

import (
	"crypto/subtle"
)

var zeroNonce [12]byte

type pendingHandshake struct {
	localIndex uint32
	ephemeral  KeyPair
	c          [32]byte
	h          [32]byte
	mac1       [16]byte
}

type initiationResult struct {
	packet  []byte
	pending *pendingHandshake
}

type responderHandshake struct {
	c               [32]byte
	h               [32]byte
	initiatorEph    [32]byte
	initiatorStatic [32]byte
	timestamp       Timestamp
}

func noiseInitialContext(suite Suite, responderPub [32]byte) ([32]byte, [32]byte) {
	c := suite.Hash([]byte(NoiseConstruction))
	h := suite.Hash(c[:], []byte(NoiseIdentifier))
	h = suite.Hash(h[:], responderPub[:])
	return c, h
}

func buildInitiation(suite Suite, local KeyPair, remotePub [32]byte, psk [32]byte, ephemeral KeyPair, sender uint32, timestamp Timestamp) ([]byte, *pendingHandshake, error) {
	var msg handshakeInitiation
	msg.Sender = sender
	msg.Ephemeral = ephemeral.Public

	c, h := noiseInitialContext(suite, remotePub)

	c = suite.KDF1(c, ephemeral.Public[:])
	h = suite.Hash(h[:], ephemeral.Public[:])

	dh, err := suite.SharedSecret(ephemeral.Private, remotePub)
	if err != nil {
		return nil, nil, err
	}
	var key [32]byte
	c, key = suite.KDF2(c, dh[:])
	encryptedStatic, err := suite.Seal(key, zeroNonce, local.Public[:], h[:])
	if err != nil {
		return nil, nil, err
	}
	copy(msg.EncryptedStatic[:], encryptedStatic)
	h = suite.Hash(h[:], msg.EncryptedStatic[:])

	dh, err = suite.SharedSecret(local.Private, remotePub)
	if err != nil {
		return nil, nil, err
	}
	c, key = suite.KDF2(c, dh[:])
	encryptedTimestamp, err := suite.Seal(key, zeroNonce, timestamp[:], h[:])
	if err != nil {
		return nil, nil, err
	}
	copy(msg.EncryptedTimestamp[:], encryptedTimestamp)
	h = suite.Hash(h[:], msg.EncryptedTimestamp[:])

	pkt := msg.marshal()
	macKey := mac1Key(suite, remotePub)
	mac1 := suite.MAC(macKey[:], pkt[:initiationMAC1Offset])
	copy(pkt[initiationMAC1Offset:initiationMAC2Offset], mac1[:])

	pending := &pendingHandshake{
		localIndex: sender,
		ephemeral:  ephemeral,
		c:          c,
		h:          h,
		mac1:       mac1,
	}
	return pkt, pending, nil
}

func consumeInitiation(suite Suite, local KeyPair, remotePub [32]byte, msg handshakeInitiation) (*responderHandshake, error) {
	c, h := noiseInitialContext(suite, local.Public)

	c = suite.KDF1(c, msg.Ephemeral[:])
	h = suite.Hash(h[:], msg.Ephemeral[:])

	dh, err := suite.SharedSecret(local.Private, msg.Ephemeral)
	if err != nil {
		return nil, ErrAuthentication
	}
	var key [32]byte
	c, key = suite.KDF2(c, dh[:])
	initiatorStatic, err := suite.Open(key, zeroNonce, msg.EncryptedStatic[:], h[:])
	if err != nil || len(initiatorStatic) != 32 {
		return nil, ErrAuthentication
	}
	var staticPub [32]byte
	copy(staticPub[:], initiatorStatic)
	if subtle.ConstantTimeCompare(staticPub[:], remotePub[:]) != 1 {
		return nil, ErrAuthentication
	}
	h = suite.Hash(h[:], msg.EncryptedStatic[:])

	dh, err = suite.SharedSecret(local.Private, staticPub)
	if err != nil {
		return nil, ErrAuthentication
	}
	c, key = suite.KDF2(c, dh[:])
	plaintextTimestamp, err := suite.Open(key, zeroNonce, msg.EncryptedTimestamp[:], h[:])
	if err != nil || len(plaintextTimestamp) != TimestampSize {
		return nil, ErrAuthentication
	}
	h = suite.Hash(h[:], msg.EncryptedTimestamp[:])

	var timestamp Timestamp
	copy(timestamp[:], plaintextTimestamp)
	return &responderHandshake{
		c:               c,
		h:               h,
		initiatorEph:    msg.Ephemeral,
		initiatorStatic: staticPub,
		timestamp:       timestamp,
	}, nil
}

func buildResponse(suite Suite, local KeyPair, psk [32]byte, inbound *responderHandshake, ephemeral KeyPair, sender, receiver uint32) ([]byte, [32]byte, [32]byte, error) {
	var msg handshakeResponse
	msg.Sender = sender
	msg.Receiver = receiver
	msg.Ephemeral = ephemeral.Public

	c := inbound.c
	h := inbound.h
	c = suite.KDF1(c, ephemeral.Public[:])
	h = suite.Hash(h[:], ephemeral.Public[:])

	dh, err := suite.SharedSecret(ephemeral.Private, inbound.initiatorEph)
	if err != nil {
		return nil, [32]byte{}, [32]byte{}, err
	}
	c = suite.KDF1(c, dh[:])

	dh, err = suite.SharedSecret(ephemeral.Private, inbound.initiatorStatic)
	if err != nil {
		return nil, [32]byte{}, [32]byte{}, err
	}
	c = suite.KDF1(c, dh[:])

	var tau [32]byte
	c, tau, _ = suite.KDF3(c, psk[:])
	h = suite.Hash(h[:], tau[:])

	dh, err = suite.SharedSecret(local.Private, inbound.initiatorStatic)
	if err != nil {
		return nil, [32]byte{}, [32]byte{}, err
	}
	var key [32]byte
	c, key = suite.KDF2(c, dh[:])
	encryptedNothing, err := suite.Seal(key, zeroNonce, nil, h[:])
	if err != nil {
		return nil, [32]byte{}, [32]byte{}, err
	}
	copy(msg.EncryptedNothing[:], encryptedNothing)
	h = suite.Hash(h[:], msg.EncryptedNothing[:])
	_ = h

	recv, send := suite.KDF2(c, nil)
	pkt := msg.marshal()
	macKey := mac1Key(suite, inbound.initiatorStatic)
	mac1 := suite.MAC(macKey[:], pkt[:responseMAC1Offset])
	copy(pkt[responseMAC1Offset:responseMAC2Offset], mac1[:])
	return pkt, send, recv, nil
}

func consumeResponse(suite Suite, local KeyPair, remotePub [32]byte, psk [32]byte, pending *pendingHandshake, msg handshakeResponse) ([32]byte, [32]byte, error) {
	c := pending.c
	h := pending.h

	c = suite.KDF1(c, msg.Ephemeral[:])
	h = suite.Hash(h[:], msg.Ephemeral[:])

	dh, err := suite.SharedSecret(pending.ephemeral.Private, msg.Ephemeral)
	if err != nil {
		return [32]byte{}, [32]byte{}, ErrAuthentication
	}
	c = suite.KDF1(c, dh[:])

	dh, err = suite.SharedSecret(local.Private, msg.Ephemeral)
	if err != nil {
		return [32]byte{}, [32]byte{}, ErrAuthentication
	}
	c = suite.KDF1(c, dh[:])

	var tau [32]byte
	c, tau, _ = suite.KDF3(c, psk[:])
	h = suite.Hash(h[:], tau[:])

	dh, err = suite.SharedSecret(local.Private, remotePub)
	if err != nil {
		return [32]byte{}, [32]byte{}, ErrAuthentication
	}
	var key [32]byte
	c, key = suite.KDF2(c, dh[:])
	nothing, err := suite.Open(key, zeroNonce, msg.EncryptedNothing[:], h[:])
	if err != nil || len(nothing) != 0 {
		return [32]byte{}, [32]byte{}, ErrAuthentication
	}
	h = suite.Hash(h[:], msg.EncryptedNothing[:])
	_ = h

	send, recv := suite.KDF2(c, nil)
	return send, recv, nil
}
