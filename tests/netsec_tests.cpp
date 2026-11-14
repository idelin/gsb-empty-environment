#include "netsec.hpp"
#include <chrono>
#include <iostream>
#include <set>
#include <string>
#include <vector>

namespace {

using netsec::AuthorizationCode;
using netsec::GroupAddress;
using netsec::GroupAuthorizer;
using netsec::GroupKind;
using netsec::GroupRegistry;
using netsec::IncomingCommand;
using netsec::RateLimitConfig;

int failures = 0;

void check(bool condition, const std::string& message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << '\n';
        ++failures;
    }
}

void expect_code(const netsec::AuthorizationResult& result,
                 AuthorizationCode expected,
                 const std::string& message) {
    if (result.code != expected) {
        std::cerr << "FAIL: " << message << " (got " << result.reason()
                  << ", expected code " << static_cast<int>(expected) << ")\n";
        ++failures;
    }
}

struct Fixture {
    using Clock = GroupAuthorizer::Clock;

    Clock::time_point time{};
    GroupAuthorizer authorizer;

    GroupAddress zone1_light{1, 1, netsec::function::light_on};
    GroupAddress zone2_light{2, 1, netsec::function::light_off};
    GroupAddress zone2_light_on{2, 1, netsec::function::light_on};
    GroupAddress zone1_valve{1, 2, netsec::function::valve_close};
    GroupAddress broadcast{0, 0, 0x20};
    GroupAddress broadcast_on{0, 0, netsec::function::light_on};
    GroupAddress central{0, 7, 0x21};

    static constexpr const char* zone1_light_panel = "1.1.1";
    static constexpr const char* zone2_light_panel = "2.1.1";
    static constexpr const char* zone1_valve_controller = "1.2.1";
    static constexpr const char* zone1_sensor = "1.1.9";
    static constexpr const char* central_console = "0.7.1";
    static constexpr const char* central_console_2 = "0.7.2";
    static constexpr const char* rogue = "1.1.50";

    Fixture()
        : authorizer(GroupRegistry{},
                     RateLimitConfig{
                         .burst = 8,
                         .refill_per_second = 0.0,
                         .duplicate_window = std::chrono::milliseconds{500},
                         .replay_window = std::chrono::seconds{10},
                         .max_replay_entries = 1024},
                     [this] { return time; }) {
        check(authorizer.register_device(zone1_light_panel, 1, 1),
              "register zone 1 light panel");
        check(authorizer.register_device(zone2_light_panel, 2, 1),
              "register zone 2 light panel");
        check(authorizer.register_device(zone1_valve_controller, 1, 2),
              "register zone 1 valve controller");
        check(authorizer.register_device(zone1_sensor, 1, 1),
              "register zone 1 sensor");
        check(authorizer.register_device(central_console, 0, 7),
              "register central console");
        check(authorizer.register_device(central_console_2, 0, 7),
              "register second central console");
        check(authorizer.register_device(rogue, 1, 1), "register rogue device");

        check(authorizer.register_group(zone1_light, GroupKind::zone),
              "register zone 1 light group");
        check(authorizer.register_group(zone2_light, GroupKind::zone),
              "register zone 2 light group");
        check(authorizer.register_group(zone2_light_on, GroupKind::zone),
              "register zone 2 light-on group");
        check(authorizer.register_group(zone1_valve, GroupKind::zone),
              "register zone 1 valve group");
        check(authorizer.register_group(broadcast, GroupKind::broadcast),
              "register broadcast group");
        check(authorizer.register_group(broadcast_on, GroupKind::broadcast),
              "register light-on broadcast group");
        check(authorizer.register_group(central, GroupKind::central),
              "register central group");

        check(authorizer.add_member(zone1_light, zone1_light_panel,
                                    {netsec::function::light_on}),
              "zone 1 panel joins zone 1 light group");
        check(authorizer.add_member(zone2_light, zone2_light_panel,
                                    {netsec::function::light_off}),
              "zone 2 panel joins zone 2 light group");
        check(authorizer.add_member(zone1_valve, zone1_valve_controller,
                                    {netsec::function::valve_close}),
              "valve controller joins valve group");
        check(authorizer.add_member(zone1_light, zone1_sensor,
                                    {netsec::function::dim}),
              "sensor joins with a different function permission");
        check(authorizer.add_member(broadcast, zone1_light_panel, {0x20}),
              "zone 1 panel has ordinary broadcast membership");
        check(authorizer.add_member(broadcast_on, zone1_light_panel,
                                    {netsec::function::light_on}),
              "zone 1 panel has ordinary light broadcast membership");
        check(authorizer.add_member(broadcast, central_console, {0x20}),
              "console has ordinary broadcast membership");
        check(authorizer.add_member(central, central_console, {0x21}),
              "console has ordinary central membership");
        check(authorizer.add_member(central, central_console_2, {0x21}),
              "second console has ordinary central membership");
        check(authorizer.grant_broadcast(broadcast, central_console, {0x20}),
              "console gets explicit broadcast authorization");
        check(authorizer.grant_central(central, central_console, {0x21}),
              "console gets explicit central authorization");
    }
};

