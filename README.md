# 征信查询授权组件

可供 Go 征信服务导入的查询授权组件。授权绑定查询人、被查人、用途、机构及查询次数；每次查询使用短时效、不可复用的 opaque 令牌。跨主体、用途/机构不符、超次数、过期及撤销后的查询都会被拒绝。

## 快速开始

```go
package main

import (
	"errors"
	"log"
	"time"

	"example.org/netsec/q0463"
)

func main() {
	authorizer := netsec.New(
		netsec.WithTokenTTL(90*time.Second),
		netsec.WithAuthorizationTTL(10*time.Minute),
	)

	// CreateAuthorization must only be called by trusted service code after
	// the data subject has consented to this exact scope.
	grant, err := authorizer.CreateAuthorization(netsec.AuthorizationRequest{
		RequesterID:   "analyst-17",
		SubjectID:     "consumer-42",
		Purpose:       "loan-review",
		InstitutionID: "bank-9",
		MaxQueries:    2,
	})
	if err != nil {
		log.Fatal(err)
	}

	token, err := authorizer.IssueToken(grant.ID)
	if err != nil {
		log.Fatal(err)
	}

	decision, err := authorizer.AuthorizeQuery(netsec.QueryRequest{
		Token:         token,
		RequesterID:   "analyst-17",
		SubjectID:     "consumer-42",
		Purpose:       "loan-review",
		InstitutionID: "bank-9",
	})
	if errors.Is(err, netsec.ErrTokenUsed) {
		log.Fatal("token replay rejected")
	}
	if err != nil {
		log.Fatal(err)
	}

	// Query only this subject. The decision comes from the server-side grant,
	// not from client-supplied parameters.
	queryCreditReport(decision.SubjectID)

	// Revocation applies to the grant and all outstanding tokens.
	_ = authorizer.Revoke(grant.ID)
}
```

`Authorize` is a convenience that creates a grant and issues its first token. Use `AuthorizeWithGrant` when the grant ID is also needed. Each successful query consumes exactly one token and one unit of the grant's budget; the consumption is atomic under concurrency.

## Security contract

- `RequesterID`, `SubjectID`, `Purpose`, and `InstitutionID` must come from the authenticated server-side request context. The authorizer binds them into every grant and checks all four on every query.
- Tokens are 256-bit random values, stored only as SHA-256 digests, and expire after two minutes by default (or earlier if the grant expires). `Decision.SubjectID` is the verified object to query.
- Query budget is charged only after all checks pass. A denied attempt neither burns the token nor consumes budget; a successful token can never be used again.
- `Revoke(idOrToken)` invalidates a grant or one individual token. `RevokeAuthorization` revokes a grant and all of its outstanding tokens.
- `AuditEntries()` returns an isolated copy of the append-only ledger. Records are hash-chained, and `VerifyAuditLog()` detects modification or truncation. Tokens are represented only by non-secret fingerprints.

The built-in ledger is in memory and lasts for the process lifetime. A production service should persist each returned `AuditEntry` to a separate append-only/WORM sink and periodically call `VerifyAuditLog`.

## Verification

The test suite covers valid queries, token replay (including concurrent replay), cross-subject/cross-purpose/cross-institution attempts, query-budget races, expiry, grant/token revocation, and audit-log tamper detection.

```sh
go test ./...
go test -race ./...
go vet ./...
```
