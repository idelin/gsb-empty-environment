#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace netsec::hpke {

using Byte = std::uint8_t;
using ByteSpan = std::span<const Byte>;
using MutableByteSpan = std::span<Byte>;
using OpaqueHandle = std::uint64_t;

inline constexpr OpaqueHandle kInvalidHandle = 0;
inline constexpr std::size_t kMaxExportLength = 65535;
inline constexpr std::size_t kMaxExporterContextLength = 65535;
inline constexpr std::size_t kMaxLabelLength = 255;

enum class HpkeMode : std::uint8_t {
    base = 0,
    psk = 1,
    auth = 2,
    auth_psk = 3,
};

enum class ErrorCode {
    invalid_argument,
    invalid_suite,
    duplicate_suite,
    missing_callback,
    missing_receiver_key,
    missing_sender_key,
    unexpected_sender_key,
    missing_psk,
    empty_psk,
    missing_psk_id,
    unexpected_psk,
    provider_failure,
    low_order_point,
    stale_generation,
    namespace_violation,
    length_limit,
    closed,
};

class Error : public std::runtime_error {
public:
    Error(ErrorCode code, std::string message);

    [[nodiscard]] ErrorCode code() const noexcept { return code_; }

private:
    ErrorCode code_;
};

struct SuiteId {
    std::uint16_t kem_id = 0;
    std::uint16_t kdf_id = 0;
    std::uint16_t aead_id = 0;

    [[nodiscard]] std::array<Byte, 10> serialized() const noexcept;

    friend bool operator==(const SuiteId&, const SuiteId&) = default;
};

[[nodiscard]] std::array<Byte, 10> serialize_suite_id(const SuiteId& id) noexcept;

// A KDF input is either ordinary bytes or an opaque secret handle.  When a
// handle is present, the callback must treat the effective input as
// prefix || secret_bytes(handle); otherwise it is prefix || bytes.
struct KdfExtractInput {
    ByteSpan prefix;
    ByteSpan bytes;
    OpaqueHandle secret = kInvalidHandle;

    [[nodiscard]] static KdfExtractInput from_bytes(ByteSpan value) noexcept;
    [[nodiscard]] static KdfExtractInput from_secret(ByteSpan prefix,
                                                     OpaqueHandle value) noexcept;
    [[nodiscard]] bool uses_secret() const noexcept { return secret != kInvalidHandle; }
};

enum class KemStatus : std::uint8_t {
    ok,
    low_order_point,
    failure,
};

struct Encapsulation {
    std::vector<Byte> enc;
    OpaqueHandle shared_secret = kInvalidHandle;
};

struct Decapsulation {
    KemStatus status = KemStatus::failure;
    OpaqueHandle shared_secret = kInvalidHandle;
};

struct KemCallbacks {
    // The sender-static handle is present only for Auth and AuthPSK.  The
    // callback owns every non-zero handle it returns until destroy_secret is
    // called by the context manager.
    std::function<std::optional<Encapsulation>(
        ByteSpan recipient_public_key,
        std::optional<OpaqueHandle> sender_static_key,
        ByteSpan sender_public_key)> encapsulate;

    // Decapsulation must return low_order_point rather than a usable secret
    // when enc is an invalid public key.  A non-zero handle returned with a
    // failure is destroyed by the context manager.
    std::function<Decapsulation(
        ByteSpan enc,
        OpaqueHandle recipient_private_key,
        ByteSpan sender_public_key)> decapsulate;

    std::function<void(OpaqueHandle)> destroy_secret;
};

struct KdfCallbacks {
    std::size_t hash_size = 0;

    // Used only to fingerprint a PSK for cache identity.  The PSK itself is
    // never copied into a cache key.
    std::function<std::vector<Byte>(ByteSpan)> hash;

    std::function<OpaqueHandle(ByteSpan salt, const KdfExtractInput& input)> extract;
    std::function<OpaqueHandle(OpaqueHandle prk,
                               ByteSpan info,
                               std::size_t length)> expand;
    std::function<bool(OpaqueHandle secret, MutableByteSpan output)> read;
    std::function<void(OpaqueHandle)> destroy_secret;
};

struct AeadCallbacks {
    std::size_t key_size = 0;
    std::size_t nonce_size = 0;
};

struct SuiteCallbacks {
    KemCallbacks kem;
    KdfCallbacks kdf;
    AeadCallbacks aead;
};

// A move-only byte string that zeroes its storage on destruction, move
// assignment, and explicit clear.
class SecureBytes {
public:
    SecureBytes() = default;
    explicit SecureBytes(std::size_t size);
    explicit SecureBytes(std::vector<Byte> bytes);
    ~SecureBytes();

    SecureBytes(const SecureBytes&) = delete;
    SecureBytes& operator=(const SecureBytes&) = delete;
    SecureBytes(SecureBytes&& other) noexcept;
    SecureBytes& operator=(SecureBytes&& other) noexcept;

