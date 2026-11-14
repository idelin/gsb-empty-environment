package netsec

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

// Admitter validates and atomically consumes MLS Welcome messages.
type Admitter struct {
	crypto       Crypto
	store        Store
	fetcher      RatchetTreeFetcher
	now          func() time.Time
	signerPolicy GroupInfoSignerPolicy
	locks        keyedMutex
}

// WelcomeAdmitter is an alias for Admitter.
type WelcomeAdmitter = Admitter

// NewAdmitter creates a Welcome admitter. A nil Store selects a new in-memory
// store. A nil Crypto makes every admission fail closed.
func NewAdmitter(crypto Crypto, opts ...Option) *Admitter {
	admitter := &Admitter{
		crypto: crypto,
		store:  NewMemoryStore(),
		now:    time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(admitter)
		}
	}
	if admitter.store == nil {
		admitter.store = NewMemoryStore()
	}
	if admitter.now == nil {
		admitter.now = time.Now
	}
	return admitter
}

// New is an alias for NewAdmitter.
func New(crypto Crypto, opts ...Option) *Admitter {
	return NewAdmitter(crypto, opts...)
}

// NewWelcomeAdmitter is an alias for NewAdmitter.
func NewWelcomeAdmitter(crypto Crypto, opts ...Option) *Admitter {
	return NewAdmitter(crypto, opts...)
}

