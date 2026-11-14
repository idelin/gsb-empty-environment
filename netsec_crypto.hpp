#pragma once

// Internal cryptographic primitives used by the confirmation component.
// These are intentionally not part of the installed public API.

#include "netsec.hpp"

#include <array>
#include <cstdint>
#include <optional>

namespace netsec::detail {

[[nodiscard]] Hash256 keccak256(ByteView data);
[[nodiscard]] Hash256 keccak256(const Hash256& prefix, ByteView suffix);

[[nodiscard]] bool scalar_is_valid(const std::array<std::uint8_t, 32>& scalar);
[[nodiscard]] bool scalar_is_low_s(const std::array<std::uint8_t, 32>& scalar);
[[nodiscard]] bool scalar_is_zero(const std::array<std::uint8_t, 32>& scalar);

// Recovers the Ethereum address that produced signature over signing_digest.
// Returns nullopt for malformed r/s, high-s signatures (when requested),
// invalid recovery ids, or a point that is not on secp256k1.
[[nodiscard]] std::optional<Address> recover_address(
    const Hash256& signing_digest,
    const CompactSignature& signature,
    bool require_low_s);

}  // namespace netsec::detail
