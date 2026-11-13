package netsec

import (
	"crypto/ecdh"
	"errors"
	"fmt"
)

type argumentKind uint8

const (
	argumentOther argumentKind = iota
	argumentParty
	argumentBytes
	argumentPrivate
	argumentPublic
	argumentKeyPair
	argumentKDFContext
)

type argument struct {
	index int
	value any
	kind  argumentKind
}

type keyMaterial struct {
	private *ecdh.PrivateKey
	public  *ecdh.PublicKey
}

type encryptPlan struct {
	plaintext       []byte
	partyU          []byte
	partyV          []byte
	senderPrivate   *ecdh.PrivateKey
	recipientPublic *ecdh.PublicKey
	sharedSecret    []byte
}

type decryptPlan struct {
	ciphertext       []byte
	partyU           []byte
	partyV           []byte
	recipientPrivate *ecdh.PrivateKey
	senderPublic     []byte
	sharedSecret     []byte
}

func parseEncryptArgs(args []any) (*encryptPlan, error) {
	if len(args) == 0 {
		return nil, errors.New("netsec: no encryption arguments")
	}

	plaintextIndex := -1
	var plaintext []byte

	// The common form is Encrypt(plaintext, parties..., keys...). Also accept
	// Encrypt(key, partyU, partyV, plaintext) when the first argument is a
	// pre-shared key and the final argument is the plaintext.
	if len(args) >= 4 {
		if key, ok := args[0].([]byte); ok && likelyStandaloneKey(len(key)) &&
			looksLikeParty(args[1]) && looksLikeParty(args[2]) &&
			!isLikelyKeyBytes(args[1]) && !isLikelyKeyBytes(args[2]) {
			if value, ok := args[len(args)-1].([]byte); ok {
				plaintextIndex = len(args) - 1
				plaintext = cloneBytes(value)
			}
		}
	}
	if plaintextIndex == -1 && len(args) >= 4 && isLikelyKeyBytes(args[0]) &&
		!isLikelyKeyBytes(args[1]) && isExplicitParty(args[2]) && isExplicitParty(args[3]) {
		if plaintextValue, ok := args[1].([]byte); ok {
			plaintextIndex = 1
			plaintext = cloneBytes(plaintextValue)
		}
	}
	if plaintextIndex == -1 && len(args) >= 4 && isExplicitParty(args[0]) && isExplicitParty(args[1]) &&
		!isExplicitParty(args[2]) {
		if index, value, ok := choosePartiesFirstPlaintext(args[2:], 2); ok {
			plaintextIndex = index
			plaintext = value
		}
	}
	if plaintextIndex == -1 && !isKeyLike(args[0]) {
		if value, ok := plaintextBytes(args[0]); ok && !isContextValue(args[0]) {
			plaintextIndex = 0
			plaintext = value
		}
	}

	classified := classifyArguments(args, plaintextIndex)
	partyU, partyV, partyArguments, err := resolveParties(classified, true)
	if err != nil {
		return nil, err
	}

	// If the plaintext was not the first argument, it is the extra raw byte
	// slice next to explicit key objects.
	if plaintextIndex == -1 {
		for i := len(classified) - 1; i >= 0; i-- {
			candidate := classified[i]
			if candidate.kind != argumentBytes || candidate.index == plaintextIndex {
				continue
			}
			if containsIndex(partyArguments, candidate.index) {
				continue
			}
			value := cloneBytes(candidate.value.([]byte))
			plaintext = value
			plaintextIndex = candidate.index
			break
		}
	}
	if plaintextIndex == -1 {
		return nil, errors.New("netsec: missing plaintext")
	}

	remaining := withoutIndexes(classified, plaintextIndex, partyArguments...)
	senderPrivate, recipientPublic, sharedSecret, err := resolveEncryptKeys(remaining)
	if err != nil {
		return nil, err
	}

	if senderPrivate == nil && recipientPublic == nil && len(sharedSecret) == 0 {
		return nil, errors.New("netsec: missing key material")
	}
	if senderPrivate == nil && recipientPublic != nil {
		senderPrivate, err = ecdh.X25519().GenerateKey(cryptoRandReader)
		if err != nil {
			return nil, err
		}
	}
	if senderPrivate != nil && recipientPublic == nil {
		return nil, errors.New("netsec: missing recipient public key")
	}

	return &encryptPlan{
		plaintext:       plaintext,
		partyU:          partyU,
		partyV:          partyV,
		senderPrivate:   senderPrivate,
		recipientPublic: recipientPublic,
		sharedSecret:    sharedSecret,
	}, nil
}

