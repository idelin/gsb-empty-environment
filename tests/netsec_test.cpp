#include "netsec.hpp"

#include <chrono>
#include <cstdint>
#include <iostream>
#include <limits>
#include <string_view>
#include <vector>

namespace {

using namespace netsec;
using namespace std::chrono_literals;

int failures = 0;

#define CHECK(condition)                                                       \
    do {                                                                       \
        if (!(condition)) {                                                    \
            std::cerr << __FILE__ << ':' << __LINE__                           \
                      << ": CHECK failed: " #condition << '\n';                \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

Ipv4Address ip(std::string_view text) {
    const auto parsed = Ipv4Address::parse(text);
    CHECK(parsed.has_value());
    return parsed.value_or(Ipv4Address{});
}

MacAddress mac(std::string_view text) {
    const auto parsed = MacAddress::parse(text);
    CHECK(parsed.has_value());
    return parsed.value_or(MacAddress{});
}

EncapsulatedFrame packet(Ipv4Address source, Vni vni, MacAddress inner_source,
                         MacAddress inner_destination,
                         TunnelGeneration generation = 0,
                         TunnelKey key = 0) {
    EncapsulatedFrame frame;
    frame.outer_source = source;
    frame.vni = vni;
    frame.generation = generation;
    frame.tunnel_key = key;
    frame.inner_source_mac = inner_source;
    frame.inner_destination_mac = inner_destination;
    frame.inner_frame_bytes = 64;
    frame.outer_datagram_bytes = 100;
    return frame;
}

AdmissionConfig no_rate_limit_config() {
    AdmissionConfig config;
    config.amplification.enabled = false;
    return config;
}

void test_unknown_outer_source_is_dropped() {
    VtepAdmission admission(no_rate_limit_config());
    const auto registered = ip("10.0.0.10");
    const auto attacker = ip("203.0.113.9");
    const auto vni = 100U;

    CHECK(admission.register_vtep(registered, vni, 7));
    const auto result =
        admission.admit(packet(attacker, vni, mac("02:00:00:00:00:01"),
                               mac("02:00:00:00:00:02")));

    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::UnknownVtep);
    CHECK(admission.counters().unknown_vtep == 1);
    CHECK(admission.counters().dropped == 1);

    // A forged source must not create a MAC learning entry that can later
    // block the real owner of that MAC.
    const auto legitimate =
        admission.admit(packet(registered, vni, mac("02:00:00:00:00:01"),
                               mac("02:00:00:00:00:02")));
    CHECK(legitimate.accepted());
    CHECK(legitimate.mac_learned);
}

void test_vni_and_tenant_binding() {
    VtepAdmission admission(no_rate_limit_config());
    const auto first = ip("10.0.0.11");
    const auto second = ip("10.0.0.12");

    CHECK(admission.register_vtep(first, 100, 7));
    CHECK(admission.register_vtep(second, 200, 8));
    CHECK(!admission.register_vtep(second, 100, 9));
    CHECK(admission.tenant_for_vni(100) == std::optional<TenantId>(7));

    // first is a valid VTEP, but it is not registered in VNI 200.
    auto result = admission.admit(packet(first, 200, mac("02:00:00:00:00:11"),
                                         mac("02:00:00:00:00:22")));
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::VniMismatch);

    result = admission.admit(packet(first, 300, mac("02:00:00:00:00:11"),
                                    mac("02:00:00:00:00:22")));
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::VniMismatch);
    CHECK(admission.counters().vni_mismatch == 2);
}

void test_inner_source_mac_conflict() {
    VtepAdmission admission(no_rate_limit_config());
    const auto first = ip("10.0.0.21");
    const auto second = ip("10.0.0.22");
    const auto tenant = 42U;
    const auto vni = 444U;
    const auto claimed = mac("02:00:00:00:00:aa");
    const auto other = mac("02:00:00:00:00:bb");

    CHECK(admission.register_vtep(first, vni, tenant));
    CHECK(admission.register_vtep(second, vni, tenant));

    auto result = admission.admit(
        packet(first, vni, claimed, mac("02:00:00:00:00:01")));
    CHECK(result.accepted());
    CHECK(result.mac_learned);

    result = admission.admit(
        packet(second, vni, claimed, mac("02:00:00:00:00:01")));
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::InnerMacConflict);
    CHECK(admission.counters().inner_mac_conflict == 1);

    // The same MAC is allowed for its original VTEP.
    result = admission.admit(
        packet(first, vni, claimed, mac("02:00:00:00:00:01")));
    CHECK(result.accepted());

    // MAC learning is VNI-scoped, so another tenant's VNI can use the MAC.
    CHECK(admission.register_vtep(second, 445, 43));
    result = admission.admit(
        packet(second, 445, claimed, mac("02:00:00:00:00:01")));
    CHECK(result.accepted());
    CHECK(admission.counters().inner_mac_conflict == 1);

    result = admission.admit(
        packet(second, vni, other, mac("02:00:00:00:00:01")));
    CHECK(result.accepted());
    CHECK(result.mac_learned);
}

