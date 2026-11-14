#include "netsec.hpp"

#include <algorithm>
#include <charconv>
#include <limits>
#include <system_error>
#include <utility>

namespace netsec {
namespace {

constexpr char kHexDigits[] = "0123456789abcdef";

std::optional<std::uint8_t> parse_hex_byte(char high, char low) noexcept {
    const auto nibble = [](char value) -> std::optional<std::uint8_t> {
        if (value >= '0' && value <= '9') {
            return static_cast<std::uint8_t>(value - '0');
        }
        if (value >= 'a' && value <= 'f') {
            return static_cast<std::uint8_t>(value - 'a' + 10);
        }
        if (value >= 'A' && value <= 'F') {
            return static_cast<std::uint8_t>(value - 'A' + 10);
        }
        return std::nullopt;
    };

    const auto upper = nibble(high);
    const auto lower = nibble(low);
    if (!upper || !lower) {
        return std::nullopt;
    }
    return static_cast<std::uint8_t>((*upper << 4U) | *lower);
}

std::size_t hash_bytes(const std::uint8_t* data, std::size_t size) noexcept {
    // FNV-1a. Addresses are untrusted input, so use a fixed-time hash.
    std::size_t result = sizeof(std::size_t) == 8
                             ? static_cast<std::size_t>(1469598103934665603ULL)
                             : static_cast<std::size_t>(2166136261U);
    const std::size_t prime = sizeof(std::size_t) == 8
                                  ? static_cast<std::size_t>(1099511628211ULL)
                                  : static_cast<std::size_t>(16777619U);
    for (std::size_t i = 0; i < size; ++i) {
        result ^= data[i];
        result *= prime;
    }
    return result;
}

}  // namespace

std::optional<Ipv4Address> Ipv4Address::parse(std::string_view text) noexcept {
    Ipv4Address result{};
    std::size_t cursor = 0;

    for (std::size_t part = 0; part < result.octets.size(); ++part) {
        const std::size_t dot = text.find('.', cursor);
        const bool is_last = part + 1 == result.octets.size();
        if ((is_last && dot != std::string_view::npos) ||
            (!is_last && dot == std::string_view::npos)) {
            return std::nullopt;
        }

        const std::size_t end = is_last ? text.size() : dot;
        if (end <= cursor) {
            return std::nullopt;
        }

        unsigned int value = 0;
        const auto [ptr, error] =
            std::from_chars(text.data() + cursor, text.data() + end, value);
        if (error != std::errc{} || ptr != text.data() + end || value > 255U) {
            return std::nullopt;
        }
        result.octets[part] = static_cast<std::uint8_t>(value);
        cursor = end + 1;
    }

    return result;
}

bool Ipv4Address::is_unspecified() const noexcept {
    return std::all_of(octets.begin(), octets.end(),
                       [](std::uint8_t value) { return value == 0; });
}

bool Ipv4Address::is_multicast() const noexcept {
    return (octets[0] & 0xF0U) == 0xE0U;
}

std::string Ipv4Address::to_string() const {
    std::string result;
    for (std::size_t i = 0; i < octets.size(); ++i) {
        if (i != 0) {
            result.push_back('.');
        }
        result += std::to_string(octets[i]);
    }
    return result;
}

std::optional<MacAddress> MacAddress::parse(std::string_view text) noexcept {
    if (text.size() != 17) {
        return std::nullopt;
    }

    const char delimiter = text[2];
    if ((delimiter != ':' && delimiter != '-') || text[5] != delimiter ||
        text[8] != delimiter || text[11] != delimiter ||
        text[14] != delimiter) {
        return std::nullopt;
    }

    MacAddress result{};
    for (std::size_t i = 0; i < result.octets.size(); ++i) {
        const std::size_t offset = i * 3;
        const auto value = parse_hex_byte(text[offset], text[offset + 1]);
        if (!value) {
            return std::nullopt;
        }
        result.octets[i] = *value;
    }
    return result;
}

bool MacAddress::is_zero() const noexcept {
    return std::all_of(octets.begin(), octets.end(),
                       [](std::uint8_t value) { return value == 0; });
}

bool MacAddress::is_multicast() const noexcept {
    return (octets[0] & 0x01U) != 0;
}

bool MacAddress::is_broadcast() const noexcept {
    return std::all_of(octets.begin(), octets.end(),
                       [](std::uint8_t value) { return value == 0xFFU; });
}

std::string MacAddress::to_string() const {
    std::string result;
    result.reserve(17);
    for (std::size_t i = 0; i < octets.size(); ++i) {
        if (i != 0) {
            result.push_back(':');
        }
        result.push_back(kHexDigits[octets[i] >> 4U]);
        result.push_back(kHexDigits[octets[i] & 0x0FU]);
    }
    return result;
}

std::string_view to_string(DropReason reason) noexcept {
    switch (reason) {
        case DropReason::None:
            return "none";
        case DropReason::UnknownVtep:
            return "unknown_vtep";
        case DropReason::VniMismatch:
            return "vni_mismatch";
        case DropReason::InvalidVni:
            return "invalid_vni";
        case DropReason::InnerMacConflict:
            return "inner_mac_conflict";
        case DropReason::Fragmented:
            return "fragmented";
        case DropReason::Oversized:
            return "oversized";
        case DropReason::AmplificationLimited:
            return "amplification_limited";
        case DropReason::TunnelEpochMismatch:
            return "tunnel_epoch_mismatch";
        case DropReason::MalformedInnerFrame:
            return "malformed_inner_frame";
    }
    return "unknown";
}

std::uint64_t AdmissionCounters::reason_count(DropReason reason) const noexcept {
    switch (reason) {
        case DropReason::None:
            return admitted;
        case DropReason::UnknownVtep:
            return unknown_vtep;
        case DropReason::VniMismatch:
            return vni_mismatch;
        case DropReason::InvalidVni:
            return invalid_vni;
        case DropReason::InnerMacConflict:
            return inner_mac_conflict;
        case DropReason::Fragmented:
            return fragmented;
        case DropReason::Oversized:
            return oversized;
        case DropReason::AmplificationLimited:
            return amplification_limited;
        case DropReason::TunnelEpochMismatch:
            return tunnel_epoch_mismatch;
        case DropReason::MalformedInnerFrame:
            return malformed_inner_frame;
    }
    return 0;
}

VtepAdmission::VtepAdmission(AdmissionConfig config)
    : config_(std::move(config)) {
    global_keys_.current = config_.initial_tunnel_epoch;
    global_keys_.previous = config_.initial_previous_tunnel_epoch;
}

bool VtepAdmission::register_vtep(const IpAddress& source, Vni vni,
                                  TenantId tenant) {
    return register_vtep(VtepRegistration{source, vni, tenant});
}

bool VtepAdmission::register_vtep(const VtepRegistration& registration) {
    std::lock_guard lock(mutex_);
    return register_vtep_locked(registration);
}

bool VtepAdmission::register_vtep_locked(
    const VtepRegistration& registration) {
    if (registration.source.is_unspecified() ||
        registration.source.is_multicast() || registration.vni > kMaxVni) {
        return false;
    }

    const auto owner = vni_tenant_.find(registration.vni);
    if (owner != vni_tenant_.end() && owner->second != registration.tenant) {
        return false;
    }

    auto& registrations = by_source_[registration.source];
    const auto existing =
        std::find_if(registrations.begin(), registrations.end(),
                     [&](const VtepRegistration& candidate) {
                         return candidate.vni == registration.vni;
                     });
    if (existing != registrations.end()) {
        return existing->tenant == registration.tenant;
    }

    registrations.push_back(registration);
    vni_tenant_[registration.vni] = registration.tenant;
    vni_sources_[registration.vni].insert(registration.source);

    if (tenant_keys_.find(registration.tenant) == tenant_keys_.end()) {
        tenant_keys_.emplace(registration.tenant, global_keys_);
    }
    return true;
}

bool VtepAdmission::unregister_vtep(const IpAddress& source, Vni vni) {
    std::lock_guard lock(mutex_);
    return unregister_vtep_locked(source, &vni);
}

bool VtepAdmission::unregister_vtep(const IpAddress& source) {
    std::lock_guard lock(mutex_);
    return unregister_vtep_locked(source, nullptr);
}

bool VtepAdmission::unregister_vtep_locked(const IpAddress& source,
                                           const Vni* vni) {
    const auto source_it = by_source_.find(source);
    if (source_it == by_source_.end()) {
        return false;
    }

    if (vni != nullptr) {
        auto& registrations = source_it->second;
        const auto registration =
            std::find_if(registrations.begin(), registrations.end(),
                         [&](const VtepRegistration& candidate) {
                             return candidate.vni == *vni;
                         });
        if (registration == registrations.end()) {
            return false;
        }

        const Vni removed_vni = registration->vni;
        registrations.erase(registration);
        remove_source_from_vni_locked(source, removed_vni);
        remove_mac_bindings_locked(source, removed_vni);
        if (registrations.empty()) {
            by_source_.erase(source_it);
            buckets_.erase(source);
        }
        return true;
    }

    const auto registrations = source_it->second;
    by_source_.erase(source_it);
    for (const auto& registration : registrations) {
        remove_source_from_vni_locked(source, registration.vni);
    }
    remove_mac_bindings_locked(source);
    buckets_.erase(source);
    return true;
}

void VtepAdmission::remove_source_from_vni_locked(const IpAddress& source,
                                                  Vni vni) {
    const auto sources = vni_sources_.find(vni);
    if (sources == vni_sources_.end()) {
        return;
    }

    sources->second.erase(source);
    if (!sources->second.empty()) {
        return;
    }

    vni_sources_.erase(sources);
    vni_tenant_.erase(vni);
    inner_mac_table_.erase(vni);
}

void VtepAdmission::remove_mac_bindings_locked(const IpAddress& source,
                                               Vni vni) {
    const auto table = inner_mac_table_.find(vni);
    if (table == inner_mac_table_.end()) {
        return;
    }

    auto& entries = table->second;
    for (auto it = entries.begin(); it != entries.end();) {
        if (it->second == source) {
            it = entries.erase(it);
        } else {
            ++it;
        }
    }
    if (entries.empty()) {
        inner_mac_table_.erase(table);
    }
}

void VtepAdmission::remove_mac_bindings_locked(const IpAddress& source) {
    for (auto table = inner_mac_table_.begin();
         table != inner_mac_table_.end();) {
        auto& entries = table->second;
        for (auto it = entries.begin(); it != entries.end();) {
            if (it->second == source) {
                it = entries.erase(it);
            } else {
                ++it;
            }
        }

        if (entries.empty()) {
            table = inner_mac_table_.erase(table);
        } else {
            ++table;
        }
    }
}

bool VtepAdmission::is_registered(const IpAddress& source, Vni vni) const {
    std::lock_guard lock(mutex_);
    const auto source_it = by_source_.find(source);
    if (source_it == by_source_.end()) {
        return false;
    }
    const auto tenant = vni_tenant_.find(vni);
    if (tenant == vni_tenant_.end()) {
        return false;
    }

    return std::any_of(
        source_it->second.begin(), source_it->second.end(),
        [&](const VtepRegistration& registration) {
            return registration.vni == vni &&
                   registration.tenant == tenant->second;
        });
}

std::optional<TenantId> VtepAdmission::tenant_for_vni(Vni vni) const {
    std::lock_guard lock(mutex_);
    const auto tenant = vni_tenant_.find(vni);
    if (tenant == vni_tenant_.end()) {
        return std::nullopt;
    }
    return tenant->second;
}

std::size_t VtepAdmission::registered_vtep_count(Vni vni) const {
    std::lock_guard lock(mutex_);
    const auto sources = vni_sources_.find(vni);
    return sources == vni_sources_.end() ? 0 : sources->second.size();
}

void VtepAdmission::rotate_tunnel_epoch(TenantId tenant, TunnelEpoch epoch,
                                        bool retain_previous) {
    std::lock_guard lock(mutex_);
    auto& ring = tenant_keys_[tenant];
    if (retain_previous) {
        ring.previous = ring.current;
    } else {
        ring.previous.reset();
    }
    ring.current = epoch;
}

void VtepAdmission::rotate_tunnel_epoch(TenantId tenant,
                                        TunnelGeneration generation,
                                        TunnelKey key, bool retain_previous) {
    rotate_tunnel_epoch(tenant, TunnelEpoch{generation, key},
                        retain_previous);
}

void VtepAdmission::rotate_global_tunnel_epoch(TunnelEpoch epoch,
                                               bool retain_previous) {
    std::lock_guard lock(mutex_);
    global_keys_.previous =
        retain_previous ? std::optional<TunnelEpoch>(global_keys_.current)
                        : std::nullopt;
    global_keys_.current = epoch;

    for (auto& [tenant, ring] : tenant_keys_) {
        (void)tenant;
        ring.previous =
            retain_previous ? std::optional<TunnelEpoch>(ring.current)
                            : std::nullopt;
        ring.current = epoch;
    }
}

void VtepAdmission::rotate_global_tunnel_epoch(TunnelGeneration generation,
                                               TunnelKey key,
                                               bool retain_previous) {
    rotate_global_tunnel_epoch(TunnelEpoch{generation, key}, retain_previous);
}

std::optional<TunnelEpoch> VtepAdmission::current_tunnel_epoch(
    TenantId tenant) const {
    std::lock_guard lock(mutex_);
    const auto ring = tenant_keys_.find(tenant);
    if (ring == tenant_keys_.end()) {
        return std::nullopt;
    }
    return ring->second.current;
}

const VtepAdmission::KeyRing* VtepAdmission::key_ring_locked(
    TenantId tenant) const {
    const auto ring = tenant_keys_.find(tenant);
    return ring == tenant_keys_.end() ? nullptr : &ring->second;
}

bool VtepAdmission::epoch_accepted_locked(const KeyRing& ring,
                                          TunnelEpoch epoch) const {
    if (ring.current == epoch) {
        return true;
    }
    return ring.previous && *ring.previous == epoch;
}

bool VtepAdmission::consume_bucket_locked(const IpAddress& source,
                                          std::size_t bytes,
                                          Clock::time_point now) {
    const auto& limits = config_.amplification;
    auto& bucket = buckets_[source];
    if (!bucket.initialized) {
        bucket.initialized = true;
        bucket.packet_tokens = static_cast<double>(limits.burst_packets);
        bucket.byte_tokens = static_cast<double>(limits.burst_bytes);
        bucket.last_refill = now;
    } else if (now > bucket.last_refill) {
        const double elapsed =
            std::chrono::duration<double>(now - bucket.last_refill).count();
        bucket.packet_tokens =
            std::min(static_cast<double>(limits.burst_packets),
                     bucket.packet_tokens +
                         elapsed * std::max(0.0, limits.packets_per_second));
        bucket.byte_tokens =
            std::min(static_cast<double>(limits.burst_bytes),
                     bucket.byte_tokens +
                         elapsed * std::max(0.0, limits.bytes_per_second));
        bucket.last_refill = now;
    }

    const double byte_cost =
        static_cast<double>(std::max<std::size_t>(bytes, 1));
    if (bucket.packet_tokens < 1.0 || bucket.byte_tokens < byte_cost) {
        return false;
    }

    bucket.packet_tokens -= 1.0;
    bucket.byte_tokens -= byte_cost;
    return true;
}

void VtepAdmission::record_drop_locked(DropReason reason) {
    ++counters_.dropped;
    switch (reason) {
        case DropReason::None:
            break;
        case DropReason::UnknownVtep:
            ++counters_.unknown_vtep;
            break;
        case DropReason::VniMismatch:
            ++counters_.vni_mismatch;
            break;
        case DropReason::InvalidVni:
            ++counters_.invalid_vni;
            break;
        case DropReason::InnerMacConflict:
            ++counters_.inner_mac_conflict;
            break;
        case DropReason::Fragmented:
            ++counters_.fragmented;
            break;
        case DropReason::Oversized:
            ++counters_.oversized;
            break;
        case DropReason::AmplificationLimited:
            ++counters_.amplification_limited;
            break;
        case DropReason::TunnelEpochMismatch:
            ++counters_.tunnel_epoch_mismatch;
            break;
        case DropReason::MalformedInnerFrame:
            ++counters_.malformed_inner_frame;
            break;
    }
}

AdmissionResult VtepAdmission::admit(const EncapsulatedFrame& frame) {
    return admit(frame, Clock::now());
}

AdmissionResult VtepAdmission::admit(const EncapsulatedFrame& frame,
                                     Clock::time_point now) {
    if (frame.vni > kMaxVni) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::InvalidVni);
        return {AdmissionDecision::Dropped, DropReason::InvalidVni, 0, false};
    }

    if (config_.reject_ip_fragments &&
        (frame.fragmented || frame.ip_fragmented || frame.more_fragments ||
         frame.fragment_offset != 0)) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::Fragmented);
        return {AdmissionDecision::Dropped, DropReason::Fragmented, 0, false};
    }

    std::size_t inner_bytes = frame.inner_frame_bytes;
    MacAddress inner_source = frame.inner_source_mac;
    MacAddress inner_destination = frame.inner_destination_mac;
    bool malformed = false;

    if (!frame.inner_frame.empty()) {
        if (frame.inner_frame.size() < 14 ||
            (inner_bytes != 0 && inner_bytes != frame.inner_frame.size())) {
            malformed = true;
        } else {
            inner_bytes = frame.inner_frame.size();
            MacAddress parsed_destination{};
            MacAddress parsed_source{};
            std::copy_n(frame.inner_frame.begin(), 6,
                        parsed_destination.octets.begin());
            std::copy_n(frame.inner_frame.begin() + 6, 6,
                        parsed_source.octets.begin());

            if ((!inner_source.is_zero() && inner_source != parsed_source) ||
                (!inner_destination.is_zero() &&
                 inner_destination != parsed_destination)) {
                malformed = true;
            } else {
                inner_source = parsed_source;
                inner_destination = parsed_destination;
            }
        }
    } else if (inner_bytes == 0) {
        malformed = true;
    }

    if (!malformed) {
        malformed = inner_source.is_zero() || inner_destination.is_zero() ||
                    (config_.reject_multicast_source_mac &&
                     inner_source.is_multicast());
    }
    if (malformed) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::MalformedInnerFrame);
        return {AdmissionDecision::Dropped, DropReason::MalformedInnerFrame, 0,
                false};
    }

    if (inner_bytes < config_.sizes.min_inner_frame_bytes) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::MalformedInnerFrame);
        return {AdmissionDecision::Dropped, DropReason::MalformedInnerFrame, 0,
                false};
    }

    if (config_.sizes.max_inner_frame_bytes != 0 &&
        inner_bytes > config_.sizes.max_inner_frame_bytes) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::Oversized);
        return {AdmissionDecision::Dropped, DropReason::Oversized, 0, false};
    }

    constexpr std::size_t kVxlanIpUdpOverhead = 36;
    if (inner_bytes >
        std::numeric_limits<std::size_t>::max() - kVxlanIpUdpOverhead) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::Oversized);
        return {AdmissionDecision::Dropped, DropReason::Oversized, 0, false};
    }
    const std::size_t outer_bytes =
        frame.outer_datagram_bytes != 0
            ? frame.outer_datagram_bytes
            : inner_bytes + kVxlanIpUdpOverhead;
    if (outer_bytes < inner_bytes) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::MalformedInnerFrame);
        return {AdmissionDecision::Dropped, DropReason::MalformedInnerFrame, 0,
                false};
    }
    if (config_.sizes.max_outer_datagram_bytes != 0 &&
        outer_bytes > config_.sizes.max_outer_datagram_bytes) {
        std::lock_guard lock(mutex_);
        record_drop_locked(DropReason::Oversized);
        return {AdmissionDecision::Dropped, DropReason::Oversized, 0, false};
    }

    std::lock_guard lock(mutex_);

    const auto source = by_source_.find(frame.outer_source);
    if (source == by_source_.end()) {
        record_drop_locked(DropReason::UnknownVtep);
        return {AdmissionDecision::Dropped, DropReason::UnknownVtep, 0, false};
    }

    const auto vni_tenant = vni_tenant_.find(frame.vni);
    if (vni_tenant == vni_tenant_.end()) {
        record_drop_locked(DropReason::VniMismatch);
        return {AdmissionDecision::Dropped, DropReason::VniMismatch, 0, false};
    }
    const TenantId tenant = vni_tenant->second;

    const auto registration = std::find_if(
        source->second.begin(), source->second.end(),
        [&](const VtepRegistration& candidate) {
            return candidate.vni == frame.vni && candidate.tenant == tenant;
        });
    if (registration == source->second.end()) {
        record_drop_locked(DropReason::VniMismatch);
        return {AdmissionDecision::Dropped, DropReason::VniMismatch, tenant,
                false};
    }

    const KeyRing* ring = key_ring_locked(tenant);
    if (ring == nullptr ||
        !epoch_accepted_locked(*ring, TunnelEpoch{frame.generation,
                                                  frame.tunnel_key})) {
        record_drop_locked(DropReason::TunnelEpochMismatch);
        return {AdmissionDecision::Dropped, DropReason::TunnelEpochMismatch,
                tenant, false};
    }

    const auto destination_table = inner_mac_table_.find(frame.vni);
    const bool destination_is_learned =
        destination_table != inner_mac_table_.end() &&
        destination_table->second.find(inner_destination) !=
            destination_table->second.end();
    if (inner_destination.is_multicast() || !destination_is_learned) {
        const auto vni_sources = vni_sources_.find(frame.vni);
        const std::size_t vtep_count =
            vni_sources == vni_sources_.end() ? 0 : vni_sources->second.size();
        const std::size_t copies = vtep_count > 0 ? vtep_count - 1 : 0;
        if (config_.amplification.max_ingress_replication_copies != 0 &&
            copies > config_.amplification.max_ingress_replication_copies) {
            record_drop_locked(DropReason::AmplificationLimited);
            return {AdmissionDecision::Dropped,
                    DropReason::AmplificationLimited, tenant, false};
        }
    }

    if (config_.amplification.enabled &&
        !consume_bucket_locked(frame.outer_source, outer_bytes, now)) {
        record_drop_locked(DropReason::AmplificationLimited);
        return {AdmissionDecision::Dropped, DropReason::AmplificationLimited,
                tenant, false};
    }

    auto& mac_table = inner_mac_table_[frame.vni];
    const auto learned_mac = mac_table.find(inner_source);
    if (learned_mac != mac_table.end() &&
        learned_mac->second != frame.outer_source) {
        record_drop_locked(DropReason::InnerMacConflict);
        return {AdmissionDecision::Dropped, DropReason::InnerMacConflict,
                tenant, false};
    }

    bool mac_learned = false;
    if (learned_mac == mac_table.end()) {
        mac_table.emplace(inner_source, frame.outer_source);
        mac_learned = true;
    }

    ++counters_.admitted;
    return {AdmissionDecision::Admitted, DropReason::None, tenant, mac_learned};
}

AdmissionCounters VtepAdmission::counters() const {
    std::lock_guard lock(mutex_);
    return counters_;
}

void VtepAdmission::reset_counters() {
    std::lock_guard lock(mutex_);
    counters_ = {};
}

AdmissionConfig VtepAdmission::config() const {
    std::lock_guard lock(mutex_);
    return config_;
}

}  // namespace netsec

std::size_t std::hash<netsec::Ipv4Address>::operator()(
    const netsec::Ipv4Address& address) const noexcept {
    return netsec::hash_bytes(address.octets.data(), address.octets.size());
}

std::size_t std::hash<netsec::MacAddress>::operator()(
    const netsec::MacAddress& address) const noexcept {
    return netsec::hash_bytes(address.octets.data(), address.octets.size());
}
