#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <functional>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <variant>
#include <vector>

namespace netsec {

using Bytes = std::vector<std::uint8_t>;
using ByteView = std::span<const std::uint8_t>;

inline ByteView as_bytes(std::string_view text) noexcept {
    return ByteView(reinterpret_cast<const std::uint8_t*>(text.data()), text.size());
}

inline std::string_view as_string_view(ByteView bytes) noexcept {
    return std::string_view(reinterpret_cast<const char*>(bytes.data()), bytes.size());
}

inline Bytes to_bytes(std::string_view text) {
    const auto view = as_bytes(text);
    return Bytes(view.begin(), view.end());
}

constexpr std::size_t kNoOffset = static_cast<std::size_t>(-1);

enum class ErrorCode : std::uint8_t {
    InvalidArgument = 1,
    InvalidUtf8,
    UnsupportedEncoding,
    ControlCharacter,
    InvalidText,
    InvalidPath,
    AbsolutePath,
    PathTraversal,
    DuplicatePath,
    PathCollision,
    InvalidManifest,
    InvalidDigest,
    LimitExceeded,
    MissingEntry,
    UnlistedEntry,
    DigestMismatch,
    SignatureInvalid,
    IoError,
};

std::string_view to_string(ErrorCode code) noexcept;

struct Error {
    ErrorCode code = ErrorCode::IoError;
    std::string message;
    std::string path;
    std::size_t offset = kNoOffset;
};

template <typename T>
class Result {
public:
    Result(T value) : value_(std::move(value)) {}
    Result(Error error) : value_(std::move(error)) {}

    bool has_value() const noexcept {
        return std::holds_alternative<T>(value_);
    }

    explicit operator bool() const noexcept { return has_value(); }

    T& value() & {
        if (!has_value()) {
            throw std::logic_error("netsec::Result::value() called on an error");
        }
        return std::get<T>(value_);
    }

    const T& value() const& {
        if (!has_value()) {
            throw std::logic_error("netsec::Result::value() called on an error");
        }
        return std::get<T>(value_);
    }

    T&& value() && {
        if (!has_value()) {
            throw std::logic_error("netsec::Result::value() called on an error");
        }
        return std::get<T>(std::move(value_));
    }

    T& operator*() & { return value(); }
    const T& operator*() const& { return value(); }
    T&& operator*() && { return std::move(value()); }
    T* operator->() { return &value(); }
    const T* operator->() const { return &value(); }

    const Error& error() const& {
        if (has_value()) {
            throw std::logic_error("netsec::Result::error() called on a value");
        }
        return std::get<Error>(value_);
    }

    Error& error() & {
        if (has_value()) {
            throw std::logic_error("netsec::Result::error() called on a value");
        }
        return std::get<Error>(value_);
    }

private:
    std::variant<T, Error> value_;
};

class Status {
public:
    Status() = default;
    explicit Status(Error error) : error_(std::move(error)) {}

    static Status success() { return Status(); }
    static Status failure(Error error) { return Status(std::move(error)); }

    bool ok() const noexcept { return !error_.has_value(); }
    explicit operator bool() const noexcept { return ok(); }

    const Error& error() const& {
        if (ok()) {
            throw std::logic_error("netsec::Status::error() called on success");
        }
        return *error_;
    }

private:
    std::optional<Error> error_;
};

struct Digest {
    std::array<std::uint8_t, 32> bytes{};

