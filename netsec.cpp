#include "netsec.hpp"

#include <optional>
#include <utility>

namespace netsec {
namespace {

constexpr std::uint8_t kVersionMask = 0xF0;
constexpr std::uint8_t kIpv6Version = 0x60;
constexpr std::uint16_t kFragmentOffsetMask = 0xFFF8;
constexpr std::uint16_t kMoreFragmentsMask = 0x0001;

std::uint16_t read_u16(const std::uint8_t* bytes) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(bytes[0]) << 8) | bytes[1]);
}

std::uint32_t read_u32(const std::uint8_t* bytes) noexcept {
    return (static_cast<std::uint32_t>(bytes[0]) << 24) |
           (static_cast<std::uint32_t>(bytes[1]) << 16) |
           (static_cast<std::uint32_t>(bytes[2]) << 8) |
           static_cast<std::uint32_t>(bytes[3]);
}

bool is_port_protocol(std::uint8_t protocol) noexcept {
    switch (protocol) {
        case kNextHeaderTcp:
        case kNextHeaderUdp:
        case kNextHeaderDccp:
        case kNextHeaderSctp:
        case kNextHeaderUdpLite:
            return true;
        default:
            return false;
    }
}

bool is_terminal_protocol(std::uint8_t next_header) noexcept {
    switch (next_header) {
        case kNextHeaderTcp:
        case kNextHeaderUdp:
        case kNextHeaderDccp:
        case kNextHeaderIcmpv6:
        case kNextHeaderNoNextHeader:
        case kNextHeaderSctp:
        case kNextHeaderUdpLite:
            return true;
        default:
            return false;
    }
}

bool is_extension_header(std::uint8_t next_header) noexcept {
    switch (next_header) {
        case kNextHeaderHopByHop:
        case kNextHeaderRouting:
        case kNextHeaderFragment:
        case kNextHeaderEsp:
        case kNextHeaderAh:
        case kNextHeaderDestinationOptions:
            return true;
        default:
            return false;
    }
}

std::optional<std::size_t> validate_options(
    std::span<const std::uint8_t> data,
    std::size_t begin,
    std::size_t end) noexcept {
    std::size_t cursor = begin;
    while (cursor < end) {
        const std::uint8_t type = data[cursor];
        if (type == 0) {
            ++cursor;
            continue;
        }
        if (cursor + 1 >= end) {
            return cursor;
        }
        const std::size_t option_length = data[cursor + 1];
        const std::size_t total_length = 2 + option_length;
        if (total_length > end - cursor) {
            return cursor;
        }
        cursor += total_length;
    }
    return std::nullopt;
}

ParseResult failure(ParseResult result,
                    ParseStatus status,
                    std::size_t offset) {
    result.status = status;
    result.error_offset = offset;
    result.payload_offset = std::nullopt;
    result.transport_offset = std::nullopt;
    result.source_port = std::nullopt;
    result.destination_port = std::nullopt;
    result.payload_kind = PayloadKind::None;
    return result;
}

void parse_ports(ParseResult& result,
                 std::span<const std::uint8_t> packet,
                 std::size_t offset) {
    result.source_port = read_u16(packet.data() + offset);
    result.destination_port =
        read_u16(packet.data() + offset + sizeof(std::uint16_t));
}

}  // namespace

UnknownExtensionPolicy default_unknown_extension_policy() noexcept {
    return UnknownExtensionPolicy::Reject;
}

bool is_recognized_extension_header(std::uint8_t next_header) noexcept {
    return is_extension_header(next_header);
}

