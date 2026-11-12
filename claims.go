package netsec

// JWT types used in NATS claims.
const (
	TypeOperator = "operator"
	TypeAccount  = "account"
	TypeUser     = "user"
)

// Claims contains the common compact-JWT claims. JSON field names follow the
// NATS JWT wire format.
type Claims struct {
	JTI      string `json:"jti,omitempty"`
	IssuedAt int64  `json:"iat,omitempty"`
	Expires  int64  `json:"exp,omitempty"`
	Issuer   string `json:"iss,omitempty"`
	Subject  string `json:"sub,omitempty"`
	Audience string `json:"aud,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
}

// AccountData is the NATS account-specific claim section.
type AccountData struct {
	SigningKeys []string         `json:"signing_keys,omitempty"`
	Revocations map[string]int64 `json:"revocations,omitempty"`
}

// AccountNATS is an alias for AccountData.
type AccountNATS = AccountData

// AccountClaims is an account JWT. The account public key is Subject and the
// trusted operator (or operator signing key) is Issuer.
type AccountClaims struct {
	Claims
	NATS AccountData `json:"nats,omitempty"`
}

// AccountIssuer returns the NATS signing_keys registered by this account.
func (claims *AccountClaims) AccountIssuer() []string {
	if claims == nil {
		return nil
	}
	return claims.NATS.SigningKeys
}

// UserData is the NATS user-specific claim section. IssuerAccount is required
// when an account signing key, rather than the account identity key, signs the
// user JWT.
type UserData struct {
	IssuerAccount string `json:"issuer_account,omitempty"`
}

// UserNATS is an alias for UserData.
type UserNATS = UserData

// UserClaims is a user JWT. Subject is the user public key used to verify
// nonce signatures.
type UserClaims struct {
	Claims
	NATS UserData `json:"nats,omitempty"`
}

// IssuerAccount returns the account named by nats.issuer_account.
func (claims *UserClaims) IssuerAccount() string {
	if claims == nil {
		return ""
	}
	return claims.NATS.IssuerAccount
}

// OperatorData is the NATS operator-specific claim section.
type OperatorData struct {
	SigningKeys []string `json:"signing_keys,omitempty"`
}

// OperatorClaims is an operator JWT.
type OperatorClaims struct {
	Claims
	NATS OperatorData `json:"nats,omitempty"`
}
