#include "netsec.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>

namespace netsec {
namespace {

int hex_value(char value) noexcept {
    if (value >= '0' && value <= '9') {
        return value - '0';
    }
    if (value >= 'a' && value <= 'f') {
        return value - 'a' + 10;
    }
    if (value >= 'A' && value <= 'F') {
        return value - 'A' + 10;
    }
    return -1;
}

std::uint16_t read_u16(const std::uint8_t* data) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(data[0]) << 8) |
        static_cast<std::uint16_t>(data[1]));
}

bool is_vlan_tpid(std::uint16_t ether_type) noexcept {
    switch (ether_type) {
        case 0x8100:  // IEEE 802.1Q
        case 0x88A8:  // IEEE 802.1ad
        case 0x9100:  // legacy Q-in-Q
        case 0x9200:  // legacy Q-in-Q
        case 0x9300:  // legacy Q-in-Q
            return true;
        default:
            return false;
    }
}

TxResult drop(TxDropReason reason) noexcept {
    return TxResult{false, reason};
}

TxResult accept() noexcept {
    return TxResult{true, TxDropReason::None};
}

}  // namespace

std::optional<MacAddress> MacAddress::parse(std::string_view text) {
    if (text.size() != 17) {
        return std::nullopt;
    }

    const char separator = text[2];
    if (separator != ':' && separator != '-') {
        return std::nullopt;
    }

    std::array<std::uint8_t, kMacOctets> octets{};
    for (std::size_t i = 0; i < kMacOctets; ++i) {
        const std::size_t offset = i * 3;
        const int high = hex_value(text[offset]);
        const int low = hex_value(text[offset + 1]);
        if (high < 0 || low < 0) {
            return std::nullopt;
        }
        if (i + 1 < kMacOctets && text[offset + 2] != separator) {
            return std::nullopt;
        }
        octets[i] = static_cast<std::uint8_t>((high << 4) | low);
    }

    return MacAddress(octets);
}

std::string MacAddress::to_string() const {
    std::string result;
    result.reserve(17);
    const char hex[] = "0123456789ABCDEF";

    for (std::size_t i = 0; i < kMacOctets; ++i) {
        if (i != 0) {
            result.push_back(':');
        }
        result.push_back(hex[(octets[i] >> 4) & 0x0F]);
        result.push_back(hex[octets[i] & 0x0F]);
    }
    return result;
}

std::size_t MacAddressHash::operator()(const MacAddress& mac) const noexcept {
    std::size_t hash = 0;
    for (const std::uint8_t octet : mac.octets) {
        hash ^= static_cast<std::size_t>(octet) +
                static_cast<std::size_t>(0x9e3779b9U) + (hash << 6) +
                (hash >> 2);
    }
    return hash;
}

const char* status_name(Status status) noexcept {
    switch (status) {
        case Status::Ok:
            return "Ok";
        case Status::InvalidArgument:
            return "InvalidArgument";
        case Status::PermissionDenied:
            return "PermissionDenied";
        case Status::NotTrusted:
            return "NotTrusted";
        case Status::NotFound:
            return "NotFound";
        case Status::AlreadyExists:
            return "AlreadyExists";
    }
    return "Unknown";
}

MailboxRequest MailboxRequest::set_mac(const MacAddress& mac) {
    MailboxRequest request;
    request.type = MailboxRequestType::SetMac;
    request.mac = mac;
    return request;
}

MailboxRequest MailboxRequest::join_vlan(VlanId vlan_id) {
    MailboxRequest request;
    request.type = MailboxRequestType::JoinVlan;
    request.vlan_id = vlan_id;
    request.enabled = true;
    return request;
}

MailboxRequest MailboxRequest::leave_vlan(VlanId vlan_id) {
    MailboxRequest request;
    request.type = MailboxRequestType::LeaveVlan;
    request.vlan_id = vlan_id;
    return request;
}

MailboxRequest MailboxRequest::set_promiscuous(bool enabled) {
    MailboxRequest request;
    request.type = MailboxRequestType::SetPromiscuous;
    request.enabled = enabled;
    return request;
}

const char* tx_drop_reason_name(TxDropReason reason) noexcept {
    switch (reason) {
        case TxDropReason::None:
            return "None";
        case TxDropReason::UnknownVf:
            return "UnknownVf";
        case TxDropReason::MalformedFrame:
            return "MalformedFrame";
        case TxDropReason::PortVlanTagPresent:
            return "PortVlanTagPresent";
        case TxDropReason::SourceMacNotAllowed:
            return "SourceMacNotAllowed";
    }
    return "Unknown";
}

