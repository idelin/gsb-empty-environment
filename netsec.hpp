#pragma once

#include <array>
#include <chrono>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace netsec {

inline constexpr std::uint32_t kMaxVni = 0xFFFFFFU;

struct Ipv4Address {
    std::array<std::uint8_t, 4> octets{};

    static constexpr Ipv4Address from_octets(std::uint8_t a, std::uint8_t b,
                                              std::uint8_t c,
                                              std::uint8_t d) noexcept {
        return Ipv4Address{{a, b, c, d}};
    }

    // Accepts dotted-decimal IPv4 only. Invalid input returns std::nullopt.
    static std::optional<Ipv4Address> parse(std::string_view text) noexcept;

    [[nodiscard]] bool is_unspecified() const noexcept;
    [[nodiscard]] bool is_multicast() const noexcept;
    [[nodiscard]] std::string to_string() const;

    auto operator<=>(const Ipv4Address&) const = default;
};

using IpAddress = Ipv4Address;

struct MacAddress {
    std::array<std::uint8_t, 6> octets{};

    static constexpr MacAddress from_octets(std::uint8_t a, std::uint8_t b,
                                            std::uint8_t c, std::uint8_t d,
                                            std::uint8_t e,
                                            std::uint8_t f) noexcept {
        return MacAddress{{a, b, c, d, e, f}};
    }

    // Accepts AA:BB:CC:DD:EE:FF and AA-BB-CC-DD-EE-FF.
    static std::optional<MacAddress> parse(std::string_view text) noexcept;

    [[nodiscard]] bool is_zero() const noexcept;
    [[nodiscard]] bool is_multicast() const noexcept;
    [[nodiscard]] bool is_broadcast() const noexcept;
    [[nodiscard]] std::string to_string() const;

    auto operator<=>(const MacAddress&) const = default;
};

}  // namespace netsec

template <>
struct std::hash<netsec::Ipv4Address> {
    std::size_t operator()(const netsec::Ipv4Address& address) const noexcept;
};

template <>
struct std::hash<netsec::MacAddress> {
    std::size_t operator()(const netsec::MacAddress& address) const noexcept;
};

namespace netsec {

using TenantId = std::uint32_t;
using Vni = std::uint32_t;
using TunnelGeneration = std::uint64_t;
using TunnelKey = std::uint32_t;

struct TunnelEpoch {
    TunnelGeneration generation = 0;
    TunnelKey key = 0;

    auto operator<=>(const TunnelEpoch&) const = default;
};

struct SizeLimits {
    std::size_t min_inner_frame_bytes = 14;
    std::size_t max_inner_frame_bytes = 1500;
    // Outer IP datagram size. The default permits one 1500-byte inner
    // Ethernet frame plus the 8-byte VXLAN and 28-byte IPv4/UDP headers.
    std::size_t max_outer_datagram_bytes = 1550;
};

struct AmplificationLimits {
    bool enabled = true;

    // Per outer-source token buckets. The bucket starts full. A zero rate
    // freezes refill but still permits the configured burst.
    std::uint64_t burst_packets = 4096;
    double packets_per_second = 4096.0;
    std::uint64_t burst_bytes = 8U * 1024U * 1024U;
    double bytes_per_second = 8.0 * 1024.0 * 1024.0;

    // VXLAN ingress replication sends one frame to every other VTEP in the
    // VNI for broadcast/multicast traffic and for unknown unicast. Requests
    // above this fan-out are rejected. Zero disables the fan-out check.
    std::uint32_t max_ingress_replication_copies = 64;
};

struct AdmissionConfig {
    SizeLimits sizes{};
    AmplificationLimits amplification{};
    TunnelEpoch initial_tunnel_epoch{};
    std::optional<TunnelEpoch> initial_previous_tunnel_epoch;
    bool reject_ip_fragments = true;
    bool reject_multicast_source_mac = true;
};

struct VtepRegistration {
    IpAddress source;
    Vni vni = 0;
    TenantId tenant = 0;

    auto operator<=>(const VtepRegistration&) const = default;
};

enum class DropReason : std::uint8_t {
    None = 0,
    UnknownVtep,
    VniMismatch,
    InvalidVni,
    InnerMacConflict,
    Fragmented,
    Oversized,
    AmplificationLimited,
    TunnelEpochMismatch,
    MalformedInnerFrame,
};

[[nodiscard]] std::string_view to_string(DropReason reason) noexcept;

enum class AdmissionDecision : std::uint8_t {
    Admitted = 0,
    Dropped,
};

struct AdmissionResult {
    AdmissionDecision decision = AdmissionDecision::Dropped;
    DropReason reason = DropReason::MalformedInnerFrame;
    TenantId tenant = 0;
    bool mac_learned = false;

    [[nodiscard]] bool accepted() const noexcept {
        return decision == AdmissionDecision::Admitted;
    }

    explicit operator bool() const noexcept { return accepted(); }
};

struct AdmissionCounters {
    std::uint64_t admitted = 0;
    std::uint64_t dropped = 0;
    std::uint64_t unknown_vtep = 0;
    std::uint64_t vni_mismatch = 0;
    std::uint64_t invalid_vni = 0;
    std::uint64_t inner_mac_conflict = 0;
    std::uint64_t fragmented = 0;
    std::uint64_t oversized = 0;
    std::uint64_t amplification_limited = 0;
    std::uint64_t tunnel_epoch_mismatch = 0;
    std::uint64_t malformed_inner_frame = 0;

