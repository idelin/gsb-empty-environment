#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <span>
#include <vector>

namespace netsec::detail {

using Bytes = std::vector<std::uint8_t>;

std::array<std::uint8_t, 32> sha256(std::span<const std::uint8_t> message);

std::array<std::uint8_t, 32> hmac_sha256(std::span<const std::uint8_t> key,
                                         std::span<const std::uint8_t> message);

std::array<std::uint8_t, 32> hkdf_sha256(std::span<const std::uint8_t> input_key_material,
                                         std::span<const std::uint8_t> salt,
                                         std::span<const std::uint8_t> info);

void chacha20_xor(std::span<const std::uint8_t, 32> key,
                  std::span<const std::uint8_t, 12> nonce,
                  std::uint32_t initial_counter,
                  std::span<const std::uint8_t> input,
                  std::span<std::uint8_t> output);

std::array<std::uint8_t, 16> poly1305_mac(std::span<const std::uint8_t, 32> key,
                                          std::span<const std::uint8_t> message);

struct AeadResult {
    Bytes ciphertext;
    std::array<std::uint8_t, 16> tag{};
};

AeadResult chacha20_poly1305_seal(std::span<const std::uint8_t, 32> key,
                                  std::span<const std::uint8_t, 12> nonce,
                                  std::span<const std::uint8_t> associated_data,
                                  std::span<const std::uint8_t> plaintext);

bool chacha20_poly1305_open(std::span<const std::uint8_t, 32> key,
                            std::span<const std::uint8_t, 12> nonce,
                            std::span<const std::uint8_t> associated_data,
                            std::span<const std::uint8_t> ciphertext,
                            std::span<const std::uint8_t, 16> tag,
                            Bytes& plaintext);

Bytes random_bytes(std::size_t count);

bool constant_time_equal(std::span<const std::uint8_t> lhs,
                         std::span<const std::uint8_t> rhs) noexcept;

}  // namespace netsec::detail
