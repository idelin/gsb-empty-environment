#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <mutex>
#include <utility>
#include <vector>

namespace netsec {
namespace {

constexpr std::size_t kIpv4Bytes = 4;
constexpr std::size_t kIpv6Bytes = 16;
constexpr std::size_t kIpv6Groups = 8;

bool parse_ipv4(std::string_view text, std::array<std::uint8_t, 4>& bytes) {
    std::size_t position = 0;
    for (std::size_t part = 0; part < 4; ++part) {
        const std::size_t begin = position;
        unsigned value = 0;
        while (position < text.size() &&
               std::isdigit(static_cast<unsigned char>(text[position])) != 0) {
            if (position - begin >= 3) {
                return false;
            }
            value = value * 10U +
                    static_cast<unsigned>(text[position] - U'0');
            if (value > 255U) {
                return false;
            }
            ++position;
        }
        if (position == begin) {
            return false;
        }
        bytes[part] = static_cast<std::uint8_t>(value);
        if (part != 3) {
            if (position >= text.size() || text[position] != U'.') {
                return false;
            }
            ++position;
        }
    }
    return position == text.size();
}

bool parse_hex_group(std::string_view token, std::uint16_t& group) {
    if (token.empty() || token.size() > 4) {
        return false;
    }
    unsigned value = 0;
    for (const char character : token) {
        unsigned digit = 0;
        if (character >= '0' && character <= '9') {
            digit = static_cast<unsigned>(character - '0');
        } else if (character >= 'a' && character <= 'f') {
            digit = static_cast<unsigned>(character - 'a') + 10U;
        } else if (character >= 'A' && character <= 'F') {
            digit = static_cast<unsigned>(character - 'A') + 10U;
        } else {
            return false;
        }
        value = value * 16U + digit;
    }
    group = static_cast<std::uint16_t>(value);
    return true;
}

bool parse_ipv6_side(std::string_view text, bool allow_ipv4_tail,
                     std::vector<std::uint16_t>& groups) {
    if (text.empty()) {
        return true;
    }

    std::size_t position = 0;
    while (position <= text.size()) {
        const std::size_t colon = text.find(U':', position);
        const std::size_t end =
            colon == std::string_view::npos ? text.size() : colon;
        const std::string_view token = text.substr(position, end - position);
        if (token.empty()) {
            return false;
        }

        if (token.find(U'.') != std::string_view::npos) {
            if (!allow_ipv4_tail || end != text.size()) {
                return false;
            }
            std::array<std::uint8_t, 4> bytes{};
            if (!parse_ipv4(token, bytes)) {
                return false;
            }
            groups.push_back(static_cast<std::uint16_t>(
                (static_cast<std::uint16_t>(bytes[0]) << 8U) |
                static_cast<std::uint16_t>(bytes[1])));
            groups.push_back(static_cast<std::uint16_t>(
                (static_cast<std::uint16_t>(bytes[2]) << 8U) |
                static_cast<std::uint16_t>(bytes[3])));
        } else {
            std::uint16_t group = 0;
            if (!parse_hex_group(token, group)) {
                return false;
            }
            groups.push_back(group);
        }

        if (end == text.size()) {
            break;
        }
        position = end + 1;
    }
    return true;
}

bool parse_ipv6(std::string_view text,
                std::array<std::uint8_t, 16>& bytes) {
    const std::size_t compression = text.find("::");
    if (compression != std::string_view::npos &&
        text.find("::", compression + 2) != std::string_view::npos) {
        return false;
    }

    const bool compressed = compression != std::string_view::npos;
    const std::string_view left =
        compressed ? text.substr(0, compression) : text;
    const std::string_view right =
        compressed ? text.substr(compression + 2) : std::string_view{};

    std::vector<std::uint16_t> left_groups;
    std::vector<std::uint16_t> right_groups;
    const bool ipv4_in_left = right.empty();
    if (!parse_ipv6_side(left, ipv4_in_left, left_groups) ||
        !parse_ipv6_side(right, true, right_groups)) {
        return false;
    }

    if (!compressed && left_groups.size() != kIpv6Groups) {
        return false;
    }
    if (compressed &&
        (left_groups.size() + right_groups.size() >= kIpv6Groups)) {
        return false;
    }

    std::array<std::uint16_t, kIpv6Groups> groups{};
    std::copy(left_groups.begin(), left_groups.end(), groups.begin());
    std::copy(right_groups.begin(), right_groups.end(),
              groups.end() - static_cast<std::ptrdiff_t>(right_groups.size()));

    for (std::size_t index = 0; index < groups.size(); ++index) {
        const std::uint16_t group = groups[index];
        bytes[index * 2] = static_cast<std::uint8_t>(group >> 8U);
        bytes[index * 2 + 1] = static_cast<std::uint8_t>(group & 0xffU);
    }
    return true;
}

std::string format_ipv4(const std::array<std::uint8_t, 16>& bytes) {
    std::string result;
    for (std::size_t index = 0; index < kIpv4Bytes; ++index) {
        if (index != 0) {
            result.push_back('.');
        }
        result += std::to_string(bytes[index]);
    }
    return result;
}

std::string format_hex_group(std::uint16_t group) {
    char buffer[4]{};
    std::size_t length = 0;
    do {
        const std::uint16_t nibble = group & 0x0fU;
        buffer[length++] = static_cast<char>(
            nibble < 10U ? U'0' + nibble : U'a' + nibble - 10U);
        group >>= 4U;
    } while (group != 0);
    std::reverse(buffer, buffer + static_cast<std::ptrdiff_t>(length));
    return std::string(buffer, length);
}

std::string format_ipv6(const std::array<std::uint8_t, 16>& bytes) {
    std::array<std::uint16_t, kIpv6Groups> groups{};
    for (std::size_t index = 0; index < groups.size(); ++index) {
        groups[index] =
            static_cast<std::uint16_t>(
                (static_cast<std::uint16_t>(bytes[index * 2]) << 8U) |
                static_cast<std::uint16_t>(bytes[index * 2 + 1]));
    }

    std::size_t best_start = 0;
    std::size_t best_length = 0;
    for (std::size_t index = 0; index < groups.size();) {
        if (groups[index] != 0) {
            ++index;
            continue;
        }
        const std::size_t begin = index;
        while (index < groups.size() && groups[index] == 0) {
            ++index;
        }
        const std::size_t length = index - begin;
        if (length >= 2 && length > best_length) {
            best_start = begin;
            best_length = length;
        }
    }

    std::string result;
    for (std::size_t index = 0; index < groups.size();) {
        if (best_length != 0 && index == best_start) {
            result += "::";
            index += best_length;
            continue;
        }
        if (!result.empty() && result.back() != U':') {
            result.push_back(':');
        }
        result += format_hex_group(groups[index]);
        ++index;
    }
    return result;
}

template <class T>
bool merge_identity(T primary, T alias1, T alias2, T& result) {
    result = 0;
    for (const T candidate : {primary, alias1, alias2}) {
        if (candidate == 0) {
            continue;
        }
        if (result != 0 && result != candidate) {
            return false;
        }
        result = candidate;
    }
    return result != 0;
}

bool resolve_endpoint(Teid flat_teid, const IpAddress& flat_address,
                      const TunnelEndpoint& endpoint, TunnelEndpoint& result) {
    result = {};

    if (flat_teid != 0 && endpoint.teid != 0 && flat_teid != endpoint.teid) {
        return false;
    }
    result.teid = endpoint.teid != 0 ? endpoint.teid : flat_teid;
    if (result.teid == 0) {
        return false;
    }

    if (endpoint.peer_address.valid() && endpoint.address.valid() &&
        endpoint.peer_address != endpoint.address) {
        return false;
    }

    IpAddress endpoint_address = endpoint.peer_address.valid()
                                     ? endpoint.peer_address
                                     : endpoint.address;
    if (flat_address.valid() && endpoint_address.valid() &&
        flat_address != endpoint_address) {
        return false;
    }

    const IpAddress effective_address =
        flat_address.valid() ? flat_address : endpoint_address;
    if (!effective_address.valid() || effective_address.is_unspecified()) {
        return false;
    }
    result.peer_address = effective_address;
    result.address = effective_address;
    return true;
}

bool is_valid_direction(Direction direction) {
    return direction == Direction::Uplink || direction == Direction::Downlink;
}

}  // namespace

IpAddress::IpAddress(std::string_view text) { *this = parse(text); }

IpAddress::IpAddress(const char* text)
    : IpAddress(text == nullptr ? std::string_view{} : std::string_view{text}) {}

IpAddress::IpAddress(std::uint32_t ipv4_host_order) noexcept
    : family_(AddressFamily::IPv4) {
    bytes_[0] = static_cast<std::uint8_t>(ipv4_host_order >> 24U);
    bytes_[1] = static_cast<std::uint8_t>(ipv4_host_order >> 16U);
    bytes_[2] = static_cast<std::uint8_t>(ipv4_host_order >> 8U);
    bytes_[3] = static_cast<std::uint8_t>(ipv4_host_order);
}

IpAddress IpAddress::parse(std::string_view text) {
    IpAddress address;
    if (text.empty()) {
        return address;
    }

    std::array<std::uint8_t, 4> ipv4{};
    if (parse_ipv4(text, ipv4)) {
        address.family_ = AddressFamily::IPv4;
        std::copy(ipv4.begin(), ipv4.end(), address.bytes_.begin());
        return address;
    }

    std::array<std::uint8_t, 16> ipv6{};
    if (parse_ipv6(text, ipv6)) {
        address.family_ = AddressFamily::IPv6;
        address.bytes_ = ipv6;
    }
    return address;
}

IpAddress IpAddress::from_string(std::string_view text) { return parse(text); }

IpAddress IpAddress::ipv4(std::uint32_t host_order) noexcept {
    return IpAddress(host_order);
}

bool IpAddress::valid() const noexcept {
    return family_ != AddressFamily::Invalid;
}

bool IpAddress::is_ipv4() const noexcept {
    return family_ == AddressFamily::IPv4;
}

bool IpAddress::is_ipv6() const noexcept {
    return family_ == AddressFamily::IPv6;
}

bool IpAddress::is_unspecified() const noexcept {
    if (!valid()) {
        return false;
    }
    const std::size_t length =
        family_ == AddressFamily::IPv4 ? kIpv4Bytes : kIpv6Bytes;
    return std::all_of(bytes_.begin(),
                       bytes_.begin() + static_cast<std::ptrdiff_t>(length),
                       [](std::uint8_t byte) { return byte == 0; });
}

AddressFamily IpAddress::family() const noexcept { return family_; }

std::string IpAddress::to_string() const {
    switch (family_) {
        case AddressFamily::IPv4:
            return format_ipv4(bytes_);
        case AddressFamily::IPv6:
            return format_ipv6(bytes_);
        case AddressFamily::Invalid:
            break;
    }
    return {};
}

std::ostream& operator<<(std::ostream& out, const IpAddress& address) {
    return out << address.to_string();
}

std::string_view to_string(DropReason reason) noexcept {
    switch (reason) {
        case DropReason::None:
            return "none";
        case DropReason::InvalidTeid:
            return "invalid-teid";
        case DropReason::UnknownTeid:
            return "unknown-teid";
        case DropReason::ConflictTeid:
            return "conflict-teid";
        case DropReason::AddressMismatch:
            return "address-mismatch";
        case DropReason::InactiveSession:
            return "inactive-session";
        case DropReason::InvalidSession:
            return "invalid-session";
    }
    return "invalid-session";
}

TunnelMap::ResolvedConfig TunnelMap::resolve_config(
    const SessionConfig& config) {
    ResolvedConfig resolved;
    UserId user_id = 0;
    if (!merge_identity(config.user_id, config.subscriber_id, UserId{},
                        user_id) ||
        !merge_identity(user_id, config.ue_id, UserId{}, resolved.user_id) ||
        !merge_identity(config.bearer_id, BearerId{}, BearerId{},
                        resolved.bearer_id) ||
        !merge_identity(config.control_session_id, config.session_id,
                        config.cp_session_id, resolved.control_session_id)) {
        return resolved;
    }

    if (!resolve_endpoint(config.uplink_teid, config.uplink_address,
                          config.uplink, resolved.uplink) ||
        !resolve_endpoint(config.downlink_teid, config.downlink_address,
                          config.downlink, resolved.downlink)) {
        return resolved;
    }

    resolved.valid = true;
    return resolved;
}

const TunnelEndpoint& TunnelMap::endpoint_for(const SessionRecord& session,
                                              Direction direction) {
    return direction == Direction::Uplink ? session.uplink : session.downlink;
}

DropReason TunnelMap::validate_direction(Direction direction) {
    return is_valid_direction(direction) ? DropReason::None
                                         : DropReason::InvalidSession;
}

TunnelMap::Slot* TunnelMap::find_slot(
    std::unordered_map<Teid, Slot>& slots, Teid teid) {
    const auto iterator = slots.find(teid);
    return iterator == slots.end() ? nullptr : &iterator->second;
}

const TunnelMap::Slot* TunnelMap::find_slot(
    const std::unordered_map<Teid, Slot>& slots, Teid teid) {
    const auto iterator = slots.find(teid);
    return iterator == slots.end() ? nullptr : &iterator->second;
}

void TunnelMap::mark_conflict(std::unordered_map<Teid, Slot>& slots,
                              Teid teid) {
    auto [iterator, inserted] = slots.try_emplace(teid);
    (void)inserted;
    iterator->second.state = SlotState::Conflicted;
}

void TunnelMap::release_slot(std::unordered_map<Teid, Slot>& slots, Teid teid,
                             const SessionHandle& owner) {
    const auto iterator = slots.find(teid);
    if (iterator == slots.end()) {
        return;
    }
    if (iterator->second.owner == owner) {
        iterator->second.state = SlotState::Free;
        iterator->second.owner = SessionHandle{};
    }
}

std::optional<SessionHandle> TunnelMap::create_session(
    const SessionConfig& config) {
    const ResolvedConfig resolved = resolve_config(config);
    if (!resolved.valid) {
        return std::nullopt;
    }

    std::unique_lock lock(mutex_);
    if (sessions_by_control_.contains(resolved.control_session_id)) {
        return std::nullopt;
    }

    bool conflict = false;
    for (const auto& [slots, teid] :
         {std::pair{&uplink_slots_, resolved.uplink.teid},
          std::pair{&downlink_slots_, resolved.downlink.teid}}) {
        const Slot* slot = find_slot(*slots, teid);
        if (slot != nullptr && slot->state != SlotState::Free) {
            mark_conflict(*slots, teid);
            conflict = true;
        }
    }
    if (conflict) {
        return std::nullopt;
    }

    std::uint64_t value = next_handle_++;
    if (value == 0) {
        value = next_handle_++;
    }
    const SessionHandle handle(value);

    SessionRecord record;
    record.handle = handle;
    record.user_id = resolved.user_id;
    record.bearer_id = resolved.bearer_id;
    record.control_session_id = resolved.control_session_id;
    record.uplink = resolved.uplink;
    record.downlink = resolved.downlink;

    sessions_.emplace(handle.value(), record);
    sessions_by_control_.emplace(resolved.control_session_id, handle);
    uplink_slots_[resolved.uplink.teid] = Slot{SlotState::Active, handle};
    downlink_slots_[resolved.downlink.teid] = Slot{SlotState::Active, handle};
    return handle;
}

std::optional<SessionHandle> TunnelMap::create_session(
    ControlSessionId control_session_id, UserId user_id, BearerId bearer_id,
    Teid uplink_teid, const IpAddress& uplink_address, Teid downlink_teid,
    const IpAddress& downlink_address) {
    SessionConfig config;
    config.control_session_id = control_session_id;
    config.user_id = user_id;
    config.bearer_id = bearer_id;
    config.uplink_teid = uplink_teid;
    config.uplink_address = uplink_address;
    config.downlink_teid = downlink_teid;
    config.downlink_address = downlink_address;
    return create_session(config);
}

bool TunnelMap::release_session(const SessionHandle& handle) {
    if (!handle.valid()) {
        return false;
    }

    std::unique_lock lock(mutex_);
    const auto iterator = sessions_.find(handle.value());
    if (iterator == sessions_.end()) {
        return false;
    }

    const SessionRecord record = iterator->second;
    release_slot(uplink_slots_, record.uplink.teid, handle);
    release_slot(downlink_slots_, record.downlink.teid, handle);

    const auto control = sessions_by_control_.find(record.control_session_id);
    if (control != sessions_by_control_.end() && control->second == handle) {
        sessions_by_control_.erase(control);
    }
    sessions_.erase(iterator);
    return true;
}

bool TunnelMap::release_session(ControlSessionId control_session_id) {
    SessionHandle handle;
    {
        std::shared_lock lock(mutex_);
        const auto iterator = sessions_by_control_.find(control_session_id);
        if (iterator == sessions_by_control_.end()) {
            return false;
        }
        handle = iterator->second;
    }
    return release_session(handle);
}

LookupResult TunnelMap::validate(Direction direction, Teid teid,
                                 const IpAddress& peer_address) const {
    LookupResult result;
    if (teid == 0) {
        result.reason = DropReason::InvalidTeid;
        return result;
    }
    const DropReason direction_error = validate_direction(direction);
    if (direction_error != DropReason::None) {
        result.reason = direction_error;
        return result;
    }
    if (!peer_address.valid()) {
        result.reason = DropReason::AddressMismatch;
        return result;
    }

    std::shared_lock lock(mutex_);
    const auto& slots =
        direction == Direction::Uplink ? uplink_slots_ : downlink_slots_;
    const Slot* slot = find_slot(slots, teid);
    if (slot == nullptr || slot->state == SlotState::Free) {
        result.reason = DropReason::UnknownTeid;
        return result;
    }
    if (slot->state == SlotState::Conflicted) {
        result.reason = DropReason::ConflictTeid;
        return result;
    }

    const auto session = sessions_.find(slot->owner.value());
    if (session == sessions_.end()) {
        result.reason = DropReason::InactiveSession;
        return result;
    }
    if (slot->owner != session->second.handle) {
        result.reason = DropReason::ConflictTeid;
        return result;
    }

    const TunnelEndpoint& expected =
        endpoint_for(session->second, direction);
    if (expected.teid != teid) {
        result.reason = DropReason::ConflictTeid;
        return result;
    }
    if (peer_address != expected.peer_address) {
        result.reason = DropReason::AddressMismatch;
        return result;
    }

    Route route;
    route.user_id = session->second.user_id;
    route.bearer_id = session->second.bearer_id;
    route.control_session_id = session->second.control_session_id;
    route.session = session->second.handle;
    route.direction = direction;
    route.teid = teid;
    route.peer_address = expected.peer_address;
    result.route = route;
    result.reason = DropReason::None;
    return result;
}

LookupResult TunnelMap::forward(const Packet& packet) const {
    IpAddress peer_address;
    if (packet.direction == Direction::Uplink) {
        peer_address = packet.source_address.valid()
                           ? packet.source_address
                           : (packet.peer_address.valid() ? packet.peer_address
                                                          : packet.address);
    } else if (packet.direction == Direction::Downlink) {
        peer_address = packet.destination_address.valid()
                           ? packet.destination_address
                           : (packet.peer_address.valid() ? packet.peer_address
                                                          : packet.address);
    }
    return validate(packet.direction, packet.teid, peer_address);
}

std::optional<Route> TunnelMap::lookup_session(
    const SessionHandle& handle) const {
    if (!handle.valid()) {
        return std::nullopt;
    }
    std::shared_lock lock(mutex_);
    const auto iterator = sessions_.find(handle.value());
    if (iterator == sessions_.end()) {
        return std::nullopt;
    }

    const SessionRecord& record = iterator->second;
    Route route;
    route.user_id = record.user_id;
    route.bearer_id = record.bearer_id;
    route.control_session_id = record.control_session_id;
    route.session = record.handle;
    route.direction = Direction::Uplink;
    route.teid = record.uplink.teid;
    route.peer_address = record.uplink.peer_address;
    return route;
}

std::optional<SessionHandle> TunnelMap::find_session(
    ControlSessionId control_session_id) const {
    std::shared_lock lock(mutex_);
    const auto iterator = sessions_by_control_.find(control_session_id);
    if (iterator == sessions_by_control_.end()) {
        return std::nullopt;
    }
    return iterator->second;
}

bool TunnelMap::is_active(const SessionHandle& handle) const {
    if (!handle.valid()) {
        return false;
    }
    std::shared_lock lock(mutex_);
    return sessions_.contains(handle.value());
}

bool TunnelMap::is_teid_active(Direction direction, Teid teid) const {
    std::shared_lock lock(mutex_);
    const auto& slots =
        direction == Direction::Uplink ? uplink_slots_ : downlink_slots_;
    const Slot* slot = find_slot(slots, teid);
    return slot != nullptr && slot->state == SlotState::Active;
}

bool TunnelMap::is_teid_conflicted(Direction direction, Teid teid) const {
    std::shared_lock lock(mutex_);
    const auto& slots =
        direction == Direction::Uplink ? uplink_slots_ : downlink_slots_;
    const Slot* slot = find_slot(slots, teid);
    return slot != nullptr && slot->state == SlotState::Conflicted;
}

std::size_t TunnelMap::session_count() const {
    std::shared_lock lock(mutex_);
    return sessions_.size();
}

void TunnelMap::clear() {
    std::unique_lock lock(mutex_);
    uplink_slots_.clear();
    downlink_slots_.clear();
    sessions_.clear();
    sessions_by_control_.clear();
    next_handle_ = 1;
}

}  // namespace netsec
