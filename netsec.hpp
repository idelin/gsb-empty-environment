#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace netsec {

inline constexpr std::size_t kMacOctets = 6;
inline constexpr std::uint16_t kMaxVlanId = 4094;
inline constexpr std::size_t kMaxVlanTagDepth = 8;

struct MacAddress {
    std::array<std::uint8_t, kMacOctets> octets{};

    constexpr MacAddress() noexcept = default;

    constexpr MacAddress(std::uint8_t octet0, std::uint8_t octet1,
                         std::uint8_t octet2, std::uint8_t octet3,
                         std::uint8_t octet4, std::uint8_t octet5) noexcept
        : octets{{octet0, octet1, octet2, octet3, octet4, octet5}} {}

    explicit constexpr MacAddress(
        std::array<std::uint8_t, kMacOctets> value) noexcept
        : octets(value) {}

    static std::optional<MacAddress> parse(std::string_view text);
    std::string to_string() const;

    friend constexpr bool operator==(const MacAddress& lhs,
                                     const MacAddress& rhs) noexcept {
        for (std::size_t i = 0; i < kMacOctets; ++i) {
            if (lhs.octets[i] != rhs.octets[i]) {
                return false;
            }
        }
        return true;
    }

    friend constexpr bool operator!=(const MacAddress& lhs,
                                     const MacAddress& rhs) noexcept {
        return !(lhs == rhs);
    }
};

struct MacAddressHash {
    std::size_t operator()(const MacAddress& mac) const noexcept;
};

enum class Status {
    Ok,
    InvalidArgument,
    PermissionDenied,
    NotTrusted,
    NotFound,
    AlreadyExists
};

const char* status_name(Status status) noexcept;

using VlanId = std::uint16_t;

constexpr bool is_valid_vlan_id(VlanId vlan_id) noexcept {
    return vlan_id <= kMaxVlanId;
}

enum class MailboxRequestType {
    Invalid,
    SetMac,
    JoinVlan,
    LeaveVlan,
    SetPromiscuous
};

struct MailboxRequest {
    MailboxRequestType type{MailboxRequestType::Invalid};
    MacAddress mac{};
    VlanId vlan_id{0};
    bool enabled{false};

    static MailboxRequest set_mac(const MacAddress& mac);
    static MailboxRequest join_vlan(VlanId vlan_id);
    static MailboxRequest leave_vlan(VlanId vlan_id);
    static MailboxRequest set_promiscuous(bool enabled);
};

enum class TxDropReason {
    None,
    UnknownVf,
    MalformedFrame,
    PortVlanTagPresent,
    SourceMacNotAllowed
};

struct TxResult {
    bool accepted{false};
    TxDropReason reason{TxDropReason::MalformedFrame};

    explicit constexpr operator bool() const noexcept { return accepted; }
};

const char* tx_drop_reason_name(TxDropReason reason) noexcept;

class VfGuard {
public:
    VfGuard() = default;

    // Constructing a guard with a MAC models PF/admin assignment. The MAC is
    // locked against guest mailbox changes by default.
    explicit VfGuard(MacAddress assigned_mac, bool trusted = false);

    // Administrator/PF policy. These calls are trusted and bypass the guest
    // mailbox permission checks.
    Status admin_set_mac(const MacAddress& mac);
    Status admin_clear_mac();

    Status admin_set_allowed_vlans(const std::vector<VlanId>& vlan_ids);
    Status admin_allow_vlan(VlanId vlan_id);
    Status admin_disallow_vlan(VlanId vlan_id);

    Status admin_set_allowed_source_macs(
        const std::vector<MacAddress>& macs);
    Status admin_allow_source_mac(const MacAddress& mac);
    Status admin_disallow_source_mac(const MacAddress& mac);

    void admin_set_port_vlan_enabled(bool enabled) noexcept;
    void admin_set_trusted(bool trusted) noexcept;

    // Guest mailbox requests. A VF cannot replace an administrator-assigned
    // MAC and cannot join a VLAN outside the administrator's allow table.
    Status request_set_mac(const MacAddress& mac);
    Status request_join_vlan(VlanId vlan_id);
    Status request_leave_vlan(VlanId vlan_id);
    Status request_set_promiscuous(bool enabled);
    Status handle_mailbox(const MailboxRequest& request);

    // PF transmit path. This validates the Ethernet source through any VLAN
    // tag stack and applies the port-VLAN/trusted policies.
    TxResult check_tx_frame(const std::uint8_t* frame,
                            std::size_t length) const noexcept;
    TxResult check_tx_frame(const std::vector<std::uint8_t>& frame) const
        noexcept;

    const std::optional<MacAddress>& assigned_mac() const noexcept {
        return assigned_mac_;
    }
    bool mac_locked() const noexcept { return mac_locked_; }
    bool trusted() const noexcept { return trusted_; }
    bool promiscuous_enabled() const noexcept { return promiscuous_; }
    bool port_vlan_enabled() const noexcept { return port_vlan_enabled_; }

    bool is_vlan_allowed(VlanId vlan_id) const noexcept;
    bool is_vlan_joined(VlanId vlan_id) const noexcept;
    bool is_source_mac_allowed(const MacAddress& mac) const noexcept;

    std::vector<VlanId> allowed_vlans() const;
    std::vector<VlanId> joined_vlans() const;
    std::vector<MacAddress> allowed_source_macs() const;

private:
    std::optional<MacAddress> assigned_mac_;
    bool mac_locked_{false};
    bool trusted_{false};
    bool promiscuous_{false};
    bool port_vlan_enabled_{false};
    std::unordered_set<VlanId> allowed_vlans_;
    std::unordered_set<VlanId> joined_vlans_;
    std::unordered_set<MacAddress, MacAddressHash> allowed_source_macs_;
};

class VfGuardManager {
public:
    using VfId = std::uint16_t;

    Status add_vf(VfId vf_id, const MacAddress& assigned_mac,
                  bool trusted = false);
    Status remove_vf(VfId vf_id);

    VfGuard* find_vf(VfId vf_id) noexcept;
    const VfGuard* find_vf(VfId vf_id) const noexcept;

    Status handle_mailbox(VfId vf_id, const MailboxRequest& request);
    TxResult check_tx_frame(VfId vf_id, const std::uint8_t* frame,
                            std::size_t length) const noexcept;
    TxResult check_tx_frame(VfId vf_id,
                            const std::vector<std::uint8_t>& frame) const
        noexcept;

private:
    std::unordered_map<VfId, VfGuard> vfs_;
};

}  // namespace netsec
