#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <fstream>
#include <ios>
#include <map>
#include <set>
#include <system_error>

#ifndef _WIN32
#include <unistd.h>
#endif

namespace netsec {

namespace {

using u8 = std::uint8_t;
using u32 = std::uint32_t;
using u64 = std::uint64_t;

constexpr std::array<u32, 64> kSha256K = {
    0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu,
    0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u,
    0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u,
    0xc19bf174u, 0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
    0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau, 0x983e5152u,
    0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
    0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu,
    0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
    0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u,
    0xd6990624u, 0xf40e3585u, 0x106aa070u, 0x19a4c116u, 0x1e376c08u,
    0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu,
    0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u,
};

constexpr std::array<u8, 16> kPayloadMagic = {
    'N', 'E', 'T', 'S', 'E', 'C', '-', 'P',
    'A', 'C', 'K', 'A', 'G', 'E', '1', 0,
};

inline u32 rotr(u32 value, unsigned count) {
    return (value >> count) | (value << (32u - count));
}

Error make_error(ErrorCode code, std::string message,
                 std::string path = {}, std::size_t offset = kNoOffset) {
    Error error;
    error.code = code;
    error.message = std::move(message);
    error.path = std::move(path);
    error.offset = offset;
    return error;
}

std::string hex_encode(ByteView bytes) {
    static constexpr char kHex[] = "0123456789abcdef";
    std::string result;
    result.resize(bytes.size() * 2);
    for (std::size_t i = 0; i < bytes.size(); ++i) {
        result[i * 2] = kHex[bytes[i] >> 4];
        result[i * 2 + 1] = kHex[bytes[i] & 0x0fu];
    }
    return result;
}

Digest sha256_impl(ByteView data) {
    std::array<u32, 8> state = {
        0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
        0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u,
    };

    Bytes padded;
    padded.reserve(data.size() + 72);
    padded.insert(padded.end(), data.begin(), data.end());
    padded.push_back(0x80u);
    while ((padded.size() % 64u) != 56u) {
        padded.push_back(0u);
    }

    const u64 bit_length = static_cast<u64>(data.size()) * 8u;
    for (int shift = 56; shift >= 0; shift -= 8) {
        padded.push_back(static_cast<u8>((bit_length >> static_cast<unsigned>(shift)) & 0xffu));
    }

    for (std::size_t offset = 0; offset < padded.size(); offset += 64) {
        std::array<u32, 64> schedule{};
        for (std::size_t i = 0; i < 16; ++i) {
            const std::size_t p = offset + i * 4;
            schedule[i] = (static_cast<u32>(padded[p]) << 24u) |
                          (static_cast<u32>(padded[p + 1]) << 16u) |
                          (static_cast<u32>(padded[p + 2]) << 8u) |
                          static_cast<u32>(padded[p + 3]);
        }
        for (std::size_t i = 16; i < 64; ++i) {
            const u32 s0 = rotr(schedule[i - 15], 7) ^ rotr(schedule[i - 15], 18) ^
                           (schedule[i - 15] >> 3);
            const u32 s1 = rotr(schedule[i - 2], 17) ^ rotr(schedule[i - 2], 19) ^
                           (schedule[i - 2] >> 10);
            schedule[i] = schedule[i - 16] + s0 + schedule[i - 7] + s1;
        }

        u32 a = state[0];
        u32 b = state[1];
        u32 c = state[2];
        u32 d = state[3];
        u32 e = state[4];
        u32 f = state[5];
        u32 g = state[6];
        u32 h = state[7];

        for (std::size_t i = 0; i < 64; ++i) {
            const u32 s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
            const u32 choose = (e & f) ^ ((~e) & g);
            const u32 temp1 = h + s1 + choose + kSha256K[i] + schedule[i];
            const u32 s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
            const u32 majority = (a & b) ^ (a & c) ^ (b & c);
            const u32 temp2 = s0 + majority;

            h = g;
            g = f;
            f = e;
            e = d + temp1;
            d = c;
            c = b;
            b = a;
            a = temp1 + temp2;
        }

        state[0] += a;
        state[1] += b;
        state[2] += c;
        state[3] += d;
        state[4] += e;
        state[5] += f;
        state[6] += g;
        state[7] += h;
    }

    Digest digest;
    for (std::size_t i = 0; i < state.size(); ++i) {
        digest.bytes[i * 4] = static_cast<u8>(state[i] >> 24u);
        digest.bytes[i * 4 + 1] = static_cast<u8>(state[i] >> 16u);
        digest.bytes[i * 4 + 2] = static_cast<u8>(state[i] >> 8u);
        digest.bytes[i * 4 + 3] = static_cast<u8>(state[i]);
    }
    return digest;
}

struct Utf8CodePoint {
    u32 value = 0;
    std::size_t width = 0;
};

bool decode_utf8(ByteView input, std::size_t offset, Utf8CodePoint& result) {
    const std::size_t remaining = input.size() - offset;
    const u8 first = input[offset];

    if (first < 0x80u) {
        result = {first, 1};
        return true;
    }

    if (first >= 0xc2u && first <= 0xdfu) {
        if (remaining < 2) {
            return false;
        }
        const u8 second = input[offset + 1];
        if ((second & 0xc0u) != 0x80u) {
            return false;
        }
        result = {static_cast<u32>(((first & 0x1fu) << 6u) | (second & 0x3fu)), 2};
        return true;
    }

    if (first >= 0xe0u && first <= 0xefu) {
        if (remaining < 3) {
            return false;
        }
        const u8 second = input[offset + 1];
        const u8 third = input[offset + 2];
        if ((third & 0xc0u) != 0x80u) {
            return false;
        }
        if (first == 0xe0u && (second < 0xa0u || second > 0xbfu)) {
            return false;
        }
        if (first == 0xedu && (second < 0x80u || second > 0x9fu)) {
            return false;
        }
        if (first != 0xe0u && first != 0xedu && (second & 0xc0u) != 0x80u) {
            return false;
        }
        result = {
            static_cast<u32>(((first & 0x0fu) << 12u) |
                             ((second & 0x3fu) << 6u) |
                             (third & 0x3fu)),
            3,
        };
        return true;
    }

    if (first >= 0xf0u && first <= 0xf4u) {
        if (remaining < 4) {
            return false;
        }
        const u8 second = input[offset + 1];
        const u8 third = input[offset + 2];
        const u8 fourth = input[offset + 3];
        if ((third & 0xc0u) != 0x80u || (fourth & 0xc0u) != 0x80u) {
            return false;
        }
        if (first == 0xf0u && (second < 0x90u || second > 0xbfu)) {
            return false;
        }
        if (first == 0xf4u && (second < 0x80u || second > 0x8fu)) {
            return false;
        }
        if (first != 0xf0u && first != 0xf4u && (second & 0xc0u) != 0x80u) {
            return false;
        }
        result = {
            static_cast<u32>(((first & 0x07u) << 18u) |
                             ((second & 0x3fu) << 12u) |
                             ((third & 0x3fu) << 6u) |
                             (fourth & 0x3fu)),
            4,
        };
        return true;
    }

    return false;
}

bool is_invisible_format_character(u32 value) {
    switch (value) {
        case 0x00adu:  // soft hyphen
        case 0x061cu:  // Arabic letter mark
        case 0x180eu:  // Mongolian vowel separator
        case 0xfeffu:  // zero width no-break space / BOM
            return true;
        default:
            break;
    }
    return (value >= 0x200bu && value <= 0x200fu) ||
           (value >= 0x202au && value <= 0x202eu) ||
           (value >= 0x2060u && value <= 0x206fu) ||
           (value >= 0xfff9u && value <= 0xfffbu) ||
           (value >= 0x1d173u && value <= 0x1d17au) ||
           value == 0xe0001u ||
           (value >= 0xe0020u && value <= 0xe007fu);
}

bool is_disallowed_control(u32 value) {
    return (value < 0x20u && value != 0x09u && value != 0x0au) ||
           (value >= 0x7fu && value <= 0x9fu);
}

bool validate_utf8(ByteView input, std::size_t& error_offset, std::string& reason) {
    std::size_t offset = 0;
    while (offset < input.size()) {
        Utf8CodePoint code_point;
        if (!decode_utf8(input, offset, code_point)) {
            error_offset = offset;
            reason = "invalid UTF-8 byte sequence";
            return false;
        }
        offset += code_point.width;
    }
    return true;
}

std::string ascii_lower(std::string_view text) {
    std::string result(text);
    for (char& ch : result) {
        const unsigned char byte = static_cast<unsigned char>(ch);
        if (byte >= 'A' && byte <= 'Z') {
            ch = static_cast<char>(byte + ('a' - 'A'));
        }
    }
    return result;
}

bool is_valid_token(std::string_view token) {
    if (token.empty() || token.size() > 255) {
        return false;
    }
    for (const unsigned char byte : token) {
        if (byte < 0x21u || byte > 0x7eu) {
            return false;
        }
    }
    return true;
}

bool is_valid_role(EntryRole role) {
    return role == EntryRole::Script || role == EntryRole::Config ||
           role == EntryRole::Data;
}

CanonicalPathPolicy path_policy_for(const Limits& limits) {
    CanonicalPathPolicy policy;
    policy.max_bytes = limits.max_path_bytes;
    return policy;
}

CanonicalTextPolicy text_policy_for(const Limits& limits) {
    CanonicalTextPolicy policy;
    policy.max_bytes = limits.max_entry_bytes;
    return policy;
}

Result<Manifest> normalize_manifest_impl(const Manifest& input, const Limits& limits) {
    if (input.entries.size() > limits.max_entries) {
        return make_error(ErrorCode::LimitExceeded, "too many manifest entries");
    }
    if (!is_valid_token(input.package_id)) {
        return make_error(ErrorCode::InvalidManifest, "invalid package id");
    }
    if (!is_valid_token(input.version)) {
        return make_error(ErrorCode::InvalidManifest, "invalid package version");
    }

    Manifest result;
    result.package_id = input.package_id;
    result.version = input.version;
    result.entries.reserve(input.entries.size());

    const CanonicalPathPolicy path_policy = path_policy_for(limits);
    for (const ManifestEntry& entry : input.entries) {
        if (!is_valid_role(entry.role)) {
            return make_error(ErrorCode::InvalidManifest, "invalid entry role",
                              entry.path);
        }

        Result<std::string> path = canonicalize_path(entry.path, path_policy);
        if (!path) {
            return path.error();
        }

        ManifestEntry normalized;
        normalized.path = std::move(path.value());
        normalized.role = entry.role;
        normalized.path_digest = sha256_impl(as_bytes(normalized.path));
        normalized.content_digest = entry.content_digest;
        result.entries.push_back(std::move(normalized));
    }

    std::sort(result.entries.begin(), result.entries.end(),
              [](const ManifestEntry& lhs, const ManifestEntry& rhs) {
                  return lhs.path < rhs.path;
              });

    std::set<std::string> exact_paths;
    std::set<std::string> lowercase_paths;
    for (const ManifestEntry& entry : result.entries) {
        if (!exact_paths.insert(entry.path).second) {
            return make_error(ErrorCode::DuplicatePath,
                              "duplicate canonical path", entry.path);
        }
        if (!lowercase_paths.insert(ascii_lower(entry.path)).second) {
            return make_error(ErrorCode::PathCollision,
                              "paths differ only by ASCII case", entry.path);
        }
    }

    return result;
}

Result<Bytes> render_manifest(const Manifest& manifest, const Limits& limits) {
    std::string rendered;
    rendered.reserve(128 + manifest.entries.size() * 160);
    rendered += "NETSEC-MANIFEST\t1\n";
    rendered += "package\t";
    rendered += manifest.package_id;
    rendered += '\n';
    rendered += "version\t";
    rendered += manifest.version;
    rendered += '\n';

    for (const ManifestEntry& entry : manifest.entries) {
        rendered += "entry\t";
        rendered += to_string(entry.role);
        rendered += '\t';
        rendered += entry.path_digest.hex();
        rendered += '\t';
        rendered += entry.content_digest.hex();
        rendered += '\t';
        rendered += entry.path;
        rendered += '\n';
    }

    if (rendered.size() > limits.max_manifest_bytes) {
        return make_error(ErrorCode::LimitExceeded, "manifest is too large");
    }

    Bytes bytes(rendered.begin(), rendered.end());
    return bytes;
}

Result<Bytes> make_signing_payload(const Manifest& manifest,
                                   ByteView canonical_manifest,
                                   const Limits& limits) {
    if (canonical_manifest.size() > 0xffffffffu ||
        manifest.entries.size() > 0xffffffffu) {
        return make_error(ErrorCode::LimitExceeded, "signing payload is too large");
    }

    Bytes payload;
    payload.reserve(16 + 4 + 4 + canonical_manifest.size() + 4 +
                    manifest.entries.size() * 80);

    payload.insert(payload.end(), kPayloadMagic.begin(), kPayloadMagic.end());

    auto append_u32 = [&payload](u32 value) {
        payload.push_back(static_cast<u8>(value >> 24u));
        payload.push_back(static_cast<u8>(value >> 16u));
        payload.push_back(static_cast<u8>(value >> 8u));
        payload.push_back(static_cast<u8>(value));
    };

    append_u32(1u);  // payload version
    append_u32(static_cast<u32>(canonical_manifest.size()));
    payload.insert(payload.end(), canonical_manifest.begin(), canonical_manifest.end());
    append_u32(static_cast<u32>(manifest.entries.size()));

    for (const ManifestEntry& entry : manifest.entries) {
        if (entry.path.size() > 0xffffffffu) {
            return make_error(ErrorCode::LimitExceeded, "path is too large",
                              entry.path);
        }
        append_u32(static_cast<u32>(entry.path.size()));
        payload.insert(payload.end(), entry.path.begin(), entry.path.end());
        payload.push_back(static_cast<u8>(entry.role));
        payload.insert(payload.end(), entry.path_digest.bytes.begin(),
                       entry.path_digest.bytes.end());
        payload.insert(payload.end(), entry.content_digest.bytes.begin(),
                       entry.content_digest.bytes.end());
    }

    (void)limits;
    return payload;
}

struct PreparedManifest {
    Manifest manifest;
    Bytes manifest_bytes;
    Bytes signing_payload;
    Digest manifest_digest{};
    Digest root_digest{};
};

Result<PreparedManifest> prepare_manifest(ByteView raw, const Limits& limits) {
    Result<Manifest> parsed = parse_manifest(raw, limits);
    if (!parsed) {
        return parsed.error();
    }

    Result<Bytes> manifest_bytes = render_manifest(parsed.value(), limits);
    if (!manifest_bytes) {
        return manifest_bytes.error();
    }

    Result<Bytes> signing_payload =
        make_signing_payload(parsed.value(), manifest_bytes.value(), limits);
    if (!signing_payload) {
        return signing_payload.error();
    }

    PreparedManifest prepared;
    prepared.manifest = std::move(parsed.value());
    prepared.manifest_bytes = std::move(manifest_bytes.value());
    prepared.signing_payload = std::move(signing_payload.value());
    prepared.manifest_digest = sha256_impl(prepared.manifest_bytes);
    prepared.root_digest = sha256_impl(prepared.signing_payload);
    return prepared;
}

Result<std::vector<CanonicalEntry>> assemble_entries(
    const Manifest& manifest,
    const std::vector<PackageFile>& files,
    const Limits& limits,
    bool reject_unlisted) {
    std::map<std::string, const PackageFile*> files_by_path;
    std::map<std::string, std::string> paths_by_lowercase;
    const CanonicalPathPolicy path_policy = path_policy_for(limits);

    for (const PackageFile& file : files) {
        if (file.bytes.size() > limits.max_entry_bytes) {
            return make_error(ErrorCode::LimitExceeded, "package entry is too large",
                              file.path);
        }

        Result<std::string> canonical_path = canonicalize_path(file.path, path_policy);
        if (!canonical_path) {
            return canonical_path.error();
        }

        const auto inserted = files_by_path.emplace(canonical_path.value(), &file);
        if (!inserted.second) {
            return make_error(ErrorCode::DuplicatePath,
                              "duplicate canonical source path",
                              canonical_path.value());
        }

        const std::string lower = ascii_lower(canonical_path.value());
        const auto lower_inserted =
            paths_by_lowercase.emplace(lower, canonical_path.value());
        if (!lower_inserted.second) {
            return make_error(ErrorCode::PathCollision,
                              "source paths differ only by ASCII case",
                              canonical_path.value());
        }
    }

    std::set<std::string> manifest_paths;
    for (const ManifestEntry& entry : manifest.entries) {
        manifest_paths.insert(entry.path);
    }

    if (reject_unlisted) {
        for (const auto& [path, file] : files_by_path) {
            (void)file;
            if (manifest_paths.find(path) == manifest_paths.end()) {
                return make_error(ErrorCode::UnlistedEntry,
                                  "source file is not listed in the manifest", path);
            }
        }
    }

    std::vector<CanonicalEntry> entries;
    entries.reserve(manifest.entries.size());
    const CanonicalTextPolicy text_policy = text_policy_for(limits);

    for (const ManifestEntry& manifest_entry : manifest.entries) {
        const auto found = files_by_path.find(manifest_entry.path);
        if (found == files_by_path.end()) {
            return make_error(ErrorCode::MissingEntry,
                              "manifest entry has no source file",
                              manifest_entry.path);
        }

        Bytes canonical_bytes;
        if (is_text_role(manifest_entry.role)) {
            Result<Bytes> text =
                canonicalize_text(found->second->bytes, text_policy);
            if (!text) {
                return text.error();
            }
            canonical_bytes = std::move(text.value());
        } else {
            canonical_bytes = found->second->bytes;
        }

        if (canonical_bytes.size() > limits.max_entry_bytes) {
            return make_error(ErrorCode::LimitExceeded, "package entry is too large",
                              manifest_entry.path);
        }

        const Digest content_digest = sha256_impl(canonical_bytes);
        if (content_digest != manifest_entry.content_digest) {
            return make_error(ErrorCode::DigestMismatch,
                              "canonical content digest does not match manifest",
                              manifest_entry.path);
        }

        CanonicalEntry entry;
        entry.path = manifest_entry.path;
        entry.role = manifest_entry.role;
        entry.bytes = std::move(canonical_bytes);
        entry.path_digest = sha256_impl(as_bytes(entry.path));
        entry.content_digest = content_digest;
        if (entry.path_digest != manifest_entry.path_digest) {
            return make_error(ErrorCode::DigestMismatch,
                              "path digest does not match manifest",
                              manifest_entry.path);
        }
        entries.push_back(std::move(entry));
    }

    return entries;
}

Result<Bytes> read_regular_file(const std::filesystem::path& path,
                                std::size_t max_bytes) {
    std::error_code error;
    const std::filesystem::file_status status =
        std::filesystem::symlink_status(path, error);
    if (error) {
        return make_error(ErrorCode::IoError, "cannot inspect file",
                          path.string());
    }
    if (!std::filesystem::exists(status)) {
        return make_error(ErrorCode::IoError, "file does not exist",
                          path.string());
    }
    if (std::filesystem::is_symlink(status)) {
        return make_error(ErrorCode::IoError, "symlinks are not allowed",
                          path.string());
    }
    if (!std::filesystem::is_regular_file(status)) {
        return make_error(ErrorCode::IoError, "not a regular file",
                          path.string());
    }

    const std::uintmax_t size = std::filesystem::file_size(path, error);
    if (error) {
        return make_error(ErrorCode::IoError, "cannot determine file size",
                          path.string());
    }
    if (size > max_bytes) {
        return make_error(ErrorCode::LimitExceeded, "file is too large",
                          path.string());
    }

    std::ifstream input(path, std::ios::binary);
    if (!input) {
        return make_error(ErrorCode::IoError, "cannot open file", path.string());
    }

    Bytes bytes(static_cast<std::size_t>(size));
    if (!bytes.empty()) {
        input.read(reinterpret_cast<char*>(bytes.data()),
                   static_cast<std::streamsize>(bytes.size()));
    }
    if (!input || input.gcount() != static_cast<std::streamsize>(bytes.size())) {
        return make_error(ErrorCode::IoError, "cannot read complete file",
                          path.string());
    }
    return bytes;
}

Status prepare_destination_root(const std::filesystem::path& root) {
    std::error_code error;
    std::filesystem::file_status status =
        std::filesystem::symlink_status(root, error);
    if (error == std::errc::no_such_file_or_directory) {
        error.clear();
        status = std::filesystem::file_status{};
    } else if (error) {
        return Status::failure(make_error(ErrorCode::IoError,
                                          "cannot inspect destination",
                                          root.string()));
    }

    if (std::filesystem::is_symlink(status)) {
        return Status::failure(make_error(ErrorCode::IoError,
                                          "destination root is a symlink",
                                          root.string()));
    }
    if (std::filesystem::exists(status)) {
        if (!std::filesystem::is_directory(status)) {
            return Status::failure(make_error(ErrorCode::IoError,
                                              "destination root is not a directory",
                                              root.string()));
        }
        return Status::success();
    }

    if (!std::filesystem::create_directories(root, error) || error) {
        return Status::failure(make_error(ErrorCode::IoError,
                                          "cannot create destination",
                                          root.string()));
    }
    return Status::success();
}

Status ensure_parent_directories(const std::filesystem::path& root,
                                 std::string_view relative) {
    std::filesystem::path current = root;
    std::size_t start = 0;
    while (true) {
        const std::size_t slash = relative.find('/', start);
        if (slash == std::string_view::npos) {
            break;
        }
        const std::string component(relative.substr(start, slash - start));
        start = slash + 1;
        if (component.empty()) {
            continue;
        }
        current /= component;

        std::error_code error;
        const std::filesystem::file_status status =
            std::filesystem::symlink_status(current, error);
        if (error == std::errc::no_such_file_or_directory) {
            error.clear();
            if (!std::filesystem::create_directory(current, error) || error) {
                return Status::failure(make_error(ErrorCode::IoError,
                                                  "cannot create package directory",
                                                  current.string()));
            }
            continue;
        }
        if (error) {
            return Status::failure(make_error(ErrorCode::IoError,
                                              "cannot inspect package directory",
                                              current.string()));
        }
        if (!std::filesystem::exists(status)) {
            if (!std::filesystem::create_directory(current, error) || error) {
                return Status::failure(make_error(ErrorCode::IoError,
                                                  "cannot create package directory",
                                                  current.string()));
            }
            continue;
        }
        if (std::filesystem::is_symlink(status)) {
            return Status::failure(make_error(ErrorCode::IoError,
                                              "package directory is a symlink",
                                              current.string()));
        }
        if (!std::filesystem::is_directory(status)) {
            return Status::failure(make_error(ErrorCode::IoError,
                                              "package path component is not a directory",
                                              current.string()));
        }
    }
    return Status::success();
}

Status write_regular_file(const std::filesystem::path& root,
                          std::string_view relative,
                          ByteView bytes,
                          bool overwrite,
                          bool executable) {
    Status parents = ensure_parent_directories(root, relative);
    if (!parents) {
        return parents;
    }

    std::filesystem::path target = root;
    target /= std::filesystem::path(std::string(relative));

    std::error_code error;
    std::filesystem::file_status status =
        std::filesystem::symlink_status(target, error);
    if (error == std::errc::no_such_file_or_directory) {
        error.clear();
        status = std::filesystem::file_status{};
    } else if (error) {
        return Status::failure(make_error(ErrorCode::IoError,
                                          "cannot inspect destination file",
                                          target.string()));
    }

    if (std::filesystem::is_symlink(status)) {
        return Status::failure(make_error(ErrorCode::IoError,
                                          "destination file is a symlink",
                                          target.string()));
    }
    if (std::filesystem::exists(status)) {
        if (!std::filesystem::is_regular_file(status)) {
            return Status::failure(make_error(ErrorCode::IoError,
                                              "destination is not a regular file",
                                              target.string()));
        }
        if (!overwrite) {
            return Status::failure(make_error(ErrorCode::IoError,
                                              "destination already exists",
                                              target.string()));
        }
    }

    static std::atomic<unsigned long long> counter{0};
    const unsigned long long sequence = counter.fetch_add(1);
    std::filesystem::path temporary = target;
#ifdef _WIN32
    const unsigned long process_id = 0;
#else
    const unsigned long process_id = static_cast<unsigned long>(::getpid());
#endif
    temporary += ".netsec-tmp-" + std::to_string(process_id) + "-" +
                 std::to_string(sequence);

    std::filesystem::remove(temporary, error);
    error.clear();

    std::ofstream output(temporary, std::ios::binary | std::ios::trunc);
    if (!output) {
        return Status::failure(make_error(ErrorCode::IoError,
                                          "cannot create temporary file",
                                          temporary.string()));
    }
    if (!bytes.empty()) {
        output.write(reinterpret_cast<const char*>(bytes.data()),
                     static_cast<std::streamsize>(bytes.size()));
    }
    output.close();
    if (!output) {
        std::filesystem::remove(temporary, error);
        return Status::failure(make_error(ErrorCode::IoError,
                                          "cannot write temporary file",
                                          temporary.string()));
    }

#ifdef _WIN32
    if (std::filesystem::exists(target)) {
        std::filesystem::remove(target, error);
        error.clear();
    }
#endif

    std::filesystem::rename(temporary, target, error);
    if (error) {
        std::filesystem::remove(temporary, error);
        return Status::failure(make_error(ErrorCode::IoError,
                                          "cannot install file",
                                          target.string()));
    }

    if (executable) {
        std::filesystem::permissions(
            target,
            std::filesystem::perms::owner_exec |
                std::filesystem::perms::group_exec |
                std::filesystem::perms::others_exec,
            std::filesystem::perm_options::add, error);
        error.clear();
    }

    Result<Bytes> read_back = read_regular_file(target, bytes.size());
    if (!read_back) {
        return Status::failure(read_back.error());
    }
    const Bytes& read_back_bytes = read_back.value();
    if (!constant_time_equal(ByteView(read_back_bytes.data(), read_back_bytes.size()),
                             bytes)) {
        return Status::failure(make_error(
            ErrorCode::IoError,
            "installed bytes differ from verified canonical bytes",
            target.string()));
    }
    return Status::success();
}

}  // namespace

std::string_view to_string(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::InvalidArgument:
            return "InvalidArgument";
        case ErrorCode::InvalidUtf8:
            return "InvalidUtf8";
        case ErrorCode::UnsupportedEncoding:
            return "UnsupportedEncoding";
        case ErrorCode::ControlCharacter:
            return "ControlCharacter";
        case ErrorCode::InvalidText:
            return "InvalidText";
        case ErrorCode::InvalidPath:
            return "InvalidPath";
        case ErrorCode::AbsolutePath:
            return "AbsolutePath";
        case ErrorCode::PathTraversal:
            return "PathTraversal";
        case ErrorCode::DuplicatePath:
            return "DuplicatePath";
        case ErrorCode::PathCollision:
            return "PathCollision";
        case ErrorCode::InvalidManifest:
            return "InvalidManifest";
        case ErrorCode::InvalidDigest:
            return "InvalidDigest";
        case ErrorCode::LimitExceeded:
            return "LimitExceeded";
        case ErrorCode::MissingEntry:
            return "MissingEntry";
        case ErrorCode::UnlistedEntry:
            return "UnlistedEntry";
        case ErrorCode::DigestMismatch:
            return "DigestMismatch";
        case ErrorCode::SignatureInvalid:
            return "SignatureInvalid";
        case ErrorCode::IoError:
            return "IoError";
    }
    return "Unknown";
}

