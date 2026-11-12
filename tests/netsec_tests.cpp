#include "netsec.hpp"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
        ++failures;
    }
}

void check_status(netsec::Status actual, netsec::Status expected,
                  const char* expression, int line) {
    if (actual != expected) {
        std::cerr << "FAIL line " << line << ": " << expression
                  << " returned " << netsec::status_name(actual)
                  << ", expected " << netsec::status_name(expected) << '\n';
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)
#define CHECK_STATUS(expression, expected) \
    check_status((expression), (expected), #expression, __LINE__)

const netsec::MacAddress kAdminMac{0x02, 0x10, 0x20, 0x30, 0x40, 0x50};
const netsec::MacAddress kAlternateMac{0x02, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE};
const netsec::MacAddress kForgedMac{0x02, 0x13, 0x37, 0x00, 0x00, 0x01};

struct VlanTag {
    std::uint16_t tpid;
    std::uint16_t vlan_id;
};

void append_u16(std::vector<std::uint8_t>& frame, std::uint16_t value) {
    frame.push_back(static_cast<std::uint8_t>(value >> 8));
    frame.push_back(static_cast<std::uint8_t>(value & 0xFF));
}

std::vector<std::uint8_t> make_frame(
    const netsec::MacAddress& source,
    const std::vector<VlanTag>& tags = {},
    std::uint16_t payload_ether_type = 0x0800) {
    std::vector<std::uint8_t> frame{0x0A, 0x00, 0x00, 0x00, 0x00, 0x01};
    frame.insert(frame.end(), source.octets.begin(), source.octets.end());

    for (const VlanTag& tag : tags) {
        append_u16(frame, tag.tpid);
        append_u16(frame, tag.vlan_id);
    }

    append_u16(frame, payload_ether_type);
    for (int i = 0; i < 18; ++i) {
        frame.push_back(0x5A);
    }
    return frame;
}

void test_admin_mac_is_locked() {
    netsec::VfGuard guard(kAdminMac);

    CHECK_STATUS(guard.request_set_mac(kForgedMac),
                 netsec::Status::PermissionDenied);
    CHECK(guard.mac_locked());
    CHECK(guard.assigned_mac().has_value());
    CHECK(*guard.assigned_mac() == kAdminMac);

    const auto assigned_frame = make_frame(kAdminMac);
    const auto forged_frame = make_frame(kForgedMac);
    CHECK(guard.check_tx_frame(assigned_frame).accepted);
    CHECK(!guard.check_tx_frame(forged_frame).accepted);
    CHECK(guard.check_tx_frame(forged_frame).reason ==
          netsec::TxDropReason::SourceMacNotAllowed);

    CHECK_STATUS(guard.admin_clear_mac(), netsec::Status::Ok);
    CHECK(!guard.mac_locked());
    CHECK(!guard.assigned_mac().has_value());
    CHECK_STATUS(guard.request_set_mac(kAlternateMac), netsec::Status::Ok);
    CHECK(guard.assigned_mac().has_value());
    CHECK(*guard.assigned_mac() == kAlternateMac);
}

void test_vlan_allow_table() {
    netsec::VfGuard guard(kAdminMac);

    CHECK_STATUS(guard.admin_set_allowed_vlans({100, 200}),
                 netsec::Status::Ok);
    CHECK(guard.is_vlan_allowed(100));
    CHECK(guard.is_vlan_allowed(200));
    CHECK(!guard.is_vlan_allowed(300));

    CHECK_STATUS(guard.request_join_vlan(100), netsec::Status::Ok);
    CHECK(guard.is_vlan_joined(100));

    CHECK_STATUS(guard.request_join_vlan(300),
                 netsec::Status::PermissionDenied);
    CHECK(!guard.is_vlan_joined(300));

    CHECK_STATUS(guard.request_join_vlan(4095),
                 netsec::Status::InvalidArgument);

    // An invalid replacement table must not partially modify policy.
    CHECK_STATUS(guard.admin_set_allowed_vlans({100, 4095}),
                 netsec::Status::InvalidArgument);
    CHECK(guard.is_vlan_allowed(100));
    CHECK(guard.is_vlan_allowed(200));
    CHECK(guard.is_vlan_joined(100));

    CHECK_STATUS(guard.request_leave_vlan(100), netsec::Status::Ok);
    CHECK(!guard.is_vlan_joined(100));

    CHECK_STATUS(guard.request_join_vlan(200), netsec::Status::Ok);
    CHECK_STATUS(guard.admin_disallow_vlan(200), netsec::Status::Ok);
    CHECK(!guard.is_vlan_allowed(200));
    CHECK(!guard.is_vlan_joined(200));

    const auto allowed = guard.allowed_vlans();
    CHECK(allowed == std::vector<netsec::VlanId>({100}));
}

void test_port_vlan_drops_in_band_tags() {
    netsec::VfGuard guard(kAdminMac);
    guard.admin_set_port_vlan_enabled(true);

    CHECK(guard.check_tx_frame(make_frame(kAdminMac)).accepted);

    const auto single_tag =
        make_frame(kAdminMac, {{0x8100, 100}});
    const auto double_tag =
        make_frame(kAdminMac, {{0x88A8, 100}, {0x8100, 200}});

    auto result = guard.check_tx_frame(single_tag);
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::PortVlanTagPresent);

    result = guard.check_tx_frame(double_tag);
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::PortVlanTagPresent);

    const std::vector<std::uint8_t> truncated(13, 0);
    result = guard.check_tx_frame(truncated);
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::MalformedFrame);

    guard.admin_set_port_vlan_enabled(false);
    CHECK(guard.check_tx_frame(double_tag).accepted);
}