    [[nodiscard]] std::uint64_t reason_count(DropReason reason) const noexcept;
};

// A decoded VXLAN packet. outer_source, frame size, and fragmentation flags
// must come from the outer IP/UDP metadata, never from the encapsulated bytes.
//
// If inner_frame is non-empty it is parsed as an Ethernet II frame and the
// explicit MAC fields may be left zero. If only metadata is supplied, set the
// MAC fields and inner_frame_bytes explicitly.
struct EncapsulatedFrame {
    IpAddress outer_source{};
    Vni vni = 0;
    TunnelGeneration generation = 0;
    TunnelKey tunnel_key = 0;

    bool fragmented = false;
    bool ip_fragmented = false;
    bool more_fragments = false;
    std::uint16_t fragment_offset = 0;

    // Total outer IPv4 datagram size. Zero means "inner_frame_bytes + 36".
    std::size_t outer_datagram_bytes = 0;
    std::size_t inner_frame_bytes = 0;

    MacAddress inner_destination_mac{};
    MacAddress inner_source_mac{};
    std::vector<std::uint8_t> inner_frame;
};

// Thread-safe admission component for a C++20 virtual-network gateway.
class VtepAdmission {
public:
    using Clock = std::chrono::steady_clock;

    explicit VtepAdmission(AdmissionConfig config = {});
    VtepAdmission(const VtepAdmission&) = delete;
    VtepAdmission& operator=(const VtepAdmission&) = delete;

    // A source can be registered in several VNIs, but each VNI has exactly one
    // owning tenant. Registration is idempotent.
    bool register_vtep(const IpAddress& source, Vni vni, TenantId tenant);
    bool register_vtep(const VtepRegistration& registration);

    bool unregister_vtep(const IpAddress& source, Vni vni);
    bool unregister_vtep(const IpAddress& source);

    [[nodiscard]] bool is_registered(const IpAddress& source,
                                     Vni vni) const;
    [[nodiscard]] std::optional<TenantId> tenant_for_vni(Vni vni) const;
    [[nodiscard]] std::size_t registered_vtep_count(Vni vni) const;

    // Install the next epoch. If retain_previous is true, the previous epoch
    // remains valid until the following rotation (configuration grace window).
    // The default retires the old epoch immediately.
    void rotate_tunnel_epoch(TenantId tenant, TunnelEpoch epoch,
                             bool retain_previous = false);
    void rotate_tunnel_epoch(TenantId tenant, TunnelGeneration generation,
                             TunnelKey key, bool retain_previous = false);
    void rotate_global_tunnel_epoch(TunnelEpoch epoch,
                                    bool retain_previous = false);
    void rotate_global_tunnel_epoch(TunnelGeneration generation, TunnelKey key,
                                    bool retain_previous = false);

    void rotate_tunnel_key(TenantId tenant, TunnelGeneration generation,
                           TunnelKey key, bool retain_previous = false) {
        rotate_tunnel_epoch(tenant, generation, key, retain_previous);
    }
    void rotate_global_tunnel_key(TunnelGeneration generation, TunnelKey key,
                                  bool retain_previous = false) {
        rotate_global_tunnel_epoch(generation, key, retain_previous);
    }

    [[nodiscard]] std::optional<TunnelEpoch> current_tunnel_epoch(
        TenantId tenant) const;

    // The overload without a timestamp uses Clock::now(). Supplying a timestamp
    // makes rate-limit tests deterministic.
    AdmissionResult admit(const EncapsulatedFrame& frame);
    AdmissionResult admit(const EncapsulatedFrame& frame, Clock::time_point now);

    [[nodiscard]] AdmissionCounters counters() const;
    [[nodiscard]] AdmissionCounters stats() const { return counters(); }
    void reset_counters();
    [[nodiscard]] AdmissionConfig config() const;

private:
    struct KeyRing {
        TunnelEpoch current{};
        std::optional<TunnelEpoch> previous;
    };

    struct TokenBucket {
        bool initialized = false;
        double packet_tokens = 0.0;
        double byte_tokens = 0.0;
        Clock::time_point last_refill{};
    };

    bool register_vtep_locked(const VtepRegistration& registration);
    bool unregister_vtep_locked(const IpAddress& source, const Vni* vni);
    void remove_source_from_vni_locked(const IpAddress& source, Vni vni);
    void remove_mac_bindings_locked(const IpAddress& source, Vni vni);
    void remove_mac_bindings_locked(const IpAddress& source);

    [[nodiscard]] const KeyRing* key_ring_locked(TenantId tenant) const;
    [[nodiscard]] bool epoch_accepted_locked(const KeyRing& ring,
                                             TunnelEpoch epoch) const;
    [[nodiscard]] bool consume_bucket_locked(const IpAddress& source,
                                             std::size_t bytes,
                                             Clock::time_point now);
    void record_drop_locked(DropReason reason);

    AdmissionConfig config_;
    mutable std::mutex mutex_;

    std::unordered_map<IpAddress, std::vector<VtepRegistration>> by_source_;
    std::unordered_map<Vni, TenantId> vni_tenant_;
    std::unordered_map<Vni, std::unordered_set<IpAddress>> vni_sources_;
    std::unordered_map<Vni, std::unordered_map<MacAddress, IpAddress>>
        inner_mac_table_;

    std::unordered_map<TenantId, KeyRing> tenant_keys_;
    KeyRing global_keys_;
    std::unordered_map<IpAddress, TokenBucket> buckets_;
    AdmissionCounters counters_;
};

using VTEPAdmission = VtepAdmission;

}  // namespace netsec