func parseDecryptArgs(args []any) (*decryptPlan, error) {
	if len(args) == 0 {
		return nil, errors.New("netsec: no decryption arguments")
	}

	ciphertextIndex := -1
	var ciphertext []byte
	if value, ok := args[0].([]byte); ok && len(value) >= minimumCiphertextSize {
		ciphertextIndex = 0
		ciphertext = value
	}
	if ciphertextIndex == -1 {
		for i := len(args) - 1; i >= 0; i-- {
			if value, ok := args[i].([]byte); ok && len(value) >= minimumCiphertextSize {
				ciphertextIndex = i
				ciphertext = value
				break
			}
		}
	}
	if ciphertextIndex == -1 {
		return nil, errors.New("netsec: missing ciphertext")
	}

	classified := classifyArguments(args, ciphertextIndex)
	partyU, partyV, partyArguments, err := resolveParties(classified, false)
	if err != nil {
		return nil, err
	}

	remaining := withoutIndexes(classified, ciphertextIndex, partyArguments...)
	recipientPrivate, senderPublic, sharedSecret, err := resolveDecryptKeys(remaining)
	if err != nil {
		return nil, err
	}
	if recipientPrivate == nil && len(sharedSecret) == 0 {
		return nil, errors.New("netsec: missing decryption key")
	}

	return &decryptPlan{
		ciphertext:       cloneBytes(ciphertext),
		partyU:           partyU,
		partyV:           partyV,
		recipientPrivate: recipientPrivate,
		senderPublic:     senderPublic,
		sharedSecret:     sharedSecret,
	}, nil
}

func classifyArguments(args []any, skip int) []argument {
	classified := make([]argument, 0, len(args))
	for index, value := range args {
		if index == skip {
			continue
		}
		classified = append(classified, argument{
			index: index,
			value: value,
			kind:  classifyArgument(value),
		})
	}
	return classified
}

func classifyArgument(value any) argumentKind {
	switch value.(type) {
	case nil:
		return argumentOther
	case string:
		return argumentParty
	case PartyInfo, *PartyInfo:
		return argumentParty
	case []byte:
		return argumentBytes
	case *ecdh.PrivateKey, ecdh.PrivateKey:
		return argumentPrivate
	case *ecdh.PublicKey, ecdh.PublicKey:
		return argumentPublic
	case *KeyPair, KeyPair:
		return argumentKeyPair
	case KDFContext, *KDFContext:
		return argumentKDFContext
	default:
		if _, ok := value.(fmt.Stringer); ok {
			return argumentParty
		}
		return argumentOther
	}
}