IncomingCommand command(const char* source,
                        const GroupAddress& target,
                        std::uint64_t sequence,
                        std::string payload = {}) {
    return IncomingCommand{source, target, target.function, sequence,
                           std::move(payload)};
}

void test_address_model() {
    check(GroupAddress{31, 7, 255}.valid(), "maximum address is valid");
    check(!GroupAddress{32, 0, 0}.valid(), "zone above 31 is invalid");
    check(!GroupAddress{0, 8, 0}.valid(), "device type above 7 is invalid");
    const GroupAddress original{5, 3, 0xA5};
    check(GroupAddress::from_raw(original.raw()) == original,
          "group address raw round trip");
}

void test_registration_rules(Fixture& fixture) {
    check(!fixture.authorizer.register_device(Fixture::zone1_light_panel, 1, 1),
          "duplicate device registration is rejected");
    check(!fixture.authorizer.register_group(fixture.zone1_light),
          "duplicate group registration is rejected");
    check(!fixture.authorizer.add_member(fixture.zone2_light,
                                         Fixture::zone1_light_panel,
                                         {netsec::function::light_off}),
          "cross-zone membership is rejected");
    check(!fixture.authorizer.add_member(fixture.zone1_valve,
                                         Fixture::zone1_light_panel,
                                         {netsec::function::valve_close}),
          "wrong device type membership is rejected");
}

void test_forgery_and_routing(Fixture& fixture) {
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.zone1_light, 1,
                            "on"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::allow, "legitimate zone control");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::central_console, fixture.broadcast, 2,
                            "all-off"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::source_mismatch, "forged source address");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.zone1_light, 3,
                            "on"),
                    "unregistered-attacker"),
                AuthorizationCode::source_mismatch,
                "unauthenticated source is rejected");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.zone2_light, 4,
                            "off"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::cross_zone, "cross-zone command");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::rogue, fixture.zone1_light, 5, "on"),
                    Fixture::rogue),
                AuthorizationCode::sender_not_member, "non-member command");
    expect_code(fixture.authorizer.authorize(
                    command("9.9.9", fixture.zone1_light, 5, "on"), "9.9.9"),
                AuthorizationCode::unknown_sender, "unknown sender");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, GroupAddress{3, 1, 1}, 5,
                            "on"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::unknown_group, "unknown group");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_sensor, fixture.zone1_light, 6,
                            "on"),
                    Fixture::zone1_sensor),
                AuthorizationCode::function_not_permitted,
                "function permission is enforced");
    IncomingCommand wrong_function =
        command(Fixture::zone1_light_panel, fixture.zone1_light, 7, "close");
    wrong_function.function = netsec::function::valve_close;
    expect_code(fixture.authorizer.authorize(wrong_function,
                                             Fixture::zone1_light_panel),
                AuthorizationCode::function_mismatch,
                "command function must match group address");
    check(!fixture.authorizer.has_group(GroupAddress{9, 9, 9}),
          "unknown group lookup");
}

