package netsec

// OPC UA security policy URIs used by this package.
const (
	SecurityPolicyNone                = "http://opcfoundation.org/UA/SecurityPolicy#None"
	SecurityPolicyBasic128Rsa15       = "http://opcfoundation.org/UA/SecurityPolicy#Basic128Rsa15"
	SecurityPolicyBasic256            = "http://opcfoundation.org/UA/SecurityPolicy#Basic256"
	SecurityPolicyBasic256Sha256      = "http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256"
	SecurityPolicyAes128Sha256RsaOaep = "http://opcfoundation.org/UA/SecurityPolicy#Aes128_Sha256_RsaOaep"
	SecurityPolicyAes256Sha256RsaPss  = "http://opcfoundation.org/UA/SecurityPolicy#Aes256_Sha256_RsaPss"
	SecurityPolicyECCNistP256         = "http://opcfoundation.org/UA/SecurityPolicy#ECC_nistP256"
	SecurityPolicyECCNistP384         = "http://opcfoundation.org/UA/SecurityPolicy#ECC_nistP384"
	SecurityPolicyECCBrainpoolP256R1  = "http://opcfoundation.org/UA/SecurityPolicy#ECC_brainpoolP256r1"
	SecurityPolicyECCBrainpoolP384R1  = "http://opcfoundation.org/UA/SecurityPolicy#ECC_brainpoolP384r1"
	SecurityPolicyECCCurve25519       = "http://opcfoundation.org/UA/SecurityPolicy#ECC_curve25519"
	SecurityPolicyECCCurve448         = "http://opcfoundation.org/UA/SecurityPolicy#ECC_curve448"
)

// TokenType is the OPC UA UserTokenType of an identity token.
type TokenType string

const (
	TokenTypeAnonymous   TokenType = "Anonymous"
	TokenTypeUserName    TokenType = "UserName"
	TokenTypeCertificate TokenType = "Certificate"
	TokenTypeIssuedToken TokenType = "IssuedToken"

	// Aliases kept for callers that use slightly different OPC UA spellings.
	TokenTypeUsername = TokenTypeUserName
	TokenTypeX509     = TokenTypeCertificate
)

// UserTokenPolicy is the subset of OPC UA UserTokenPolicy needed by
// ActivateSession admission.
//
// SecurityPolicyURI may be empty. In that case the endpoint's
// SecurityPolicyURI applies, as required by OPC UA.
type UserTokenPolicy struct {
	PolicyID          string
	TokenType         TokenType
	SecurityPolicyURI string
}

// Endpoint is the subset of an OPC UA EndpointDescription needed by
// ActivateSession admission.
type Endpoint struct {
	SecurityPolicyURI string
	UserTokenPolicies []UserTokenPolicy
	ServerCertificate []byte
}

// ActivateSessionRequest contains the admission-relevant fields of an OPC UA
// ActivateSession request together with the server nonce from CreateSession.
//
// Certificate and Signature correspond to X509IdentityToken.CertificateData,
// ClientSignature.Algorithm/Signature. ClientCertificate is the certificate
// established by CreateSession and may be left empty when the caller does not
// have it available.
type ActivateSessionRequest struct {
	PolicyID  string
	TokenType TokenType

	// UserNameIdentityToken fields.
	UserName            string
	Password            []byte
	EncryptionAlgorithm string

	// X509IdentityToken / ActivateSession fields.
	Certificate        []byte
	ClientCertificate  []byte
	SignatureAlgorithm string
	Signature          []byte

	// ServerNonce is the nonce returned by CreateSession.
	ServerNonce []byte
}

// Request is a short alias for ActivateSessionRequest.
type Request = ActivateSessionRequest

// EffectiveTokenType returns the explicit token type, or infers one from the
// populated identity token fields when TokenType was omitted.
func (r ActivateSessionRequest) EffectiveTokenType() TokenType {
	if r.TokenType != "" {
		return r.TokenType
	}
	switch {
	case len(r.Certificate) > 0:
		return TokenTypeCertificate
	case len(r.Password) > 0 || r.UserName != "":
		return TokenTypeUserName
	default:
		return TokenTypeAnonymous
	}
}
