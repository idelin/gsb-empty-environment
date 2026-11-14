#pragma once

#include "netsec.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <vector>

namespace netsec::detail {

class Sha256 {
 public:
  Sha256();

  void update(std::span<const Byte> data);
  Digest finish();

 private:
  void transform(const Byte block[64]);

  std::array<std::uint32_t, 8> state_{};
  std::array<Byte, 64> buffer_{};
  std::size_t buffered_{};
  std::uint64_t total_bytes_{};
  bool finished_{};
};

Digest sha256(std::span<const Byte> data);
Digest hmac_sha256(std::span<const Byte> key, std::span<const Byte> data);
std::vector<Byte> hkdf_expand(std::span<const Byte> prk,
                              std::span<const Byte> info, std::size_t length);
Bytes secure_random(std::size_t length);

std::array<Byte, 32> x25519(std::span<const Byte> private_key,
                            std::span<const Byte> peer_public_key);

Bytes aead_seal(std::span<const Byte> key, std::span<const Byte> nonce,
                std::span<const Byte> aad, std::span<const Byte> plaintext);
std::optional<Bytes> aead_open(std::span<const Byte> key,
                               std::span<const Byte> nonce,
                               std::span<const Byte> aad,
                               std::span<const Byte> sealed);

bool constant_time_equal(std::span<const Byte> lhs,
                         std::span<const Byte> rhs);

}  // namespace netsec::detail
