#pragma once

#include <array>
#include <chrono>
#include <cstdint>
#include <optional>
#include <string>
#include <string_view>

namespace netsec {

/// Timestamp and duration unit used by the validator.
using Timestamp = std::chrono::nanoseconds;

/// Ethernet destination address as it appears on the wire.
using MacAddress = std::array<std::uint8_t, 6>;

/// Immutable subscription policy used to decide whether a publication is
/// trustworthy. All identity fields are compared exactly.
struct SubscriptionConfig {
    std::uint16_t appId{};
    MacAddress destinationMac{};
    std::string goId;
    std::string dataSet;
    std::uint32_t configVersion{};

    /// Maximum accepted age of a publication, measured from its timestamp.
    Timestamp maxTimestampAge{std::chrono::seconds{5}};

    /// Clock skew tolerated for a publication timestamped slightly in the
    /// future.
    Timestamp maxFutureSkew{std::chrono::seconds{1}};

    /// Hard upper bound for the timeAllowedToLive value carried by a
    /// publication. A publisher cannot extend its own replay window.
    Timestamp maxTimeToLive{std::chrono::seconds{5}};

    /// A new stNum denotes a new process state and must carry a newer
    /// timestamp. This is the anti-replay barrier used when an attacker
    /// reuses an old frame but edits stNum.
    bool requireTimestampAdvanceOnNewState{true};
};

/// Publication metadata extracted from a GOOSE/SV-style frame. The payload
/// itself is owned by the communication stack; only the envelope needed for
/// trust decisions is represented here.
struct PublishMessage {
    std::uint16_t appId{};
    MacAddress destinationMac{};
    std::string goId;
    std::string dataSet;

    std::uint32_t stNum{};
    std::uint32_t sqNum{};

    /// Publication timestamp. It is compared with the local receive time.
    Timestamp timestamp{};

    /// timeAllowedToLive carried by the publication.
    Timestamp timeToLive{std::chrono::seconds{2}};

    std::uint32_t configVersion{};
};

/// Reason a publication was not accepted. `none` is returned only for an
/// accepted publication.
enum class RejectReason : std::uint8_t {
    none = 0,
    not_configured,
    app_id_mismatch,
    destination_mac_mismatch,
    go_id_mismatch,
    unknown_data_set,
    config_version_mismatch,
    invalid_time_to_live,
    time_to_live_exceeds_limit,
    timestamp_in_future,
    timestamp_expired,
    time_to_live_expired,
    state_rollback,
    sequence_rollback,
    replay,
    timestamp_rollback,
};

[[nodiscard]] std::string_view to_string(RejectReason reason) noexcept;

struct ValidationResult {
    bool accepted{false};
    RejectReason reason{RejectReason::not_configured};

    [[nodiscard]] explicit operator bool() const noexcept { return accepted; }
};

/// High-water mark of the last publication that passed every check.
struct TrustedState {
    std::uint32_t stNum{};
    std::uint32_t sqNum{};
    Timestamp timestamp{};
    std::uint32_t configVersion{};
};

/// Validates publications for one subscription.
///
/// State is only advanced after the complete frame has been accepted, so a
/// rejected frame can never make a later forgery look legitimate. One
/// instance is intended to be used by one subscription; synchronise access if
/// the stack calls it from multiple threads.
class PublishValidator {
public:
    PublishValidator() = default;
    explicit PublishValidator(SubscriptionConfig config);

    /// Installs a subscription policy. A change to subscription identity or
    /// configuration version starts a fresh sequence. Re-applying an
    /// identical policy keeps the replay high-water mark.
    ///
    /// Throws std::invalid_argument when a time bound is not positive (or a
    /// future-skew bound is negative).
    void configure(SubscriptionConfig config);

    /// Clears the accepted-state high-water mark. The subscription policy is
    /// retained.
    void reset() noexcept;

    /// Validates against the current system clock.
    [[nodiscard]] ValidationResult validate(const PublishMessage& message);

    /// Validates against an explicit receive time. This overload makes the
    /// component deterministic for gateways and tests.
    [[nodiscard]] ValidationResult validate(const PublishMessage& message,
                                            Timestamp receivedAt);

    [[nodiscard]] bool configured() const noexcept { return config_.has_value(); }

    [[nodiscard]] const std::optional<SubscriptionConfig>& configuration() const noexcept {
        return config_;
    }

    [[nodiscard]] const std::optional<TrustedState>& trusted_state() const noexcept {
        return trusted_;
    }

private:
    std::optional<SubscriptionConfig> config_;
    std::optional<TrustedState> trusted_;
};

}  // namespace netsec
