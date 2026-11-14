#pragma once

#include <cstdint>
#include <filesystem>
#include <span>
#include <vector>

#include "netsec/manifest.hpp"
#include "netsec/result.hpp"

namespace netsec {

class ChunkVerifier {
public:
    explicit ChunkVerifier(const Manifest& manifest);

    Outcome verify_chunk(std::uint64_t index,
                         std::span<const std::uint8_t> chunk_bytes);

    [[nodiscard]] Outcome finish() const;

private:
    Manifest manifest_;
    std::vector<bool> seen_;
    std::uint64_t observed_bytes_{0};
    Sha256 payload_hasher_;
    bool failed_{false};
};

Outcome verify_payload_chunks(const Manifest& manifest,
                              std::span<const std::uint8_t> payload);

Sha256Digest read_back_hash(const std::filesystem::path& installed_path);

Outcome verify_installed_readback(const Manifest& manifest,
                                  const std::filesystem::path& installed_path);

}  // namespace netsec