// Admit validates welcome against local and commits its consumption exactly
// once. Concurrent calls for the same Welcome are serialized; only one can
// commit. No durable state is changed unless every check succeeds.
func (a *Admitter) Admit(ctx context.Context, welcome *Welcome, local LocalParticipant) (*Admission, error) {
	if a == nil || a.crypto == nil || a.store == nil {
		return nil, ErrNotConfigured
	}
	if welcome == nil {
		return nil, fmt.Errorf("%w: nil welcome", ErrInvalidWelcome)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Snapshot the caller's input so later mutation cannot change what was
	// validated or what the external tree callback is bound to.
	welcome = cloneWelcome(welcome)
	keyPackage := cloneKeyPackage(local.KeyPackage)
	local = LocalParticipant{
		KeyPackage:      keyPackage,
		PrivateKey:      local.PrivateKey,
		PSKs:            clonePSKMap(local.PSKs),
		ExpectedGroupID: cloneBytes(local.ExpectedGroupID),
	}
	now := a.now()

	if err := validateKeyPackage(keyPackage, now); err != nil {
		return nil, err
	}

	keyPackageRef := a.keyPackageReference(keyPackage)
	if len(keyPackageRef) == 0 {
		return nil, fmt.Errorf("%w: empty key package reference", ErrInvalidKeyPackage)
	}
	welcomeID := a.crypto.Digest("MLS-Welcome-ID", canonicalWelcome(welcome))
	if len(welcomeID) == 0 {
		return nil, fmt.Errorf("%w: crypto returned empty welcome id", ErrNotConfigured)
	}

	unlock := a.locks.lock(string(welcomeID))
	defer unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.store.WelcomeConsumed(welcomeID) {
		return nil, fmt.Errorf("%w: %x", ErrWelcomeAlreadyConsumed, welcomeID)
	}
	if a.store.KeyPackageConsumed(keyPackageRef) {
		return nil, fmt.Errorf("%w: %x", ErrKeyPackageConsumed, keyPackageRef)
	}

	groupInfo := welcome.GroupInfo
	if err := validateGroupInfo(groupInfo); err != nil {
		return nil, err
	}
	if len(keyPackage.GroupID) > 0 && !bytes.Equal(keyPackage.GroupID, groupInfo.GroupID) {
		return nil, fmt.Errorf("%w: key package is bound to a different group", ErrGroupMismatch)
	}
	if len(local.ExpectedGroupID) > 0 && !bytes.Equal(local.ExpectedGroupID, groupInfo.GroupID) {
		return nil, fmt.Errorf("%w: local participant expects a different group", ErrGroupMismatch)
	}
	if keyPackage.CipherSuite != groupInfo.CipherSuite {
		return nil, fmt.Errorf("%w: key package %d, group info %d", ErrCipherSuiteMismatch, keyPackage.CipherSuite, groupInfo.CipherSuite)
	}
	if len(welcome.Secrets) == 0 {
		return nil, fmt.Errorf("%w: no encrypted group secrets", ErrInvalidWelcome)
	}

	current, hasCurrent := a.store.LoadGroup(groupInfo.GroupID)
	if hasCurrent {
		if groupInfo.Epoch <= current.Epoch {
			return nil, fmt.Errorf("%w: welcome epoch %d, current epoch %d", ErrEpochRollback, groupInfo.Epoch, current.Epoch)
		}
		if current.CipherSuite != groupInfo.CipherSuite {
			return nil, fmt.Errorf("%w: current %d, welcome %d", ErrCipherSuiteMismatch, current.CipherSuite, groupInfo.CipherSuite)
		}
		if len(current.TranscriptHash) > 0 && len(welcome.PriorTranscriptHash) > 0 &&
			!bytes.Equal(current.TranscriptHash, welcome.PriorTranscriptHash) {
			return nil, fmt.Errorf("%w: prior transcript is not the current group transcript", ErrTranscriptHashMismatch)
		}
	}

	treeBytes, tree, treeHash, err := a.resolveTree(ctx, welcome, welcomeID, groupInfo)
	if err != nil {
		return nil, err
	}
	if !extensionsEqual(tree.Extensions, groupInfo.Extensions) {
		return nil, fmt.Errorf("%w: group info and tree extensions differ", ErrExtensionsMismatch)
	}

	signer, err := a.authorizeSigner(groupInfo, tree)
	if err != nil {
		return nil, err
	}
	if err := a.crypto.VerifyGroupInfo(groupInfo, signer, treeHash); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGroupInfoSignature, err)
	}

	if err := validateKeyPackageLeaf(tree, keyPackage); err != nil {
		return nil, err
	}
	if err := a.crypto.VerifyKeyPackageCredential(keyPackage, local.PrivateKey); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyPackageCredential, err)
	}

	priorTranscript := cloneBytes(welcome.PriorTranscriptHash)
	if hasCurrent && len(current.TranscriptHash) > 0 {
		priorTranscript = cloneBytes(current.TranscriptHash)
	}
	transcriptInput := TranscriptInput{
		GroupID:             cloneBytes(groupInfo.GroupID),
		Epoch:               groupInfo.Epoch,
		CipherSuite:         groupInfo.CipherSuite,
		PriorTranscriptHash: priorTranscript,
		TreeHash:            cloneBytes(treeHash),
		Extensions:          cloneExtensions(groupInfo.Extensions),
		ConfirmationTag:     cloneBytes(welcome.ConfirmationTag),
	}
	transcriptHash, err := a.crypto.ComputeTranscriptHash(transcriptInput)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranscriptHashMismatch, err)
	}
	if len(transcriptHash) == 0 || !bytes.Equal(transcriptHash, groupInfo.TranscriptHash) {
		return nil, fmt.Errorf("%w", ErrTranscriptHashMismatch)
	}

	candidate, psks, err := a.collectCandidate(ctx, welcome, groupInfo, keyPackage, keyPackageRef, local)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	joinerInput := JoinerSecretInput{
		GroupID:        cloneBytes(groupInfo.GroupID),
		Epoch:          groupInfo.Epoch,
		CipherSuite:    groupInfo.CipherSuite,
		KeyPackageRef:  cloneBytes(keyPackageRef),
		JoinerSecret:   cloneBytes(candidate.JoinerSecret),
		PSKIDs:         cloneStrings(candidate.PSKIDs),
		PSKs:           clonePSKMap(psks),
		TreeHash:       cloneBytes(treeHash),
		TranscriptHash: cloneBytes(groupInfo.TranscriptHash),
		Extensions:     cloneExtensions(groupInfo.Extensions),
	}
	if err := a.crypto.VerifyJoinerSecret(joinerInput); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrJoinerSecret, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	newState := GroupState{
		GroupID:        cloneBytes(groupInfo.GroupID),
		Epoch:          groupInfo.Epoch,
		CipherSuite:    groupInfo.CipherSuite,
		TreeHash:       cloneBytes(treeHash),
		TranscriptHash: cloneBytes(groupInfo.TranscriptHash),
		Extensions:     cloneExtensions(groupInfo.Extensions),
		RatchetTree:    cloneBytes(treeBytes),
		Signer:         groupInfo.Signer,
	}
	commit := Commit{
		WelcomeID:     cloneBytes(welcomeID),
		KeyPackageRef: cloneBytes(keyPackageRef),
		GroupID:       cloneBytes(groupInfo.GroupID),
		HasGroup:      hasCurrent,
		ExpectedEpoch: current.Epoch,
		NewState:      newState,
	}
	if err := a.store.Commit(commit); err != nil {
		return nil, err
	}

	return &Admission{
		WelcomeID:      cloneBytes(welcomeID),
		GroupID:        cloneBytes(groupInfo.GroupID),
		Epoch:          groupInfo.Epoch,
		CipherSuite:    groupInfo.CipherSuite,
		KeyPackageRef:  cloneBytes(keyPackageRef),
		JoinerSecret:   cloneBytes(candidate.JoinerSecret),
		PathSecret:     cloneBytes(candidate.PathSecret),
		PSKIDs:         cloneStrings(candidate.PSKIDs),
		TreeHash:       cloneBytes(treeHash),
		TranscriptHash: cloneBytes(groupInfo.TranscriptHash),
		Extensions:     cloneExtensions(groupInfo.Extensions),
		Signer:         groupInfo.Signer,
		Tree:           cloneTree(tree),
	}, nil
}

