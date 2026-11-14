#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <vector>

namespace netsec {

inline constexpr std::uint8_t kNextHeaderHopByHop = 0;
inline constexpr std::uint8_t kNextHeaderTcp = 6;
inline constexpr std::uint8_t kNextHeaderUdp = 17;
inline constexpr std::uint8_t kNextHeaderDccp = 33;
inline constexpr std::uint8_t kNextHeaderIpv6 = 41;
inline constexpr std::uint8_t kNextHeaderRouting = 43;
inline constexpr std::uint8_t kNextHeaderFragment = 44;
inline constexpr std::uint8_t kNextHeaderEsp = 50;
inline constexpr std::uint8_t kNextHeaderAh = 51;
inline constexpr std::uint8_t kNextHeaderIcmpv6 = 58;
inline constexpr std::uint8_t kNextHeaderNoNextHeader = 59;
inline constexpr std::uint8_t kNextHeaderDestinationOptions = 60;
inline constexpr std::uint8_t kNextHeaderSctp = 132;
inline constexpr std::uint8_t kNextHeaderUdpLite = 136;

inline constexpr std::size_t kIpv6FixedHeaderSize = 40;
inline constexpr std::size_t kFragmentHeaderSize = 8;
inline constexpr std::size_t kEspMinimumHeaderSize = 8;
inline constexpr std::size_t kAhMinimumHeaderSize = 12;
inline constexpr std::size_t kTransportPortPrefixSize = 4;

enum class ParseStatus : std::uint8_t {
    Ok = 0,
    TruncatedPacket,
    InvalidVersion,
    InvalidPayloadLength,
    InvalidExtensionHeader,
    UnknownExtensionHeader,
    ExtensionChainTooLong,
    ExtensionChainTooLarge,
    NestingLimitExceeded,
    TruncatedUpperLayerHeader,
    UnsupportedJumboPayload,
};

enum class PayloadKind : std::uint8_t {
    None = 0,
    UpperLayer,
    Transport = UpperLayer,
    NonInitialFragment,
    EncryptedEsp,
    NoNextHeader,
    OpaqueProtocol,
};

enum class UnknownExtensionPolicy : std::uint8_t {
    Reject = 0,
    TreatAsOpaqueUpperLayer,
};

struct ParsePolicy {
    std::size_t max_extension_headers = 32;
    std::size_t max_chain_bytes = 4096;
    std::size_t max_nesting_depth = 4;
    UnknownExtensionPolicy unknown_extension_policy = UnknownExtensionPolicy::Reject;
};

struct Ipv6Header {
    std::uint8_t traffic_class = 0;
    std::uint32_t flow_label = 0;
    std::uint16_t payload_length = 0;
    std::uint8_t next_header = 0;
    std::uint8_t hop_limit = 0;
    std::size_t offset = 0;
    std::size_t nesting_depth = 0;
    std::vector<std::uint8_t> source_address{16, 0};
    std::vector<std::uint8_t> destination_address{16, 0};
};

struct ExtensionHeader {
    std::uint8_t type = 0;
    std::uint8_t next_header = 0;
    std::size_t offset = 0;
    std::size_t length = 0;
    std::size_t chain_index = 0;
    std::size_t nesting_depth = 0;
};

struct FragmentHeader {
    std::size_t header_offset = 0;
    std::uint16_t fragment_offset = 0;
    bool more_fragments = false;
    std::uint32_t identification = 0;
    std::uint8_t next_header = 0;
};

struct ParseResult {
    ParseStatus status = ParseStatus::Ok;
    std::size_t packet_length = 0;
    std::size_t declared_packet_length = 0;
    std::size_t extension_chain_bytes = 0;
    std::size_t nesting_depth = 0;
    std::uint8_t final_next_header = 0;
    std::uint8_t upper_layer_protocol = 0;
    PayloadKind payload_kind = PayloadKind::None;
    std::optional<std::size_t> payload_offset;
    std::optional<std::size_t> transport_offset;
    std::optional<std::size_t> esp_header_offset;
    std::optional<std::size_t> error_offset;
    std::optional<std::uint16_t> source_port;
    std::optional<std::uint16_t> destination_port;
    std::optional<FragmentHeader> fragment;
    Ipv6Header fixed_header;
    std::vector<Ipv6Header> ipv6_layers;
    std::vector<ExtensionHeader> extension_headers;

    [[nodiscard]] bool ok() const noexcept { return status == ParseStatus::Ok; }

    [[nodiscard]] bool has_upper_layer() const noexcept {
        return payload_kind == PayloadKind::UpperLayer ||
               payload_kind == PayloadKind::OpaqueProtocol;
    }

    [[nodiscard]] bool has_ports() const noexcept {
        return source_port.has_value() && destination_port.has_value();
    }
};

struct TransportRule {
    std::uint8_t protocol = 0;
    std::optional<std::uint16_t> source_port;
    std::optional<std::uint16_t> destination_port;
};

[[nodiscard]] UnknownExtensionPolicy default_unknown_extension_policy() noexcept;

[[nodiscard]] bool is_recognized_extension_header(std::uint8_t next_header) noexcept;

[[nodiscard]] ParseResult parse_ipv6_extension_chain(
    std::span<const std::uint8_t> packet,
    const ParsePolicy& policy = {});

[[nodiscard]] ParseResult parse_ipv6_extension_chain(
    const std::uint8_t* packet,
    std::size_t length,
    const ParsePolicy& policy = {});

[[nodiscard]] ParseResult parse_ipv6(
    std::span<const std::uint8_t> packet,
    const ParsePolicy& policy = {});

[[nodiscard]] ParseResult parse(
    std::span<const std::uint8_t> packet,
    const ParsePolicy& policy = {});

[[nodiscard]] bool rule_matches(const ParseResult& result, const TransportRule& rule) noexcept;

class Ipv6ExtensionWalker {
public:
    explicit Ipv6ExtensionWalker(ParsePolicy policy = {});

    [[nodiscard]] ParseResult walk(std::span<const std::uint8_t> packet) const;

    [[nodiscard]] const ParsePolicy& policy() const noexcept { return policy_; }

private:
    ParsePolicy policy_;
};

using Parser = Ipv6ExtensionWalker;
using Policy = ParsePolicy;
using Ipv6ParseResult = ParseResult;
using Ipv6ExtensionHeader = ExtensionHeader;
using IPv6Header = Ipv6Header;
using IPv6ExtensionHeader = ExtensionHeader;
using IPv6ParseResult = ParseResult;
using IPv6ExtensionWalker = Ipv6ExtensionWalker;

}  // namespace netsec
