package samlsec

import (
	"time"
)

const (
	testIDP      = "https://idp.example.com/"
	testSP       = "https://sp.example.com/acs"
	testAudience = "https://sp.example.com/"
	testRequest  = "_authn-request-1"
)

// fixtureConfig 控制标准合法响应的构造参数。
type fixtureConfig struct {
	responseID string
	assertID   string
	subject    string
	issuer     string
	audience   string
	recipient  string
	inRespTo   string

	notBefore    time.Time
	notOnOrAfter time.Time
	bearerExpire time.Time

	// signResponse 为 true 时签名挂在 Response 上，否则挂在 Assertion 上。
	signResponse bool

	// includeAttributes 控制是否生成 AttributeStatement。
	includeAttributes bool
}

func defaultFixture(now time.Time) fixtureConfig {
	return fixtureConfig{
		responseID:        "_response-1",
		assertID:          "_assertion-1",
		subject:           "alice@example.com",
		issuer:            testIDP,
		audience:          testAudience,
		recipient:         testSP,
		inRespTo:          testRequest,
		notBefore:         now.Add(-5 * time.Minute),
		notOnOrAfter:      now.Add(10 * time.Minute),
		bearerExpire:      now.Add(10 * time.Minute),
		includeAttributes: true,
	}
}

// buildResponse 构造（尚未签名的）完整 Response 树。
func buildResponse(cfg fixtureConfig) *Node {
	response := elem(NSSAMLP, "Response", "samlp",
		nsAttr("samlp", NSSAMLP),
		nsAttr("saml", NSSAML),
		attr("", "ID", "", cfg.responseID),
		attr("", "Version", "", "2.0"),
		attr("", "IssueInstant", "", cfg.notBefore.Add(time.Minute).UTC().Format(time.RFC3339)),
	)
	adopt(response,
		elem(NSSAML, "Issuer", "saml"),
	)
	response.Children[0].Children = []*Node{text(cfg.issuer)}

	assertion := buildAssertion(cfg)
	adopt(response, assertion)

	if cfg.signResponse {
		adopt(response, buildSignaturePlaceholder(cfg.responseID))
	} else {
		adopt(assertion, buildSignaturePlaceholder(cfg.assertID))
	}
	return response
}

func buildAssertion(cfg fixtureConfig) *Node {
	assertion := elem(NSSAML, "Assertion", "saml",
		attr("", "ID", "", cfg.assertID),
		attr("", "Version", "", "2.0"),
		attr("", "IssueInstant", "", cfg.notBefore.UTC().Format(time.RFC3339)),
	)

	issuer := elem(NSSAML, "Issuer", "saml")
	adopt(issuer, text(cfg.issuer))

	subject := elem(NSSAML, "Subject", "saml")
	nameID := elem(NSSAML, "NameID", "saml",
		attr("", "Format", "", "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress"))
	adopt(nameID, text(cfg.subject))
	confirmation := elem(NSSAML, "SubjectConfirmation", "saml",
		attr("", "Method", "", "urn:oasis:names:tc:SAML:2.0:cm:bearer"))
	confirmationData := elem(NSSAML, "SubjectConfirmationData", "saml",
		attr("", "NotOnOrAfter", "", cfg.bearerExpire.UTC().Format(time.RFC3339)),
		attr("", "Recipient", "", cfg.recipient),
		attr("", "InResponseTo", "", cfg.inRespTo))
	adopt(confirmation, confirmationData)
	adopt(subject, nameID, confirmation)

	conditions := elem(NSSAML, "Conditions", "saml",
		attr("", "NotBefore", "", cfg.notBefore.UTC().Format(time.RFC3339)),
		attr("", "NotOnOrAfter", "", cfg.notOnOrAfter.UTC().Format(time.RFC3339)))
	restriction := elem(NSSAML, "AudienceRestriction", "saml")
	audience := elem(NSSAML, "Audience", "saml")
	adopt(audience, text(cfg.audience))
	adopt(restriction, audience)
	adopt(conditions, restriction)

	adopt(assertion, issuer, subject, conditions)

	if cfg.includeAttributes {
		stmt := elem(NSSAML, "AttributeStatement", "saml")
		roleAttr := elem(NSSAML, "Attribute", "saml",
			attr("", "Name", "", "role"),
			attr("", "NameFormat", "", "urn:oasis:names:tc:SAML:2.0:attrname-format:basic"))
		roleVal := elem(NSSAML, "AttributeValue", "saml")
		adopt(roleVal, text("user"))
		adopt(roleAttr, roleVal)
		adopt(stmt, roleAttr)
		adopt(assertion, stmt)
	}
	return assertion
}

// buildSignaturePlaceholder 生成待签名的 ds:Signature 骨架；
// DigestValue 与 SignatureValue 由 testKey.sign 填充。
func buildSignaturePlaceholder(targetID string) *Node {
	sig := elem(NSDSIG, "Signature", "ds", nsAttr("ds", NSDSIG))

	signedInfo := elem(NSDSIG, "SignedInfo", "ds")
	adopt(signedInfo,
		elem(NSDSIG, "CanonicalizationMethod", "ds",
			attr("", "Algorithm", "", AlgoExcC14N)),
	)
	adopt(signedInfo,
		elem(NSDSIG, "SignatureMethod", "ds",
			attr("", "Algorithm", "", AlgoRSASHA256)),
	)
	reference := elem(NSDSIG, "Reference", "ds", attr("", "URI", "", "#"+targetID))
	transforms := elem(NSDSIG, "Transforms", "ds")
	adopt(transforms,
		elem(NSDSIG, "Transform", "ds", attr("", "Algorithm", "", AlgoEnveloped)),
		elem(NSDSIG, "Transform", "ds", attr("", "Algorithm", "", AlgoExcC14N)),
	)
	adopt(reference,
		transforms,
		elem(NSDSIG, "DigestMethod", "ds", attr("", "Algorithm", "", AlgoSHA256)),
		elem(NSDSIG, "DigestValue", "ds"),
	)
	adopt(signedInfo, reference)

	valueNode := elem(NSDSIG, "SignatureValue", "ds")
	adopt(sig, signedInfo, valueNode)
	return sig
}

func signedResponseBytes(key *testKey, cfg fixtureConfig) []byte {
	root := buildResponse(cfg)
	if cfg.signResponse {
		key.sign(root)
	} else {
		key.sign(root.FindChild(NSSAML, "Assertion"))
	}
	return []byte(serializeXML(root))
}

func baseOptions(key *testKey, now time.Time) Options {
	return Options{
		TrustedKey:           key.public(),
		ExpectedAudience:     testAudience,
		ExpectedRecipient:    testSP,
		ExpectedInResponseTo: testRequest,
		ExpectedIssuer:       testIDP,
		ClockSkew:            30 * time.Second,
		Now:                  func() time.Time { return now },
		Replay:               NewMemoryReplayCache(time.Hour),
	}
}

// parseTree 是测试中绕过文本直接取 DOM 的快捷方式。
func parseTree(raw []byte) *Document {
	doc, err := Parse(raw, nil)
	if err != nil {
		panic("test parse failed: " + err.Error())
	}
	return doc
}