// AdmitWelcome is an alias for Admit.
func (a *Admitter) AdmitWelcome(ctx context.Context, welcome *Welcome, local LocalParticipant) (*Admission, error) {
	return a.Admit(ctx, welcome, local)
}

// ProcessWelcome is an alias for Admit.
func (a *Admitter) ProcessWelcome(ctx context.Context, welcome *Welcome, local LocalParticipant) (*Admission, error) {
	return a.Admit(ctx, welcome, local)
}

// AdmitWithKeyPackage is a convenience wrapper around Admit.
func (a *Admitter) AdmitWithKeyPackage(ctx context.Context, welcome *Welcome, keyPackage KeyPackage, privateKey any, psks map[string][]byte) (*Admission, error) {
	return a.Admit(ctx, welcome, LocalParticipant{
		KeyPackage: keyPackage,
		PrivateKey: privateKey,
		PSKs:       psks,
	})
}

func validateKeyPackage(keyPackage KeyPackage, now time.Time) error {
	if keyPackage.Leaf.Blank {
		return fmt.Errorf("%w: blank leaf", ErrInvalidKeyPackage)
	}
	if keyPackage.Leaf.Credential.IsZero() {
		return fmt.Errorf("%w: missing leaf credential", ErrInvalidKeyPackage)
	}
	if len(keyPackage.Leaf.SignatureKey) == 0 {
		return fmt.Errorf("%w: missing leaf signature key", ErrInvalidKeyPackage)
	}
	if keyPackage.NotYetValid(now) {
		return fmt.Errorf("%w: not before %s", ErrKeyPackageNotYetValid, keyPackage.NotBefore)
	}
	if keyPackage.Expired(now) {
		return fmt.Errorf("%w: expired at %s", ErrKeyPackageExpired, keyPackage.NotAfter)
	}
	return nil
}

func validateGroupInfo(groupInfo GroupInfo) error {
	if len(groupInfo.GroupID) == 0 {
		return fmt.Errorf("%w: empty group id", ErrInvalidGroupInfo)
	}
	if len(groupInfo.TreeHash) == 0 {
		return fmt.Errorf("%w: empty tree hash", ErrInvalidGroupInfo)
	}
	if len(groupInfo.TranscriptHash) == 0 {
		return fmt.Errorf("%w: empty transcript hash", ErrInvalidGroupInfo)
	}
	if len(groupInfo.Signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrInvalidGroupInfo)
	}
	if _, err := normalizePSKIDs(groupInfo.RequiredPSKIDs); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidGroupInfo, err)
	}
	return nil
}

func (a *Admitter) keyPackageReference(keyPackage KeyPackage) []byte {
	if len(keyPackage.Reference) > 0 {
		return cloneBytes(keyPackage.Reference)
	}
	return a.crypto.Digest("MLS-KeyPackage-Reference", canonicalKeyPackage(keyPackage))
}

