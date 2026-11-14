#pragma once

#include <array>
#include <concepts>
#include <cstddef>
#include <cstdint>
#include <map>
#include <mutex>
#include <optional>
#include <ranges>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

// The protection scope is part of every envelope. It is never inferred from
// the caller at decryption time: the caller must ask for the same scope that
// was bound when the envelope was created.
enum class ProtectionScope : std::uint8_t {
    User = 1,
    Machine = 2,
};
using Scope = ProtectionScope;

// A verified Windows-style security identity. `sid` and `logon_session` must
// come from the service's authenticated token/session, not from request data.
struct Subject {
    std::string sid;
    std::string logon_session;

    friend bool operator==(const Subject&, const Subject&) = default;
};
using Identity = Subject;

// `CallerContext` is supplied by the credential service after it has verified
// the caller. `is_service_account` and `machine_scope_explicitly_declared`
// must also be derived from trusted service configuration/token state.
//
// Machine scope is deliberately opt-in: it requires BOTH a service account
// and an explicit machine-scope declaration. This prevents an ordinary user
// from turning a machine-scoped secret into a user-scoped one (or vice versa).
struct CallerContext {
    Subject subject;
    ProtectionScope requested_scope = ProtectionScope::User;
    bool is_service_account = false;
    bool machine_scope_explicitly_declared = false;
};
using Caller = CallerContext;

using Key = std::array<std::uint8_t, 32>;
using RevocationId = std::array<std::uint8_t, 16>;

enum class ErrorCode {
    InvalidArgument = 1,
    InvalidEnvelope,
    UnsupportedVersion,
    UnknownKeyGeneration,
    RevokedKeyGeneration,
    RevokedEnvelope,
    IdentityMismatch,
    SessionMismatch,
    ScopeMismatch,
    MachineScopeNotAllowed,
    EntropyMismatch,
    AuthenticationFailed,
    NoActiveKeyGeneration,
    ProtectionFailure,
};

std::string_view to_string(ErrorCode code) noexcept;

class ProtectionError : public std::runtime_error {
public:
    ProtectionError(ErrorCode code, std::string message);

    ErrorCode code() const noexcept { return code_; }

private:
    ErrorCode code_;
};

// Metadata that can be inspected without any key. The values are still
// unauthenticated until unprotect() succeeds, but the revocation ID is needed
// by a service's revocation path.
struct EnvelopeInfo {
    std::uint16_t version = 0;
    ProtectionScope scope = ProtectionScope::User;
    std::uint32_t key_generation = 0;
    RevocationId revocation_id{};
    Subject subject;
    bool has_entropy = false;
    std::size_t ciphertext_size = 0;
};

// Optional entropy accepted as bytes, a string, or any contiguous range. The
// value itself is never stored in the envelope; only its SHA-256 commitment is.
class Entropy {
public:
    Entropy() noexcept = default;

    Entropy(std::span<const std::uint8_t> bytes) noexcept : bytes_(bytes) {}

    Entropy(std::string_view text) noexcept
        : bytes_(reinterpret_cast<const std::uint8_t*>(text.data()),
                 text.size()) {}

    Entropy(const char* text) noexcept
        : Entropy(text != nullptr ? std::string_view(text)
                                  : std::string_view()) {}

    Entropy(const std::string& text) noexcept
        : Entropy(std::string_view(text)) {}

    template <std::ranges::contiguous_range Range>
        requires std::same_as<std::ranges::range_value_t<Range>, std::uint8_t>
    Entropy(Range&& range) noexcept
        : bytes_(std::ranges::data(range), std::ranges::size(range)) {}

    template <std::ranges::contiguous_range Range>
        requires std::same_as<std::ranges::range_value_t<Range>, char>
    Entropy(Range&& range) noexcept
        : bytes_(reinterpret_cast<const std::uint8_t*>(
                     std::ranges::data(range)),
                 std::ranges::size(range)) {}

    std::span<const std::uint8_t> bytes() const noexcept { return bytes_; }
    bool empty() const noexcept { return bytes_.empty(); }

private:
    std::span<const std::uint8_t> bytes_;
};

struct ProtectOptions {
    // If omitted, a fresh random revocation ID is generated for the envelope.
    std::optional<RevocationId> revocation_id;
    // If omitted, the context's active generation is used.
    std::optional<std::uint32_t> key_generation;
};

// A protection context owns an imported key ring and all policy state.
// It is thread-safe. Keys are never exported by this API.
class ProtectionContext {
public:
    // Creates a random generation-1 master key and makes it active.
    ProtectionContext();

    // Imports a caller-provided master key as a generation. This is the
    // "importable" entry point for a credential service that already has its
    // key material in an OS-protected store.
    explicit ProtectionContext(Key master_key,
                               std::uint32_t generation = 1,
                               bool make_active = true);