std::string_view to_string(EntryRole role) noexcept {
    switch (role) {
        case EntryRole::Script:
            return "script";
        case EntryRole::Config:
            return "config";
        case EntryRole::Data:
            return "data";
    }
    return "invalid";
}

std::optional<EntryRole> entry_role_from_string(std::string_view text) noexcept {
    if (text == "script") {
        return EntryRole::Script;
    }
    if (text == "config") {
        return EntryRole::Config;
    }
    if (text == "data") {
        return EntryRole::Data;
    }
    return std::nullopt;
}

bool is_text_role(EntryRole role) noexcept {
    return role == EntryRole::Script || role == EntryRole::Config;
}

std::string Digest::hex() const {
    return hex_encode(ByteView(bytes.data(), bytes.size()));
}

Result<Digest> digest_from_hex(std::string_view text) {
    if (text.size() != 64) {
        return make_error(ErrorCode::InvalidDigest,
                          "SHA-256 digest must contain 64 hex characters");
    }

    auto nibble = [](char ch) -> int {
        if (ch >= '0' && ch <= '9') {
            return ch - '0';
        }
        if (ch >= 'a' && ch <= 'f') {
            return ch - 'a' + 10;
        }
        if (ch >= 'A' && ch <= 'F') {
            return ch - 'A' + 10;
        }
        return -1;
    };

    Digest digest;
    for (std::size_t i = 0; i < 32; ++i) {
        const int high = nibble(text[i * 2]);
        const int low = nibble(text[i * 2 + 1]);
        if (high < 0 || low < 0) {
            return make_error(ErrorCode::InvalidDigest,
                              "SHA-256 digest contains non-hex characters");
        }
        digest.bytes[i] = static_cast<u8>((high << 4) | low);
    }
    return digest;
}