func resolveParties(classified []argument, encryption bool) ([]byte, []byte, []int, error) {
	var explicit []argument
	var contextParties [][]byte
	var kdfContexts []KDFContext

	for _, candidate := range classified {
		switch candidate.kind {
		case argumentParty:
			explicit = append(explicit, candidate)
		case argumentKDFContext:
			var context KDFContext
			switch value := candidate.value.(type) {
			case KDFContext:
				context = value
			case *KDFContext:
				if value != nil {
					context = *value
				}
			}
			kdfContexts = append(kdfContexts, context)
			if len(context.PartyUInfo) != 0 || len(context.PartyVInfo) != 0 {
				contextParties = append(contextParties, context.PartyUInfo, context.PartyVInfo)
			}
		}
	}

	if len(explicit) >= 2 {
		u, err := partyBytes(explicit[0].value)
		if err != nil {
			return nil, nil, nil, err
		}
		v, err := partyBytes(explicit[1].value)
		if err != nil {
			return nil, nil, nil, err
		}
		return u, v, []int{explicit[0].index, explicit[1].index}, nil
	}

	if len(contextParties) >= 2 {
		return cloneBytes(contextParties[0]), cloneBytes(contextParties[1]), nil, nil
	}

	if len(kdfContexts) > 0 && len(explicit) == 0 {
		return nil, nil, nil, errors.New("netsec: KDF context is missing party information")
	}

	var byteCandidates []argument
	var keyCandidates int
	for _, candidate := range classified {
		switch candidate.kind {
		case argumentBytes:
			byteCandidates = append(byteCandidates, candidate)
		case argumentPrivate, argumentPublic, argumentKeyPair:
			keyCandidates++
		}
	}

	if len(explicit) == 1 {
		if len(byteCandidates) == 0 {
			return nil, nil, nil, errors.New("netsec: missing one party")
		}
		first, err := partyBytes(explicit[0].value)
		if err != nil {
			return nil, nil, nil, err
		}
		second := cloneBytes(byteCandidates[0].value.([]byte))
		return first, second, []int{explicit[0].index, byteCandidates[0].index}, nil
	}

	if len(byteCandidates) < 2 {
		return nil, nil, nil, errors.New("netsec: missing partyU and partyV")
	}

	if keyCandidates > 0 {
		u := byteCandidates[len(byteCandidates)-2]
		v := byteCandidates[len(byteCandidates)-1]
		return cloneBytes(u.value.([]byte)), cloneBytes(v.value.([]byte)), []int{u.index, v.index}, nil
	}

	if len(byteCandidates) >= 4 && plausibleRawKeyPair(byteCandidates[0].value.([]byte), byteCandidates[1].value.([]byte)) {
		u := byteCandidates[len(byteCandidates)-2]
		v := byteCandidates[len(byteCandidates)-1]
		return cloneBytes(u.value.([]byte)), cloneBytes(v.value.([]byte)), []int{u.index, v.index}, nil
	}

	if len(byteCandidates) == 2 {
		if encryption {
			return nil, nil, nil, errors.New("netsec: cannot distinguish party information from key material")
		}
		// For decryption two raw values normally describe an ECDH private
		// key and the sender public key, not two parties.
		return nil, nil, nil, errors.New("netsec: missing partyU and partyV")
	}

	keyIndex := chooseSharedKeyIndex(byteCandidates)
	var parties []argument
	var indexes []int
	for index, candidate := range byteCandidates {
		if index != keyIndex {
			parties = append(parties, candidate)
			indexes = append(indexes, candidate.index)
		}
	}
	if len(parties) < 2 {
		return nil, nil, nil, errors.New("netsec: missing partyU and partyV")
	}
	return cloneBytes(parties[0].value.([]byte)), cloneBytes(parties[1].value.([]byte)), indexes[:2], nil
}

func resolveEncryptKeys(remaining []argument) (*ecdh.PrivateKey, *ecdh.PublicKey, []byte, error) {
	var keyCandidates []argument
	var raw [][]byte
	for _, candidate := range remaining {
		switch candidate.kind {
		case argumentPrivate, argumentPublic, argumentKeyPair:
			keyCandidates = append(keyCandidates, candidate)
		case argumentBytes:
			raw = append(raw, candidate.value.([]byte))
		}
	}

	if len(keyCandidates) >= 2 {
		first, err := materialFrom(keyCandidates[0].value)
		if err != nil {
			return nil, nil, nil, err
		}
		second, err := materialFrom(keyCandidates[1].value)
		if err != nil {
			return nil, nil, nil, err
		}
		sender, recipient, err := encryptionKeyRoles(first, second)
		return sender, recipient, nil, err
	}

	if len(keyCandidates) == 1 {
		material, err := materialFrom(keyCandidates[0].value)
		if err != nil {
			return nil, nil, nil, err
		}
		if material.private != nil {
			return nil, material.private.PublicKey(), nil, nil
		}
		return nil, material.public, nil, nil
	}

	switch len(raw) {
	case 1:
		return nil, nil, cloneBytes(raw[0]), nil
	case 2:
		privateKey, publicKey, err := rawKeyPair(raw[0], raw[1])
		if err == nil {
			return privateKey, publicKey, nil, nil
		}
		return nil, nil, nil, errors.New("netsec: ambiguous raw key material")
	default:
		if len(raw) == 0 {
			return nil, nil, nil, errors.New("netsec: missing key material")
		}
		return nil, nil, nil, errors.New("netsec: too many raw key values")
	}
}