void test_tx_source_allowlist() {
    netsec::VfGuard guard(kAdminMac);
    CHECK_STATUS(guard.admin_allow_source_mac(kAlternateMac),
                 netsec::Status::Ok);

    CHECK(guard.check_tx_frame(make_frame(kAdminMac)).accepted);
    CHECK(guard.check_tx_frame(make_frame(kAlternateMac)).accepted);

    auto result = guard.check_tx_frame(make_frame(kForgedMac));
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::SourceMacNotAllowed);

    // Source extraction must remain correct through a stacked VLAN header.
    const auto tagged_alternate =
        make_frame(kAlternateMac, {{0x8100, 100}, {0x8100, 200}});
    CHECK(guard.check_tx_frame(tagged_alternate).accepted);

    const auto tagged_forged =
        make_frame(kForgedMac, {{0x88A8, 100}, {0x8100, 200}});
    result = guard.check_tx_frame(tagged_forged);
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::SourceMacNotAllowed);

    CHECK_STATUS(guard.admin_disallow_source_mac(kAlternateMac),
                 netsec::Status::Ok);
    result = guard.check_tx_frame(make_frame(kAlternateMac));
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::SourceMacNotAllowed);
}

void test_promiscuous_requires_trust() {
    netsec::VfGuard guard(kAdminMac, false);

    CHECK_STATUS(guard.request_set_promiscuous(true),
                 netsec::Status::NotTrusted);
    CHECK(!guard.promiscuous_enabled());

    CHECK_STATUS(guard.request_set_promiscuous(false), netsec::Status::Ok);
    CHECK(!guard.promiscuous_enabled());

    guard.admin_set_trusted(true);
    CHECK(guard.trusted());
    CHECK_STATUS(guard.request_set_promiscuous(true), netsec::Status::Ok);
    CHECK(guard.promiscuous_enabled());

    guard.admin_set_trusted(false);
    CHECK(!guard.trusted());
    CHECK(!guard.promiscuous_enabled());
    CHECK_STATUS(guard.request_set_promiscuous(true),
                 netsec::Status::NotTrusted);
    CHECK(!guard.promiscuous_enabled());
}