    [[nodiscard]] ByteSpan bytes() const noexcept { return bytes_; }
    [[nodiscard]] MutableByteSpan mutable_bytes() noexcept { return bytes_; }
    [[nodiscard]] std::size_t size() const noexcept { return bytes_.size(); }
    [[nodiscard]] bool empty() const noexcept { return bytes_.empty(); }
    [[nodiscard]] std::vector<Byte> copy() const;

    void clear() noexcept;

    friend bool operator==(const SecureBytes& lhs, const SecureBytes& rhs) noexcept {
        return lhs.bytes_ == rhs.bytes_;
    }

private:
    std::vector<Byte> bytes_;
};

struct ContextMetadata {
    SuiteId suite;
    HpkeMode mode = HpkeMode::base;
    std::uint64_t generation = 0;
    bool receiver_side = false;
    std::vector<Byte> enc;
    std::vector<Byte> receiver_public_key;
    std::vector<Byte> sender_public_key;
    std::vector<Byte> info;
    std::optional<std::vector<Byte>> psk_id;
};

struct SenderSetupParams {
    SuiteId suite;
    std::vector<Byte> receiver_public_key;
    std::vector<Byte> info;
    std::optional<std::vector<Byte>> psk;
    std::optional<std::vector<Byte>> psk_id;
    std::optional<OpaqueHandle> sender_static_key;
    std::vector<Byte> sender_public_key;
    std::optional<std::uint64_t> generation;
};

struct ReceiverSetupParams {
    SuiteId suite;
    std::vector<Byte> enc;
    std::vector<Byte> receiver_public_key;
    OpaqueHandle receiver_private_key = kInvalidHandle;
    std::vector<Byte> info;
    std::optional<std::vector<Byte>> psk;
    std::optional<std::vector<Byte>> psk_id;
    std::vector<Byte> sender_public_key;
    std::optional<std::uint64_t> generation;
};

namespace detail {
struct HpkeContextAccess;
}

// An immutable HPKE context.  Copies share the context identity and its
// revocation state, but never expose the exporter secret itself.
class HpkeContext {
public:
    // Internal, incomplete implementation token.
    struct Impl;

    HpkeContext() = default;
    HpkeContext(const HpkeContext&) = default;
    HpkeContext& operator=(const HpkeContext&) = default;
    HpkeContext(HpkeContext&&) noexcept = default;
    HpkeContext& operator=(HpkeContext&&) noexcept = default;
    ~HpkeContext() = default;

    [[nodiscard]] explicit operator bool() const noexcept;
    [[nodiscard]] const ContextMetadata& metadata() const;

    // label must stay inside the manager's exporter namespace, for example
    // "message-host.stream" for namespace "message-host".  The scoped label is
    // the application label used by RFC 9180 LabeledExpand; exporter_context is
    // passed byte-for-byte as the labeled info value.
    [[nodiscard]] SecureBytes export_secret(
        std::string_view label,
        ByteSpan exporter_context,
        std::size_t length) const;

    [[nodiscard]] SecureBytes aead_key() const;
    [[nodiscard]] SecureBytes base_nonce() const;

private:
    std::shared_ptr<const Impl> impl_;

    explicit HpkeContext(std::shared_ptr<const Impl> impl) noexcept;
    friend class HpkeContextManager;
    friend struct detail::HpkeContextAccess;
};

class HpkeContextManager {
public:
    explicit HpkeContextManager(
        std::string exporter_namespace,
        std::size_t max_cache_entries = 128);
    ~HpkeContextManager();

    HpkeContextManager(const HpkeContextManager&) = delete;
    HpkeContextManager& operator=(const HpkeContextManager&) = delete;
    HpkeContextManager(HpkeContextManager&&) noexcept;
    HpkeContextManager& operator=(HpkeContextManager&&) noexcept;

    void register_suite(SuiteId id, SuiteCallbacks callbacks);

    [[nodiscard]] HpkeContext setup_base(const SenderSetupParams& params);
    [[nodiscard]] HpkeContext setup_psk(const SenderSetupParams& params);
    [[nodiscard]] HpkeContext setup_auth(const SenderSetupParams& params);
    [[nodiscard]] HpkeContext setup_auth_psk(const SenderSetupParams& params);

    [[nodiscard]] HpkeContext setup_base_receiver(const ReceiverSetupParams& params);
    [[nodiscard]] HpkeContext setup_psk_receiver(const ReceiverSetupParams& params);
    [[nodiscard]] HpkeContext setup_auth_receiver(const ReceiverSetupParams& params);
    [[nodiscard]] HpkeContext setup_auth_psk_receiver(const ReceiverSetupParams& params);

    [[nodiscard]] std::string make_label(std::string_view leaf) const;
    [[nodiscard]] std::uint64_t current_generation() const;

    // Invalidates all live contexts from older generations and destroys every
    // cached exporter/key/nonce secret before returning.
    std::uint64_t rotate_receiver_key();

private:
    struct Impl;
    std::shared_ptr<Impl> impl_;
};

using HPKEContext = HpkeContext;
using HPKEContextManager = HpkeContextManager;

}  // namespace netsec::hpke