ParseResult parse_ipv6_extension_chain(
    std::span<const std::uint8_t> packet,
    const ParsePolicy& policy) {
    ParseResult result;
    result.packet_length = packet.size();

    if (packet.size() < kIpv6FixedHeaderSize) {
        return failure(std::move(result),
                       ParseStatus::TruncatedPacket,
                       packet.size());
    }
    if ((packet[0] & kVersionMask) != kIpv6Version) {
        return failure(std::move(result), ParseStatus::InvalidVersion, 0);
    }

    result.fixed_header.traffic_class = static_cast<std::uint8_t>(
        ((packet[0] & 0x0F) << 4) | (packet[1] >> 4));
    result.fixed_header.flow_label =
        (static_cast<std::uint32_t>(packet[1] & 0x0F) << 16) |
        (static_cast<std::uint32_t>(packet[2]) << 8) |
        static_cast<std::uint32_t>(packet[3]);
    result.fixed_header.payload_length = read_u16(packet.data() + 4);
    result.fixed_header.next_header = packet[6];
    result.fixed_header.hop_limit = packet[7];
    result.fixed_header.offset = 0;
    result.fixed_header.nesting_depth = 0;
    result.fixed_header.source_address.assign(packet.begin() + 8,
                                               packet.begin() + 24);
    result.fixed_header.destination_address.assign(packet.begin() + 24,
                                                    packet.begin() + 40);

    const std::size_t declared_packet_length =
        kIpv6FixedHeaderSize +
        static_cast<std::size_t>(result.fixed_header.payload_length);
    result.declared_packet_length = declared_packet_length;

    if (result.fixed_header.payload_length == 0 &&
        packet.size() > kIpv6FixedHeaderSize) {
        return failure(std::move(result),
                       ParseStatus::UnsupportedJumboPayload,
                       packet.size());
    }
    if (declared_packet_length > packet.size()) {
        return failure(std::move(result),
                       ParseStatus::TruncatedPacket,
                       packet.size());
    }

    result.ipv6_layers.push_back(result.fixed_header);

    std::size_t cursor = kIpv6FixedHeaderSize;
    std::size_t payload_end = declared_packet_length;
    std::size_t nesting_depth = 0;
    std::size_t extension_count = 0;
    std::uint8_t next_header = result.fixed_header.next_header;

    for (;;) {
        if (next_header == kNextHeaderNoNextHeader) {
            result.final_next_header = next_header;
            result.upper_layer_protocol = next_header;
            result.payload_kind = PayloadKind::NoNextHeader;
            result.payload_offset = cursor;
            result.nesting_depth = nesting_depth;
            return result;
        }

        if (next_header == kNextHeaderIpv6) {
            if (nesting_depth + 1 > policy.max_nesting_depth) {
                return failure(std::move(result),
                               ParseStatus::NestingLimitExceeded,
                               cursor);
            }
            if (cursor > payload_end ||
                payload_end - cursor < kIpv6FixedHeaderSize) {
                return failure(std::move(result),
                               ParseStatus::TruncatedPacket,
                               cursor);
            }
            if ((packet[cursor] & kVersionMask) != kIpv6Version) {
                return failure(std::move(result),
                               ParseStatus::InvalidVersion,
                               cursor);
            }

            Ipv6Header nested;
            nested.traffic_class = static_cast<std::uint8_t>(
                ((packet[cursor] & 0x0F) << 4) | (packet[cursor + 1] >> 4));
            nested.flow_label =
                (static_cast<std::uint32_t>(packet[cursor + 1] & 0x0F) << 16) |
                (static_cast<std::uint32_t>(packet[cursor + 2]) << 8) |
                static_cast<std::uint32_t>(packet[cursor + 3]);
            nested.payload_length = read_u16(packet.data() + cursor + 4);
            nested.next_header = packet[cursor + 6];
            nested.hop_limit = packet[cursor + 7];
            nested.offset = cursor;
            nested.nesting_depth = nesting_depth + 1;
            nested.source_address.assign(packet.begin() + cursor + 8,
                                         packet.begin() + cursor + 24);
            nested.destination_address.assign(packet.begin() + cursor + 24,
                                              packet.begin() + cursor + 40);

            const std::size_t nested_end =
                cursor + kIpv6FixedHeaderSize +
                static_cast<std::size_t>(nested.payload_length);
            if (nested_end > payload_end) {
                return failure(std::move(result),
                               ParseStatus::InvalidPayloadLength,
                               cursor + 4);
            }

            ++nesting_depth;
            result.nesting_depth = nesting_depth;
            result.ipv6_layers.push_back(nested);
            payload_end = nested_end;
            cursor += kIpv6FixedHeaderSize;
            next_header = nested.next_header;
            continue;
        }

        if (is_terminal_protocol(next_header)) {
            result.final_next_header = next_header;
            result.upper_layer_protocol = next_header;
            result.payload_kind = PayloadKind::UpperLayer;
            result.payload_offset = cursor;
            result.transport_offset = cursor;
            result.nesting_depth = nesting_depth;

            if (is_port_protocol(next_header)) {
                if (cursor > payload_end ||
                    payload_end - cursor < kTransportPortPrefixSize) {
                    return failure(std::move(result),
                                   ParseStatus::TruncatedUpperLayerHeader,
                                   cursor);
                }
                parse_ports(result, packet, cursor);
            }
            return result;
        }

        if (next_header == kNextHeaderEsp) {
            if (extension_count + 1 > policy.max_extension_headers) {
                return failure(std::move(result),
                               ParseStatus::ExtensionChainTooLong,
                               cursor);
            }
            if (cursor > payload_end ||
                payload_end - cursor < kEspMinimumHeaderSize) {
                return failure(std::move(result),
                               ParseStatus::TruncatedPacket,
                               cursor);
            }
            if ((cursor % 4) != 0) {
                return failure(std::move(result),
                               ParseStatus::InvalidExtensionHeader,
                               cursor);
            }
            if (result.extension_chain_bytes + kEspMinimumHeaderSize >
                policy.max_chain_bytes) {
                return failure(std::move(result),
                               ParseStatus::ExtensionChainTooLarge,
                               cursor);
            }
            result.extension_chain_bytes += kEspMinimumHeaderSize;
            result.final_next_header = next_header;
            result.upper_layer_protocol = next_header;
            result.payload_kind = PayloadKind::EncryptedEsp;
            result.payload_offset = cursor;
            result.esp_header_offset = cursor;
            result.nesting_depth = nesting_depth;
            return result;
        }

        if (!is_extension_header(next_header)) {
            if (policy.unknown_extension_policy !=
                UnknownExtensionPolicy::TreatAsOpaqueUpperLayer) {
                return failure(std::move(result),
                               ParseStatus::UnknownExtensionHeader,
                               cursor);
            }
            result.final_next_header = next_header;
            result.upper_layer_protocol = next_header;
            result.payload_kind = PayloadKind::OpaqueProtocol;
            result.payload_offset = cursor;
            result.transport_offset = cursor;
            result.nesting_depth = nesting_depth;
            return result;
        }

        if (extension_count + 1 > policy.max_extension_headers) {
            return failure(std::move(result),
                           ParseStatus::ExtensionChainTooLong,
                           cursor);
        }
        if (cursor > payload_end || cursor + 2 > payload_end) {
            return failure(std::move(result),
                           ParseStatus::TruncatedPacket,
                           cursor);
        }
        if ((cursor % 4) != 0) {
            return failure(std::move(result),
                           ParseStatus::InvalidExtensionHeader,
                           cursor);
        }

        const std::uint8_t header_type = next_header;
        const std::uint8_t following_header = packet[cursor];
        std::size_t header_length = 0;

        switch (header_type) {
            case kNextHeaderHopByHop:
            case kNextHeaderRouting:
            case kNextHeaderDestinationOptions:
                header_length =
                    (static_cast<std::size_t>(packet[cursor + 1]) + 1) * 8;
                break;
            case kNextHeaderFragment:
                header_length = kFragmentHeaderSize;
                break;
            case kNextHeaderAh:
                header_length =
                    (static_cast<std::size_t>(packet[cursor + 1]) + 2) * 4;
                if (header_length < kAhMinimumHeaderSize) {
                    return failure(std::move(result),
                                   ParseStatus::InvalidExtensionHeader,
                                   cursor + 1);
                }
                break;
            default:
                return failure(std::move(result),
                               ParseStatus::UnknownExtensionHeader,
                               cursor);
        }

        if (header_length > payload_end - cursor) {
            return failure(std::move(result),
                           ParseStatus::InvalidExtensionHeader,
                           cursor + 1);
        }
        const std::size_t alignment =
            (header_type == kNextHeaderAh) ? 4 : 8;
        if ((header_length % alignment) != 0) {
            return failure(std::move(result),
                           ParseStatus::InvalidExtensionHeader,
                           cursor + 1);
        }

        if ((header_type == kNextHeaderHopByHop ||
             header_type == kNextHeaderDestinationOptions) &&
            header_length > 2) {
            const auto bad_option =
                validate_options(packet, cursor + 2, cursor + header_length);
            if (bad_option.has_value()) {
                return failure(std::move(result),
                               ParseStatus::InvalidExtensionHeader,
                               *bad_option);
            }
        }

        if (result.extension_chain_bytes + header_length >
            policy.max_chain_bytes) {
            return failure(std::move(result),
                           ParseStatus::ExtensionChainTooLarge,
                           cursor);
        }

        ExtensionHeader extension;
        extension.type = header_type;
        extension.next_header = following_header;
        extension.offset = cursor;
        extension.length = header_length;
        extension.chain_index = extension_count;
        extension.nesting_depth = nesting_depth;
        result.extension_headers.push_back(extension);
        result.extension_chain_bytes += header_length;
        ++extension_count;

        if (header_type == kNextHeaderFragment) {
            const std::uint16_t fragment_field =
                read_u16(packet.data() + cursor + 2);
            FragmentHeader fragment_header;
            fragment_header.header_offset = cursor;
            fragment_header.fragment_offset = static_cast<std::uint16_t>(
                fragment_field & kFragmentOffsetMask);
            fragment_header.more_fragments =
                (fragment_field & kMoreFragmentsMask) != 0;
            fragment_header.identification =
                read_u32(packet.data() + cursor + 4);
            fragment_header.next_header = following_header;
            result.fragment = fragment_header;

            if (fragment_header.fragment_offset != 0) {
                result.final_next_header = following_header;
                result.upper_layer_protocol = kNextHeaderFragment;
                result.payload_kind = PayloadKind::NonInitialFragment;
                result.payload_offset = cursor + kFragmentHeaderSize;
                result.nesting_depth = nesting_depth;
                return result;
            }
        }

        cursor += header_length;
        next_header = following_header;
        result.final_next_header = next_header;
    }
}

