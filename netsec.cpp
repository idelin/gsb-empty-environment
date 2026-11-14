#include "netsec.hpp"

#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

void validate_config(const SubscriptionConfig& config) {
    if (config.maxTimestampAge <= Timestamp::zero()) {
        throw std::invalid_argument("maxTimestampAge must be positive");
    }
    if (config.maxFutureSkew < Timestamp::zero()) {
        throw std::invalid_argument("maxFutureSkew must not be negative");
    }
    if (config.maxTimeToLive <= Timestamp::zero()) {
        throw std::invalid_argument("maxTimeToLive must be positive");
    }
}

bool same_subscription(const SubscriptionConfig& lhs,
                       const SubscriptionConfig& rhs) noexcept {
    return lhs.appId == rhs.appId &&
           lhs.destinationMac == rhs.destinationMac &&
           lhs.goId == rhs.goId &&
           lhs.dataSet == rhs.dataSet &&
           lhs.configVersion == rhs.configVersion;
}

ValidationResult reject(RejectReason reason) noexcept {
    return ValidationResult{false, reason};
}

}  // namespace

std::string_view to_string(RejectReason reason) noexcept {
    switch (reason) {
        case RejectReason::none:
            return "none";
        case RejectReason::not_configured:
            return "not_configured";
        case RejectReason::app_id_mismatch:
            return "app_id_mismatch";
        case RejectReason::destination_mac_mismatch:
            return "destination_mac_mismatch";
        case RejectReason::go_id_mismatch:
            return "go_id_mismatch";
        case RejectReason::unknown_data_set:
            return "unknown_data_set";
        case RejectReason::config_version_mismatch:
            return "config_version_mismatch";
        case RejectReason::invalid_time_to_live:
            return "invalid_time_to_live";
        case RejectReason::time_to_live_exceeds_limit:
            return "time_to_live_exceeds_limit";
        case RejectReason::timestamp_in_future:
            return "timestamp_in_future";
        case RejectReason::timestamp_expired:
            return "timestamp_expired";
        case RejectReason::time_to_live_expired:
            return "time_to_live_expired";
        case RejectReason::state_rollback:
            return "state_rollback";
        case RejectReason::sequence_rollback:
            return "sequence_rollback";
        case RejectReason::replay:
            return "replay";
        case RejectReason::timestamp_rollback:
            return "timestamp_rollback";
    }
    return "unknown";
}

PublishValidator::PublishValidator(SubscriptionConfig config) {
    configure(std::move(config));
}

void PublishValidator::configure(SubscriptionConfig config) {
    validate_config(config);

    const bool preserve_trust =
        config_.has_value() && same_subscription(*config_, config);

    config_ = std::move(config);
    if (!preserve_trust) {
        trusted_.reset();
    }
}

void PublishValidator::reset() noexcept {
    trusted_.reset();
}

ValidationResult PublishValidator::validate(const PublishMessage& message) {
    const Timestamp received_at = std::chrono::duration_cast<Timestamp>(
        std::chrono::system_clock::now().time_since_epoch());
    return validate(message, received_at);
}

ValidationResult PublishValidator::validate(const PublishMessage& message,
                                            Timestamp receivedAt) {
    if (!config_.has_value()) {
        return reject(RejectReason::not_configured);
    }

    const SubscriptionConfig& config = *config_;

    if (message.appId != config.appId) {
        return reject(RejectReason::app_id_mismatch);
    }
    if (message.destinationMac != config.destinationMac) {
        return reject(RejectReason::destination_mac_mismatch);
    }
    if (message.goId != config.goId) {
        return reject(RejectReason::go_id_mismatch);
    }
    if (message.dataSet != config.dataSet) {
        return reject(RejectReason::unknown_data_set);
    }
    if (message.configVersion != config.configVersion) {
        return reject(RejectReason::config_version_mismatch);
    }

    if (message.timeToLive <= Timestamp::zero()) {
        return reject(RejectReason::invalid_time_to_live);
    }
    if (message.timeToLive > config.maxTimeToLive) {
        return reject(RejectReason::time_to_live_exceeds_limit);
    }

    if (message.timestamp > receivedAt + config.maxFutureSkew) {
        return reject(RejectReason::timestamp_in_future);
    }

    const Timestamp age = receivedAt - message.timestamp;
    if (age > config.maxTimestampAge) {
        return reject(RejectReason::timestamp_expired);
    }
    if (age > message.timeToLive) {
        return reject(RejectReason::time_to_live_expired);
    }

    if (trusted_.has_value()) {
        const TrustedState& last = *trusted_;

        if (message.stNum < last.stNum) {
            return reject(RejectReason::state_rollback);
        }

        if (message.stNum == last.stNum) {
            if (message.sqNum < last.sqNum) {
                return reject(RejectReason::sequence_rollback);
            }
            if (message.sqNum == last.sqNum) {
                return reject(RejectReason::replay);
            }
            if (message.timestamp < last.timestamp) {
                return reject(RejectReason::timestamp_rollback);
            }
        } else {
            if (message.timestamp < last.timestamp) {
                return reject(RejectReason::timestamp_rollback);
            }
            if (config.requireTimestampAdvanceOnNewState &&
                message.timestamp == last.timestamp) {
                // A replayed frame whose stNum was edited upward still
                // carries the old state timestamp.
                return reject(RejectReason::timestamp_rollback);
            }
        }
    }

    trusted_ = TrustedState{
        message.stNum,
        message.sqNum,
        message.timestamp,
        message.configVersion,
    };

    return ValidationResult{true, RejectReason::none};
}

}  // namespace netsec
