#include "netsec.hpp"

#include <cstdlib>
#include <iostream>
#include <string>
#include <string_view>

namespace {

int failures = 0;

void check(bool condition, std::string_view message) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << message << '\n';
    }
}

netsec::SessionConfig make_session(std::uint64_t control,
                                   std::uint64_t user,
                                   std::uint64_t bearer,
                                   netsec::Teid uplink_teid,
                                   const netsec::IpAddress& uplink_address,
                                   netsec::Teid downlink_teid,
                                   const netsec::IpAddress& downlink_address) {
    netsec::SessionConfig config;
    config.control_session_id = control;
    config.user_id = user;
    config.bearer_id = bearer;
    config.uplink_teid = uplink_teid;
    config.uplink_address = uplink_address;
    config.downlink_teid = downlink_teid;
    config.downlink_address = downlink_address;
    return config;
}

netsec::LookupResult uplink_packet(const netsec::TunnelMap& map,
                                   netsec::Teid teid,
                                   std::string_view source) {
    netsec::Packet packet;
    packet.direction = netsec::Direction::Uplink;
    packet.teid = teid;
    packet.source_address = netsec::IpAddress{source};
    return map.forward(packet);
}

netsec::LookupResult downlink_packet(const netsec::TunnelMap& map,
                                     netsec::Teid teid,
                                     std::string_view destination) {
    netsec::Packet packet;
    packet.direction = netsec::Direction::Downlink;
    packet.teid = teid;
    packet.destination_address = netsec::IpAddress{destination};
    return map.forward(packet);
}

void test_addresses() {
    const netsec::IpAddress ipv4{"192.0.2.10"};
    check(ipv4.valid(), "IPv4 address should parse");
    check(ipv4.is_ipv4(), "IPv4 family");
    check(ipv4.to_string() == "192.0.2.10", "IPv4 canonical form");
    check(netsec::IpAddress{0x0a000001}.to_string() == "10.0.0.1",
          "IPv4 integer construction is host-order");

    const netsec::IpAddress ipv6{"2001:db8::1"};
    check(ipv6.valid(), "IPv6 address should parse");
    check(ipv6.is_ipv6(), "IPv6 family");
    check(ipv6.to_string() == "2001:db8::1", "IPv6 canonical form");

    check(!netsec::IpAddress{"not-an-address"}.valid(),
          "malformed address must be invalid");
    check(!netsec::IpAddress{"300.0.0.1"}.valid(),
          "out-of-range IPv4 octet must be invalid");
}

void test_legitimate_forwarding() {
    netsec::TunnelMap map;
    const auto session = map.create_session(
        make_session(10, 20, 30, 0x100, netsec::IpAddress{"192.0.2.10"},
                     0x200, netsec::IpAddress{"198.51.100.20"}));
    check(session.has_value(), "valid session should be created");

    const auto uplink = uplink_packet(map, 0x100, "192.0.2.10");
    check(uplink.allowed(), "uplink with bound TEID and address must pass");
    check(uplink->user_id == 20, "uplink must resolve the bound user");
    check(uplink->bearer_id == 30, "uplink must resolve the bound bearer");
    check(uplink->control_session_id == 10,
          "uplink must resolve the control-plane session");

    const auto downlink = downlink_packet(map, 0x200, "198.51.100.20");
    check(downlink.allowed(), "downlink with bound TEID and address must pass");
    check(downlink->bearer_id == 30, "downlink must resolve the bound bearer");

    check(!uplink_packet(map, 0x100, "198.51.100.20").allowed(),
          "uplink source address must be checked");
    check(!downlink_packet(map, 0x200, "192.0.2.10").allowed(),
          "downlink destination address must be checked");
    check(!uplink_packet(map, 0x200, "198.51.100.20").allowed(),
          "a downlink TEID must not be accepted on the uplink path");
}

void test_guessed_teid_is_dropped() {
    netsec::TunnelMap map;
    const auto session = map.create_session(
        make_session(11, 21, 31, 0x101, netsec::IpAddress{"192.0.2.11"},
                     0x201, netsec::IpAddress{"198.51.100.21"}));
    check(session.has_value(), "session setup");

    const auto guessed = uplink_packet(map, 0xdead, "192.0.2.11");
    check(!guessed.allowed(), "guessed TEID must be dropped");
    check(guessed.reason == netsec::DropReason::UnknownTeid,
          "guessed TEID reason");

    const auto zero = uplink_packet(map, 0, "192.0.2.11");
    check(!zero.allowed(), "TEID zero must be dropped");
    check(zero.reason == netsec::DropReason::InvalidTeid,
          "zero TEID reason");
}

