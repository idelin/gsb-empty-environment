#include "netsec.hpp"

#include <chrono>
#include <cstdio>
#include <stdexcept>
#include <string>

namespace {

using namespace netsec;
using namespace std::chrono_literals;

int failures = 0;

void expect(bool condition, const char* what) {
    if (!condition) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        ++failures;
    }
}

void expect_rejected(const ValidationResult& result, RejectReason reason,
                     const char* what) {
    if (result.accepted || result.reason != reason) {
        std::fprintf(stderr, "FAIL: %s (accepted=%d, reason=%s, expected=%s)\n",
                     what, result.accepted ? 1 : 0,
                     std::string(to_string(result.reason)).c_str(),
                     std::string(to_string(reason)).c_str());
        ++failures;
    }
}

SubscriptionConfig make_config() {
    SubscriptionConfig config;
    config.appId = 0x1234;
    config.destinationMac = {0x01, 0x0c, 0xcd, 0x01, 0x00, 0x01};
    config.goId = "IED1/LLN0$GO$gcb01";
    config.dataSet = "IED1/LLN0$DS$ds01";
    config.configVersion = 7;
    config.maxTimestampAge = 5s;
    config.maxFutureSkew = 1s;
    config.maxTimeToLive = 3s;
    return config;
}

const Timestamp kReceiveBase = std::chrono::seconds{1'000'000};

PublishMessage make_message(std::uint32_t stNum, std::uint32_t sqNum,
                            Timestamp timestamp) {
    PublishMessage message;
    message.appId = 0x1234;
    message.destinationMac = {0x01, 0x0c, 0xcd, 0x01, 0x00, 0x01};
    message.goId = "IED1/LLN0$GO$gcb01";
    message.dataSet = "IED1/LLN0$DS$ds01";
    message.stNum = stNum;
    message.sqNum = sqNum;
    message.timestamp = timestamp;
    message.timeToLive = 2s;
    message.configVersion = 7;
    return message;
}

void test_unconfigured() {
    PublishValidator validator;
    const auto result = validator.validate(make_message(1, 0, kReceiveBase),
                                           kReceiveBase);
    expect_rejected(result, RejectReason::not_configured,
                    "an unconfigured validator accepts nothing");
}

void test_legal_publication_and_retransmission() {
    PublishValidator validator(make_config());

    const auto first = validator.validate(make_message(10, 0, kReceiveBase),
                                          kReceiveBase + 100ms);
    expect(first.accepted, "first legitimate publication is accepted");
    expect(validator.trusted_state().has_value(),
           "accepted publication advances trusted state");
    expect(validator.trusted_state()->stNum == 10 &&
               validator.trusted_state()->sqNum == 0,
           "trusted state records stNum/sqNum");

    const auto retransmission =
        validator.validate(make_message(10, 1, kReceiveBase),
                           kReceiveBase + 200ms);
    expect(retransmission.accepted,
           "sqNum advance within the same state is accepted");

    const auto next_state =
        validator.validate(make_message(11, 0, kReceiveBase + 300ms),
                           kReceiveBase + 400ms);
    expect(next_state.accepted,
           "stNum advance with a newer timestamp is accepted");
    expect(validator.trusted_state()->stNum == 11 &&
               validator.trusted_state()->sqNum == 0,
           "trusted state advances to the new state");
}

void test_replay_and_rollback() {
    PublishValidator validator(make_config());

    expect(validator.validate(make_message(20, 0, kReceiveBase),
                              kReceiveBase + 10ms)
               .accepted,
           "baseline publication accepted");
    expect(validator.validate(make_message(20, 1, kReceiveBase + 1ms),
                              kReceiveBase + 20ms)
               .accepted,
           "sequence advance accepted");

    expect_rejected(validator.validate(make_message(20, 1, kReceiveBase + 1ms),
                                       kReceiveBase + 30ms),
                    RejectReason::replay,
                    "exact replay is rejected");

    expect_rejected(validator.validate(make_message(20, 0, kReceiveBase),
                                       kReceiveBase + 40ms),
                    RejectReason::sequence_rollback,
                    "sqNum rollback is rejected");

    expect_rejected(validator.validate(make_message(19, 9, kReceiveBase + 2ms),
                                       kReceiveBase + 50ms),
                    RejectReason::state_rollback,
                    "stNum rollback is rejected");
    expect(validator.trusted_state()->stNum == 20 &&
               validator.trusted_state()->sqNum == 1,
           "rejected frames do not move trusted state");
}

void test_forged_state_number() {
    PublishValidator validator(make_config());

    expect(validator.validate(make_message(30, 1, kReceiveBase),
                              kReceiveBase + 10ms)
               .accepted,
           "baseline publication accepted");

    // Replay the trusted frame with stNum edited upward. It is still inside
    // the TTL window, so only the state timestamp can expose the forgery.
    PublishMessage forged = make_message(31, 1, kReceiveBase);
    expect_rejected(validator.validate(forged, kReceiveBase + 20ms),
                    RejectReason::timestamp_rollback,
                    "replayed frame with forged stNum is rejected");

    // A genuinely newer state changes the state timestamp as well.
    expect(validator.validate(make_message(31, 0, kReceiveBase + 30ms),
                              kReceiveBase + 40ms)
               .accepted,
           "genuine state change is accepted");
}

