#include "netsec/verification.hpp"

#include <fstream>

#include "netsec/encoding.hpp"

namespace netsec {

ChunkVerifier::ChunkVerifier(const Manifest& manifest)
    : manifest_(manifest), seen_(manifest.chunks.size(), false) {}

Outcome ChunkVerifier::verify_chunk(std::uint64_t index,
                                    std::span<const std::uint8_t> chunk_bytes) {
    if (failed_) {
        return fail(VerifyCode::ChunkHashMismatch, "verifier already failed");
    }
    if (index >= manifest_.chunks.size()) {
        failed_ = true;
        return fail(VerifyCode::ChunkIndexGap, "chunk index out of manifest range");
    }
    if (seen_[static_cast<std::size_t>(index)]) {
        failed_ = true;
        return fail(VerifyCode::ChunkIndexGap, "chunk was submitted twice");
    }

    const ChunkInfo& expected = manifest_.chunks[static_cast<std::size_t>(index)];
    if (chunk_bytes.size() != expected.length) {
        failed_ = true;
        return fail(VerifyCode::ChunkLengthMismatch,
                    "chunk " + std::to_string(index) + " has length " +
                        std::to_string(chunk_bytes.size()) + ", expected " +
                        std::to_string(expected.length));
    }

    const Sha256Digest actual = sha256(chunk_bytes);
    if (!constant_time_equal(actual, expected.sha256)) {
        failed_ = true;
        return fail(VerifyCode::ChunkHashMismatch,
                    "chunk " + std::to_string(index) + " sha256 mismatch");
    }

    seen_[static_cast<std::size_t>(index)] = true;
    observed_bytes_ += chunk_bytes.size();
    payload_hasher_.update(chunk_bytes);
    return Outcome{};
}

Outcome ChunkVerifier::finish() const {
    if (failed_) {
        return fail(VerifyCode::ChunkHashMismatch, "verifier already failed");
    }
    for (std::size_t i = 0; i < seen_.size(); ++i) {
        if (!seen_[i]) {
            return fail(VerifyCode::ChunkIndexGap,
                        "chunk " + std::to_string(i) + " was never verified");
        }
    }
    if (observed_bytes_ != manifest_.payload_size) {
        return fail(VerifyCode::PayloadSizeMismatch,
                    "verified payload size does not match manifest");
    }

    Sha256 hasher = payload_hasher_;
    const Sha256Digest payload_digest = hasher.finish();
    if (!constant_time_equal(payload_digest, manifest_.payload_sha256)) {
        return fail(VerifyCode::PayloadHashMismatch,
                    "recomputed payload sha256 does not match manifest");
    }
    return Outcome{};
}

Outcome verify_payload_chunks(const Manifest& manifest,
                              std::span<const std::uint8_t> payload) {
    if (payload.size() != manifest.payload_size) {
        return fail(VerifyCode::PayloadSizeMismatch,
                    "payload size " + std::to_string(payload.size()) +
                        " does not match manifest " + std::to_string(manifest.payload_size));
    }

    ChunkVerifier verifier(manifest);
    std::size_t offset = 0;
    for (std::uint64_t index = 0;
         index < manifest.chunks.size() && offset < payload.size(); ++index) {
        const std::size_t length =
            static_cast<std::size_t>(manifest.chunks[static_cast<std::size_t>(index)].length);
        if (offset + length > payload.size()) {
            return fail(VerifyCode::ChunkLengthMismatch,
                        "chunk extends past payload boundary");
        }
        const Outcome outcome =
            verifier.verify_chunk(index, payload.subspan(offset, length));
        if (!outcome) {
            return outcome;
        }
        offset += length;
    }
    return verifier.finish();
}

Sha256Digest read_back_hash(const std::filesystem::path& installed_path) {
    std::ifstream stream(installed_path, std::ios::binary);
    if (!stream) {
        throw std::filesystem::filesystem_error(
            "cannot open installed file for readback", installed_path,
            std::make_error_code(std::errc::io_error));
    }

    Sha256 hasher;
    constexpr std::size_t buffer_size = 64 * 1024;
    std::vector<char> buffer(buffer_size);
    std::uint64_t total = 0;
    while (stream) {
        stream.read(buffer.data(), static_cast<std::streamsize>(buffer.size()));
        const std::streamsize read = stream.gcount();
        if (read > 0) {
            hasher.update(std::span<const std::uint8_t>(
                reinterpret_cast<const std::uint8_t*>(buffer.data()),
                static_cast<std::size_t>(read)));
            total += static_cast<std::uint64_t>(read);
        }
    }
    (void)total;
    return hasher.finish();
}

Outcome verify_installed_readback(const Manifest& manifest,
                                  const std::filesystem::path& installed_path) {
    std::error_code error;
    if (!std::filesystem::exists(installed_path, error) || error) {
        return fail(VerifyCode::IoError,
                    "installed path does not exist: " + installed_path.string());
    }
    const auto actual_size = std::filesystem::file_size(installed_path, error);
    if (error) {
        return fail(VerifyCode::IoError,
                    "cannot stat installed path: " + error.message());
    }
    if (static_cast<std::uint64_t>(actual_size) != manifest.payload_size) {
        return fail(VerifyCode::ReadbackMismatch,
                    "readback size " + std::to_string(actual_size) +
                        " does not match manifest payload size " +
                        std::to_string(manifest.payload_size));
    }

    const Sha256Digest actual_hash = read_back_hash(installed_path);
    if (!constant_time_equal(actual_hash, manifest.payload_sha256)) {
        return fail(VerifyCode::ReadbackMismatch,
                    "readback sha256 " + to_hex(actual_hash) +
                        " does not match manifest payload sha256");
    }
    return Outcome{};
}

}  // namespace netsec