    std::string hex() const;
    bool operator==(const Digest& other) const noexcept { return bytes == other.bytes; }
    bool operator!=(const Digest& other) const noexcept { return !(*this == other); }
};

Result<Digest> digest_from_hex(std::string_view hex);

Digest sha256(ByteView data);
Digest sha256(std::string_view data);

Bytes hmac_sha256(ByteView key, ByteView message);
Bytes hmac_sha256(std::string_view key, std::string_view message);

bool constant_time_equal(ByteView lhs, ByteView rhs) noexcept;

struct CanonicalTextPolicy {
    bool strip_utf8_bom = true;
    bool normalize_newlines = true;
    bool reject_controls = true;
    bool reject_invisible_format_characters = true;
    std::size_t max_bytes = 256u * 1024u * 1024u;
};

struct CanonicalPathPolicy {
    bool ascii_only = true;
    bool reject_percent_encoding = true;
    bool reject_colon = true;
    std::size_t max_bytes = 4096;
};

struct Limits {
    std::size_t max_manifest_bytes = 1024u * 1024u;
    std::size_t max_entry_bytes = 256u * 1024u * 1024u;
    std::size_t max_entries = 4096;
    std::size_t max_path_bytes = 4096;
};

// Canonical text is strict UTF-8, without a leading BOM and with LF line
// endings. Encoding conversion is deliberately not performed: UTF-16 and
// legacy encodings are rejected instead of being silently transcoded.
Result<Bytes> canonicalize_text(ByteView input,
                                const CanonicalTextPolicy& policy = {});
Result<std::string> canonicalize_text(std::string_view input,
                                      const CanonicalTextPolicy& policy = {});

// Canonical paths are relative, '/'-separated and ASCII-only. Empty and '.'
// components are collapsed; '..', backslashes, percent escapes and colons are
// rejected.
Result<std::string> canonicalize_path(std::string_view input,
                                      const CanonicalPathPolicy& policy = {});

enum class EntryRole : std::uint8_t {
    Script = 1,
    Config = 2,
    Data = 3,
};

std::string_view to_string(EntryRole role) noexcept;
std::optional<EntryRole> entry_role_from_string(std::string_view text) noexcept;
bool is_text_role(EntryRole role) noexcept;

struct ManifestEntry {
    std::string path;
    EntryRole role = EntryRole::Data;
    Digest path_digest{};
    Digest content_digest{};
};

struct Manifest {
    std::string package_id;
    std::string version;
    std::vector<ManifestEntry> entries;
};

Result<Bytes> serialize_manifest(const Manifest& manifest,
                                 const Limits& limits = {});
Result<Manifest> parse_manifest(ByteView raw,
                                const Limits& limits = {});
Result<Manifest> parse_manifest(std::string_view raw,
                                const Limits& limits = {});

Result<Bytes> build_signing_payload(const Manifest& manifest,
                                    const Limits& limits = {});

struct PackageFile {
    std::string path;
    EntryRole role = EntryRole::Data;
    Bytes bytes;
};

struct PackageDraft {
    std::string package_id;
    std::string version;
    std::vector<PackageFile> files;
};

struct PackageSource {
    Bytes manifest;
    std::vector<PackageFile> files;
    Bytes signature;
};

struct CanonicalEntry {
    std::string path;
    EntryRole role = EntryRole::Data;
    Bytes bytes;
    Digest path_digest{};
    Digest content_digest{};
};

using SignFunction = std::function<Bytes(ByteView)>;
using VerifyFunction = std::function<bool(ByteView, ByteView)>;

// Builds the canonical package form. Text files are canonicalized before the
// manifest is written, so the manifest digests refer to the exact bytes an
// installer will consume.
Result<PackageSource> build_package(const PackageDraft& draft,
                                    const Limits& limits = {});

// Canonicalizes and validates an existing source package, then signs the
// canonical signing payload. The returned source contains only canonical
// bytes and canonical paths.
Result<PackageSource> sign_package(PackageSource source,
                                   const SignFunction& sign,
                                   const Limits& limits = {});

Result<PackageSource> build_hmac_package(const PackageDraft& draft,
                                         ByteView key,
                                         const Limits& limits = {});
Result<PackageSource> build_hmac_package(const PackageDraft& draft,
                                         std::string_view key,
                                         const Limits& limits = {});

VerifyFunction hmac_verify_function(Bytes key);
VerifyFunction hmac_verify_function(std::string_view key);

// Verification does two independent checks:
//   1. the detached signature over the canonical signing payload, and
//   2. every manifest entry's path and canonical content digest.
// The returned object owns the exact canonical bytes that were verified.
class VerifiedPackage {
public:
    const std::string& package_id() const noexcept { return package_id_; }
    const std::string& version() const noexcept { return version_; }
    const Manifest& manifest() const noexcept { return manifest_; }
    const Bytes& manifest_bytes() const noexcept { return manifest_bytes_; }
    ByteView signing_payload() const noexcept { return signing_payload_; }
    const Digest& manifest_digest() const noexcept { return manifest_digest_; }
    const Digest& root_digest() const noexcept { return root_digest_; }
    std::span<const CanonicalEntry> entries() const noexcept { return entries_; }

private:
    friend Result<VerifiedPackage> verify_package(const PackageSource&,
                                                  const VerifyFunction&,
                                                  const Limits&);

    VerifiedPackage(std::string package_id,
                    std::string version,
                    Manifest manifest,
                    Bytes manifest_bytes,
                    Bytes signing_payload,
                    Digest manifest_digest,
                    Digest root_digest,
                    std::vector<CanonicalEntry> entries)
        : package_id_(std::move(package_id)),
          version_(std::move(version)),
          manifest_(std::move(manifest)),
          manifest_bytes_(std::move(manifest_bytes)),
          signing_payload_(std::move(signing_payload)),
          manifest_digest_(manifest_digest),
          root_digest_(root_digest),
          entries_(std::move(entries)) {}

    std::string package_id_;
    std::string version_;
    Manifest manifest_;
    Bytes manifest_bytes_;
    Bytes signing_payload_;
    Digest manifest_digest_{};
    Digest root_digest_{};
    std::vector<CanonicalEntry> entries_;
};

Result<VerifiedPackage> verify_package(const PackageSource& source,
                                       const VerifyFunction& verify,
                                       const Limits& limits = {});
Result<VerifiedPackage> verify_hmac_package(const PackageSource& source,
                                            ByteView key,
                                            const Limits& limits = {});
Result<VerifiedPackage> verify_hmac_package(const PackageSource& source,
                                            std::string_view key,
                                            const Limits& limits = {});

struct InstallOptions {
    bool write_manifest = false;
    bool overwrite = true;
};

// Writes only VerifiedPackage entries. There is intentionally no overload
// taking PackageSource, because installing unverified source bytes is the bug
// this component exists to prevent.
Status install_package(const VerifiedPackage& package,
                       const std::filesystem::path& destination,
                       const InstallOptions& options = {});

// Convenience composition for installers that receive a source package and a
// verifier directly. Verification always happens before any file is written.
Status verify_and_install(const PackageSource& source,
                          const VerifyFunction& verify,
                          const std::filesystem::path& destination,
                          const Limits& limits = {},
                          const InstallOptions& options = {});

// Directory layout: manifest.txt, signature.bin, and the files listed in the
// manifest. Files not listed in the manifest are never installed.
Result<PackageSource> load_package(const std::filesystem::path& directory,
                                   const Limits& limits = {});
Status write_package(const PackageSource& source,
                     const std::filesystem::path& directory);

}  // namespace netsec