void test_identity_binding() {
    PublishValidator validator(make_config());
    expect(validator.validate(make_message(1, 0, kReceiveBase),
                              kReceiveBase + 10ms)
               .accepted,
           "baseline publication accepted");

    auto app_id = make_message(2, 0, kReceiveBase + 1ms);
    app_id.appId = 0x4321;
    expect_rejected(validator.validate(app_id, kReceiveBase + 20ms),
                    RejectReason::app_id_mismatch,
                    "APPID substitution is rejected");

    auto mac = make_message(2, 0, kReceiveBase + 1ms);
    mac.destinationMac[5] = 0x02;
    expect_rejected(validator.validate(mac, kReceiveBase + 20ms),
                    RejectReason::destination_mac_mismatch,
                    "destination MAC substitution is rejected");

    auto go_id = make_message(2, 0, kReceiveBase + 1ms);
    go_id.goId = "IED1/LLN0$GO$gcb02";
    expect_rejected(validator.validate(go_id, kReceiveBase + 20ms),
                    RejectReason::go_id_mismatch,
                    "goID substitution is rejected");

    auto data_set = make_message(2, 0, kReceiveBase + 1ms);
    data_set.dataSet = "IED1/LLN0$DS$unknown";
    expect_rejected(validator.validate(data_set, kReceiveBase + 20ms),
                    RejectReason::unknown_data_set,
                    "unknown data set is rejected");

    auto version = make_message(2, 0, kReceiveBase + 1ms);
    version.configVersion = 8;
    expect_rejected(validator.validate(version, kReceiveBase + 20ms),
                    RejectReason::config_version_mismatch,
                    "configuration version mismatch is rejected");

    expect(validator.trusted_state()->stNum == 1,
           "identity rejection leaves trusted state unchanged");
}

void test_time_and_ttl() {
    PublishValidator validator(make_config());

    auto future = make_message(1, 0, kReceiveBase + 2s);
    expect_rejected(validator.validate(future, kReceiveBase),
                    RejectReason::timestamp_in_future,
                    "timestamp beyond future skew is rejected");

    auto stale = make_message(1, 0, kReceiveBase - 6s);
    expect_rejected(validator.validate(stale, kReceiveBase),
                    RejectReason::timestamp_expired,
                    "timestamp older than max age is rejected");

    auto zero_ttl = make_message(1, 0, kReceiveBase);
    zero_ttl.timeToLive = 0ms;
    expect_rejected(validator.validate(zero_ttl, kReceiveBase),
                    RejectReason::invalid_time_to_live,
                    "non-positive TTL is rejected");

    auto excessive_ttl = make_message(1, 0, kReceiveBase);
    excessive_ttl.timeToLive = 4s;
    expect_rejected(validator.validate(excessive_ttl, kReceiveBase),
                    RejectReason::time_to_live_exceeds_limit,
                    "TTL above the configured ceiling is rejected");

    auto expired_ttl = make_message(1, 0, kReceiveBase);
    expect_rejected(validator.validate(expired_ttl, kReceiveBase + 2500ms),
                    RejectReason::time_to_live_expired,
                    "publication past timeAllowedToLive is rejected");

    expect(!validator.trusted_state().has_value(),
           "time rejection does not establish trusted state");
}

void test_configuration_change() {
    PublishValidator validator(make_config());

    expect(validator.validate(make_message(5, 0, kReceiveBase),
                              kReceiveBase + 10ms)
               .accepted,
           "publication for the original configuration accepted");

    SubscriptionConfig changed = make_config();
    changed.configVersion = 8;
    validator.configure(changed);

    auto old_version = make_message(6, 0, kReceiveBase + 1ms);
    expect_rejected(validator.validate(old_version, kReceiveBase + 20ms),
                    RejectReason::config_version_mismatch,
                    "publication for the superseded configuration rejected");

    auto new_version = make_message(1, 0, kReceiveBase + 2ms);
    new_version.configVersion = 8;
    expect(validator.validate(new_version, kReceiveBase + 30ms).accepted,
           "publication for the new configuration accepted");
    expect(validator.trusted_state()->configVersion == 8,
           "trusted state records the new configuration version");

    // Re-applying the same policy must not clear the replay high-water mark.
    validator.configure(changed);
    expect_rejected(validator.validate(new_version, kReceiveBase + 40ms),
                    RejectReason::replay,
                    "identical reconfiguration preserves replay protection");

    // An explicitly different subscription starts a new sequence.
    SubscriptionConfig other = make_config();
    other.appId = 0x7777;
    other.configVersion = 1;
    validator.configure(other);
    auto other_message = make_message(1, 0, kReceiveBase + 3ms);
    other_message.appId = 0x7777;
    other_message.configVersion = 1;
    expect(validator.validate(other_message, kReceiveBase + 50ms).accepted,
           "new subscription identity starts a new trusted sequence");
}

void test_configuration_validation() {
    SubscriptionConfig config = make_config();
    config.maxTimestampAge = 0ms;

    bool threw = false;
    try {
        PublishValidator validator(config);
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    expect(threw, "invalid time bounds are rejected at configuration time");
}

}  // namespace

int main() {
    test_unconfigured();
    test_legal_publication_and_retransmission();
    test_replay_and_rollback();
    test_forged_state_number();
    test_identity_binding();
    test_time_and_ttl();
    test_configuration_change();
    test_configuration_validation();

    if (failures != 0) {
        std::fprintf(stderr, "%d test(s) failed\n", failures);
        return 1;
    }

    std::puts("all netsec tests passed");
    return 0;
}