Digest sha256(ByteView data) {
    return sha256_impl(data);
}

Digest sha256(std::string_view data) {
    return sha256_impl(as_bytes(data));
}

Bytes hmac_sha256(ByteView key, ByteView message) {
    Bytes normalized_key(key.begin(), key.end());
    if (normalized_key.size() > 64) {
        const Digest digest = sha256_impl(normalized_key);
        normalized_key.assign(digest.bytes.begin(), digest.bytes.end());
    }

    Bytes inner(64, 0x36u);
    Bytes outer(64, 0x5cu);
    for (std::size_t i = 0; i < normalized_key.size(); ++i) {
        inner[i] ^= normalized_key[i];
        outer[i] ^= normalized_key[i];
    }

    inner.insert(inner.end(), message.begin(), message.end());
    const Digest inner_digest = sha256_impl(inner);

    outer.insert(outer.end(), inner_digest.bytes.begin(), inner_digest.bytes.end());
    const Digest outer_digest = sha256_impl(outer);
    return Bytes(outer_digest.bytes.begin(), outer_digest.bytes.end());
}

Bytes hmac_sha256(std::string_view key, std::string_view message) {
    return hmac_sha256(as_bytes(key), as_bytes(message));
}

bool constant_time_equal(ByteView lhs, ByteView rhs) noexcept {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    u8 difference = 0;
    for (std::size_t i = 0; i < lhs.size(); ++i) {
        difference |= static_cast<u8>(lhs[i] ^ rhs[i]);
    }
    return difference == 0;
}

