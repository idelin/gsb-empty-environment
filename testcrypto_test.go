package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

type testCrypto struct {
	mu      sync.Mutex
	next    uint64
	secrets map[string]GroupSecrets
}

func newTestCrypto() *testCrypto {
	return &testCrypto{secrets: make(map[string]GroupSecrets)}
}

func testDigest(domain string, parts ...[]byte) []byte {
	h := sha256.New()
	h.Write([]byte("netsec-test-v1"))
	h.Write(appendString(nil, domain))
	for _, part := range parts {
		h.Write(appendBytes(nil, part))
	}
	return h.Sum(nil)
}

func (c *testCrypto) Digest(domain string, parts ...[]byte) []byte {
	return testDigest(domain, parts...)
}

func (c *testCrypto) DecryptGroupSecrets(enc EncryptedGroupSecrets, kp KeyPackage) (GroupSecrets, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	secret, ok := c.secrets[hex.EncodeToString(enc.Ciphertext)]
	if !ok {
		return GroupSecrets{}, fmt.Errorf("test crypto: unknown ciphertext")
	}
	if !bytes.Equal(secret.KeyPackageRef, enc.KeyPackageRef) {
		return GroupSecrets{}, fmt.Errorf("test crypto: ciphertext belongs to another key package")
	}
	return cloneTestGroupSecrets(secret), nil
}

func (c *testCrypto) seal(ref []byte, secret GroupSecrets) EncryptedGroupSecrets {
	c.mu.Lock()
	c.next++
	nonce := c.next
	c.mu.Unlock()

	ciphertext := testDigest("ciphertext",
		ref,
		secret.JoinerSecret,
		secret.GroupID,
		[]byte(fmt.Sprintf("%d", secret.Epoch)),
		canonicalPSKIDs(secret.PSKIDs),
		[]byte(fmt.Sprintf("%d", nonce)),
	)
	c.mu.Lock()
	c.secrets[hex.EncodeToString(ciphertext)] = cloneTestGroupSecrets(secret)
	c.mu.Unlock()
	return EncryptedGroupSecrets{KeyPackageRef: cloneBytes(ref), Ciphertext: ciphertext}
}

func cloneTestGroupSecrets(secret GroupSecrets) GroupSecrets {
	secret.GroupID = cloneBytes(secret.GroupID)
	secret.KeyPackageRef = cloneBytes(secret.KeyPackageRef)
	secret.JoinerSecret = cloneBytes(secret.JoinerSecret)
	secret.PathSecret = cloneBytes(secret.PathSecret)
	secret.PSKIDs = cloneStrings(secret.PSKIDs)
	return secret
}

func testGroupInfoSignature(groupInfo GroupInfo, signer LeafNode) []byte {
	unsigned := cloneGroupInfo(groupInfo)
	unsigned.Signature = nil
	return testDigest("group-info-signature", signer.SignatureKey, canonicalGroupInfo(unsigned))
}

func (c *testCrypto) VerifyGroupInfo(groupInfo GroupInfo, signer LeafNode, treeHash []byte) error {
	expected := testGroupInfoSignature(groupInfo, signer)
	if !bytes.Equal(expected, groupInfo.Signature) {
		return fmt.Errorf("test crypto: bad signature")
	}
	if !bytes.Equal(groupInfo.TreeHash, treeHash) {
		return fmt.Errorf("test crypto: signature tree hash differs")
	}
	return nil
}

func (c *testCrypto) VerifyKeyPackageCredential(kp KeyPackage, privateKey any) error {
	expected := "private:" + hex.EncodeToString(kp.Leaf.Credential.PublicKey)
	switch key := privateKey.(type) {
	case []byte:
		if string(key) != expected {
			return fmt.Errorf("test crypto: private key mismatch")
		}
	case string:
		if key != expected {
			return fmt.Errorf("test crypto: private key mismatch")
		}
	default:
		return fmt.Errorf("test crypto: unsupported private key type")
	}
	return nil
}