func resolveDecryptKeys(remaining []argument) (*ecdh.PrivateKey, []byte, []byte, error) {
	var keyCandidates []argument
	var raw [][]byte
	for _, candidate := range remaining {
		switch candidate.kind {
		case argumentPrivate, argumentPublic, argumentKeyPair:
			keyCandidates = append(keyCandidates, candidate)
		case argumentBytes:
			raw = append(raw, candidate.value.([]byte))
		}
	}

	if len(keyCandidates) >= 2 {
		first, err := materialFrom(keyCandidates[0].value)
		if err != nil {
			return nil, nil, nil, err
		}
		second, err := materialFrom(keyCandidates[1].value)
		if err != nil {
			return nil, nil, nil, err
		}
		recipient, sender, err := decryptionKeyRoles(first, second)
		return recipient, sender, nil, err
	}

	if len(keyCandidates) == 1 {
		material, err := materialFrom(keyCandidates[0].value)
		if err != nil {
			return nil, nil, nil, err
		}
		if material.private == nil {
			return nil, nil, nil, errors.New("netsec: decryption requires a private key")
		}
		return material.private, nil, nil, nil
	}

	switch len(raw) {
	case 1:
		return nil, nil, cloneBytes(raw[0]), nil
	case 2:
		privateKey, publicKey, err := rawKeyPair(raw[0], raw[1])
		if err == nil {
			return privateKey, publicKey.Bytes(), nil, nil
		}
		return nil, nil, nil, errors.New("netsec: ambiguous raw key material")
	default:
		if len(raw) == 0 {
			return nil, nil, nil, errors.New("netsec: missing key material")
		}
		return nil, nil, nil, errors.New("netsec: too many raw key values")
	}
}

func encryptionKeyRoles(first, second keyMaterial) (*ecdh.PrivateKey, *ecdh.PublicKey, error) {
	switch {
	case first.private != nil && second.public != nil:
		return first.private, second.public, nil
	case first.public != nil && second.private != nil:
		return second.private, first.public, nil
	case first.private != nil && second.private != nil:
		return first.private, second.private.PublicKey(), nil
	default:
		return nil, nil, errors.New("netsec: encryption requires a sender private key and a recipient public key")
	}
}

func decryptionKeyRoles(first, second keyMaterial) (*ecdh.PrivateKey, []byte, error) {
	switch {
	case first.private != nil && second.public != nil:
		return first.private, cloneBytes(second.public.Bytes()), nil
	case first.public != nil && second.private != nil:
		return second.private, cloneBytes(first.public.Bytes()), nil
	case first.private != nil && second.private != nil:
		return first.private, cloneBytes(second.private.PublicKey().Bytes()), nil
	default:
		return nil, nil, errors.New("netsec: decryption requires a recipient private key")
	}
}

func materialFrom(value any) (keyMaterial, error) {
	if privateKey, err := privateKeyFrom(value); err == nil {
		return keyMaterial{private: privateKey, public: privateKey.PublicKey()}, nil
	}
	if publicKey, err := publicKeyFrom(value); err == nil {
		return keyMaterial{public: publicKey}, nil
	}
	return keyMaterial{}, errors.New("netsec: unsupported key material")
}

