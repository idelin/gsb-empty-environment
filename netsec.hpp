#pragma once

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>

namespace netsec {

inline constexpr std::size_t kWpsPinDigits = 8;
inline constexpr std::size_t kWpsFirstHalfDigits = 4;
inline constexpr std::size_t kWpsSecondHalfDigits = 3;
inline constexpr std::size_t kWpsMacBytes = 6;

using PinDigits = std::array<std::uint8_t, kWpsPinDigits>;
using FirstHalf = std::array<std::uint8_t, kWpsFirstHalfDigits>;
using SecondHalf = std::array<std::uint8_t, kWpsSecondHalfDigits>;
using RegistrarMac = std::array<std::uint8_t, kWpsMacBytes>;

struct PinCommitment {
    FirstHalf first{};
    SecondHalf second{};
    std::uint8_t checksum = 0;
};

[[nodiscard]] std::uint8_t wps_checksum(const PinDigits& pin) noexcept;
[[nodiscard]] bool wps_checksum_valid(const PinDigits& pin) noexcept;
[[nodiscard]] std::optional<PinDigits> parse_pin(std::string_view text) noexcept;
[[nodiscard]] std::string pin_to_string(const PinDigits& pin);

struct LockoutPolicy {
    std::uint32_t failure_threshold = 3;
    std::chrono::seconds initial_lockout{60};
    std::chrono::seconds maximum_lockout{24 * 60 * 60};
};

enum class VerificationCode {
    Success,
    Failure,
    InvalidPin,
    InvalidChecksum,
    Locked,
    PersistenceError
};

struct VerificationResult {
    VerificationCode code = VerificationCode::Failure;
    std::chrono::seconds retry_after{0};

    [[nodiscard]] bool succeeded() const noexcept {
        return code == VerificationCode::Success;
    }

    [[nodiscard]] bool locked() const noexcept {
        return code == VerificationCode::Locked;
    }

    friend bool operator==(const VerificationResult& lhs,
                           const VerificationResult& rhs) noexcept {
        return lhs.code == rhs.code && lhs.retry_after == rhs.retry_after;
    }

    friend bool operator!=(const VerificationResult& lhs,
                           const VerificationResult& rhs) noexcept {
        return !(lhs == rhs);
    }
};

class WpsPinVerifier {
public:
    using Clock = std::function<std::chrono::system_clock::time_point()>;

    WpsPinVerifier(const PinDigits& expected_pin, std::string state_file,
                   LockoutPolicy policy = {}, Clock clock = {});
    WpsPinVerifier(std::string_view expected_pin, std::string state_file,
                   LockoutPolicy policy = {}, Clock clock = {});

    WpsPinVerifier(const WpsPinVerifier&) = delete;
    WpsPinVerifier& operator=(const WpsPinVerifier&) = delete;

    [[nodiscard]] VerificationResult verify(
        const PinDigits& submitted,
        const RegistrarMac& registrar_mac = {});

    [[nodiscard]] VerificationResult verify(
        std::string_view submitted,
        const RegistrarMac& registrar_mac = {});

    [[nodiscard]] VerificationResult verify(
        const PinCommitment& commitment,
        const RegistrarMac& registrar_mac = {});

    [[nodiscard]] bool is_locked() const;
    [[nodiscard]] std::chrono::seconds remaining_lockout() const;
    [[nodiscard]] std::uint32_t failure_count() const;
    [[nodiscard]] std::uint32_t lockout_count() const;
    [[nodiscard]] const LockoutPolicy& policy() const noexcept;
    [[nodiscard]] const std::string& state_file() const noexcept;

private:
    struct PersistentState {
        std::uint32_t failures = 0;
        std::uint32_t lockouts = 0;
        std::int64_t lock_until_epoch_seconds = 0;
    };

    VerificationResult verify_unlocked(const PinDigits& submitted);
    VerificationResult reject_if_locked(std::int64_t now_epoch_seconds);
    VerificationResult record_failure(std::int64_t now_epoch_seconds);
    std::chrono::seconds stage_duration(std::uint32_t lockout_number) const;
    std::chrono::seconds remaining_locked(std::int64_t now_epoch_seconds) const;
    std::int64_t now_epoch_seconds() const;
    void load_state();
    void save_state() const;
    void validate_configuration() const;
    void validate_expected_pin() const;

    PinDigits expected_pin_{};
    std::string state_file_;
    LockoutPolicy policy_;
    Clock clock_;
    PersistentState state_{};
    mutable std::mutex mutex_;
};

using PinVerifier = WpsPinVerifier;
using WpsPin = PinDigits;
using WpsLockoutPolicy = LockoutPolicy;
using WpsVerificationResult = VerificationResult;

}  // namespace netsec