func testTranscriptHash(input TranscriptInput) []byte {
	return testDigest("transcript",
		input.GroupID,
		[]byte(fmt.Sprintf("%d", input.Epoch)),
		[]byte(fmt.Sprintf("%d", input.CipherSuite)),
		input.PriorTranscriptHash,
		input.TreeHash,
		canonicalExtensions(input.Extensions),
		input.ConfirmationTag,
	)
}

func (c *testCrypto) ComputeTranscriptHash(input TranscriptInput) ([]byte, error) {
	return testTranscriptHash(input), nil
}

func testJoinerSecret(input JoinerSecretInput) []byte {
	parts := [][]byte{
		input.GroupID,
		[]byte(fmt.Sprintf("%d", input.Epoch)),
		[]byte(fmt.Sprintf("%d", input.CipherSuite)),
		input.KeyPackageRef,
		input.TreeHash,
		input.TranscriptHash,
		canonicalExtensions(input.Extensions),
	}
	sortedIDs := cloneStrings(input.PSKIDs)
	sort.Slice(sortedIDs, func(i, j int) bool {
		return bytes.Compare(sortedIDs[i], sortedIDs[j]) < 0
	})
	parts = append(parts, canonicalPSKIDs(sortedIDs))
	for _, id := range sortedIDs {
		psk, ok := input.PSKs[string(id)]
		if !ok {
			parts = append(parts, []byte("MISSING"))
		} else {
			parts = append(parts, psk)
		}
	}
	return testDigest("joiner-secret", parts...)
}

func (c *testCrypto) VerifyJoinerSecret(input JoinerSecretInput) error {
	expected := testJoinerSecret(input)
	if !bytes.Equal(expected, input.JoinerSecret) {
		return fmt.Errorf("test crypto: joiner secret does not match group context")
	}
	return nil
}

func (c *testCrypto) HashRatchetTree(tree []byte) []byte {
	sum := sha256.Sum256(tree)
	return sum[:]
}

func (c *testCrypto) DecodeRatchetTree(tree []byte) (RatchetTree, error) {
	var decoded RatchetTree
	if err := json.Unmarshal(tree, &decoded); err != nil {
		return RatchetTree{}, err
	}
	return decoded, nil
}

func encodeTestTree(tree RatchetTree) []byte {
	encoded, err := json.Marshal(tree)
	if err != nil {
		panic(err)
	}
	return encoded
}

func defaultTestTree() RatchetTree {
	return RatchetTree{
		Leaves: []LeafNode{
			{
				Index:         0,
				Credential:    Credential{Type: 1, Identity: []byte("admin"), PublicKey: []byte("admin-pub")},
				SignatureKey:  []byte("admin-signature"),
				EncryptionKey: []byte("admin-encryption"),
			},
			{
				Index:         1,
				Credential:    Credential{Type: 1, Identity: []byte("joiner"), PublicKey: []byte("joiner-pub")},
				SignatureKey:  []byte("joiner-signature"),
				EncryptionKey: []byte("joiner-encryption"),
			},
		},
		Extensions: []Extension{{Type: 1, Data: []byte("group-context")}},
	}
}

type fixtureOptions struct {
	groupID         []byte
	epoch           uint64
	keyPackageRef   []byte
	targetGroup     []byte
	cipherSuite     CipherSuite
	signer          uint32
	leafIndex       uint32
	tree            *RatchetTree
	pskIDs          [][]byte
	requiredPSKIDs  [][]byte
	psks            map[string][]byte
	priorTranscript []byte
	confirmationTag []byte
	external        bool
	binding         []byte
	notAfter        time.Time
}

type fixture struct {
	crypto          *testCrypto
	store           *MemoryStore
	kp              KeyPackage
	local           LocalParticipant
	welcome         *Welcome
	tree            RatchetTree
	treeBytes       []byte
	treeHash        []byte
	groupID         []byte
	epoch           uint64
	suite           CipherSuite
	psks            map[string][]byte
	secret          GroupSecrets
	confirmationTag []byte
}

