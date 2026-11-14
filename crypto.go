package netsec

import "time"

// Crypto contains all cryptographic operations used by Welcome admission.
// Implementations may use any MLS library and must authenticate ciphertexts,
// signatures, digests and credentials. The component never performs
// cryptography itself.
type Crypto interface {
	// Digest returns a deterministic, collision-resistant digest over domain
	// and the length-delimited parts.
	Digest(domain string, parts ...[]byte) []byte

	// DecryptGroupSecrets authenticates and decrypts enc for kp. It must fail
	// when enc does not belong to kp or the ciphertext is invalid.
	DecryptGroupSecrets(enc EncryptedGroupSecrets, kp KeyPackage) (GroupSecrets, error)

	// VerifyGroupInfo verifies gi.Signature using signer's signature key. The
	// signature must cover the complete GroupInfo, including TreeHash,
	// TranscriptHash and Extensions.
	VerifyGroupInfo(gi GroupInfo, signer LeafNode, treeHash []byte) error

	// VerifyKeyPackageCredential proves that the private key held locally
	// corresponds to kp.Leaf.Credential.
	VerifyKeyPackageCredential(kp KeyPackage, privateKey any) error

	// VerifyJoinerSecret verifies that the decrypted joiner secret and PSKs
	// derive the key schedule for this exact group context.
	VerifyJoinerSecret(input JoinerSecretInput) error

	// ComputeTranscriptHash computes the confirmed transcript hash for input.
	ComputeTranscriptHash(input TranscriptInput) ([]byte, error)

	// HashRatchetTree hashes the canonical encoded ratchet tree bytes.
	HashRatchetTree(tree []byte) []byte

	// DecodeRatchetTree decodes the canonical ratchet tree bytes.
	DecodeRatchetTree(tree []byte) (RatchetTree, error)
}

// CryptoFuncs adapts functions to Crypto. A nil function makes the
// corresponding operation fail closed.
type CryptoFuncs struct {
	DigestFunc                     func(domain string, parts ...[]byte) []byte
	DecryptGroupSecretsFunc        func(enc EncryptedGroupSecrets, kp KeyPackage) (GroupSecrets, error)
	VerifyGroupInfoFunc            func(gi GroupInfo, signer LeafNode, treeHash []byte) error
	VerifyKeyPackageCredentialFunc func(kp KeyPackage, privateKey any) error
	VerifyJoinerSecretFunc         func(input JoinerSecretInput) error
	ComputeTranscriptHashFunc      func(input TranscriptInput) ([]byte, error)
	HashRatchetTreeFunc            func(tree []byte) []byte
	DecodeRatchetTreeFunc          func(tree []byte) (RatchetTree, error)
}

func (f CryptoFuncs) Digest(domain string, parts ...[]byte) []byte {
	if f.DigestFunc == nil {
		return nil
	}
	return f.DigestFunc(domain, parts...)
}

func (f CryptoFuncs) DecryptGroupSecrets(enc EncryptedGroupSecrets, kp KeyPackage) (GroupSecrets, error) {
	if f.DecryptGroupSecretsFunc == nil {
		return GroupSecrets{}, ErrNotConfigured
	}
	return f.DecryptGroupSecretsFunc(enc, kp)
}

func (f CryptoFuncs) VerifyGroupInfo(gi GroupInfo, signer LeafNode, treeHash []byte) error {
	if f.VerifyGroupInfoFunc == nil {
		return ErrNotConfigured
	}
	return f.VerifyGroupInfoFunc(gi, signer, treeHash)
}

func (f CryptoFuncs) VerifyKeyPackageCredential(kp KeyPackage, privateKey any) error {
	if f.VerifyKeyPackageCredentialFunc == nil {
		return ErrNotConfigured
	}
	return f.VerifyKeyPackageCredentialFunc(kp, privateKey)
}

func (f CryptoFuncs) VerifyJoinerSecret(input JoinerSecretInput) error {
	if f.VerifyJoinerSecretFunc == nil {
		return ErrNotConfigured
	}
	return f.VerifyJoinerSecretFunc(input)
}

func (f CryptoFuncs) ComputeTranscriptHash(input TranscriptInput) ([]byte, error) {
	if f.ComputeTranscriptHashFunc == nil {
		return nil, ErrNotConfigured
	}
	return f.ComputeTranscriptHashFunc(input)
}

func (f CryptoFuncs) HashRatchetTree(tree []byte) []byte {
	if f.HashRatchetTreeFunc == nil {
		return nil
	}
	return f.HashRatchetTreeFunc(tree)
}

func (f CryptoFuncs) DecodeRatchetTree(tree []byte) (RatchetTree, error) {
	if f.DecodeRatchetTreeFunc == nil {
		return RatchetTree{}, ErrNotConfigured
	}
	return f.DecodeRatchetTreeFunc(tree)
}

// GroupInfoSignerPolicy adds application policy to the structural check that a
// GroupInfo signer is a non-blank tree member with a signature key.
type GroupInfoSignerPolicy interface {
	AuthorizeGroupInfoSigner(gi GroupInfo, tree RatchetTree, signer LeafNode) error
}

// GroupInfoSignerPolicyFunc adapts a function to GroupInfoSignerPolicy.
type GroupInfoSignerPolicyFunc func(gi GroupInfo, tree RatchetTree, signer LeafNode) error

func (f GroupInfoSignerPolicyFunc) AuthorizeGroupInfoSigner(gi GroupInfo, tree RatchetTree, signer LeafNode) error {
	return f(gi, tree, signer)
}

// Option configures an Admitter.
type Option func(*Admitter)

// WithStore replaces the default in-memory persistent store.
func WithStore(store Store) Option {
	return func(a *Admitter) {
		if store != nil {
			a.store = store
		}
	}
}

// WithTreeFetcher installs the callback used for external ratchet trees.
func WithTreeFetcher(fetcher RatchetTreeFetcher) Option {
	return func(a *Admitter) {
		a.fetcher = fetcher
	}
}

// WithClock replaces time.Now. It is primarily useful for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(a *Admitter) {
		if now != nil {
			a.now = now
		}
	}
}

// WithSignerPolicy adds application-level GroupInfo signer authorization.
func WithSignerPolicy(policy GroupInfoSignerPolicy) Option {
	return func(a *Admitter) {
		a.signerPolicy = policy
	}
}
