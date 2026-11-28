package jar

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 1. 强模式下缺失 request 的明文授权请求必须被硬拒（防降级）。
func TestPlaintextDowngradeRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)

	values := url.Values{}
	values.Set("client_id", testClientID)
	values.Set("redirect_uri", "https://evil.example/cb")

	_, err := validator.ParseQuery(values)
	assertSentinel(t, err, ErrPlaintextDowngrade)

	// 服务端不强制但客户端强制时，同样必须硬拒。
	looseKeys := setupKeys(t, true)
	looseValidator := NewValidator(Policy{Issuer: testIssuer, RequireSignedRequestObject: false},
		looseKeys.registry, NewMemoryReplayCache())
	_, err = looseValidator.ParseQuery(values)
	assertSentinel(t, err, ErrPlaintextDowngrade)
}

// 2. 双重策略均允许明文时才放行，且结果标记为未签名。
func TestPlaintextAllowedWhenBothPoliciesPermit(t *testing.T) {
	keys := setupKeys(t, false)
	validator := NewValidator(Policy{Issuer: testIssuer, RequireSignedRequestObject: false},
		keys.registry, NewMemoryReplayCache())

	values := url.Values{}
	values.Set("client_id", testClientID)
	values.Set("state", "plain-state")
	result, err := validator.ParseQuery(values)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Signed {
		t.Fatal("plaintext request must be marked unsigned")
	}
	if result.State != "plain-state" {
		t.Fatalf("state = %q", result.State)
	}
}

// 3. request 与 request_uri 同时出现必须硬拒；单独 request_uri 也不被进程内支持。
func TestRequestAndRequestURIMutuallyExclusive(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()
	token := signClaims(t, "RS256", requestObjectTyp, validClaims(now),
		keys.rsaPrivate, testKID+"-rsa")

	both := queryWith(token, "request_uri", "https://cdn.example/req.jwt")
	_, err := validator.ParseQuery(both)
	assertSentinel(t, err, ErrBothRequestAndRequestURI)

	onlyURI := url.Values{}
	onlyURI.Set("request_uri", "https://cdn.example/req.jwt")
	onlyURI.Set("client_id", testClientID)
	_, err = validator.ParseQuery(onlyURI)
	assertSentinel(t, err, ErrRequestURINotSupported)
}

// 4. 外层未签名 redirect_uri 覆盖/冲突内层值必须硬拒，且成功路径只采信内层值。
func TestOuterRedirectURIConflictRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()
	token := signClaims(t, "RS256", requestObjectTyp, validClaims(now),
		keys.rsaPrivate, testKID+"-rsa")

	evilValues := queryWith(token,
		"client_id", testClientID,
		"redirect_uri", "https://attacker.example/steal-callback")
	_, err := validator.ParseQuery(evilValues)
	assertSentinel(t, err, ErrOuterInnerConflict)

	// 外层多出内层不存在的未签名参数同样硬拒。
	extraValues := queryWith(token, "client_id", testClientID, "prompt", "consent")
	_, err = validator.ParseQuery(extraValues)
	assertSentinel(t, err, ErrOuterInnerConflict)

	// 无冲突的成功路径：业务参数严格取自 JWT 内层。
	goodValues := queryWith(token, "client_id", testClientID)
	result, err := validator.ParseQuery(goodValues)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Signed {
		t.Fatal("result must be signed")
	}
	if result.RedirectURI != "https://client.example.gov.cn/callback" {
		t.Fatalf("redirect_uri must come from JWT, got %q", result.RedirectURI)
	}
	if result.Params["redirect_uri"] != "https://client.example.gov.cn/callback" {
		t.Fatalf("params hijacked: %q", result.Params["redirect_uri"])
	}
	if result.ClientID != testClientID {
		t.Fatalf("client_id = %q", result.ClientID)
	}
}

// 5. 每个受保护参数的外层冲突都必须被拦截。
func TestAllProtectedOuterConflictsRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()
	token := signClaims(t, "ES256", requestObjectTyp, validClaims(now),
		keys.ecPrivate, testKID+"-ec")

	cases := map[string]string{
		"scope":          "evil-scope",
		"state":          "evil-state",
		"nonce":          "evil-nonce",
		"code_challenge": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	for param, evil := range cases {
		values := queryWith(token, "client_id", testClientID, param, evil)
		_, err := validator.ParseQuery(values)
		assertSentinel(t, err, ErrOuterInnerConflict)
	}
}