Result<Bytes> canonicalize_text(ByteView input, const CanonicalTextPolicy& policy) {
    if (input.size() > policy.max_bytes) {
        return make_error(ErrorCode::LimitExceeded, "text input is too large");
    }

    if (input.size() >= 2) {
        const u8 first = input[0];
        const u8 second = input[1];
        if ((first == 0xffu && second == 0xfeu) ||
            (first == 0xfeu && second == 0xffu)) {
            return make_error(ErrorCode::UnsupportedEncoding,
                              "UTF-16/UTF-32 byte order mark is not supported");
        }
    }
    if (input.size() >= 4 && input[0] == 0x00u && input[1] == 0x00u &&
        input[2] == 0xfeu && input[3] == 0xffu) {
        return make_error(ErrorCode::UnsupportedEncoding,
                          "UTF-32 byte order mark is not supported");
    }

    Bytes output;
    output.reserve(input.size());

    std::size_t offset = 0;
    if (input.size() >= 3 && input[0] == 0xefu && input[1] == 0xbbu &&
        input[2] == 0xbfu) {
        if (policy.strip_utf8_bom) {
            offset = 3;
        } else {
            output.insert(output.end(), input.begin(), input.begin() + 3);
            offset = 3;
        }
    }

    while (offset < input.size()) {
        Utf8CodePoint code_point;
        const std::size_t start = offset;
        if (!decode_utf8(input, offset, code_point)) {
            return make_error(ErrorCode::InvalidUtf8,
                              "invalid UTF-8 byte sequence", {}, offset);
        }
        offset += code_point.width;

        if (code_point.value == 0xfeffu) {
            if (policy.strip_utf8_bom) {
                return make_error(
                    ErrorCode::InvalidText,
                    "U+FEFF is only allowed as a single leading BOM", {}, start);
            }
            if (policy.reject_invisible_format_characters) {
                return make_error(ErrorCode::ControlCharacter,
                                  "invisible format character is not allowed",
                                  {}, start);
            }
        }

        if (policy.normalize_newlines && code_point.value == 0x0du) {
            if (offset < input.size() && input[offset] == 0x0au) {
                ++offset;
            }
            output.push_back(static_cast<u8>('\n'));
        } else if (code_point.value == 0x0au) {
            output.push_back(static_cast<u8>('\n'));
        } else {
            if (policy.reject_controls && is_disallowed_control(code_point.value)) {
                return make_error(ErrorCode::ControlCharacter,
                                  "control character is not allowed", {}, start);
            }
            if (policy.reject_invisible_format_characters &&
                is_invisible_format_character(code_point.value)) {
                return make_error(ErrorCode::ControlCharacter,
                                  "invisible format character is not allowed",
                                  {}, start);
            }
            output.insert(output.end(), input.begin() + static_cast<std::ptrdiff_t>(start),
                          input.begin() + static_cast<std::ptrdiff_t>(offset));
        }

        if (output.size() > policy.max_bytes) {
            return make_error(ErrorCode::LimitExceeded,
                              "canonical text output is too large");
        }
    }

    return output;
}