VfGuard::VfGuard(MacAddress assigned_mac, bool trusted)
    : assigned_mac_(assigned_mac), mac_locked_(true), trusted_(trusted) {}

Status VfGuard::admin_set_mac(const MacAddress& mac) {
    assigned_mac_ = mac;
    mac_locked_ = true;
    return Status::Ok;
}

Status VfGuard::admin_clear_mac() {
    assigned_mac_.reset();
    mac_locked_ = false;
    return Status::Ok;
}

Status VfGuard::admin_set_allowed_vlans(
    const std::vector<VlanId>& vlan_ids) {
    for (const VlanId vlan_id : vlan_ids) {
        if (!is_valid_vlan_id(vlan_id)) {
            return Status::InvalidArgument;
        }
    }

    std::unordered_set<VlanId> replacement;
    for (const VlanId vlan_id : vlan_ids) {
        replacement.insert(vlan_id);
    }
    allowed_vlans_.swap(replacement);

    for (auto it = joined_vlans_.begin(); it != joined_vlans_.end();) {
        if (allowed_vlans_.count(*it) == 0) {
            it = joined_vlans_.erase(it);
        } else {
            ++it;
        }
    }

    return Status::Ok;
}

Status VfGuard::admin_allow_vlan(VlanId vlan_id) {
    if (!is_valid_vlan_id(vlan_id)) {
        return Status::InvalidArgument;
    }
    allowed_vlans_.insert(vlan_id);
    return Status::Ok;
}

Status VfGuard::admin_disallow_vlan(VlanId vlan_id) {
    if (!is_valid_vlan_id(vlan_id)) {
        return Status::InvalidArgument;
    }
    allowed_vlans_.erase(vlan_id);
    joined_vlans_.erase(vlan_id);
    return Status::Ok;
}

Status VfGuard::admin_set_allowed_source_macs(
    const std::vector<MacAddress>& macs) {
    std::unordered_set<MacAddress, MacAddressHash> replacement;
    for (const MacAddress& mac : macs) {
        replacement.insert(mac);
    }
    allowed_source_macs_.swap(replacement);
    return Status::Ok;
}

Status VfGuard::admin_allow_source_mac(const MacAddress& mac) {
    allowed_source_macs_.insert(mac);
    return Status::Ok;
}

Status VfGuard::admin_disallow_source_mac(const MacAddress& mac) {
    allowed_source_macs_.erase(mac);
    return Status::Ok;
}

void VfGuard::admin_set_port_vlan_enabled(bool enabled) noexcept {
    port_vlan_enabled_ = enabled;
}

void VfGuard::admin_set_trusted(bool trusted) noexcept {
    trusted_ = trusted;
    if (!trusted_) {
        promiscuous_ = false;
    }
}

Status VfGuard::request_set_mac(const MacAddress& mac) {
    if (mac_locked_) {
        return Status::PermissionDenied;
    }
    assigned_mac_ = mac;
    return Status::Ok;
}

Status VfGuard::request_join_vlan(VlanId vlan_id) {
    if (!is_valid_vlan_id(vlan_id)) {
        return Status::InvalidArgument;
    }
    if (allowed_vlans_.count(vlan_id) == 0) {
        return Status::PermissionDenied;
    }
    joined_vlans_.insert(vlan_id);
    return Status::Ok;
}

Status VfGuard::request_leave_vlan(VlanId vlan_id) {
    if (!is_valid_vlan_id(vlan_id)) {
        return Status::InvalidArgument;
    }
    joined_vlans_.erase(vlan_id);
    return Status::Ok;
}

Status VfGuard::request_set_promiscuous(bool enabled) {
    if (!enabled) {
        promiscuous_ = false;
        return Status::Ok;
    }
    if (!trusted_) {
        promiscuous_ = false;
        return Status::NotTrusted;
    }
    promiscuous_ = true;
    return Status::Ok;
}

Status VfGuard::handle_mailbox(const MailboxRequest& request) {
    switch (request.type) {
        case MailboxRequestType::SetMac:
            return request_set_mac(request.mac);
        case MailboxRequestType::JoinVlan:
            return request_join_vlan(request.vlan_id);
        case MailboxRequestType::LeaveVlan:
            return request_leave_vlan(request.vlan_id);
        case MailboxRequestType::SetPromiscuous:
            return request_set_promiscuous(request.enabled);
        case MailboxRequestType::Invalid:
            return Status::InvalidArgument;
    }
    return Status::InvalidArgument;
}