func cloneKeyPackage(keyPackage KeyPackage) KeyPackage {
	keyPackage.Reference = cloneBytes(keyPackage.Reference)
	keyPackage.GroupID = cloneBytes(keyPackage.GroupID)
	keyPackage.Leaf = cloneLeaf(keyPackage.Leaf)
	return keyPackage
}

func clonePSKMap(psks map[string][]byte) map[string][]byte {
	if psks == nil {
		return nil
	}
	out := make(map[string][]byte, len(psks))
	for id, psk := range psks {
		out[id] = cloneBytes(psk)
	}
	return out
}

func (a *Admitter) resolveTree(ctx context.Context, welcome *Welcome, welcomeID []byte, groupInfo GroupInfo) ([]byte, RatchetTree, []byte, error) {
	hasInline := len(welcome.RatchetTree) > 0
	hasExternal := welcome.TreeReference != nil
	if hasInline == hasExternal {
		return nil, RatchetTree{}, nil, fmt.Errorf("%w: inline=%t external=%t", ErrTreeSource, hasInline, hasExternal)
	}
	if len(groupInfo.TreeHash) == 0 {
		return nil, RatchetTree{}, nil, fmt.Errorf("%w: group info has no tree hash", ErrInvalidGroupInfo)
	}

	var (
		treeBytes  []byte
		predecoded *RatchetTree
	)
	if hasInline {
		treeBytes = cloneBytes(welcome.RatchetTree)
		if welcome.Tree != nil {
			cloned := cloneTree(*welcome.Tree)
			predecoded = &cloned
		}
	} else {
		reference := welcome.TreeReference
		if len(reference.GroupID) > 0 && !bytes.Equal(reference.GroupID, groupInfo.GroupID) {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: tree reference group id", ErrGroupMismatch)
		}
		if reference.Epoch != groupInfo.Epoch {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: tree reference epoch %d, group info epoch %d", ErrEpochMismatch, reference.Epoch, groupInfo.Epoch)
		}
		if len(reference.TreeHash) == 0 || !bytes.Equal(reference.TreeHash, groupInfo.TreeHash) {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: tree reference digest", ErrTreeHashMismatch)
		}
		if a.fetcher == nil {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: no tree fetcher", ErrTreeFetch)
		}
		binding := cloneBytes(reference.Binding)
		if len(binding) == 0 {
			binding = cloneBytes(welcomeID)
		}
		request := TreeFetchRequest{
			GroupID:     cloneBytes(groupInfo.GroupID),
			Epoch:       groupInfo.Epoch,
			CipherSuite: groupInfo.CipherSuite,
			TreeHash:    cloneBytes(groupInfo.TreeHash),
			WelcomeID:   cloneBytes(welcomeID),
			Binding:     cloneBytes(binding),
		}
		material, err := a.fetcher.FetchRatchetTree(ctx, request)
		if err != nil {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: %v", ErrTreeFetch, err)
		}
		// Copy before hashing or decoding. The callback may be backed by a
		// shared cache that another goroutine replaces immediately.
		treeBytes = cloneBytes(material.Bytes)
		if len(treeBytes) == 0 {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: empty external tree", ErrTreeFetch)
		}
		if !bytes.Equal(material.Binding, request.Binding) {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: response binding", ErrTreeBinding)
		}
		if !bytes.Equal(material.Digest, groupInfo.TreeHash) {
			return nil, RatchetTree{}, nil, fmt.Errorf("%w: response digest", ErrTreeHashMismatch)
		}
		if material.Tree != nil {
			cloned := cloneTree(*material.Tree)
			predecoded = &cloned
		}
	}

	actualHash := a.crypto.HashRatchetTree(treeBytes)
	if len(actualHash) == 0 {
		return nil, RatchetTree{}, nil, fmt.Errorf("%w: crypto returned empty tree hash", ErrNotConfigured)
	}
	if !bytes.Equal(actualHash, groupInfo.TreeHash) {
		return nil, RatchetTree{}, nil, fmt.Errorf("%w: bytes do not match group info", ErrTreeHashMismatch)
	}
	decoded, err := a.crypto.DecodeRatchetTree(treeBytes)
	if err != nil {
		return nil, RatchetTree{}, nil, fmt.Errorf("%w: %v", ErrTreeDecode, err)
	}
	if predecoded != nil && !treesEqual(decoded, *predecoded) {
		return nil, RatchetTree{}, nil, fmt.Errorf("%w: decoded tree differs from supplied tree", ErrTreeDecode)
	}
	return treeBytes, cloneTree(decoded), cloneBytes(actualHash), nil
}