Result<std::string> canonicalize_text(std::string_view input,
                                      const CanonicalTextPolicy& policy) {
    Result<Bytes> canonical = canonicalize_text(as_bytes(input), policy);
    if (!canonical) {
        return canonical.error();
    }
    const std::string_view view = as_string_view(canonical.value());
    return std::string(view);
}

Result<std::string> canonicalize_path(std::string_view input,
                                      const CanonicalPathPolicy& policy) {
    if (input.empty()) {
        return make_error(ErrorCode::InvalidPath, "path is empty");
    }
    if (input.size() > policy.max_bytes) {
        return make_error(ErrorCode::LimitExceeded, "path is too long");
    }
    if (input.front() == '/') {
        return make_error(ErrorCode::AbsolutePath,
                          "absolute paths are not allowed", std::string(input));
    }
    if (input.back() == '/') {
        return make_error(ErrorCode::InvalidPath,
                          "trailing slash is not allowed", std::string(input));
    }

    std::size_t bad_offset = 0;
    std::string reason;
    if (!validate_utf8(as_bytes(input), bad_offset, reason)) {
        return make_error(ErrorCode::InvalidUtf8, std::move(reason),
                          std::string(input), bad_offset);
    }

    for (const unsigned char byte : input) {
        if (byte < 0x20u || byte == 0x7fu) {
            return make_error(ErrorCode::ControlCharacter,
                              "control character in path", std::string(input));
        }
        if (byte >= 0x80u && policy.ascii_only) {
            return make_error(ErrorCode::InvalidPath,
                              "non-ASCII paths are not allowed",
                              std::string(input));
        }
        if (byte == '\\') {
            return make_error(ErrorCode::InvalidPath,
                              "backslashes are not allowed in paths",
                              std::string(input));
        }
        if (byte == '%' && policy.reject_percent_encoding) {
            return make_error(ErrorCode::InvalidPath,
                              "percent escapes are not allowed in paths",
                              std::string(input));
        }
        if (byte == ':' && policy.reject_colon) {
            return make_error(ErrorCode::InvalidPath,
                              "colons are not allowed in paths",
                              std::string(input));
        }
    }

    std::string output;
    output.reserve(input.size());
    std::size_t start = 0;
    while (start <= input.size()) {
        const std::size_t slash = input.find('/', start);
        const std::size_t end =
            slash == std::string_view::npos ? input.size() : slash;
        const std::string_view component = input.substr(start, end - start);

        if (component.empty() || component == ".") {
            if (slash == std::string_view::npos) {
                break;
            }
            start = slash + 1;
            continue;
        }
        if (component == "..") {
            return make_error(ErrorCode::PathTraversal,
                              "parent directory segment is not allowed",
                              std::string(input));
        }
        if (component.back() == '.' || component.back() == ' ') {
            return make_error(ErrorCode::InvalidPath,
                              "path component ends with a dot or space",
                              std::string(input));
        }

        if (!output.empty()) {
            output.push_back('/');
        }
        output.append(component);

        if (slash == std::string_view::npos) {
            break;
        }
        start = slash + 1;
    }

    if (output.empty()) {
        return make_error(ErrorCode::InvalidPath,
                          "path has no usable components", std::string(input));
    }
    if (output.size() > policy.max_bytes) {
        return make_error(ErrorCode::LimitExceeded, "canonical path is too long");
    }
    return output;
}

