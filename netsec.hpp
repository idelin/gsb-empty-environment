#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace netsec {

/// Size of a network key in bytes.
inline constexpr std::size_t kNetworkKeySize = 32;

using Bytes = std::vector<std::uint8_t>;

/// An opaque, one-time install code.
///
/// The token is bound to one device identity and to the network key generation
/// in which it was issued.  A code is invalidated by a successful join, by
/// issuing a replacement code for the same device, and by committing a key
/// rotation.
struct InstallCode {
    std::string token;

    InstallCode() = default;
    InstallCode(std::string value) : token(std::move(value)) {}
    InstallCode(const char* value) : token(value != nullptr ? value : "") {}

    [[nodiscard]] bool empty() const noexcept { return token.empty(); }
    explicit operator bool() const noexcept { return !empty(); }
};

struct JoinRequest {
    std::string device_id;
    InstallCode install_code;
};

enum class RegistrationStatus {
    Registered,
    AlreadyRegistered,
    DeviceRevoked,
    EmptyDeviceId,
};

enum class IssueStatus {
    Issued,
    EmptyDeviceId,
    UnknownDevice,
    DeviceRevoked,
    DeviceAlreadyJoined,
    RotationInProgress,
};

enum class JoinStatus {
    Accepted,
    MalformedRequest,
    UnknownDevice,
    DeviceRevoked,
    DeviceAlreadyJoined,
    InvalidInstallCode,
    InstallCodeDeviceMismatch,
    InstallCodeAlreadyUsed,
    StaleInstallCode,
    RotationInProgress,
};

struct IssueResult {
    IssueStatus status = IssueStatus::EmptyDeviceId;
    InstallCode code;

    [[nodiscard]] bool ok() const noexcept { return status == IssueStatus::Issued; }
};

/// Result of a join attempt.
///
/// `network_key` is populated only when `status == JoinStatus::Accepted`.
/// Every rejection path leaves both the network key and `device_secret` empty.
struct JoinResult {
    JoinStatus status = JoinStatus::MalformedRequest;
    std::optional<Bytes> network_key;
    std::string device_secret;
    std::uint64_t generation = 0;

    [[nodiscard]] bool accepted() const noexcept {
        return status == JoinStatus::Accepted;
    }
    [[nodiscard]] const std::string& credential() const noexcept {
        return device_secret;
    }
};

/// A network-key delivery for one device.
///
/// The network key is encrypted and authenticated with a key derived from that
/// device's post-join secret.  The ciphertext differs for every device and
/// generation.  The coordinator never stores the plaintext key in this object.
struct KeyDelivery {
    std::string device_id;
    std::uint64_t generation = 0;
    Bytes nonce;
    Bytes ciphertext;
    Bytes tag;
};

enum class RotationStatus {
    AwaitingConfirmation,
    Committed,
    AlreadyInProgress,
    GenerationExhausted,
};

struct RotationResult {
    RotationStatus status = RotationStatus::GenerationExhausted;
    std::uint64_t generation = 0;
    std::vector<KeyDelivery> deliveries;

    [[nodiscard]] bool committed() const noexcept {
        return status == RotationStatus::Committed;
    }
};

enum class ConfirmationStatus {
    AwaitingConfirmation,
    Committed,
    NoPendingRotation,
    UnknownDevice,
    DeviceNotJoined,
    DeviceRevoked,
    GenerationRollbackBlocked,
    GenerationMismatch,
    AlreadyConfirmed,
    AuthenticationFailed,
    InvalidDeviceSecret,
};

struct ConfirmationResult {
    ConfirmationStatus status = ConfirmationStatus::NoPendingRotation;
    std::uint64_t generation = 0;
    std::size_t pending_confirmations = 0;

    [[nodiscard]] bool committed() const noexcept {
        return status == ConfirmationStatus::Committed;
    }
};