// 6. 外层 client_id 与内层不一致必须硬拒。
func TestOuterClientIDMismatchRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()
	token := signClaims(t, "RS256", requestObjectTyp, validClaims(now),
		keys.rsaPrivate, testKID+"-rsa")

	values := queryWith(token, "client_id", "another-client")
	_, err := validator.ParseQuery(values)
	assertSentinel(t, err, ErrOuterInnerConflict)
}

// 7. typ 不是 oauth-authz-req+jwt 必须硬拒，防 ID Token/Access Token 类型混淆。
func TestNonRequestObjectTypRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()

	for _, typ := range []string{"JWT", "at+jwt", "id-token+jwt", "", "OAUTH-AUTHZ-REQ+JWT"} {
		token := signClaims(t, "RS256", typ, validClaims(now),
			keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrInvalidTyp)
	}
}

// 8. none 与对称 HS* 算法必须被硬拒，即便载荷其它声明完全合法。
func TestNoneAndSymmetricAlgorithmsRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()

	noneToken := signClaims(t, "none", requestObjectTyp, validClaims(now), nil, "")
	_, err := validator.ParseQuery(queryWith(noneToken, "client_id", testClientID))
	assertSentinel(t, err, ErrAlgorithmNotAllowed)

	hsToken := signClaims(t, "HS256", requestObjectTyp, validClaims(now), nil, "")
	_, err = validator.ParseQuery(queryWith(hsToken, "client_id", testClientID))
	assertSentinel(t, err, ErrAlgorithmNotAllowed)

	// 不在策略白名单内的非对称算法同样拒绝（默认白名单只有 RS256/PS256/ES256）。
	es384Token := signClaims(t, "ES384", requestObjectTyp, validClaims(now),
		keys.ecPrivate, testKID+"-ec")
	_, err = validator.ParseQuery(queryWith(es384Token, "client_id", testClientID))
	assertSentinel(t, err, ErrAlgorithmNotAllowed)
}

// 9. 篡改签名后必须验签失败。
func TestTamperedSignatureRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()
	token := signClaims(t, "RS256", requestObjectTyp, validClaims(now),
		keys.rsaPrivate, testKID+"-rsa")

	parts := strings.Split(token, ".")
	if !strings.Contains(parts[2], "A") {
		parts[2] = parts[2] + "A"
	} else {
		parts[2] = strings.ReplaceAll(parts[2], "A", "B")
	}
	tampered := strings.Join(parts, ".")
	_, err := validator.ParseQuery(queryWith(tampered, "client_id", testClientID))
	assertSentinel(t, err, ErrSignatureInvalid)
}

// 10. jti 重放：同一个请求对象第二次提交必须被原子防重放拦截。
func TestJTIReplayRejected(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()
	claims := validClaims(now)
	claims["jti"] = "fixed-replay-jti"
	token := signClaims(t, "RS256", requestObjectTyp, claims,
		keys.rsaPrivate, testKID+"-rsa")

	first, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
	if err != nil {
		t.Fatalf("first submission should pass: %v", err)
	}
	if first.JTI != "fixed-replay-jti" {
		t.Fatalf("jti = %q", first.JTI)
	}

	_, err = validator.ParseQuery(queryWith(token, "client_id", testClientID))
	assertSentinel(t, err, ErrReplayedJTI)
}

// 11. iss/aud/exp/nbf/有效期/嵌套声明等声明级规则。
func TestClaimValidation(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()

	t.Run("iss mismatch", func(t *testing.T) {
		claims := validClaims(now)
		claims["iss"] = "https://attacker.example"
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrInvalidClaim)
	})

	t.Run("aud mismatch", func(t *testing.T) {
		claims := validClaims(now)
		claims["aud"] = "https://wrong-issuer.example"
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrInvalidClaim)
	})

	t.Run("aud multi entry", func(t *testing.T) {
		claims := validClaims(now)
		claims["aud"] = []string{testIssuer, "https://other.example"}
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrInvalidClaim)
	})

	t.Run("expired", func(t *testing.T) {
		claims := validClaims(now)
		claims["nbf"] = now.Add(-2 * time.Hour).Unix()
		claims["exp"] = now.Add(-1 * time.Hour).Unix()
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrInvalidClaim)
	})

	t.Run("nbf in future", func(t *testing.T) {
		claims := validClaims(now)
		claims["nbf"] = now.Add(10 * time.Minute).Unix()
		claims["exp"] = now.Add(12 * time.Minute).Unix()
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrInvalidClaim)
	})

	t.Run("lifetime exceeds max", func(t *testing.T) {
		claims := validClaims(now)
		claims["nbf"] = now.Unix()
		claims["exp"] = now.Add(600 * time.Second).Unix()
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrLifetimeExceeded)
	})

	t.Run("missing jti", func(t *testing.T) {
		claims := validClaims(now)
		delete(claims, "jti")
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrInvalidClaim)
	})

	t.Run("nested request claim", func(t *testing.T) {
		claims := validClaims(now)
		claims["request"] = "eyJhbGciOiJub25lIn0.xxx.yyy"
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrNestedRequest)
	})

	t.Run("nested request_uri claim", func(t *testing.T) {
		claims := validClaims(now)
		claims["request_uri"] = "https://cdn.example/nested.jwt"
		token := signClaims(t, "RS256", requestObjectTyp, claims, keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrNestedRequest)
	})
}