Result<Bytes> serialize_manifest(const Manifest& manifest, const Limits& limits) {
    Result<Manifest> normalized = normalize_manifest_impl(manifest, limits);
    if (!normalized) {
        return normalized.error();
    }
    return render_manifest(normalized.value(), limits);
}

Result<Manifest> parse_manifest(ByteView raw, const Limits& limits) {
    if (raw.size() > limits.max_manifest_bytes) {
        return make_error(ErrorCode::LimitExceeded, "manifest is too large");
    }

    CanonicalTextPolicy text_policy;
    text_policy.max_bytes = limits.max_manifest_bytes;
    Result<Bytes> canonical = canonicalize_text(raw, text_policy);
    if (!canonical) {
        return canonical.error();
    }

    const std::string_view text = as_string_view(canonical.value());
    std::vector<std::string_view> lines;
    std::size_t line_start = 0;
    while (line_start < text.size()) {
        const std::size_t newline = text.find('\n', line_start);
        if (newline == std::string_view::npos) {
            lines.push_back(text.substr(line_start));
            break;
        }
        lines.push_back(text.substr(line_start, newline - line_start));
        line_start = newline + 1;
    }

    if (lines.empty() || lines.front() != "NETSEC-MANIFEST\t1") {
        return make_error(ErrorCode::InvalidManifest,
                          "missing canonical manifest header");
    }

    Manifest manifest;
    bool have_package = false;
    bool have_version = false;
    const CanonicalPathPolicy path_policy = path_policy_for(limits);

    for (std::size_t line_index = 1; line_index < lines.size(); ++line_index) {
        const std::string_view line = lines[line_index];
        if (line.empty()) {
            return make_error(ErrorCode::InvalidManifest,
                              "empty manifest line", {}, line_index);
        }

        std::vector<std::string_view> fields;
        std::size_t field_start = 0;
        while (true) {
            const std::size_t tab = line.find('\t', field_start);
            if (tab == std::string_view::npos) {
                fields.push_back(line.substr(field_start));
                break;
            }
            fields.push_back(line.substr(field_start, tab - field_start));
            field_start = tab + 1;
        }

        if (fields[0] == "package") {
            if (fields.size() != 2 || have_package) {
                return make_error(ErrorCode::InvalidManifest,
                                  "invalid package line");
            }
            if (!is_valid_token(fields[1])) {
                return make_error(ErrorCode::InvalidManifest,
                                  "invalid package id");
            }
            manifest.package_id = std::string(fields[1]);
            have_package = true;
            continue;
        }

        if (fields[0] == "version") {
            if (fields.size() != 2 || have_version) {
                return make_error(ErrorCode::InvalidManifest,
                                  "invalid version line");
            }
            if (!is_valid_token(fields[1])) {
                return make_error(ErrorCode::InvalidManifest,
                                  "invalid package version");
            }
            manifest.version = std::string(fields[1]);
            have_version = true;
            continue;
        }

        if (fields[0] != "entry" || fields.size() != 5) {
            return make_error(ErrorCode::InvalidManifest,
                              "invalid manifest line", {}, line_index);
        }

        const std::optional<EntryRole> role = entry_role_from_string(fields[1]);
        if (!role.has_value()) {
            return make_error(ErrorCode::InvalidManifest,
                              "unknown entry role", {}, line_index);
        }

        Result<Digest> path_digest = digest_from_hex(fields[2]);
        if (!path_digest) {
            return path_digest.error();
        }
        Result<Digest> content_digest = digest_from_hex(fields[3]);
        if (!content_digest) {
            return content_digest.error();
        }

        Result<std::string> path = canonicalize_path(fields[4], path_policy);
        if (!path) {
            return path.error();
        }

        const Digest computed_path_digest = sha256_impl(as_bytes(path.value()));
        if (computed_path_digest != path_digest.value()) {
            return make_error(ErrorCode::DigestMismatch,
                              "manifest path digest does not match path",
                              path.value());
        }

        ManifestEntry entry;
        entry.path = std::move(path.value());
        entry.role = *role;
        entry.path_digest = computed_path_digest;
        entry.content_digest = content_digest.value();
        manifest.entries.push_back(std::move(entry));

        if (manifest.entries.size() > limits.max_entries) {
            return make_error(ErrorCode::LimitExceeded,
                              "too many manifest entries");
        }
    }

    if (!have_package || !have_version) {
        return make_error(ErrorCode::InvalidManifest,
                          "manifest is missing package or version");
    }

    return normalize_manifest_impl(manifest, limits);
}