func newFixture(t *testing.T, opts fixtureOptions) *fixture {
	t.Helper()

	groupID := cloneBytes(opts.groupID)
	if len(groupID) == 0 {
		groupID = []byte("group-A")
	}
	epoch := opts.epoch
	if epoch == 0 {
		epoch = 1
	}
	suite := opts.cipherSuite
	if suite == 0 {
		suite = 1
	}
	keyPackageRef := cloneBytes(opts.keyPackageRef)
	if len(keyPackageRef) == 0 {
		keyPackageRef = []byte("kp-1")
	}
	leafIndex := opts.leafIndex
	if leafIndex == 0 && opts.tree == nil {
		leafIndex = 1
	}
	tree := defaultTestTree()
	if opts.tree != nil {
		tree = cloneTree(*opts.tree)
	}
	psks := clonePSKMap(opts.psks)
	confirmationTag := cloneBytes(opts.confirmationTag)
	if len(confirmationTag) == 0 {
		confirmationTag = []byte("confirmation")
	}

	crypto := newTestCrypto()
	treeBytes := encodeTestTree(tree)
	treeHash := crypto.HashRatchetTree(treeBytes)

	groupInfo := GroupInfo{
		GroupID:        cloneBytes(groupID),
		Epoch:          epoch,
		CipherSuite:    suite,
		TreeHash:       cloneBytes(treeHash),
		Extensions:     cloneExtensions(tree.Extensions),
		RequiredPSKIDs: cloneStrings(opts.requiredPSKIDs),
		Signer:         opts.signer,
	}
	groupInfo.TranscriptHash = testTranscriptHash(TranscriptInput{
		GroupID:             cloneBytes(groupID),
		Epoch:               epoch,
		CipherSuite:         suite,
		PriorTranscriptHash: cloneBytes(opts.priorTranscript),
		TreeHash:            cloneBytes(treeHash),
		Extensions:          cloneExtensions(tree.Extensions),
		ConfirmationTag:     cloneBytes(confirmationTag),
	})
	if int(groupInfo.Signer) < len(tree.Leaves) {
		groupInfo.Signature = testGroupInfoSignature(groupInfo, tree.Leaves[groupInfo.Signer])
	} else {
		groupInfo.Signature = []byte("invalid-signer")
	}

	var leaf LeafNode
	if int(leafIndex) < len(tree.Leaves) {
		leaf = cloneLeaf(tree.Leaves[leafIndex])
	}
	notAfter := opts.notAfter
	if notAfter.IsZero() {
		notAfter = testNow().Add(time.Hour)
	}
	keyPackage := KeyPackage{
		Reference:   cloneBytes(keyPackageRef),
		GroupID:     cloneBytes(opts.targetGroup),
		CipherSuite: suite,
		LeafIndex:   leafIndex,
		Leaf:        leaf,
		NotAfter:    notAfter,
	}

	secret := GroupSecrets{
		GroupID:       cloneBytes(groupID),
		Epoch:         epoch,
		CipherSuite:   suite,
		KeyPackageRef: cloneBytes(keyPackageRef),
		PSKIDs:        cloneStrings(opts.pskIDs),
		PathSecret:    []byte("path-secret"),
	}
	secret.JoinerSecret = testJoinerSecret(JoinerSecretInput{
		GroupID:        cloneBytes(groupID),
		Epoch:          epoch,
		CipherSuite:    suite,
		KeyPackageRef:  cloneBytes(keyPackageRef),
		PSKIDs:         cloneStrings(opts.pskIDs),
		PSKs:           clonePSKMap(psks),
		TreeHash:       cloneBytes(treeHash),
		TranscriptHash: cloneBytes(groupInfo.TranscriptHash),
		Extensions:     cloneExtensions(tree.Extensions),
	})

	privateKey := []byte("private:" + hex.EncodeToString(leaf.Credential.PublicKey))
	seed := crypto.seal(keyPackageRef, secret)
	welcome := &Welcome{
		GroupInfo:           groupInfo,
		Secrets:             []EncryptedGroupSecrets{seed},
		RatchetTree:         cloneBytes(treeBytes),
		Tree:                nil,
		PriorTranscriptHash: cloneBytes(opts.priorTranscript),
		ConfirmationTag:     cloneBytes(confirmationTag),
	}
	clonedTree := cloneTree(tree)
	welcome.Tree = &clonedTree
	if opts.external {
		welcome.RatchetTree = nil
		welcome.Tree = nil
		welcome.TreeReference = &RatchetTreeReference{
			GroupID:  cloneBytes(groupID),
			Epoch:    epoch,
			TreeHash: cloneBytes(treeHash),
			Binding:  cloneBytes(opts.binding),
		}
	}

	return &fixture{
		crypto:          crypto,
		store:           NewMemoryStore(),
		kp:              keyPackage,
		local:           LocalParticipant{KeyPackage: keyPackage, PrivateKey: privateKey, PSKs: clonePSKMap(psks)},
		welcome:         welcome,
		tree:            tree,
		treeBytes:       treeBytes,
		treeHash:        treeHash,
		groupID:         groupID,
		epoch:           epoch,
		suite:           suite,
		psks:            psks,
		secret:          secret,
		confirmationTag: confirmationTag,
	}
}

