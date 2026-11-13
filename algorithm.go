package netsec

// PublicKeyAlgorithm is an OpenPGP public-key algorithm identifier.
type PublicKeyAlgorithm uint8

const (
	PubKeyRSAEncryptSign PublicKeyAlgorithm = 1
	PubKeyRSAEncryptOnly PublicKeyAlgorithm = 2
	PubKeyRSASignOnly    PublicKeyAlgorithm = 3
	PubKeyElGamalEncrypt PublicKeyAlgorithm = 16
	PubKeyDSA            PublicKeyAlgorithm = 17
	PubKeyECDH           PublicKeyAlgorithm = 18
	PubKeyECDSA          PublicKeyAlgorithm = 19
	PubKeyElGamalSign    PublicKeyAlgorithm = 20
	PubKeyEdDSA          PublicKeyAlgorithm = 22
	PubKeyX25519         PublicKeyAlgorithm = 25
	PubKeyX448           PublicKeyAlgorithm = 26
	PubKeyEd25519        PublicKeyAlgorithm = 27
	PubKeyEd448          PublicKeyAlgorithm = 28
)

// HashAlgorithm is an OpenPGP hash algorithm identifier.
type HashAlgorithm uint8

const (
	HashMD5       HashAlgorithm = 1
	HashSHA1      HashAlgorithm = 2
	HashRIPEMD160 HashAlgorithm = 3
	HashSHA256    HashAlgorithm = 8
	HashSHA384    HashAlgorithm = 9
	HashSHA512    HashAlgorithm = 10
	HashSHA224    HashAlgorithm = 11
	HashSHA3_256  HashAlgorithm = 12
	HashSHA3_512  HashAlgorithm = 14
)

// SignatureType is an OpenPGP signature type.
type SignatureType uint8

const (
	SigCertGeneric       SignatureType = 0x10
	SigCertPersona       SignatureType = 0x11
	SigCertCasual        SignatureType = 0x12
	SigCertPositive      SignatureType = 0x13
	SigSubkeyBinding     SignatureType = 0x18
	SigPrimaryKeyBinding SignatureType = 0x19
	SigDirectKey         SignatureType = 0x1f
	SigKeyRevocation     SignatureType = 0x20
	SigSubkeyRevocation  SignatureType = 0x28
	SigCertRevocation    SignatureType = 0x30
	SigTimestamp         SignatureType = 0x40
	SigThirdParty        SignatureType = 0x50
)

func (a PublicKeyAlgorithm) String() string {
	switch a {
	case PubKeyRSAEncryptSign:
		return "RSA"
	case PubKeyRSAEncryptOnly:
		return "RSA (encrypt-only)"
	case PubKeyRSASignOnly:
		return "RSA (sign-only)"
	case PubKeyElGamalEncrypt:
		return "ElGamal (encrypt-only)"
	case PubKeyDSA:
		return "DSA"
	case PubKeyECDH:
		return "ECDH"
	case PubKeyECDSA:
		return "ECDSA"
	case PubKeyElGamalSign:
		return "ElGamal (sign)"
	case PubKeyEdDSA:
		return "EdDSA"
	case PubKeyX25519:
		return "X25519"
	case PubKeyX448:
		return "X448"
	case PubKeyEd25519:
		return "Ed25519"
	case PubKeyEd448:
		return "Ed448"
	default:
		return "unknown public-key algorithm"
	}
}

func (a HashAlgorithm) String() string {
	switch a {
	case HashMD5:
		return "MD5"
	case HashSHA1:
		return "SHA-1"
	case HashRIPEMD160:
		return "RIPEMD-160"
	case HashSHA256:
		return "SHA-256"
	case HashSHA384:
		return "SHA-384"
	case HashSHA512:
		return "SHA-512"
	case HashSHA224:
		return "SHA-224"
	case HashSHA3_256:
		return "SHA3-256"
	case HashSHA3_512:
		return "SHA3-512"
	default:
		return "unknown hash algorithm"
	}
}