void test_raw_ethernet_frame_is_parsed() {
    VtepAdmission admission(no_rate_limit_config());
    const auto source = ip("10.0.0.31");
    const auto vni = 500U;
    const auto inner_source = mac("02:00:00:00:00:31");
    const auto inner_destination = mac("02:00:00:00:00:32");
    CHECK(admission.register_vtep(source, vni, 5));

    EncapsulatedFrame frame;
    frame.outer_source = source;
    frame.vni = vni;
    frame.inner_frame.resize(18);
    for (std::size_t i = 0; i < inner_destination.octets.size(); ++i) {
        frame.inner_frame[i] = inner_destination.octets[i];
        frame.inner_frame[i + 6] = inner_source.octets[i];
    }

    const auto result = admission.admit(frame);
    CHECK(result.accepted());
    CHECK(result.mac_learned);

    // A mismatch between trusted metadata and the decapsulated frame is
    // malformed and must not bypass the learning table.
    frame.inner_source_mac = mac("02:00:00:00:00:99");
    const auto mismatch = admission.admit(frame);
    CHECK(!mismatch.accepted());
    CHECK(mismatch.reason == DropReason::MalformedInnerFrame);
}

void test_fragmented_and_oversized_requests_are_dropped() {
    VtepAdmission admission(no_rate_limit_config());
    const auto source = ip("10.0.0.41");
    const auto vni = 600U;
    const auto inner_source = mac("02:00:00:00:00:41");
    const auto inner_destination = mac("02:00:00:00:00:42");
    CHECK(admission.register_vtep(source, vni, 6));

    auto frame = packet(source, vni, inner_source, inner_destination);
    frame.fragmented = true;
    auto result = admission.admit(frame);
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::Fragmented);

    frame = packet(source, vni, inner_source, inner_destination);
    frame.inner_frame_bytes = 9000;
    result = admission.admit(frame);
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::Oversized);

    frame = packet(source, vni, inner_source, inner_destination);
    frame.outer_datagram_bytes = 2000;
    result = admission.admit(frame);
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::Oversized);

    const auto counters = admission.counters();
    CHECK(counters.fragmented == 1);
    CHECK(counters.oversized == 2);
    CHECK(counters.inner_mac_conflict == 0);
}

void test_broadcast_and_unknown_unicast_fanout() {
    AdmissionConfig config;
    config.amplification.enabled = false;
    config.amplification.max_ingress_replication_copies = 2;
    VtepAdmission admission(config);
    const auto vni = 700U;
    const auto source = ip("10.0.0.51");
    for (int i = 0; i < 4; ++i) {
        CHECK(admission.register_vtep(
            ip("10.0.0." + std::to_string(51 + i)), vni, 7));
    }
    CHECK(admission.registered_vtep_count(vni) == 4);

    const auto now = VtepAdmission::Clock::time_point{100s};
    auto result = admission.admit(
        packet(source, vni, mac("02:00:00:00:00:51"),
               mac("ff:ff:ff:ff:ff:ff")),
        now);
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::AmplificationLimited);

    // Unknown unicast also floods to every other VTEP.
    result = admission.admit(
        packet(source, vni, mac("02:00:00:00:00:51"),
               mac("02:00:00:00:00:77")),
        now);
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::AmplificationLimited);
    CHECK(admission.counters().amplification_limited == 2);
}

void test_reflection_rate_limit() {
    AdmissionConfig config;
    config.amplification.enabled = true;
    config.amplification.burst_packets = 2;
    config.amplification.packets_per_second = 1;
    config.amplification.burst_bytes = 10000;
    config.amplification.bytes_per_second = 10000;
    config.amplification.max_ingress_replication_copies =
        std::numeric_limits<std::uint32_t>::max();
    VtepAdmission admission(config);

    const auto vni = 701U;
    const auto source = ip("10.0.0.55");
    const auto other = ip("10.0.0.56");
    CHECK(admission.register_vtep(source, vni, 7));
    CHECK(admission.register_vtep(other, vni, 7));

    // Learn the destination from the other VTEP first, avoiding the fan-out
    // limit while exercising the separate per-source token bucket.
    const auto destination = mac("02:00:00:00:00:60");
    const auto now = VtepAdmission::Clock::time_point{100s};
    auto result = admission.admit(
        packet(other, vni, destination, mac("02:00:00:00:00:70")), now);
    CHECK(result.accepted());

    auto first = packet(source, vni, mac("02:00:00:00:00:55"), destination);
    result = admission.admit(first, now);
    CHECK(result.accepted());
    result = admission.admit(first, now);
    CHECK(result.accepted());
    result = admission.admit(first, now);
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::AmplificationLimited);
    CHECK(admission.counters().amplification_limited == 1);

    // A different registered VTEP has an independent bucket.
    result = admission.admit(
        packet(other, vni, mac("02:00:00:00:00:56"), destination), now);
    CHECK(result.accepted());
}

