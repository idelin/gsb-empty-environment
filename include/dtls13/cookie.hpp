// SPDX-License-Identifier: MIT
// Stateless cookie exchange for the DTLS 1.3 HelloRetryRequest DoS
// defense (RFC 9147 section 5.2).

#ifndef DTLS13_COOKIE_HPP
#define DTLS13_COOKIE_HPP

#include <cstdint>
#include <memory>

#include "dtls13/crypto.hpp"
#include "dtls13/types.hpp"

namespace dtls13 {

// Abstract clock so cookie expiry is fully deterministic in tests.
class IClock {
public:
    virtual ~IClock() = default;
    virtual std::uint64_t nowSeconds() const noexcept = 0;
};

class FixedClock final : public IClock {
public:
    explicit FixedClock(std::uint64_t now = 0) noexcept : now_(now) {}
    std::uint64_t nowSeconds() const noexcept override { return now_; }
    void set(std::uint64_t now) noexcept { now_ = now; }

private:
    std::uint64_t now_;
};

struct CookieConfig {
    // Bounds the cookie extension payload a client is allowed to echo.
    std::size_t max_cookie_size = 256;
};

// Binds an unsigned ClientHello (identified by a host-computed identity
// hash, e.g. SHA-256 over client address + ClientHello bytes) to an
// HMAC-signed, timestamped token. No per-client server state is retained.
class CookieManager {
public:
    CookieManager(std::unique_ptr<ICookieSigner> signer,
                  CookieConfig config = {})
        : signer_(std::move(signer)), config_(config) {}

    // Issue the cookie that goes into HelloRetryRequest.
    Result<Bytes> issue(ByteView client_identity);

    // Validate the cookie echoed inside the second ClientHello. Any
    // length/format/expiry/integrity failure returns BadCookie and never
    // touches the signer in a way that leaks timing information beyond
    // the constant-time MAC comparison.
    ErrorCode validate(ByteView client_identity, ByteView cookie);

private:
    std::unique_ptr<ICookieSigner> signer_;
    CookieConfig config_;
};

}  // namespace dtls13

#endif  // DTLS13_COOKIE_HPP