func withoutIndexes(arguments []argument, skip int, more ...int) []argument {
	skipped := make(map[int]struct{}, len(more)+1)
	skipped[skip] = struct{}{}
	for _, index := range more {
		skipped[index] = struct{}{}
	}
	result := make([]argument, 0, len(arguments))
	for _, candidate := range arguments {
		if _, ok := skipped[candidate.index]; ok {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func containsIndex(indexes []int, index int) bool {
	for _, candidate := range indexes {
		if candidate == index {
			return true
		}
	}
	return false
}

func choosePartiesFirstPlaintext(args []any, offset int) (int, []byte, bool) {
	type candidate struct {
		index  int
		value  []byte
		keyLen bool
	}
	var candidates []candidate
	for localIndex, value := range args {
		bytesValue, ok := value.([]byte)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{
			index:  offset + localIndex,
			value:  cloneBytes(bytesValue),
			keyLen: likelyStandaloneKey(len(bytesValue)),
		})
	}
	if len(candidates) == 0 {
		return 0, nil, false
	}

	var nonKey []int
	for index, candidate := range candidates {
		if !candidate.keyLen {
			nonKey = append(nonKey, index)
		}
	}
	chosen := 0
	if len(candidates) >= 3 && plausibleRawKeyPair(candidates[0].value, candidates[1].value) {
		chosen = len(candidates) - 1
	} else {
		switch len(nonKey) {
		case 1:
			chosen = nonKey[0]
		default:
			chosen = 0
			for index := 1; index < len(candidates); index++ {
				if len(candidates[index].value) < len(candidates[chosen].value) {
					chosen = index
				}
			}
		}
	}
	selected := candidates[chosen]
	return selected.index, selected.value, true
}

func chooseSharedKeyIndex(candidates []argument) int {
	bestIndex := -1
	bestLength := -1
	secondLength := -1
	for index, candidate := range candidates {
		length := len(candidate.value.([]byte))
		if length > bestLength {
			secondLength = bestLength
			bestLength = length
			bestIndex = index
		} else if length > secondLength {
			secondLength = length
		}
	}
	if bestIndex >= 0 && bestLength >= 16 && bestLength > secondLength {
		return bestIndex
	}
	return 0
}

func looksLikeParty(value any) bool {
	switch typed := value.(type) {
	case string, PartyInfo, *PartyInfo:
		return true
	case []byte:
		return len(typed) != 0 && len(typed) <= 64
	default:
		return false
	}
}

func likelyStandaloneKey(length int) bool {
	return length == 16 || length == 24 || length == 32 || length >= 48
}

func plausibleRawKeyPair(private, public []byte) bool {
	if len(private) != 32 && len(private) != 48 && len(private) != 66 {
		return false
	}
	switch len(public) {
	case 32, 65, 97, 133:
		return true
	default:
		return false
	}
}

func rawKeyPair(privateBytes, publicBytes []byte) (*ecdh.PrivateKey, *ecdh.PublicKey, error) {
	curve := curveForKeyMaterial(privateBytes, publicBytes)
	privateKey, err := curve.NewPrivateKey(privateBytes)
	if err != nil {
		return nil, nil, err
	}
	publicKey, err := curve.NewPublicKey(publicBytes)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, publicKey, nil
}

func isLikelyKeyBytes(value any) bool {
	bytesValue, ok := value.([]byte)
	return ok && likelyStandaloneKey(len(bytesValue))
}

func plaintextBytes(value any) ([]byte, bool) {
	switch typed := value.(type) {
	case []byte:
		return cloneBytes(typed), true
	case string:
		return []byte(typed), true
	default:
		return nil, false
	}
}

func isKeyLike(value any) bool {
	switch value.(type) {
	case *ecdh.PrivateKey, ecdh.PrivateKey, *ecdh.PublicKey, ecdh.PublicKey, *KeyPair, KeyPair:
		return true
	default:
		return false
	}
}

func isContextValue(value any) bool {
	switch value.(type) {
	case KDFContext, *KDFContext:
		return true
	default:
		return false
	}
}

func isExplicitParty(value any) bool {
	switch value.(type) {
	case string, PartyInfo, *PartyInfo:
		return true
	default:
		return false
	}
}