func (a *Admitter) authorizeSigner(groupInfo GroupInfo, tree RatchetTree) (LeafNode, error) {
	if uint64(groupInfo.Signer) >= uint64(len(tree.Leaves)) {
		return LeafNode{}, fmt.Errorf("%w: signer %d", ErrSignerNotMember, groupInfo.Signer)
	}
	signer := tree.Leaves[groupInfo.Signer]
	if signer.Blank {
		return LeafNode{}, fmt.Errorf("%w: blank leaf %d", ErrSignerNotAuthorized, groupInfo.Signer)
	}
	if signer.Credential.IsZero() {
		return LeafNode{}, fmt.Errorf("%w: missing credential at leaf %d", ErrSignerNotAuthorized, groupInfo.Signer)
	}
	if len(signer.SignatureKey) == 0 {
		return LeafNode{}, fmt.Errorf("%w: missing signature key at leaf %d", ErrSignerNotAuthorized, groupInfo.Signer)
	}
	if groupInfo.SignerCredential != nil && !groupInfo.SignerCredential.Equal(signer.Credential) {
		return LeafNode{}, fmt.Errorf("%w: signer credential mismatch", ErrSignerNotAuthorized)
	}
	if a.signerPolicy != nil {
		if err := a.signerPolicy.AuthorizeGroupInfoSigner(groupInfo, cloneTree(tree), cloneLeaf(signer)); err != nil {
			return LeafNode{}, fmt.Errorf("%w: %v", ErrSignerNotAuthorized, err)
		}
	}
	return cloneLeaf(signer), nil
}

func validateKeyPackageLeaf(tree RatchetTree, keyPackage KeyPackage) error {
	if uint64(keyPackage.LeafIndex) >= uint64(len(tree.Leaves)) {
		return fmt.Errorf("%w: leaf index %d out of range", ErrKeyPackageLeafMismatch, keyPackage.LeafIndex)
	}
	leaf := tree.Leaves[keyPackage.LeafIndex]
	if leaf.Blank {
		return fmt.Errorf("%w: tree leaf %d is blank", ErrKeyPackageLeafMismatch, keyPackage.LeafIndex)
	}
	if !leaf.Credential.Equal(keyPackage.Leaf.Credential) {
		return fmt.Errorf("%w: credential", ErrKeyPackageLeafMismatch)
	}
	if len(keyPackage.Leaf.SignatureKey) > 0 && !bytes.Equal(leaf.SignatureKey, keyPackage.Leaf.SignatureKey) {
		return fmt.Errorf("%w: signature key", ErrKeyPackageLeafMismatch)
	}
	if len(keyPackage.Leaf.EncryptionKey) > 0 && !bytes.Equal(leaf.EncryptionKey, keyPackage.Leaf.EncryptionKey) {
		return fmt.Errorf("%w: encryption key", ErrKeyPackageLeafMismatch)
	}
	return nil
}