func testNow() time.Time {
	return time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
}

// refresh recomputes transcript hash, signature and joiner secret after a test
// deliberately changes signed group state.
func (f *fixture) refresh() {
	f.welcome.GroupInfo.TranscriptHash = testTranscriptHash(TranscriptInput{
		GroupID:             cloneBytes(f.groupID),
		Epoch:               f.welcome.GroupInfo.Epoch,
		CipherSuite:         f.welcome.GroupInfo.CipherSuite,
		PriorTranscriptHash: cloneBytes(f.welcome.PriorTranscriptHash),
		TreeHash:            cloneBytes(f.welcome.GroupInfo.TreeHash),
		Extensions:          cloneExtensions(f.welcome.GroupInfo.Extensions),
		ConfirmationTag:     cloneBytes(f.welcome.ConfirmationTag),
	})
	if int(f.welcome.GroupInfo.Signer) < len(f.tree.Leaves) {
		f.welcome.GroupInfo.Signature = testGroupInfoSignature(f.welcome.GroupInfo, f.tree.Leaves[f.welcome.GroupInfo.Signer])
	}
	f.secret.JoinerSecret = testJoinerSecret(JoinerSecretInput{
		GroupID:        cloneBytes(f.welcome.GroupInfo.GroupID),
		Epoch:          f.welcome.GroupInfo.Epoch,
		CipherSuite:    f.welcome.GroupInfo.CipherSuite,
		KeyPackageRef:  cloneBytes(f.kp.Reference),
		PSKIDs:         cloneStrings(f.secret.PSKIDs),
		PSKs:           clonePSKMap(f.local.PSKs),
		TreeHash:       cloneBytes(f.welcome.GroupInfo.TreeHash),
		TranscriptHash: cloneBytes(f.welcome.GroupInfo.TranscriptHash),
		Extensions:     cloneExtensions(f.welcome.GroupInfo.Extensions),
	})
	f.welcome.Secrets[0] = f.crypto.seal(f.kp.Reference, f.secret)
}

func (f *fixture) setTree(tree RatchetTree) {
	f.tree = cloneTree(tree)
	f.treeBytes = encodeTestTree(tree)
	f.treeHash = f.crypto.HashRatchetTree(f.treeBytes)
	f.welcome.GroupInfo.TreeHash = cloneBytes(f.treeHash)
	if f.welcome.TreeReference != nil {
		f.welcome.TreeReference.TreeHash = cloneBytes(f.treeHash)
		f.welcome.RatchetTree = nil
		f.welcome.Tree = nil
	} else {
		f.welcome.RatchetTree = cloneBytes(f.treeBytes)
		cloned := cloneTree(tree)
		f.welcome.Tree = &cloned
	}
	f.refresh()
}

func (f *fixture) externalize(binding []byte) {
	f.welcome.RatchetTree = nil
	f.welcome.Tree = nil
	f.welcome.TreeReference = &RatchetTreeReference{
		GroupID:  cloneBytes(f.groupID),
		Epoch:    f.epoch,
		TreeHash: cloneBytes(f.treeHash),
		Binding:  cloneBytes(binding),
	}
}

func (f *fixture) admitter(opts ...Option) *Admitter {
	options := []Option{WithStore(f.store), WithClock(testNow)}
	options = append(options, opts...)
	return NewAdmitter(f.crypto, options...)
}
