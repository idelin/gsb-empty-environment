# mailauth

`mailauth` is a standard-library-only Go authentication engine for SMTP ingress
gateways. It implements SPF (RFC 7208), DKIM (RFC 6376), and DMARC (RFC 7489),
including a bundled Public Suffix List for organizational-domain alignment.

## Packages

- `prescreen.local/ps-517b39ba66/spf` — SPF record evaluation.
- `prescreen.local/ps-517b39ba66/dkim` — DKIM signing and verification.
- `prescreen.local/ps-517b39ba66/dmarc` — DMARC discovery, alignment, and policy.
- `prescreen.local/ps-517b39ba66/psl` — Public Suffix List parsing and
  organizational-domain derivation.
- `prescreen.local/ps-517b39ba66` — combined SPF + DKIM + DMARC report.
- `cmd/mailauth` — JSON command-line verifier.

DNS is injected through small resolver interfaces, so the engine can run against
`*net.Resolver` in production and deterministic fakes in tests. `DefaultResolver`
is `net.DefaultResolver`.

## Combined Verification

```go
report, err := mailauth.Verify(ctx, rawMessage, mailauth.Options{
	IP:       net.ParseIP("203.0.113.10"),
	MailFrom: "alice@example.com",
	Helo:     "mx.example.net",
})
if err != nil {
	log.Fatal(err)
}

fmt.Println(report.SPF.Result)        // pass / fail / softfail / ...
fmt.Println(len(report.DKIM))         // one result per DKIM-Signature
fmt.Println(report.DMARC.Status)      // pass / fail / none / ...
fmt.Println(report.DMARC.Disposition) // none / quarantine / reject
```

The CLI reads a message file (or stdin) and prints the report as JSON:

```sh
go run ./cmd/mailauth -ip 203.0.113.10 -mail-from alice@example.com message.eml
```

## SPF Semantics

- Mechanisms are evaluated left to right. `ip4`, `ip6`, and `all` never consume
  the DNS-term budget.
- `include`, `a`, `mx`, `ptr`, `exists`, and `redirect` share one recursive
  budget of 10 terms. A request for term 11 returns `permerror`.
- Void DNS answers (NXDOMAIN or no records) share one recursive budget of 2.
  The third void answer returns `permerror`.
- `include` applies its prefix qualifier only when the included record returns
  `pass`. `fail`, `softfail`, and `neutral` continue to the next mechanism.
  `temperror` and `permerror` propagate.
- `redirect` is considered only when the record contains no `all` mechanism and
  no earlier term matched.

```go
evaluation, _ := spf.Evaluate(ctx, spf.Options{
	IP:       net.ParseIP("203.0.113.10"),
	Domain:   "example.com",
	Resolver: resolver,
})
if evaluation.Result == spf.PermError {
	log.Printf("bad SPF: %s", evaluation.Explanation)
}
```

## DKIM Semantics

- Supports `simple` and `relaxed` header/body canonicalization independently.
- Header names listed in `h=` are consumed from the bottom of the header block
  upward, one instance per occurrence. If `h=from:from` appears with only one
  `From` field, the second occurrence is the empty value. Appending a forged
  `From` at the top therefore changes the signed input and fails verification.
- Supports SHA-1/SHA-256 RSA signatures, Ed25519 signatures, `l=` body-length
  truncation, and `x=` expiration checks.
- `dkim.Sign` is provided for tests and for gateways that add signatures.

```go
results, _ := dkim.Verify(rawMessage, resolver)
for _, result := range results {
	if result.Status == dkim.Pass {
		fmt.Println("valid signature from", result.Domain)
	}
}
```

## DMARC Semantics

- DMARC records are discovered at `_dmarc.<From domain>` and then at the
  organizational domain when different.
- The embedded PSL supports exact, wildcard (`*.ck`), and exception
  (`!www.ck`) rules, including private-section entries.
- `adkim`/`aspf` accept `r` (relaxed organizational-domain alignment) and `s`
  (strict exact-domain alignment).
- DMARC passes only when at least one of SPF or DKIM passes **and** aligns with
  the header From domain.
- On failure, `p`, `sp`, and `pct` select the final `none`, `quarantine`, or
  `reject` disposition. `sp` applies when the record is inherited from a parent
  and the From domain is a subdomain.

```go
result, _ := dmarc.Evaluate(ctx, dmarc.Options{
	FromDomain: "sub.example.com",
	SPF:        dmarc.SPFResult{Status: "pass", Domain: "mail.example.com"},
	DKIM:       []dmarc.DKIMResult{{Status: "pass", Domain: "example.com"}},
	Resolver:   resolver,
})
```

`pct` sampling is deterministic and keyed by `Options.PctSeed` (default: the
From domain) so gateway decisions are reproducible and unit-testable.

## Tests

```sh
go test ./...
go test -race ./...
```

The tests cover the 10-term limit, the two-void limit, `include` continuation,
DKIM bottom-up duplicate-header consumption, relaxed/simple canonicalization,
`l=` and `x=`, PSL wildcard/exception behavior, strict/relaxed DMARC alignment,
`sp` overrides, and PSL-derived organizational domains.

The bundled `psl/public_suffix_list.dat` is the Public Suffix List from
<https://publicsuffix.org/>.