void test_cross_session_injection_is_dropped() {
    netsec::TunnelMap map;
    const auto first = map.create_session(
        make_session(12, 22, 32, 0x102, netsec::IpAddress{"192.0.2.12"},
                     0x202, netsec::IpAddress{"198.51.100.22"}));
    const auto second = map.create_session(
        make_session(13, 23, 33, 0x103, netsec::IpAddress{"192.0.2.13"},
                     0x203, netsec::IpAddress{"198.51.100.23"}));
    check(first.has_value() && second.has_value(), "both sessions setup");

    const auto injected_uplink =
        uplink_packet(map, 0x102, "192.0.2.13");
    check(!injected_uplink.allowed(),
          "TEID from one session with another session's address is dropped");
    check(injected_uplink.reason == netsec::DropReason::AddressMismatch,
          "cross-session uplift reason");

    const auto injected_downlink =
        downlink_packet(map, 0x203, "198.51.100.22");
    check(!injected_downlink.allowed(),
          "TEID from one session with another destination is dropped");
    check(injected_downlink.reason == netsec::DropReason::AddressMismatch,
          "cross-session downlink reason");

    int delivered = 0;
    netsec::Packet packet;
    packet.direction = netsec::Direction::Uplink;
    packet.teid = 0x102;
    packet.source_address = netsec::IpAddress{"192.0.2.13"};
    check(!map.dispatch(packet, [&](const netsec::Route&) { ++delivered; }),
          "injected packet dispatch must fail");
    check(delivered == 0, "injected packet reaches no bearer");
}

void test_release_and_reuse() {
    netsec::TunnelMap map;
    const auto old_session = map.create_session(
        make_session(14, 24, 34, 0x104, netsec::IpAddress{"192.0.2.14"},
                     0x204, netsec::IpAddress{"198.51.100.24"}));
    check(old_session.has_value(), "old session setup");

    check(map.release_session(*old_session), "release old session");
    check(!uplink_packet(map, 0x104, "192.0.2.14").allowed(),
          "released uplink TEID must be unknown");
    check(!downlink_packet(map, 0x204, "198.51.100.24").allowed(),
          "released downlink TEID must be unknown");

    const auto reused = map.create_session(
        make_session(14, 25, 35, 0x104, netsec::IpAddress{"192.0.2.15"},
                     0x204, netsec::IpAddress{"198.51.100.25"}));
    check(reused.has_value(), "released control-session id and TEIDs can be reused");
    check(map.is_active(*reused), "reused session active");

    check(!map.release_session(*old_session),
          "stale handle must not release the reused session");
    check(map.is_active(*reused), "reused session survives stale release");
    const auto reused_uplink = uplink_packet(map, 0x104, "192.0.2.15");
    check(reused_uplink.allowed(), "reused TEID routes to the new bearer");
    check(reused_uplink->user_id == 25 && reused_uplink->bearer_id == 35,
          "reused TEID resolves only the new owner");

    check(map.release_session(*reused), "release reused session");
    check(!map.is_active(*reused), "reused session is gone");
}

void test_conflicting_teid_is_quarantined() {
    netsec::TunnelMap map;
    const auto owner = map.create_session(
        make_session(15, 26, 36, 0x105, netsec::IpAddress{"192.0.2.16"},
                     0x205, netsec::IpAddress{"198.51.100.26"}));
    check(owner.has_value(), "owner session setup");

    const auto conflicting = map.create_session(
        make_session(16, 27, 37, 0x105, netsec::IpAddress{"192.0.2.17"},
                     0x206, netsec::IpAddress{"198.51.100.27"}));
    check(!conflicting.has_value(), "conflicting TEID registration is rejected");
    check(map.session_count() == 1, "conflicting session is not installed");
    check(map.is_teid_conflicted(netsec::Direction::Uplink, 0x105),
          "ambiguous TEID is quarantined");

    const auto ambiguous = uplink_packet(map, 0x105, "192.0.2.16");
    check(!ambiguous.allowed(), "quarantined TEID must not be forwarded");
    check(ambiguous.reason == netsec::DropReason::ConflictTeid,
          "conflicting TEID reason");

    check(downlink_packet(map, 0x205, "198.51.100.26").allowed(),
          "conflict in one direction must not corrupt the other direction");

    check(map.release_session(*owner), "release conflicting owner");
    check(!map.is_teid_conflicted(netsec::Direction::Uplink, 0x105),
          "conflict clears when the owner is released");

    const auto replacement = map.create_session(
        make_session(17, 28, 38, 0x105, netsec::IpAddress{"192.0.2.18"},
                     0x207, netsec::IpAddress{"198.51.100.28"}));
    check(replacement.has_value(), "TEID can be assigned after conflict clears");
    check(uplink_packet(map, 0x105, "192.0.2.18").allowed(),
          "replacement session receives the TEID");
}

