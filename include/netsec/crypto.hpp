#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

inline constexpr std::size_t kSha256Size = 32;
inline constexpr std::size_t kSha256BlockSize = 64;

using Sha256Digest = std::array<std::uint8_t, kSha256Size>;

class Sha256 {
public:
    Sha256() noexcept;

    void update(std::span<const std::uint8_t> data) noexcept;
    void update(std::string_view data) noexcept;

    [[nodiscard]] Sha256Digest finish() noexcept;

    void reset() noexcept;

private:
    void transform(const std::uint8_t block[kSha256BlockSize]) noexcept;

    std::uint32_t state_[8];
    std::uint64_t bit_length_;
    std::uint8_t buffer_[kSha256BlockSize];
    std::size_t buffer_length_;
    bool finished_;
};

[[nodiscard]] Sha256Digest sha256(std::span<const std::uint8_t> data) noexcept;
[[nodiscard]] Sha256Digest sha256(std::string_view data) noexcept;

[[nodiscard]] Sha256Digest hmac_sha256(std::span<const std::uint8_t> key,
                                       std::span<const std::uint8_t> message) noexcept;

[[nodiscard]] bool constant_time_equal(std::span<const std::uint8_t> lhs,
                                       std::span<const std::uint8_t> rhs) noexcept;

}  // namespace netsec
