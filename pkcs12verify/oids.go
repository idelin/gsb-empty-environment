package pkcs12verify

import "encoding/asn1"

// ASN.1 object identifiers used by PKCS#7, PKCS#9, PKCS#12 and PKCS#5.
var (
	// PKCS#7 / PKCS#12 content types.
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidEncryptedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}

	// PKCS#12 bag identifiers.
	oidKeyBag            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 1}
	oidPKCS8ShroudedKBag = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 2}
	oidCertBag           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 3}
	oidCRLBag            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 4}
	oidSecretBag         = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 5}
	oidSafeContentsBag   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 6}
	oidX509Certificate   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 22, 1}
	oidSDSICertificate   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 22, 2}

	// PKCS#12 legacy password-based MAC and encryption (all SHA-1/RC2/3DES).
	oidPbeWithSHAAnd128RC4   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 1}
	oidPbeWithSHAAnd40BitRC4 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 2}
	oidPbeWithSHAAnd3Key3DES = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 3}
	oidPbeWithSHAAnd2Key3DES = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 4}
	oidPbeWithSHAAnd128RC2   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 5}
	oidPbeWithSHAAnd40BitRC2 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 6}

	// PKCS#5.
	oidPbeWithMD2AndDES  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 1}
	oidPbeWithMD5AndDES  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 3}
	oidPbeWithSHA1AndDES = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 10}
	oidPBES2             = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBKDF2            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
	oidPBMAC1            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 14}

	// PBKDF2 PRF algorithms (RFC 8018 Appendix B.2).
	oidHMACWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 7}
	oidHMACWithSHA224 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 8}
	oidHMACWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}
	oidHMACWithSHA384 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 10}
	oidHMACWithSHA512 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 11}

	// Digest algorithms.
	oidMD5    = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	oidSHA1   = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA224 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}

	// AES (NIST algorithms, pkcs5PBES2 schemes).
	oidAES128CBC = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 2}
	oidAES192CBC = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 22}
	oidAES256CBC = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
	oidAES128GCM = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 6}
	oidAES192GCM = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 26}
	oidAES256GCM = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 46}

	// Additional legacy ciphers that must never be accepted.
	oidDESCBC  = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 7}
	oidRC2CBC  = asn1.ObjectIdentifier{1, 2, 840, 113549, 3, 2}
	oidRC4     = asn1.ObjectIdentifier{1, 2, 840, 113549, 3, 4}
	oid3DESCBC = asn1.ObjectIdentifier{1, 2, 840, 113535, 3, 7}
)

// weakAlgorithms maps every explicitly known legacy / weak algorithm OID to a
// short human-readable description for diagnostics. Default-deny still applies
// to any OID that is neither here nor on an allow list.
var weakAlgorithms = map[string]string{
	oidPbeWithSHAAnd128RC4.String():   "pbeWithSHAAnd128BitRC4",
	oidPbeWithSHAAnd40BitRC4.String(): "pbeWithSHAAnd40BitRC4",
	oidPbeWithSHAAnd3Key3DES.String(): "pbeWithSHAAnd3-KeyTripleDES-CBC",
	oidPbeWithSHAAnd2Key3DES.String(): "pbeWithSHAAnd2-KeyTripleDES-CBC",
	oidPbeWithSHAAnd128RC2.String():   "pbeWithSHAAnd128BitRC2-CBC",
	oidPbeWithSHAAnd40BitRC2.String(): "pbeWithSHAAnd40BitRC2-CBC",
	oidPbeWithMD2AndDES.String():      "pbeWithMD2AndDES-CBC",
	oidPbeWithMD5AndDES.String():      "pbeWithMD5AndDES-CBC",
	oidPbeWithSHA1AndDES.String():     "pbeWithSHA1AndDES-CBC",
	oidDESCBC.String():                "desCBC",
	oidRC2CBC.String():                "rc2CBC",
	oidRC4.String():                   "rc4",
	oid3DESCBC.String():               "des-ede3-cbc",
	oidHMACWithSHA1.String():          "hmacWithSHA1",
	oidSHA1.String():                  "sha1",
	oidMD5.String():                   "md5",
}