/// Opens a per-device key delivery with the device secret returned by a
/// successful join.  Returns std::nullopt if the secret, device identity,
/// generation, ciphertext, or authentication tag is invalid.  A caller without
/// the device secret cannot recover the network key.
[[nodiscard]] std::optional<Bytes> open_key_update(
    const KeyDelivery& delivery,
    std::string_view device_secret);

/// Trust-center state for one IoT network.
///
/// Typical coordinator flow:
///   1. register_device(device_id)
///   2. issue_install_code(device_id) and provision the code out of band
///   3. join({device_id, code})
///   4. begin_key_rotation(), deliver each KeyDelivery to its device
///   5. confirm_key_update(device_id, generation, device_secret) per device
///
/// A rotation commits only after every joined device confirms.  The committed
/// generation is monotonic: stale confirmations and rollback attempts are
/// rejected.  Unused install codes become stale when a rotation commits.
class TrustCenter {
public:
    /// Creates a trust center with a fresh random root secret.
    TrustCenter();

    /// Creates a trust center with caller-supplied root key material.  A
    /// non-empty value of at least 16 bytes is recommended for production use.
    explicit TrustCenter(Bytes root_secret);
    explicit TrustCenter(std::string_view root_secret);

    ~TrustCenter();

    TrustCenter(const TrustCenter&) = delete;
    TrustCenter& operator=(const TrustCenter&) = delete;

    TrustCenter(TrustCenter&&) noexcept;
    TrustCenter& operator=(TrustCenter&&) noexcept;

    /// Adds a device identity to the allow-list.  Unknown identities are never
    /// issued install codes and can never join.
    [[nodiscard]] RegistrationStatus register_device(const std::string& device_id);

    /// Permanently prevents a device from joining or confirming updates.
    [[nodiscard]] bool revoke_device(const std::string& device_id);

    /// Issues a one-time code bound to `device_id` and the current generation.
    /// Issuing another code for the same device invalidates the previous one.
    [[nodiscard]] IssueResult issue_install_code(const std::string& device_id);

    [[nodiscard]] JoinResult join(const JoinRequest& request);
    [[nodiscard]] JoinResult join(
        const std::string& device_id,
        const InstallCode& install_code);

    /// Starts a two-phase rotation to `current_generation() + 1`.  Each joined
    /// device receives a distinct authenticated ciphertext.  If there are no
    /// joined devices the generation advances immediately.
    [[nodiscard]] RotationResult begin_key_rotation();

    /// Confirms receipt of the pending generation.  The device secret proves
    /// possession of the credential issued by the successful join.  The
    /// pending key is committed when the last joined device confirms.
    [[nodiscard]] ConfirmationResult confirm_key_update(
        const std::string& device_id,
        std::uint64_t generation,
        std::string_view device_secret);

    /// Abandons an uncommitted rotation.  The committed generation and key are
    /// left untouched.  Returns false when no rotation is pending.
    [[nodiscard]] bool abort_key_rotation();

    /// Network-key rollback is never permitted.  This method always returns
    /// false; it exists so a coordinator can make an explicit rollback request
    /// without bypassing the monotonic-generation invariant.
    [[nodiscard]] bool rollback_generation(std::uint64_t target_generation) noexcept;

    [[nodiscard]] std::uint64_t current_generation() const;
    [[nodiscard]] std::optional<std::uint64_t> pending_generation() const;
    [[nodiscard]] std::size_t pending_confirmation_count() const;

    [[nodiscard]] bool is_registered(const std::string& device_id) const;
    [[nodiscard]] bool is_joined(const std::string& device_id) const;
    [[nodiscard]] bool is_revoked(const std::string& device_id) const;
    [[nodiscard]] std::size_t joined_device_count() const;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

[[nodiscard]] std::string_view to_string(RegistrationStatus status) noexcept;
[[nodiscard]] std::string_view to_string(IssueStatus status) noexcept;
[[nodiscard]] std::string_view to_string(JoinStatus status) noexcept;
[[nodiscard]] std::string_view to_string(RotationStatus status) noexcept;
[[nodiscard]] std::string_view to_string(ConfirmationStatus status) noexcept;

}  // namespace netsec