void test_mailbox_dispatch() {
    netsec::VfGuard guard(kAdminMac);
    CHECK_STATUS(guard.admin_allow_vlan(100), netsec::Status::Ok);

    CHECK_STATUS(
        guard.handle_mailbox(netsec::MailboxRequest::set_mac(kForgedMac)),
        netsec::Status::PermissionDenied);
    CHECK(*guard.assigned_mac() == kAdminMac);

    CHECK_STATUS(
        guard.handle_mailbox(netsec::MailboxRequest::join_vlan(100)),
        netsec::Status::Ok);
    CHECK(guard.is_vlan_joined(100));

    CHECK_STATUS(
        guard.handle_mailbox(netsec::MailboxRequest::join_vlan(300)),
        netsec::Status::PermissionDenied);
    CHECK(!guard.is_vlan_joined(300));

    CHECK_STATUS(
        guard.handle_mailbox(netsec::MailboxRequest::leave_vlan(100)),
        netsec::Status::Ok);
    CHECK(!guard.is_vlan_joined(100));

    CHECK_STATUS(
        guard.handle_mailbox(netsec::MailboxRequest::set_promiscuous(false)),
        netsec::Status::Ok);
    CHECK_STATUS(
        guard.handle_mailbox(netsec::MailboxRequest{}),
        netsec::Status::InvalidArgument);
}

void test_manager_isolates_vfs() {
    netsec::VfGuardManager manager;
    CHECK_STATUS(manager.add_vf(0, kAdminMac), netsec::Status::Ok);
    CHECK_STATUS(manager.add_vf(1, kAlternateMac), netsec::Status::Ok);
    CHECK_STATUS(manager.add_vf(0, kAdminMac), netsec::Status::AlreadyExists);

    CHECK_STATUS(
        manager.handle_mailbox(
            0, netsec::MailboxRequest::set_mac(kForgedMac)),
        netsec::Status::PermissionDenied);
    CHECK_STATUS(
        manager.handle_mailbox(
            99, netsec::MailboxRequest::set_mac(kForgedMac)),
        netsec::Status::NotFound);

    CHECK(manager.check_tx_frame(0, make_frame(kAdminMac)).accepted);
    CHECK(manager.check_tx_frame(1, make_frame(kAlternateMac)).accepted);

    const auto forged = make_frame(kForgedMac);
    auto result = manager.check_tx_frame(0, forged);
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::SourceMacNotAllowed);

    result = manager.check_tx_frame(99, forged);
    CHECK(!result.accepted);
    CHECK(result.reason == netsec::TxDropReason::UnknownVf);

    CHECK_STATUS(manager.remove_vf(1), netsec::Status::Ok);
    CHECK(manager.find_vf(1) == nullptr);
    CHECK_STATUS(manager.remove_vf(1), netsec::Status::NotFound);
}

void test_mac_address_helpers() {
    const auto parsed = netsec::MacAddress::parse("02:aa:bb:cc:dd:ee");
    CHECK(parsed.has_value());
    if (parsed) {
        CHECK(*parsed == kAlternateMac);
        CHECK(parsed->to_string() == "02:AA:BB:CC:DD:EE");
    }
    CHECK(!netsec::MacAddress::parse("02:aa:bb:cc:dd").has_value());
    CHECK(!netsec::MacAddress::parse("02-aa-bb-cc-dd:ee").has_value());
    CHECK(!netsec::MacAddress::parse("02:aa:bb:cc:dd:zz").has_value());
}

}  // namespace

int main() {
    test_admin_mac_is_locked();
    test_vlan_allow_table();
    test_port_vlan_drops_in_band_tags();
    test_tx_source_allowlist();
    test_promiscuous_requires_trust();
    test_mailbox_dispatch();
    test_manager_isolates_vfs();
    test_mac_address_helpers();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }

    std::cout << "all netsec tests passed\n";
    return 0;
}
