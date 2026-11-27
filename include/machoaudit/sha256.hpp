#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <vector>

namespace machoaudit {

// Self-contained FIPS 180-4 SHA-256 implementation. No external dependencies.
class Sha256 {
public:
    using Digest = std::array<std::uint8_t, 32>;

    Sha256();

    void reset();
    void update(const std::uint8_t* data, std::size_t length);
    Digest finalize();

    static Digest hash(const std::uint8_t* data, std::size_t length);
    static Digest hashBytes(const std::vector<std::uint8_t>& data);

private:
    void processBlock(const std::uint8_t* block);

    std::uint32_t state_[8];
    std::uint64_t bitLength_;
    std::uint8_t buffer_[64];
    std::size_t bufferLength_;
    bool finalized_;
};

}  // namespace machoaudit