// 12. 结构性攻击与入口/算法兼容性。
func TestStructuralAndCrossAlgs(t *testing.T) {
	keys := setupKeys(t, true)
	validator := newTestValidator(keys.registry)
	now := time.Now()
	goodToken := signClaims(t, "RS256", requestObjectTyp, validClaims(now),
		keys.rsaPrivate, testKID+"-rsa")

	t.Run("duplicate parameter", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet,
			"https://as.example.gov.cn/authorize?request="+url.QueryEscape(goodToken)+
				"&state=a&state=b", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		_, err = validator.ParseHTTPRequest(req)
		assertSentinel(t, err, ErrDuplicateParameter)
	})

	t.Run("unknown client", func(t *testing.T) {
		claims := validClaims(now)
		claims["iss"] = "ghost-client"
		claims["client_id"] = "ghost-client"
		token := signClaims(t, "RS256", requestObjectTyp, claims,
			keys.rsaPrivate, testKID+"-rsa")
		_, err := validator.ParseQuery(queryWith(token))
		assertSentinel(t, err, ErrUnknownClient)
	})

	t.Run("unknown kid", func(t *testing.T) {
		token := signClaims(t, "RS256", requestObjectTyp, validClaims(now),
			keys.rsaPrivate, "no-such-kid")
		_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		assertSentinel(t, err, ErrKeyNotFound)
	})

	t.Run("PS256 success", func(t *testing.T) {
		token := signClaims(t, "PS256", requestObjectTyp, validClaims(now),
			keys.rsaPrivate, testKID+"-rsa")
		result, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		if err != nil {
			t.Fatalf("PS256 should verify: %v", err)
		}
		if result.Scope != "openid profile" || result.Nonce != "nonce-abc" {
			t.Fatalf("inner params not trusted: %+v", result)
		}
	})

	t.Run("ES256 success", func(t *testing.T) {
		token := signClaims(t, "ES256", requestObjectTyp, validClaims(now),
			keys.ecPrivate, testKID+"-ec")
		result, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
		if err != nil {
			t.Fatalf("ES256 should verify: %v", err)
		}
		if !result.Signed || result.CodeChallenge == "" {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("rsa alg against ec key rejected", func(t *testing.T) {
		// 注册一个只有 EC 公钥的客户端，却用 RS256 名义签名：
		// 验签端发现密钥族与算法不匹配必须失败而非放行。
		ecOnly := NewRegisteredClient("ec-only-client", true)
		ecOnly.AddKey("ec-kid", &keys.ecPrivate.PublicKey)
		keys.registry.Register(ecOnly)

		claims := validClaims(now)
		claims["iss"] = "ec-only-client"
		claims["client_id"] = "ec-only-client"
		token := signClaims(t, "RS256", requestObjectTyp, claims,
			keys.ecPrivate, "ec-kid")
		_, err := validator.ParseQuery(queryWith(token))
		assertSentinel(t, err, ErrSignatureInvalid)
	})

	t.Run("malformed jwt", func(t *testing.T) {
		_, err := validator.ParseQuery(queryWith("not-a-jwt"))
		assertSentinel(t, err, ErrMalformedJWT)
	})
}

// 13. 自定义 ReplayCache 可被宿主替换，且失败即拒绝请求。
type denyReplayCache struct{}

func (denyReplayCache) Reserve(_, _ string, _ time.Time) bool { return false }

func TestCustomReplayCacheDenial(t *testing.T) {
	keys := setupKeys(t, true)
	validator := NewValidator(Policy{Issuer: testIssuer, RequireSignedRequestObject: true},
		keys.registry, denyReplayCache{})
	now := time.Now()
	token := signClaims(t, "RS256", requestObjectTyp, validClaims(now),
		keys.rsaPrivate, testKID+"-rsa")
	_, err := validator.ParseQuery(queryWith(token, "client_id", testClientID))
	assertSentinel(t, err, ErrReplayedJTI)
}