TxResult VfGuard::check_tx_frame(const std::uint8_t* frame,
                                 std::size_t length) const noexcept {
    if (frame == nullptr || length < 14) {
        return drop(TxDropReason::MalformedFrame);
    }

    const std::uint16_t ether_type = read_u16(frame + 12);
    if (port_vlan_enabled_ && is_vlan_tpid(ether_type)) {
        return drop(TxDropReason::PortVlanTagPresent);
    }

    std::size_t type_offset = 12;
    std::size_t tag_depth = 0;
    while (is_vlan_tpid(read_u16(frame + type_offset))) {
        ++tag_depth;
        if (tag_depth > kMaxVlanTagDepth) {
            return drop(TxDropReason::MalformedFrame);
        }

        // A VLAN header is TPID, TCI, and a following EtherType. If the
        // following EtherType is absent, the tag stack is malformed.
        if (length - type_offset < 6) {
            return drop(TxDropReason::MalformedFrame);
        }
        type_offset += 4;
    }

    const MacAddress source{frame[6], frame[7], frame[8],
                            frame[9], frame[10], frame[11]};
    if (!is_source_mac_allowed(source)) {
        return drop(TxDropReason::SourceMacNotAllowed);
    }

    return accept();
}

TxResult VfGuard::check_tx_frame(
    const std::vector<std::uint8_t>& frame) const noexcept {
    return check_tx_frame(frame.data(), frame.size());
}

bool VfGuard::is_vlan_allowed(VlanId vlan_id) const noexcept {
    return allowed_vlans_.count(vlan_id) != 0;
}

bool VfGuard::is_vlan_joined(VlanId vlan_id) const noexcept {
    return joined_vlans_.count(vlan_id) != 0;
}

bool VfGuard::is_source_mac_allowed(const MacAddress& mac) const noexcept {
    if (assigned_mac_ && *assigned_mac_ == mac) {
        return true;
    }
    return allowed_source_macs_.count(mac) != 0;
}

std::vector<VlanId> VfGuard::allowed_vlans() const {
    std::vector<VlanId> result(allowed_vlans_.begin(), allowed_vlans_.end());
    std::sort(result.begin(), result.end());
    return result;
}

std::vector<VlanId> VfGuard::joined_vlans() const {
    std::vector<VlanId> result(joined_vlans_.begin(), joined_vlans_.end());
    std::sort(result.begin(), result.end());
    return result;
}

std::vector<MacAddress> VfGuard::allowed_source_macs() const {
    std::vector<MacAddress> result(allowed_source_macs_.begin(),
                                   allowed_source_macs_.end());
    std::sort(result.begin(), result.end(),
              [](const MacAddress& lhs, const MacAddress& rhs) {
                  return lhs.to_string() < rhs.to_string();
              });
    return result;
}

Status VfGuardManager::add_vf(VfId vf_id, const MacAddress& assigned_mac,
                              bool trusted) {
    if (vfs_.count(vf_id) != 0) {
        return Status::AlreadyExists;
    }
    vfs_.emplace(vf_id, VfGuard(assigned_mac, trusted));
    return Status::Ok;
}

Status VfGuardManager::remove_vf(VfId vf_id) {
    const auto it = vfs_.find(vf_id);
    if (it == vfs_.end()) {
        return Status::NotFound;
    }
    vfs_.erase(it);
    return Status::Ok;
}

VfGuard* VfGuardManager::find_vf(VfId vf_id) noexcept {
    const auto it = vfs_.find(vf_id);
    return it == vfs_.end() ? nullptr : &it->second;
}

const VfGuard* VfGuardManager::find_vf(VfId vf_id) const noexcept {
    const auto it = vfs_.find(vf_id);
    return it == vfs_.end() ? nullptr : &it->second;
}

Status VfGuardManager::handle_mailbox(VfId vf_id,
                                      const MailboxRequest& request) {
    VfGuard* guard = find_vf(vf_id);
    if (guard == nullptr) {
        return Status::NotFound;
    }
    return guard->handle_mailbox(request);
}

TxResult VfGuardManager::check_tx_frame(VfId vf_id, const std::uint8_t* frame,
                                        std::size_t length) const noexcept {
    const VfGuard* guard = find_vf(vf_id);
    if (guard == nullptr) {
        return drop(TxDropReason::UnknownVf);
    }
    return guard->check_tx_frame(frame, length);
}

TxResult VfGuardManager::check_tx_frame(
    VfId vf_id, const std::vector<std::uint8_t>& frame) const noexcept {
    return check_tx_frame(vf_id, frame.data(), frame.size());
}

}  // namespace netsec
