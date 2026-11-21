#pragma once

#include "rdpauth/types.hpp"

#include <cstdint>
#include <span>
#include <string_view>

namespace rdp::auth {

// Self-contained SHA-256 / HMAC-SHA256 so the component has no external
// crypto dependency. Suitable for binding tokens, MACs and key derivation.

Hash256 sha256(std::span<const std::uint8_t> data);

Hash256 hmac_sha256(std::span<const std::uint8_t> key,
                    std::span<const std::uint8_t> message);

bool constant_time_equal(std::span<const std::uint8_t> a,
                         std::span<const std::uint8_t> b) noexcept;

inline Hash256 hash_of(std::string_view text) {
    return sha256({reinterpret_cast<const std::uint8_t*>(text.data()), text.size()});
}

} // namespace rdp::auth