void test_duplicate_control_session_is_rejected_without_poisoning() {
    netsec::TunnelMap map;
    const auto first = map.create_session(
        make_session(18, 29, 39, 0x106, netsec::IpAddress{"192.0.2.19"},
                     0x206, netsec::IpAddress{"198.51.100.29"}));
    const auto duplicate = map.create_session(
        make_session(18, 30, 40, 0x107, netsec::IpAddress{"192.0.2.20"},
                     0x207, netsec::IpAddress{"198.51.100.30"}));
    check(first.has_value(), "first control session setup");
    check(!duplicate.has_value(), "duplicate control session id is rejected");
    check(uplink_packet(map, 0x106, "192.0.2.19").allowed(),
          "duplicate control session does not poison existing routes");
}

void test_dispatch_reaches_exactly_one_bearer() {
    netsec::TunnelMap map;
    const auto first = map.create_session(
        make_session(19, 31, 41, 0x108, netsec::IpAddress{"192.0.2.21"},
                     0x208, netsec::IpAddress{"198.51.100.31"}));
    const auto second = map.create_session(
        make_session(20, 32, 42, 0x109, netsec::IpAddress{"192.0.2.22"},
                     0x209, netsec::IpAddress{"198.51.100.32"}));
    check(first.has_value() && second.has_value(), "two sessions setup");

    int deliveries = 0;
    netsec::BearerId delivered_bearer = 0;
    netsec::Packet packet;
    packet.direction = netsec::Direction::Uplink;
    packet.teid = 0x108;
    packet.source_address = netsec::IpAddress{"192.0.2.21"};
    check(map.dispatch(packet, [&](const netsec::Route& route) {
        ++deliveries;
        delivered_bearer = route.bearer_id;
    }), "legitimate packet dispatch succeeds");
    check(deliveries == 1, "packet reaches exactly one bearer");
    check(delivered_bearer == 41, "packet reaches its own bearer");
}

void test_alternate_configuration_spellings() {
    netsec::TunnelMap map;
    netsec::SessionConfig config;
    config.subscriber_id = 33;
    config.session_id = 21;
    config.bearer_id = 43;
    config.uplink = netsec::TunnelEndpoint{
        .teid = 0x10a,
        .peer_address = netsec::IpAddress{"192.0.2.23"},
    };
    config.downlink = netsec::TunnelEndpoint{
        .teid = 0x20a,
        .address = netsec::IpAddress{"198.51.100.33"},
    };

    const auto session = map.add_session(config);
    check(session.has_value(), "nested endpoint configuration is accepted");
    check(map.validate_uplink(0x10a, "192.0.2.23").allowed(),
          "nested uplink endpoint routes");
    check(map.validate_downlink(0x20a, "198.51.100.33").allowed(),
          "nested downlink endpoint routes");

    netsec::TunnelMap primitive_map;
    const auto primitive = primitive_map.establish_session(
        22, 34, 44, 0x10b, "192.0.2.24", 0x20b, "198.51.100.34");
    check(primitive.has_value(), "primitive session arguments are accepted");
    check(primitive_map.forward_uplink(0x10b, "192.0.2.24").allowed(),
          "primitive-argument session routes");
}

}  // namespace

int main() {
    test_addresses();
    test_legitimate_forwarding();
    test_guessed_teid_is_dropped();
    test_cross_session_injection_is_dropped();
    test_release_and_reuse();
    test_conflicting_teid_is_quarantined();
    test_duplicate_control_session_is_rejected_without_poisoning();
    test_dispatch_reaches_exactly_one_bearer();
    test_alternate_configuration_spellings();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return EXIT_FAILURE;
    }
    std::cout << "all tunnel-map tests passed\n";
    return EXIT_SUCCESS;
}