void test_tunnel_key_and_generation_rotation() {
    const auto source = ip("10.0.0.61");
    const auto vni = 800U;
    const auto tenant = 8U;
    AdmissionConfig config;
    config.amplification.enabled = false;
    config.initial_tunnel_epoch = TunnelEpoch{10, 0x11223344U};
    VtepAdmission admission(config);
    CHECK(admission.register_vtep(source, vni, tenant));

    const auto inner_source = mac("02:00:00:00:00:61");
    const auto inner_destination = mac("02:00:00:00:00:62");
    auto result = admission.admit(packet(source, vni, inner_source,
                                         inner_destination, 10, 0x11223344U));
    CHECK(result.accepted());

    result = admission.admit(packet(source, vni, inner_source,
                                    inner_destination, 10, 0xDEADBEEFU));
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::TunnelEpochMismatch);

    admission.rotate_tunnel_epoch(tenant, TunnelEpoch{11, 0x55667788U}, true);
    result = admission.admit(packet(source, vni, inner_source,
                                    inner_destination, 10, 0x11223344U));
    CHECK(result.accepted());  // one rotation grace window
    result = admission.admit(packet(source, vni, inner_source,
                                    inner_destination, 11, 0x55667788U));
    CHECK(result.accepted());

    admission.rotate_tunnel_epoch(tenant, TunnelEpoch{12, 0xAABBCCDDU}, false);
    result = admission.admit(packet(source, vni, inner_source,
                                    inner_destination, 10, 0x11223344U));
    CHECK(!result.accepted());  // generation 10 is now retired
    CHECK(result.reason == DropReason::TunnelEpochMismatch);
    result = admission.admit(packet(source, vni, inner_source,
                                    inner_destination, 12, 0xAABBCCDDU));
    CHECK(result.accepted());

    CHECK(admission.current_tunnel_epoch(tenant) ==
          std::optional<TunnelEpoch>(TunnelEpoch{12, 0xAABBCCDDU}));
    CHECK(admission.counters().tunnel_epoch_mismatch == 2);
}

void test_unregister_removes_authorization_and_learning() {
    VtepAdmission admission(no_rate_limit_config());
    const auto first = ip("10.0.0.71");
    const auto second = ip("10.0.0.72");
    const auto vni = 900U;
    const auto claimed = mac("02:00:00:00:00:71");

    CHECK(admission.register_vtep(first, vni, 9));
    CHECK(admission.register_vtep(second, vni, 9));
    CHECK(admission.admit(packet(first, vni, claimed,
                                 mac("02:00:00:00:00:99")))
              .accepted());

    CHECK(admission.unregister_vtep(first, vni));
    CHECK(!admission.is_registered(first, vni));

    auto result =
        admission.admit(packet(first, vni, claimed,
                               mac("02:00:00:00:00:99")));
    CHECK(!result.accepted());
    CHECK(result.reason == DropReason::UnknownVtep);

    // The learned MAC belonged to the unregistered VTEP and is released.
    result =
        admission.admit(packet(second, vni, claimed,
                               mac("02:00:00:00:00:99")));
    CHECK(result.accepted());
    CHECK(result.mac_learned);
}

void test_address_parsing() {
    CHECK(ip("192.0.2.1").to_string() == "192.0.2.1");
    CHECK(!Ipv4Address::parse("192.0.2").has_value());
    CHECK(!Ipv4Address::parse("192.0.2.256").has_value());
    CHECK(!Ipv4Address::parse("192.0.2.-1").has_value());
    CHECK(mac("02:00:00:00:00:01").to_string() == "02:00:00:00:00:01");
    CHECK(mac("02-00-00-00-00-01") == mac("02:00:00:00:00:01"));
    CHECK(!MacAddress::parse("02:00:00:00:00").has_value());
}

}  // namespace

int main() {
    test_unknown_outer_source_is_dropped();
    test_vni_and_tenant_binding();
    test_inner_source_mac_conflict();
    test_raw_ethernet_frame_is_parsed();
    test_fragmented_and_oversized_requests_are_dropped();
    test_broadcast_and_unknown_unicast_fanout();
    test_reflection_rate_limit();
    test_tunnel_key_and_generation_rotation();
    test_unregister_removes_authorization_and_learning();
    test_address_parsing();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all VTEP admission tests passed\n";
    return 0;
}