Result<Manifest> parse_manifest(std::string_view raw, const Limits& limits) {
    return parse_manifest(as_bytes(raw), limits);
}

Result<Bytes> build_signing_payload(const Manifest& manifest, const Limits& limits) {
    Result<Manifest> normalized = normalize_manifest_impl(manifest, limits);
    if (!normalized) {
        return normalized.error();
    }
    Result<Bytes> manifest_bytes = render_manifest(normalized.value(), limits);
    if (!manifest_bytes) {
        return manifest_bytes.error();
    }
    return make_signing_payload(normalized.value(), manifest_bytes.value(), limits);
}

Result<PackageSource> build_package(const PackageDraft& draft, const Limits& limits) {
    if (draft.files.size() > limits.max_entries) {
        return make_error(ErrorCode::LimitExceeded, "too many package files");
    }

    Manifest manifest;
    manifest.package_id = draft.package_id;
    manifest.version = draft.version;
    manifest.entries.reserve(draft.files.size());

    std::vector<PackageFile> canonical_files;
    canonical_files.reserve(draft.files.size());
    const CanonicalPathPolicy path_policy = path_policy_for(limits);
    const CanonicalTextPolicy text_policy = text_policy_for(limits);

    for (const PackageFile& file : draft.files) {
        if (!is_valid_role(file.role)) {
            return make_error(ErrorCode::InvalidArgument, "invalid entry role",
                              file.path);
        }
        if (file.bytes.size() > limits.max_entry_bytes) {
            return make_error(ErrorCode::LimitExceeded, "package entry is too large",
                              file.path);
        }

        Result<std::string> path = canonicalize_path(file.path, path_policy);
        if (!path) {
            return path.error();
        }

        Bytes canonical_bytes;
        if (is_text_role(file.role)) {
            Result<Bytes> text = canonicalize_text(file.bytes, text_policy);
            if (!text) {
                return text.error();
            }
            canonical_bytes = std::move(text.value());
        } else {
            canonical_bytes = file.bytes;
        }

        ManifestEntry entry;
        entry.path = path.value();
        entry.role = file.role;
        entry.content_digest = sha256_impl(canonical_bytes);
        manifest.entries.push_back(std::move(entry));

        PackageFile canonical_file;
        canonical_file.path = std::move(path.value());
        canonical_file.role = file.role;
        canonical_file.bytes = std::move(canonical_bytes);
        canonical_files.push_back(std::move(canonical_file));
    }

    Result<Manifest> normalized = normalize_manifest_impl(manifest, limits);
    if (!normalized) {
        return normalized.error();
    }
    Result<Bytes> manifest_bytes = render_manifest(normalized.value(), limits);
    if (!manifest_bytes) {
        return manifest_bytes.error();
    }

    std::map<std::string, PackageFile> files_by_path;
    for (PackageFile& file : canonical_files) {
        const auto inserted = files_by_path.emplace(file.path, std::move(file));
        if (!inserted.second) {
            return make_error(ErrorCode::DuplicatePath,
                              "duplicate canonical package path", file.path);
        }
    }

    PackageSource source;
    source.manifest = std::move(manifest_bytes.value());
    source.files.reserve(normalized.value().entries.size());
    for (const ManifestEntry& entry : normalized.value().entries) {
        auto found = files_by_path.find(entry.path);
        if (found == files_by_path.end()) {
            return make_error(ErrorCode::InvalidArgument,
                              "internal manifest/file mismatch", entry.path);
        }
        source.files.push_back(std::move(found->second));
    }
    return source;
}

Result<PackageSource> sign_package(PackageSource source,
                                   const SignFunction& sign,
                                   const Limits& limits) {
    if (!sign) {
        return make_error(ErrorCode::InvalidArgument, "signer is not set");
    }

    Result<PreparedManifest> prepared = prepare_manifest(source.manifest, limits);
    if (!prepared) {
        return prepared.error();
    }

    Result<std::vector<CanonicalEntry>> entries =
        assemble_entries(prepared.value().manifest, source.files, limits, true);
    if (!entries) {
        return entries.error();
    }

    Bytes signature = sign(prepared.value().signing_payload);
    if (signature.empty()) {
        return make_error(ErrorCode::SignatureInvalid,
                          "signer returned an empty signature");
    }

    PackageSource signed_source;
    signed_source.manifest = std::move(prepared.value().manifest_bytes);
    signed_source.signature = std::move(signature);
    signed_source.files.reserve(entries.value().size());
    for (CanonicalEntry& entry : entries.value()) {
        PackageFile file;
        file.path = std::move(entry.path);
        file.role = entry.role;
        file.bytes = std::move(entry.bytes);
        signed_source.files.push_back(std::move(file));
    }
    return signed_source;
}

Result<PackageSource> build_hmac_package(const PackageDraft& draft,
                                         ByteView key,
                                         const Limits& limits) {
    Result<PackageSource> built = build_package(draft, limits);
    if (!built) {
        return built.error();
    }

    Bytes key_copy(key.begin(), key.end());
    return sign_package(
        std::move(built.value()),
        [key_copy = std::move(key_copy)](ByteView payload) {
            return hmac_sha256(key_copy, payload);
        },
        limits);
}

Result<PackageSource> build_hmac_package(const PackageDraft& draft,
                                         std::string_view key,
                                         const Limits& limits) {
    return build_hmac_package(draft, as_bytes(key), limits);
}

