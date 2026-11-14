#include "netsec/manifest.hpp"

#include <set>
#include <sstream>

#include "netsec/encoding.hpp"

namespace netsec {

namespace {

void require_no_colon(const std::string& value, const char* field) {
    if (value.find(':') != std::string::npos || value.find('\n') != std::string::npos) {
        throw ParseError(std::string("invalid character in ") + field);
    }
}

Sha256Digest parse_digest(const std::string& hex) {
    if (hex.size() != kSha256Size * 2) {
        throw ParseError("sha256 must be 64 hex characters");
    }
    const std::vector<std::uint8_t> bytes = from_hex(hex);
    Sha256Digest digest{};
    std::copy(bytes.begin(), bytes.end(), digest.begin());
    return digest;
}

std::vector<std::string_view> split_lines(std::string_view text) {
    std::vector<std::string_view> lines;
    std::size_t begin = 0;
    while (begin <= text.size()) {
        const std::size_t newline = text.find('\n', begin);
        if (newline == std::string_view::npos) {
            if (begin < text.size()) {
                lines.push_back(text.substr(begin));
            }
            break;
        }
        lines.push_back(text.substr(begin, newline - begin));
        begin = newline + 1;
    }
    return lines;
}

Outcome validate_internals(const Manifest& manifest) {
    if (manifest.format_version != kManifestFormatVersion) {
        return fail(VerifyCode::UnsupportedFormatVersion,
                    "unsupported manifest format version");
    }
    if (manifest.platform.empty() || manifest.channel.empty()) {
        return fail(VerifyCode::Malformed, "platform and channel must be non-empty");
    }
    if (manifest.chunks.empty()) {
        return fail(VerifyCode::Malformed, "manifest must declare at least one chunk");
    }
    if (manifest.payload_size == 0) {
        return fail(VerifyCode::Malformed, "payload size must be non-zero");
    }

    std::uint64_t total = 0;
    for (std::size_t i = 0; i < manifest.chunks.size(); ++i) {
        const ChunkInfo& chunk = manifest.chunks[i];
        if (chunk.index != i) {
            return fail(VerifyCode::ChunkIndexGap,
                        "chunk indices must start at zero and be contiguous");
        }
        if (chunk.length == 0) {
            return fail(VerifyCode::Malformed, "zero-length chunk is not allowed");
        }
        total += chunk.length;
    }
    if (total != manifest.payload_size) {
        return fail(VerifyCode::PayloadSizeMismatch,
                    "sum of chunk lengths does not equal payload-size");
    }
    return Outcome{};
}

}  // namespace

std::string serialize_canonical(const Manifest& manifest) {
    std::string out;
    out.reserve(256 + manifest.chunks.size() * 96);
    out += "netsec-manifest:";
    out += manifest.format_version;
    out += '\n';
    out += "version:";
    out += to_string(manifest.version);
    out += '\n';
    out += "platform:";
    out += manifest.platform;
    out += '\n';
    out += "channel:";
    out += manifest.channel;
    out += '\n';
    out += "payload-size:";
    out += std::to_string(manifest.payload_size);
    out += '\n';
    out += "payload-sha256:";
    out += to_hex(manifest.payload_sha256);
    out += '\n';
    for (const ChunkInfo& chunk : manifest.chunks) {
        out += "chunk:";
        out += std::to_string(chunk.index);
        out += ':';
        out += std::to_string(chunk.length);
        out += ':';
        out += to_hex(chunk.sha256);
        out += '\n';
    }
    return out;
}

ValueOutcome<Manifest> parse_manifest(std::string_view canonical_body) {
    Manifest manifest;
    bool seen_format = false;
    bool seen_version = false;
    bool seen_platform = false;
    bool seen_channel = false;
    bool seen_size = false;
    bool seen_payload_hash = false;

    try {
        for (std::string_view raw_line : split_lines(canonical_body)) {
            if (raw_line.empty()) {
                return fail_value<Manifest>(VerifyCode::Malformed, "empty line in manifest body");
            }
            const std::size_t first_colon = raw_line.find(':');
            if (first_colon == std::string_view::npos) {
                return fail_value<Manifest>(VerifyCode::Malformed, "manifest line missing ':'");
            }
            const std::string key(raw_line.substr(0, first_colon));
            const std::string value(raw_line.substr(first_colon + 1));

            if (key == "netsec-manifest") {
                if (seen_format) throw ParseError("duplicate format version");
                seen_format = true;
                manifest.format_version = value;
            } else if (key == "version") {
                if (seen_version) throw ParseError("duplicate version");
                seen_version = true;
                manifest.version = Version::parse(value);
            } else if (key == "platform") {
                if (seen_platform) throw ParseError("duplicate platform");
                seen_platform = true;
                require_no_colon(value, key.c_str());
                manifest.platform = value;
            } else if (key == "channel") {
                if (seen_channel) throw ParseError("duplicate channel");
                seen_channel = true;
                require_no_colon(value, key.c_str());
                manifest.channel = value;
            } else if (key == "payload-size") {
                if (seen_size) throw ParseError("duplicate payload-size");
                seen_size = true;
                manifest.payload_size = std::stoull(value);
            } else if (key == "payload-sha256") {
                if (seen_payload_hash) throw ParseError("duplicate payload-sha256");
                seen_payload_hash = true;
                manifest.payload_sha256 = parse_digest(value);
            } else if (key == "chunk") {
                const std::size_t colon1 = value.find(':');
                const std::size_t colon2 = value.find(':', colon1 + 1);
                if (colon1 == std::string::npos || colon2 == std::string::npos ||
                    value.find(':', colon2 + 1) != std::string::npos) {
                    return fail_value<Manifest>(VerifyCode::Malformed,
                                                "chunk line must be chunk:index:length:sha256");
                }
                ChunkInfo chunk;
                chunk.index = std::stoull(value.substr(0, colon1));
                chunk.length = std::stoull(value.substr(colon1 + 1, colon2 - colon1 - 1));
                chunk.sha256 = parse_digest(value.substr(colon2 + 1));
                manifest.chunks.push_back(std::move(chunk));
            } else {
                return fail_value<Manifest>(VerifyCode::Malformed,
                                            "unknown manifest field: " + key);
            }
        }
    } catch (const std::exception& error) {
        return fail_value<Manifest>(VerifyCode::Malformed, error.what());
    }

    if (!seen_format || !seen_version || !seen_platform || !seen_channel || !seen_size ||
        !seen_payload_hash) {
        return fail_value<Manifest>(VerifyCode::Malformed, "missing required manifest field");
    }

    if (Outcome outcome = validate_internals(manifest); !outcome) {
        return fail_value<Manifest>(outcome.code, outcome.detail);
    }
    return success(std::move(manifest));
}

std::string SignedManifest::encode() const {
    std::string wire = body;
    if (wire.empty() || wire.back() != '\n') {
        wire += '\n';
    }
    wire += "signature:";
    wire += key_id;
    wire += ':';
    wire += algorithm;
    wire += ':';
    wire += to_base64(signature);
    wire += '\n';
    return wire;
}

ValueOutcome<SignedManifest> parse_signed_manifest(std::string_view wire) {
    if (wire.empty()) {
        return fail_value<SignedManifest>(VerifyCode::Malformed, "empty signed manifest");
    }
    if (wire.back() == '\n') {
        wire.remove_suffix(1);
    }
    const std::size_t last_newline = wire.rfind('\n');
    if (last_newline == std::string_view::npos) {
        return fail_value<SignedManifest>(VerifyCode::Malformed,
                                          "signed manifest must contain a signature line");
    }

    const std::string_view signature_line = wire.substr(last_newline + 1);
    const std::string_view body = wire.substr(0, last_newline + 1);
    if (signature_line.rfind("signature:", 0) != 0) {
        return fail_value<SignedManifest>(VerifyCode::Malformed,
                                          "final line must be a signature record");
    }
    const std::string_view fields = signature_line.substr(std::string_view("signature:").size());
    const std::size_t colon1 = fields.find(':');
    const std::size_t colon2 = fields.find(':', colon1 + 1);
    if (colon1 == std::string_view::npos || colon2 == std::string_view::npos ||
        fields.find(':', colon2 + 1) != std::string::npos || colon1 == 0) {
        return fail_value<SignedManifest>(VerifyCode::Malformed,
                                          "malformed signature record");
    }

    SignedManifest signed_manifest;
    signed_manifest.body = std::string(body);
    signed_manifest.key_id = std::string(fields.substr(0, colon1));
    signed_manifest.algorithm = std::string(fields.substr(colon1 + 1, colon2 - colon1 - 1));
    try {
        signed_manifest.signature = from_base64(fields.substr(colon2 + 1));
    } catch (const std::exception& error) {
        return fail_value<SignedManifest>(VerifyCode::Malformed, error.what());
    }
    if (signed_manifest.signature.size() != kSha256Size) {
        return fail_value<SignedManifest>(VerifyCode::BadSignature,
                                          "signature has unexpected length");
    }
    return success(std::move(signed_manifest));
}

HmacManifestSigner::HmacManifestSigner(std::string key_id,
                                       std::span<const std::uint8_t> key)
    : key_id_(std::move(key_id)), key_(key.begin(), key.end()) {}

std::string HmacManifestSigner::key_id() const {
    return key_id_;
}

std::string HmacManifestSigner::algorithm() const {
    return std::string(kHmacSha256Algorithm);
}

std::vector<std::uint8_t> HmacManifestSigner::sign(
    std::span<const std::uint8_t> canonical_bytes) const {
    const Sha256Digest digest = hmac_sha256(key_, canonical_bytes);
    return std::vector<std::uint8_t>(digest.begin(), digest.end());
}

HmacManifestVerifier::HmacManifestVerifier(std::span<const std::uint8_t> key)
    : key_(key.begin(), key.end()) {}

Outcome HmacManifestVerifier::verify(const std::string&,
                                     const std::string& algorithm,
                                     std::span<const std::uint8_t> canonical_bytes,
                                     std::span<const std::uint8_t> signature) const {
    if (algorithm != kHmacSha256Algorithm) {
        return fail(VerifyCode::UnsupportedAlgorithm,
                    "expected algorithm hmac-sha256, got " + algorithm);
    }
    const Sha256Digest expected = hmac_sha256(key_, canonical_bytes);
    if (!constant_time_equal(expected, signature)) {
        return fail(VerifyCode::BadSignature, "manifest signature mismatch");
    }
    return Outcome{};
}

KeyedManifestVerifier::KeyedManifestVerifier(std::vector<KeyEntry> keys)
    : keys_(std::move(keys)) {}

Outcome KeyedManifestVerifier::verify(const std::string& key_id,
                                      const std::string& algorithm,
                                      std::span<const std::uint8_t> canonical_bytes,
                                      std::span<const std::uint8_t> signature) const {
    for (const KeyEntry& entry : keys_) {
        if (entry.key_id == key_id) {
            const HmacManifestVerifier verifier(entry.key);
            return verifier.verify(key_id, algorithm, canonical_bytes, signature);
        }
    }
    return fail(VerifyCode::UnknownKey, "no trusted key matches key-id " + key_id);
}

ValueOutcome<SignedManifest> sign_manifest(const Manifest& manifest,
                                           const IManifestSigner& signer) {
    if (Outcome outcome = validate_internals(manifest); !outcome) {
        return fail_value<SignedManifest>(outcome.code, outcome.detail);
    }

    SignedManifest signed_manifest;
    signed_manifest.body = serialize_canonical(manifest);
    signed_manifest.key_id = signer.key_id();
    signed_manifest.algorithm = signer.algorithm();
    signed_manifest.signature = signer.sign(std::span<const std::uint8_t>(
        reinterpret_cast<const std::uint8_t*>(signed_manifest.body.data()),
        signed_manifest.body.size()));
    return success(std::move(signed_manifest));
}

ValueOutcome<Manifest> verify_signed_manifest(const SignedManifest& signed_manifest,
                                              const IManifestVerifier& verifier) {
    auto parsed = parse_manifest(signed_manifest.body);
    if (!parsed) {
        return fail_value<Manifest>(parsed.code, parsed.detail);
    }
    if (parsed.value.format_version != kManifestFormatVersion) {
        return fail_value<Manifest>(VerifyCode::UnsupportedFormatVersion,
                                    "unsupported manifest format version");
    }

    const Outcome signature_result = verifier.verify(
        signed_manifest.key_id, signed_manifest.algorithm,
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(signed_manifest.body.data()),
            signed_manifest.body.size()),
        signed_manifest.signature);
    if (!signature_result) {
        return fail_value<Manifest>(signature_result.code, signature_result.detail);
    }
    return success(std::move(parsed.value));
}

}  // namespace netsec