func (a *Admitter) collectCandidate(ctx context.Context, welcome *Welcome, groupInfo GroupInfo, keyPackage KeyPackage, keyPackageRef []byte, localParticipant LocalParticipant) (GroupSecrets, map[string][]byte, error) {
	type decrypted struct {
		index  int
		secret GroupSecrets
	}

	seenRefs := make(map[string]struct{}, len(welcome.Secrets))
	var localEntries []decrypted
	var all []decrypted

	for i, encrypted := range welcome.Secrets {
		if err := ctx.Err(); err != nil {
			return GroupSecrets{}, nil, err
		}
		if len(encrypted.KeyPackageRef) == 0 || len(encrypted.Ciphertext) == 0 {
			return GroupSecrets{}, nil, fmt.Errorf("%w: malformed encrypted secret %d", ErrInvalidWelcome, i)
		}
		refKey := string(encrypted.KeyPackageRef)
		if _, exists := seenRefs[refKey]; exists {
			return GroupSecrets{}, nil, fmt.Errorf("%w: duplicate key package reference in entry %d", ErrAmbiguousCandidate, i)
		}
		seenRefs[refKey] = struct{}{}

		isLocal := bytes.Equal(encrypted.KeyPackageRef, keyPackageRef)
		secret, err := a.crypto.DecryptGroupSecrets(encrypted, keyPackage)
		if err != nil {
			if isLocal {
				return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d: %v", ErrSecretDecryption, i, err)
			}
			continue
		}
		if !bytes.Equal(secret.KeyPackageRef, encrypted.KeyPackageRef) {
			return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d key package reference", ErrMixedCandidate, i)
		}
		if !bytes.Equal(secret.GroupID, groupInfo.GroupID) {
			return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d group id", ErrMixedCandidate, i)
		}
		if secret.Epoch != groupInfo.Epoch {
			return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d epoch %d", ErrMixedCandidate, i, secret.Epoch)
		}
		if secret.CipherSuite != groupInfo.CipherSuite {
			return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d cipher suite %d", ErrMixedCandidate, i, secret.CipherSuite)
		}
		if len(secret.JoinerSecret) == 0 {
			return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d empty joiner secret", ErrInvalidGroupSecrets, i)
		}
		pskIDs, err := normalizePSKIDs(secret.PSKIDs)
		if err != nil {
			return GroupSecrets{}, nil, fmt.Errorf("entry %d: %w", i, err)
		}
		secret.PSKIDs = pskIDs
		secret.GroupID = cloneBytes(secret.GroupID)
		secret.KeyPackageRef = cloneBytes(secret.KeyPackageRef)
		secret.JoinerSecret = cloneBytes(secret.JoinerSecret)
		secret.PathSecret = cloneBytes(secret.PathSecret)

		entry := decrypted{index: i, secret: secret}
		all = append(all, entry)
		if isLocal {
			localEntries = append(localEntries, entry)
		}
	}

	if len(localEntries) == 0 {
		return GroupSecrets{}, nil, fmt.Errorf("%w: %x", ErrNoMatchingSecret, keyPackageRef)
	}
	if len(localEntries) > 1 {
		return GroupSecrets{}, nil, fmt.Errorf("%w: %d local entries", ErrAmbiguousCandidate, len(localEntries))
	}
	chosen := localEntries[0].secret

	// Every secret that could be opened must describe the same Welcome. A
	// valid Welcome carries one joiner secret and one PSK set for all members;
	// disagreement indicates a substituted GroupInfo or mixed secrets.
	for _, entry := range all {
		if !bytes.Equal(entry.secret.JoinerSecret, chosen.JoinerSecret) {
			return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d joiner secret", ErrMixedCandidate, entry.index)
		}
		if !bytes.Equal(canonicalPSKIDs(entry.secret.PSKIDs), canonicalPSKIDs(chosen.PSKIDs)) {
			return GroupSecrets{}, nil, fmt.Errorf("%w: entry %d psk ids", ErrMixedCandidate, entry.index)
		}
	}

	required, err := normalizePSKIDs(groupInfo.RequiredPSKIDs)
	if err != nil {
		return GroupSecrets{}, nil, fmt.Errorf("%w: %v", ErrInvalidGroupInfo, err)
	}
	available := make(map[string]struct{}, len(chosen.PSKIDs))
	for _, id := range chosen.PSKIDs {
		available[string(id)] = struct{}{}
	}
	for _, id := range required {
		if _, ok := available[string(id)]; !ok {
			return GroupSecrets{}, nil, fmt.Errorf("%w: missing required psk %x", ErrMissingPSK, id)
		}
	}

	psks := make(map[string][]byte, len(chosen.PSKIDs))
	for _, id := range chosen.PSKIDs {
		psk, ok := localParticipant.PSKs[string(id)]
		if !ok || len(psk) == 0 {
			return GroupSecrets{}, nil, fmt.Errorf("%w: %x", ErrMissingPSK, id)
		}
		psks[string(id)] = cloneBytes(psk)
	}
	return chosen, psks, nil
}