ParseResult parse_ipv6_extension_chain(
    const std::uint8_t* packet,
    std::size_t length,
    const ParsePolicy& policy) {
    if (packet == nullptr) {
        return parse_ipv6_extension_chain(std::span<const std::uint8_t>{}, policy);
    }
    return parse_ipv6_extension_chain(
        std::span<const std::uint8_t>(packet, length), policy);
}

ParseResult parse_ipv6(
    std::span<const std::uint8_t> packet,
    const ParsePolicy& policy) {
    return parse_ipv6_extension_chain(packet, policy);
}

ParseResult parse(
    std::span<const std::uint8_t> packet,
    const ParsePolicy& policy) {
    return parse_ipv6_extension_chain(packet, policy);
}

bool rule_matches(const ParseResult& result, const TransportRule& rule) noexcept {
    if (!result.ok() || result.payload_kind == PayloadKind::None ||
        result.upper_layer_protocol != rule.protocol) {
        return false;
    }
    if (rule.source_port.has_value() &&
        (!result.source_port.has_value() ||
         *result.source_port != *rule.source_port)) {
        return false;
    }
    if (rule.destination_port.has_value() &&
        (!result.destination_port.has_value() ||
         *result.destination_port != *rule.destination_port)) {
        return false;
    }
    return true;
}

Ipv6ExtensionWalker::Ipv6ExtensionWalker(ParsePolicy policy)
    : policy_(policy) {}

ParseResult Ipv6ExtensionWalker::walk(
    std::span<const std::uint8_t> packet) const {
    return parse_ipv6_extension_chain(packet, policy_);
}

}  // namespace netsec