void test_special_groups(Fixture& fixture) {
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.broadcast, 20,
                            "all-off"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::broadcast_not_authorized,
                "broadcast abuse is denied");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::central_console_2, fixture.central, 21,
                            "central-off"),
                    Fixture::central_console_2),
                AuthorizationCode::central_not_authorized,
                "central abuse is denied");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::central_console, fixture.broadcast, 22,
                            "explicit-broadcast"),
                    Fixture::central_console),
                AuthorizationCode::allow, "explicitly authorized broadcast");
    check(fixture.authorizer.add_member(fixture.broadcast,
                                        Fixture::central_console, {0x20}),
          "ordinary membership update after explicit grant");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::central_console, fixture.broadcast, 24,
                            "explicit-broadcast-2"),
                    Fixture::central_console),
                AuthorizationCode::allow,
                "explicit grant survives membership update");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::central_console, fixture.central, 23,
                            "explicit-central"),
                    Fixture::central_console),
                AuthorizationCode::allow, "explicitly authorized central");
}

void test_replay_and_rate(Fixture& fixture) {
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.zone1_light, 30,
                            "on-30"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::allow, "first sequenced command");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.zone1_light, 30,
                            "on-30"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::duplicate, "same sequence is rejected");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.zone1_light, 31,
                            "on-30"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::duplicate, "identical payload is rejected");
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone1_light_panel, fixture.zone1_light, 32,
                            "on-32"),
                    Fixture::zone1_light_panel),
                AuthorizationCode::allow, "distinct command is allowed");
    for (std::uint64_t i = 0; i < 8; ++i) {
        const std::string payload = "rate-" + std::to_string(i);
        expect_code(fixture.authorizer.authorize(
                        command(Fixture::zone2_light_panel, fixture.zone2_light,
                                100 + i, payload),
                        Fixture::zone2_light_panel),
                    AuthorizationCode::allow,
                    "command within rate burst " + std::to_string(i));
    }
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone2_light_panel, fixture.zone2_light, 200,
                            "rate-over"),
                    Fixture::zone2_light_panel),
                AuthorizationCode::rate_limited, "rate limit is enforced");
    fixture.time += std::chrono::seconds{11};
    expect_code(fixture.authorizer.authorize(
                    command(Fixture::zone2_light_panel, fixture.zone2_light, 201,
                            "rate-over-2"),
                    Fixture::zone2_light_panel),
                AuthorizationCode::rate_limited,
                "zero refill keeps the rate limit in place");
}

void test_route_filtering(Fixture& fixture) {
    const std::vector<GroupAddress> requested{
        fixture.zone1_light, fixture.zone2_light_on, fixture.broadcast_on};
    const auto decision = fixture.authorizer.authorize_many(
        command(Fixture::zone1_light_panel, fixture.zone1_light, 300,
                "batch-on"),
        Fixture::zone1_light_panel, requested);
    check(decision.results.size() == requested.size(),
          "one decision per requested group");
    check(decision.authorized_targets.size() == 1,
          "only authorized groups receive the command");
    check(decision.authorized_targets.front() == fixture.zone1_light,
          "authorized target is the sender's own group");
    expect_code(decision.results[0], AuthorizationCode::allow,
                "own group allowed in batch");
    expect_code(decision.results[1], AuthorizationCode::cross_zone,
                "cross-zone target denied in batch");
    expect_code(decision.results[2], AuthorizationCode::broadcast_not_authorized,
                "broadcast target denied in batch");
}

}  // namespace

int main() {
    test_address_model();
    Fixture fixture;
    test_registration_rules(fixture);
    test_forgery_and_routing(fixture);
    test_special_groups(fixture);
    test_replay_and_rate(fixture);
    test_route_filtering(fixture);
    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "netsec tests passed\n";
    return 0;
}
