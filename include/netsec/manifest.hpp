#pragma once

#include <cstdint>
#include <memory>
#include <span>
#include <string>
#include <vector>

#include "netsec/crypto.hpp"
#include "netsec/result.hpp"
#include "netsec/version.hpp"

namespace netsec {

inline constexpr std::string_view kManifestFormatVersion = "1";
inline constexpr std::string_view kHmacSha256Algorithm = "hmac-sha256";

struct ChunkInfo {
    std::uint64_t index{0};
    std::uint64_t length{0};
    Sha256Digest sha256{};

    auto operator<=>(const ChunkInfo&) const = default;
};

struct Manifest {
    std::string format_version;
    Version version{};
    std::string platform;
    std::string channel;
    std::uint64_t payload_size{0};
    Sha256Digest payload_sha256{};
    std::vector<ChunkInfo> chunks;
};

std::string serialize_canonical(const Manifest& manifest);

ValueOutcome<Manifest> parse_manifest(std::string_view canonical_body);

struct SignedManifest {
    std::string body;
    std::string key_id;
    std::string algorithm;
    std::vector<std::uint8_t> signature;

    [[nodiscard]] std::string encode() const;
};

ValueOutcome<SignedManifest> parse_signed_manifest(std::string_view wire);

class IManifestSigner {
public:
    virtual ~IManifestSigner() = default;

    [[nodiscard]] virtual std::string key_id() const = 0;
    [[nodiscard]] virtual std::string algorithm() const = 0;
    [[nodiscard]] virtual std::vector<std::uint8_t> sign(
        std::span<const std::uint8_t> canonical_bytes) const = 0;
};

class IManifestVerifier {
public:
    virtual ~IManifestVerifier() = default;

    [[nodiscard]] virtual Outcome verify(const std::string& key_id,
                                         const std::string& algorithm,
                                         std::span<const std::uint8_t> canonical_bytes,
                                         std::span<const std::uint8_t> signature) const = 0;
};

class HmacManifestSigner final : public IManifestSigner {
public:
    HmacManifestSigner(std::string key_id, std::span<const std::uint8_t> key);

    [[nodiscard]] std::string key_id() const override;
    [[nodiscard]] std::string algorithm() const override;
    [[nodiscard]] std::vector<std::uint8_t> sign(
        std::span<const std::uint8_t> canonical_bytes) const override;

private:
    std::string key_id_;
    std::vector<std::uint8_t> key_;
};

class HmacManifestVerifier final : public IManifestVerifier {
public:
    explicit HmacManifestVerifier(std::span<const std::uint8_t> key);

    [[nodiscard]] Outcome verify(const std::string& key_id,
                                 const std::string& algorithm,
                                 std::span<const std::uint8_t> canonical_bytes,
                                 std::span<const std::uint8_t> signature) const override;

private:
    std::vector<std::uint8_t> key_;
};

class KeyedManifestVerifier final : public IManifestVerifier {
public:
    struct KeyEntry {
        std::string key_id;
        std::vector<std::uint8_t> key;
    };

    explicit KeyedManifestVerifier(std::vector<KeyEntry> keys);

    [[nodiscard]] Outcome verify(const std::string& key_id,
                                 const std::string& algorithm,
                                 std::span<const std::uint8_t> canonical_bytes,
                                 std::span<const std::uint8_t> signature) const override;

private:
    std::vector<KeyEntry> keys_;
};

ValueOutcome<SignedManifest> sign_manifest(const Manifest& manifest,
                                           const IManifestSigner& signer);

ValueOutcome<Manifest> verify_signed_manifest(const SignedManifest& signed_manifest,
                                              const IManifestVerifier& verifier);

}  // namespace netsec
