// SPDX-License-Identifier: MIT

#include "dtls13/cookie.hpp"

namespace dtls13 {

Result<Bytes> CookieManager::issue(ByteView client_identity) {
    if (client_identity.empty()) {
        return ErrorCode::BadArgument;
    }
    Bytes token = signer_->sign(client_identity);
    if (token.empty()) {
        return ErrorCode::BadCookie;
    }
    return token;
}

ErrorCode CookieManager::validate(ByteView client_identity,
                                  ByteView cookie) {
    if (client_identity.empty() || cookie.empty()) {
        return ErrorCode::BadCookie;
    }
    if (cookie.size() > config_.max_cookie_size) {
        return ErrorCode::BadCookie;
    }
    // HMAC verification is constant-time inside the signer.
    if (!signer_->verify(client_identity, cookie)) {
        return ErrorCode::BadCookie;
    }
    return ErrorCode::Ok;
}

}  // namespace dtls13
