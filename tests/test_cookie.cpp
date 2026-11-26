// SPDX-License-Identifier: MIT

#include "dtls13/cookie.hpp"
#include "test_framework.hpp"

#include <cstring>
#include <memory>

using namespace dtls13;

namespace {

ByteView view(const char* s) {
    return ByteView(reinterpret_cast<const std::uint8_t*>(s),
                    std::strlen(s));
}

}  // namespace

TEST_CASE(cookie_roundtrip_validates_identity) {
    auto signer = std::make_unique<HmacCookieSigner>(
        view("server-secret"), 1000, 60);
    CookieManager manager(std::move(signer));

    auto issued = manager.issue(view("client-1"));
    CHECK(issued.ok());
    CHECK_EQ(issued.value.size(), HmacCookieSigner::kTokenLength);

    CHECK_EQ(manager.validate(view("client-1"), ByteView(issued.value)),
             ErrorCode::Ok);
    // Cookie bound to a different identity must fail.
    CHECK_EQ(manager.validate(view("client-2"), ByteView(issued.value)),
             ErrorCode::BadCookie);
}

TEST_CASE(cookie_expiry_is_enforced) {
    auto signer = std::make_unique<HmacCookieSigner>(
        view("server-secret"), 1000, 60);
    CookieManager issuer(std::move(signer));
    auto issued = issuer.issue(view("client-1"));
    CHECK(issued.ok());

    auto later_signer = std::make_unique<HmacCookieSigner>(
        view("server-secret"), 1061, 60);
    CookieManager validator(std::move(later_signer));
    CHECK_EQ(validator.validate(view("client-1"), ByteView(issued.value)),
             ErrorCode::BadCookie);

    // Still valid exactly at the lifetime boundary.
    auto boundary_signer = std::make_unique<HmacCookieSigner>(
        view("server-secret"), 1060, 60);
    CookieManager boundary(std::move(boundary_signer));
    CHECK_EQ(boundary.validate(view("client-1"), ByteView(issued.value)),
             ErrorCode::Ok);
}

TEST_CASE(cookie_tamper_and_wrong_secret_fail) {
    auto signer = std::make_unique<HmacCookieSigner>(
        view("server-secret"), 1000, 60);
    CookieManager manager(std::move(signer));
    auto issued = manager.issue(view("client-1"));

    Bytes tampered = issued.value;
    tampered[10] = static_cast<std::uint8_t>(tampered[10] ^ 0x01);
    CHECK_EQ(manager.validate(view("client-1"), ByteView(tampered)),
             ErrorCode::BadCookie);

    auto other_signer = std::make_unique<HmacCookieSigner>(
        view("wrong-secret"), 1000, 60);
    CookieManager other(std::move(other_signer));
    CHECK_EQ(other.validate(view("client-1"), ByteView(issued.value)),
             ErrorCode::BadCookie);
}

TEST_CASE(cookie_rejects_empty_and_oversized_input) {
    auto signer = std::make_unique<HmacCookieSigner>(
        view("server-secret"), 1000, 60);
    CookieManager manager(std::move(signer));

    CHECK_EQ(manager.issue(ByteView()).code, ErrorCode::BadArgument);
    auto issued = manager.issue(view("client-1"));
    CHECK(issued.ok());

    CookieConfig config;
    config.max_cookie_size = 4;
    auto small_signer = std::make_unique<HmacCookieSigner>(
        view("server-secret"), 1000, 60);
    CookieManager limited(std::move(small_signer), config);
    CHECK_EQ(limited.validate(view("client-1"), ByteView(issued.value)),
             ErrorCode::BadCookie);
}