VerifyFunction hmac_verify_function(Bytes key) {
    return [key = std::move(key)](ByteView payload, ByteView signature) {
        if (signature.size() != 32) {
            return false;
        }
        const Bytes expected = hmac_sha256(key, payload);
        return constant_time_equal(ByteView(expected.data(), expected.size()),
                                   signature);
    };
}

VerifyFunction hmac_verify_function(std::string_view key) {
    return hmac_verify_function(to_bytes(key));
}

Result<VerifiedPackage> verify_package(const PackageSource& source,
                                       const VerifyFunction& verify,
                                       const Limits& limits) {
    if (!verify) {
        return make_error(ErrorCode::InvalidArgument, "verifier is not set");
    }
    if (source.signature.empty()) {
        return make_error(ErrorCode::SignatureInvalid,
                          "package has no detached signature");
    }

    Result<PreparedManifest> prepared = prepare_manifest(source.manifest, limits);
    if (!prepared) {
        return prepared.error();
    }

    if (!verify(prepared.value().signing_payload, source.signature)) {
        return make_error(ErrorCode::SignatureInvalid,
                          "signature does not cover the canonical package");
    }

    Result<std::vector<CanonicalEntry>> entries =
        assemble_entries(prepared.value().manifest, source.files, limits, true);
    if (!entries) {
        return entries.error();
    }

    PreparedManifest& prepared_value = prepared.value();
    std::string package_id = prepared_value.manifest.package_id;
    std::string version = prepared_value.manifest.version;
    Manifest manifest = std::move(prepared_value.manifest);
    Bytes manifest_bytes = std::move(prepared_value.manifest_bytes);
    Bytes signing_payload = std::move(prepared_value.signing_payload);
    const Digest manifest_digest = prepared_value.manifest_digest;
    const Digest root_digest = prepared_value.root_digest;
    std::vector<CanonicalEntry> canonical_entries = std::move(entries.value());

    return VerifiedPackage(std::move(package_id), std::move(version),
                           std::move(manifest), std::move(manifest_bytes),
                           std::move(signing_payload), manifest_digest,
                           root_digest, std::move(canonical_entries));
}

Result<VerifiedPackage> verify_hmac_package(const PackageSource& source,
                                            ByteView key,
                                            const Limits& limits) {
    return verify_package(source, hmac_verify_function(Bytes(key.begin(), key.end())),
                          limits);
}

Result<VerifiedPackage> verify_hmac_package(const PackageSource& source,
                                            std::string_view key,
                                            const Limits& limits) {
    return verify_hmac_package(source, as_bytes(key), limits);
}

Status install_package(const VerifiedPackage& package,
                       const std::filesystem::path& destination,
                       const InstallOptions& options) {
    Status root = prepare_destination_root(destination);
    if (!root) {
        return root;
    }

    if (options.write_manifest) {
        for (const CanonicalEntry& entry : package.entries()) {
            if (entry.path == "manifest.txt") {
                return Status::failure(make_error(
                    ErrorCode::InvalidPath,
                    "package entry conflicts with manifest.txt", entry.path));
            }
        }
        Status written = write_regular_file(destination, "manifest.txt",
                                            package.manifest_bytes(),
                                            options.overwrite, false);
        if (!written) {
            return written;
        }
    }

    for (const CanonicalEntry& entry : package.entries()) {
        Result<std::string> path = canonicalize_path(entry.path);
        if (!path) {
            return Status::failure(path.error());
        }
        if (path.value() != entry.path) {
            return Status::failure(make_error(
                ErrorCode::InvalidPath,
                "verified entry path is not canonical", entry.path));
        }

        Status written = write_regular_file(
            destination, entry.path, entry.bytes, options.overwrite,
            entry.role == EntryRole::Script);
        if (!written) {
            return written;
        }
    }
    return Status::success();
}

Status verify_and_install(const PackageSource& source,
                          const VerifyFunction& verify,
                          const std::filesystem::path& destination,
                          const Limits& limits,
                          const InstallOptions& options) {
    Result<VerifiedPackage> verified = verify_package(source, verify, limits);
    if (!verified) {
        return Status::failure(verified.error());
    }
    return install_package(verified.value(), destination, options);
}

Result<PackageSource> load_package(const std::filesystem::path& directory,
                                   const Limits& limits) {
    std::error_code error;
    const std::filesystem::file_status directory_status =
        std::filesystem::symlink_status(directory, error);
    if (error || !std::filesystem::is_directory(directory_status)) {
        return make_error(ErrorCode::IoError,
                          "package directory does not exist or is not a directory",
                          directory.string());
    }

    Result<Bytes> manifest =
        read_regular_file(directory / "manifest.txt", limits.max_manifest_bytes);
    if (!manifest) {
        return manifest.error();
    }

    constexpr std::size_t kMaxSignatureBytes = 64u * 1024u;
    Result<Bytes> signature =
        read_regular_file(directory / "signature.bin", kMaxSignatureBytes);
    if (!signature) {
        return signature.error();
    }

    Result<Manifest> parsed = parse_manifest(manifest.value(), limits);
    if (!parsed) {
        return parsed.error();
    }

    PackageSource source;
    source.manifest = std::move(manifest.value());
    source.signature = std::move(signature.value());
    source.files.reserve(parsed.value().entries.size());

    for (const ManifestEntry& entry : parsed.value().entries) {
        if (entry.path == "manifest.txt" || entry.path == "signature.bin") {
            return make_error(ErrorCode::InvalidManifest,
                              "manifest entry uses a reserved package name",
                              entry.path);
        }

        Result<Bytes> bytes =
            read_regular_file(directory / entry.path, limits.max_entry_bytes);
        if (!bytes) {
            Error file_error = bytes.error();
            file_error.path = entry.path;
            return file_error;
        }

        PackageFile file;
        file.path = entry.path;
        file.role = entry.role;
        file.bytes = std::move(bytes.value());
        source.files.push_back(std::move(file));
    }
    return source;
}

Status write_package(const PackageSource& source,
                     const std::filesystem::path& directory) {
    Status root = prepare_destination_root(directory);
    if (!root) {
        return root;
    }

    Status manifest = write_regular_file(
        directory, "manifest.txt", source.manifest, true, false);
    if (!manifest) {
        return manifest;
    }

    Status signature = write_regular_file(
        directory, "signature.bin", source.signature, true, false);
    if (!signature) {
        return signature;
    }

    for (const PackageFile& file : source.files) {
        Result<std::string> path = canonicalize_path(file.path);
        if (!path) {
            return Status::failure(path.error());
        }
        if (path.value() == "manifest.txt" || path.value() == "signature.bin") {
            return Status::failure(make_error(
                ErrorCode::InvalidPath,
                "package file uses a reserved package name", path.value()));
        }
        Status written = write_regular_file(
            directory, path.value(), file.bytes, true,
            file.role == EntryRole::Script);
        if (!written) {
            return written;
        }
    }
    return Status::success();
}

}  // namespace netsec