    ProtectionContext(const ProtectionContext&) = delete;
    ProtectionContext& operator=(const ProtectionContext&) = delete;
    ProtectionContext(ProtectionContext&&) = delete;
    ProtectionContext& operator=(ProtectionContext&&) = delete;
    ~ProtectionContext();

    std::uint32_t active_generation() const;

    // Imports/registers a key generation. The generation must be non-zero and
    // not already present. Returns the generation number.
    std::uint32_t import_generation(std::uint32_t generation, Key master_key,
                                    bool make_active = false);

    // Adds a fresh random active generation after the current highest one.
    std::uint32_t rotate();
    // Adds a caller-supplied active generation after the current highest one.
    std::uint32_t rotate(Key next_master_key);

    // Revocation is permanent for this context. Revoking the active generation
    // makes the context unable to protect until another generation is activated.
    void revoke_generation(std::uint32_t generation);
    void revoke_key_generation(std::uint32_t generation) {
        revoke_generation(generation);
    }
    bool is_generation_revoked(std::uint32_t generation) const;

    // Envelope-level revocation (for example when a credential is deleted).
    void revoke_envelope(const RevocationId& id);
    void revoke_blob(const RevocationId& id) { revoke_envelope(id); }
    bool is_envelope_revoked(const RevocationId& id) const;

    // Creates a sealed envelope. `owner` is the SID/session that will be
    // required at decryption time. The caller must be that same subject (or a
    // verified service account sealing on its behalf) and must be allowed to
    // request `scope`. A service account can seal but can never open material
    // owned by another subject.
    std::vector<std::uint8_t> protect(
        const CallerContext& caller, const Subject& owner, ProtectionScope scope,
        std::span<const std::uint8_t> plaintext,
        Entropy optional_entropy = {},
        const ProtectOptions& options = {});

    // Convenience overload: owner is caller.subject and scope is
    // caller.requested_scope.
    std::vector<std::uint8_t> protect(
        const CallerContext& caller, std::span<const std::uint8_t> plaintext,
        Entropy optional_entropy = {},
        const ProtectOptions& options = {});

    std::vector<std::uint8_t> protect(
        const CallerContext& caller, const Subject& owner, ProtectionScope scope,
        std::string_view plaintext,
        Entropy optional_entropy = {},
        const ProtectOptions& options = {});

    std::vector<std::uint8_t> protect(
        const CallerContext& caller, std::string_view plaintext,
        Entropy optional_entropy = {},
        const ProtectOptions& options = {});

    // Opens an envelope. All header/policy checks run before the ciphertext is
    // decrypted. Throws ProtectionError on any mismatch.
    std::vector<std::uint8_t> unprotect(
        const CallerContext& caller, std::span<const std::uint8_t> envelope,
        Entropy optional_entropy = {});

    // Parses only the public envelope header. Throws ProtectionError on a
    // structurally invalid/unsupported envelope.
    EnvelopeInfo inspect(std::span<const std::uint8_t> envelope) const;

    // Returns a lowercase hexadecimal revocation ID.
    static std::string revocation_id_hex(const RevocationId& id);
    // Parses a 32-digit hexadecimal revocation ID (case-insensitive).
    static RevocationId revocation_id_from_hex(std::string_view hex);

private:
    struct ParsedEnvelope {
        EnvelopeInfo info;
        std::vector<std::uint8_t> header;
        std::vector<std::uint8_t> ciphertext;
        std::array<std::uint8_t, 32> tag{};
        std::array<std::uint8_t, 32> salt{};
        std::array<std::uint8_t, 12> nonce{};
        std::array<std::uint8_t, 32> entropy_digest{};
        std::uint8_t flags = 0;
    };

    static ParsedEnvelope parse(std::span<const std::uint8_t> envelope);
    struct DerivedKeys {
        Key encryption;
        Key authentication;
    };

    static void authorize_writer(const CallerContext& caller,
                                 const Subject& owner, ProtectionScope scope);
    static void authorize_caller(const CallerContext& caller,
                                 const Subject& owner, ProtectionScope scope);
    static void check_scope_policy(const CallerContext& caller,
                                   ProtectionScope scope);
    static DerivedKeys derive_key(const Key& master_key,
                                  const ParsedEnvelope& parsed);
    static std::vector<std::uint8_t> decrypt(
        const Key& key, const std::array<std::uint8_t, 12>& nonce,
        std::span<const std::uint8_t> ciphertext);

    std::uint32_t next_generation_locked() const;
    Key active_key_locked() const;
    Key generation_key_locked(std::uint32_t generation) const;

    mutable std::mutex mutex_;
    std::map<std::uint32_t, Key> keys_;
    std::map<std::uint32_t, bool> revoked_generations_;
    std::map<RevocationId, bool> revoked_envelopes_;
    std::uint32_t active_generation_ = 0;
    std::uint32_t highest_generation_ = 0;
};

}  // namespace netsec
