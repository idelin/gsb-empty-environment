# Submission Transport Policy

`netsec` is a small, importable C++20 policy component for SMTP submission
clients. It answers one question before a client sends `AUTH` or message
content: is this connection permitted by the configured transport policy?

The policy engine is deliberately independent of a particular TLS or SMTP
implementation. The client records what it observed, and the engine returns
`allow`, `require-starttls`, or `deny`.

## Guarantees

- A connection record captures the server host, port, advertised EHLO
  capabilities, STARTTLS result, TLS version/cipher, and certificate chain.
- Encryption-required connections cannot reach AUTH or DATA while the channel
  is plaintext. A missing STARTTLS capability, rejected STARTTLS command,
  failed handshake, missing certificate, hostname mismatch, bad validity,
  untrusted chain, or DANE mismatch is fatal.
- STARTTLS completion alone is not enough: the leaf identity must match the
  server host and the chain must satisfy Web PKI or the applicable DANE
  record.
- MTA-STS and DANE records are cached per domain. Lookups are explicit about
  `Missing`, `Valid`, `NotYetValid`, and `Expired`; expired enforce/DANE
  records fail closed instead of silently downgrading. A cached record without
  `expires_at` is treated as expired.
- AUTH is permitted only on TLS, or on a local loopback when
  `allow_plaintext_auth_on_loopback` is explicitly enabled. Plaintext message
  content has a separate, default-off loopback opt-in.
- The first inspected endpoint is pinned. A retry that changes host or port is
  rejected, and a plaintext retry after an encrypted attempt is rejected.
- Credentials and bodies never enter the policy API or connection record.
  `WireTranscript` is an optional audit helper that can assert a secret
  appeared only on the TLS channel.

## Import

With CMake:

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(your_client PRIVATE netsec::netsec)
```

The library installs `netsec.hpp` and an exported `netsec::netsec` target.
Include it with:

```cpp
#include "netsec.hpp"
```

## Client flow

```cpp
#include "netsec.hpp"

using namespace netsec;

auto cache = std::make_shared<PolicyCache>();

MtaStsPolicy sts;
sts.domain = "example.com";
sts.mode = MtaStsMode::Enforce;
sts.mx_patterns = {"mail.example.com"};
sts.expires_at = std::chrono::system_clock::now() + std::chrono::hours(24);
cache->store(sts);

DanePolicy dane;
dane.domain = "example.com";
dane.has_tlsa = true;
dane.tlsa_records.push_back(TlsaRecord{3, 1, 1, "sha256-hex"});
dane.expires_at = std::chrono::system_clock::now() + std::chrono::hours(12);
cache->store(dane);

SubmissionPolicyConfig config;  // encryption is Required by default
SubmissionSession session(cache, config);

ConnectionRecord record;
record.host = "mail.example.com";
record.port = 587;
record.policy_domain = "example.com";
record.advertised_capabilities =
    parse_ehlo_capabilities("250-mail.example.com\r\n250-STARTTLS\r\n"
                            "250-AUTH PLAIN LOGIN\r\n");

PolicyVerdict verdict = session.inspect(record);
if (verdict.requires_starttls()) {
  // Issue STARTTLS, complete the handshake, and fill in:
  // record.starttls = StartTlsResult::Completed;
  // record.tls_version = ...;
  // record.certificate_chain = observed chain, leaf first;
  verdict = session.inspect(record);
}
if (!verdict.allowed()) {
  // Abort this connection. Do not retry on a different port through this
  // session, and never fall back to plaintext.
  return;
}

if (!session.authorize_auth("PLAIN").allowed) {
  // Do not send credentials.
  return;
}
if (!session.authorize_content().allowed) {
  // Do not send the message body.
  return;
}
```

For implicit TLS (for example, a dedicated TLS submission port), set
`record.channel = ChannelState::ImplicitTls` and populate the certificate
chain. For a staged STARTTLS connection, first inspect with
`StartTlsResult::NotAttempted`; after the handshake, update the same record with
`StartTlsResult::Completed` and inspect it again. The session pins the original
host and port across those inspections and across retries.

## Build and test

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

The tests cover capability stripping, certificate replacement, DANE/MTA-STS
expiry, retry port fallback, a legitimate encrypted submission, and local
loopback behavior. Transcript assertions verify that passwords and message
bodies appear only on encrypted events.
