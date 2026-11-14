#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;

void require(bool condition, const char* expression, int line) {
    if (!condition) {
        throw std::runtime_error(std::string("check failed at line ") +
                                 std::to_string(line) + ": " + expression);
    }
}

#define REQUIRE(expression) require((expression), #expression, __LINE__)

void set_u16(Bytes& bytes, std::size_t offset, std::uint16_t value) {
    bytes[offset] = static_cast<std::uint8_t>(value >> 8);
    bytes[offset + 1] = static_cast<std::uint8_t>(value);
}

void set_u32(Bytes& bytes, std::size_t offset, std::uint32_t value) {
    bytes[offset] = static_cast<std::uint8_t>(value >> 24);
    bytes[offset + 1] = static_cast<std::uint8_t>(value >> 16);
    bytes[offset + 2] = static_cast<std::uint8_t>(value >> 8);
    bytes[offset + 3] = static_cast<std::uint8_t>(value);
}

Bytes ipv6_packet(std::uint8_t next_header, const Bytes& payload) {
    Bytes packet(netsec::kIpv6FixedHeaderSize + payload.size(), 0);
    packet[0] = 0x60;
    set_u16(packet, 4, static_cast<std::uint16_t>(payload.size()));
    packet[6] = next_header;
    packet[7] = 64;
    for (std::size_t i = 0; i < 16; ++i) {
        packet[8 + i] = static_cast<std::uint8_t>(0x10 + i);
        packet[24 + i] = static_cast<std::uint8_t>(0x20 + i);
    }
    std::copy(payload.begin(), payload.end(), packet.begin() + 40);
    return packet;
}

void append_u16(Bytes& bytes, std::uint16_t value) {
    bytes.push_back(static_cast<std::uint8_t>(value >> 8));
    bytes.push_back(static_cast<std::uint8_t>(value));
}

Bytes make_udp(std::uint16_t source_port, std::uint16_t destination_port) {
    Bytes udp;
    append_u16(udp, source_port);
    append_u16(udp, destination_port);
    append_u16(udp, 8);
    append_u16(udp, 0);
    return udp;
}

Bytes make_ext_header(std::uint8_t next_header, std::uint8_t hdr_ext_len) {
    Bytes header(static_cast<std::size_t>(hdr_ext_len + 1) * 8, 0);
    header[0] = next_header;
    header[1] = hdr_ext_len;
    return header;
}

void append_bytes(Bytes& destination, const Bytes& source) {
    destination.insert(destination.end(), source.begin(), source.end());
}

Bytes full_extension_chain_packet() {
    Bytes payload;
    append_bytes(payload, make_ext_header(netsec::kNextHeaderRouting, 0));
    append_bytes(payload, make_ext_header(netsec::kNextHeaderFragment, 1));

    Bytes fragment(8, 0);
    fragment[0] = netsec::kNextHeaderDestinationOptions;
    append_bytes(payload, fragment);

    append_bytes(payload, make_ext_header(netsec::kNextHeaderAh, 0));

    Bytes ah(12, 0);
    ah[0] = netsec::kNextHeaderUdp;
    ah[1] = 1;
    set_u32(ah, 4, 0x01020304);
    set_u32(ah, 8, 7);
    append_bytes(payload, ah);

    append_bytes(payload, make_udp(1234, 8080));
    return ipv6_packet(netsec::kNextHeaderHopByHop, payload);
}

Bytes unknown_extension_packet(std::uint8_t unknown_header) {
    Bytes payload(8, 0);
    payload[0] = unknown_header;
    append_bytes(payload, make_udp(1234, 8080));
    return ipv6_packet(netsec::kNextHeaderHopByHop, payload);
}

Bytes many_destination_headers(std::size_t count) {
    Bytes payload;
    for (std::size_t i = 0; i < count; ++i) {
        const std::uint8_t next =
            (i + 1 == count) ? netsec::kNextHeaderUdp
                             : netsec::kNextHeaderDestinationOptions;
        append_bytes(payload, make_ext_header(next, 0));
    }
    append_bytes(payload, make_udp(1234, 8080));
    return ipv6_packet(netsec::kNextHeaderDestinationOptions, payload);
}

Bytes truncated_extension_packet() {
    Bytes payload = make_ext_header(netsec::kNextHeaderUdp, 1);
    payload[1] = 3;
    append_bytes(payload, make_udp(1234, 8080));
    return ipv6_packet(netsec::kNextHeaderHopByHop, payload);
}

Bytes bad_option_length_packet() {
    Bytes payload = make_ext_header(netsec::kNextHeaderUdp, 0);
    payload[2] = 0x01;
    payload[3] = 0xC8;
    append_bytes(payload, make_udp(1234, 8080));
    return ipv6_packet(netsec::kNextHeaderHopByHop, payload);
}

Bytes bad_ah_length_packet() {
    Bytes payload(8, 0);
    payload[0] = netsec::kNextHeaderUdp;
    payload[1] = 0;
    append_bytes(payload, make_udp(1234, 8080));
    return ipv6_packet(netsec::kNextHeaderAh, payload);
}

Bytes declared_length_controls_offset_packet() {
    Bytes payload = make_ext_header(netsec::kNextHeaderUdp, 0);
    Bytes junk(8, 0);
    junk[0] = 0xDE;
    junk[1] = 0xAD;
    append_bytes(payload, junk);
    append_bytes(payload, make_udp(1234, 8080));
    return ipv6_packet(netsec::kNextHeaderHopByHop, payload);
}

Bytes nested_packet(std::size_t inner_levels) {
    Bytes udp = make_udp(1234, 8080);
    Bytes inner = udp;
    for (std::size_t i = 0; i < inner_levels; ++i) {
        Bytes layer(netsec::kIpv6FixedHeaderSize, 0);
        layer[0] = 0x60;
        set_u16(layer, 4, static_cast<std::uint16_t>(inner.size()));
        layer[6] = (i == 0) ? netsec::kNextHeaderUdp
                            : netsec::kNextHeaderIpv6;
        layer[7] = 32;
        for (std::size_t j = 0; j < 16; ++j) {
            layer[8 + j] = static_cast<std::uint8_t>(i + j);
            layer[24 + j] = static_cast<std::uint8_t>(i + 16 + j);
        }
        append_bytes(layer, inner);
        inner = std::move(layer);
    }
    return ipv6_packet(netsec::kNextHeaderIpv6, inner);
}

Bytes fragment_packet(std::uint16_t fragment_offset, bool more_fragments,
                      bool include_udp) {
    Bytes fragment(8, 0);
    fragment[0] = netsec::kNextHeaderUdp;
    const std::uint16_t field =
        static_cast<std::uint16_t>(fragment_offset |
                                   (more_fragments ? 1U : 0U));
    set_u16(fragment, 2, field);
    set_u32(fragment, 4, 0xAABBCCDD);
    if (include_udp) {
        append_bytes(fragment, make_udp(1234, 8080));
    }
    return ipv6_packet(netsec::kNextHeaderFragment, fragment);
}

Bytes esp_packet() {
    Bytes payload(8, 0);
    payload[0] = netsec::kNextHeaderUdp;
    payload[1] = 0;
    set_u32(payload, 0, 0x01020304);
    set_u32(payload, 4, 1);
    return ipv6_packet(netsec::kNextHeaderEsp, payload);
}

void test_legal_chain_and_rule_match() {
    const Bytes packet = full_extension_chain_packet();
    const auto result = netsec::parse_ipv6_extension_chain(packet);

    REQUIRE(result.ok());
    REQUIRE(result.extension_headers.size() == 5);
    REQUIRE(result.extension_headers[0].type == netsec::kNextHeaderHopByHop);
    REQUIRE(result.extension_headers[1].type == netsec::kNextHeaderRouting);
    REQUIRE(result.extension_headers[2].type == netsec::kNextHeaderFragment);
    REQUIRE(result.extension_headers[3].type == netsec::kNextHeaderDestinationOptions);
    REQUIRE(result.extension_headers[4].type == netsec::kNextHeaderAh);
    REQUIRE(result.extension_headers[4].length == 12);
    REQUIRE(result.transport_offset.has_value());
    REQUIRE(*result.transport_offset == 92);
    REQUIRE(result.upper_layer_protocol == netsec::kNextHeaderUdp);
    REQUIRE(result.source_port == 1234);
    REQUIRE(result.destination_port == 8080);
    REQUIRE(result.payload_kind == netsec::PayloadKind::UpperLayer);

    netsec::TransportRule matching;
    matching.protocol = netsec::kNextHeaderUdp;
    matching.destination_port = 8080;
    REQUIRE(netsec::rule_matches(result, matching));

    netsec::TransportRule wrong_port;
    wrong_port.protocol = netsec::kNextHeaderUdp;
    wrong_port.destination_port = 9090;
    REQUIRE(!netsec::rule_matches(result, wrong_port));
}

void test_unknown_extension_is_rejected_instead_of_skipped() {
    for (const std::uint8_t unknown : {135, 139, 140, 253, 254}) {
        const auto result = netsec::parse_ipv6_extension_chain(
            unknown_extension_packet(unknown));
        REQUIRE(result.status == netsec::ParseStatus::UnknownExtensionHeader);
        REQUIRE(!result.ok());
        REQUIRE(result.error_offset == 48);
        REQUIRE(!result.transport_offset.has_value());
        REQUIRE(!result.source_port.has_value());
        REQUIRE(result.payload_kind == netsec::PayloadKind::None);
    }

    netsec::ParsePolicy policy;
    policy.unknown_extension_policy =
        netsec::UnknownExtensionPolicy::TreatAsOpaqueUpperLayer;
    const auto opaque = netsec::parse_ipv6_extension_chain(
        unknown_extension_packet(253), policy);
    REQUIRE(opaque.ok());
    REQUIRE(opaque.payload_kind == netsec::PayloadKind::OpaqueProtocol);
    REQUIRE(opaque.upper_layer_protocol == 253);
    REQUIRE(!opaque.has_ports());
}

void test_chain_limits() {
    const auto packet = many_destination_headers(6);

    netsec::ParsePolicy policy;
    policy.max_extension_headers = 4;
    const auto count_limited =
        netsec::parse_ipv6_extension_chain(packet, policy);
    REQUIRE(count_limited.status == netsec::ParseStatus::ExtensionChainTooLong);
    REQUIRE(count_limited.error_offset == 72);
    REQUIRE(count_limited.extension_headers.size() == 4);
    REQUIRE(!count_limited.transport_offset.has_value());

    netsec::ParsePolicy byte_policy;
    byte_policy.max_chain_bytes = 24;
    const auto bytes_limited =
        netsec::parse_ipv6_extension_chain(packet, byte_policy);
    REQUIRE(bytes_limited.status == netsec::ParseStatus::ExtensionChainTooLarge);
    REQUIRE(bytes_limited.extension_headers.size() == 3);
    REQUIRE(!bytes_limited.source_port.has_value());
}

void test_malformed_lengths_and_truncation() {
    const auto truncated =
        netsec::parse_ipv6_extension_chain(truncated_extension_packet());
    REQUIRE(truncated.status == netsec::ParseStatus::InvalidExtensionHeader);
    REQUIRE(truncated.error_offset == 41);
    REQUIRE(!truncated.transport_offset.has_value());

    const auto bad_option =
        netsec::parse_ipv6_extension_chain(bad_option_length_packet());
    REQUIRE(bad_option.status == netsec::ParseStatus::InvalidExtensionHeader);
    REQUIRE(bad_option.error_offset == 42);
    REQUIRE(bad_option.extension_headers.empty());

    const auto bad_ah =
        netsec::parse_ipv6_extension_chain(bad_ah_length_packet());
    REQUIRE(bad_ah.status == netsec::ParseStatus::InvalidExtensionHeader);
    REQUIRE(bad_ah.error_offset == 41);
    REQUIRE(bad_ah.extension_headers.empty());

    const auto declared =
        netsec::parse_ipv6_extension_chain(declared_length_controls_offset_packet());
    REQUIRE(declared.ok());
    REQUIRE(declared.transport_offset.has_value());
    REQUIRE(*declared.transport_offset == 48);
    REQUIRE(declared.source_port == 0xDEAD);
    REQUIRE(declared.destination_port == 0x0000);

    netsec::TransportRule rule;
    rule.protocol = netsec::kNextHeaderUdp;
    rule.destination_port = 8080;
    REQUIRE(!netsec::rule_matches(declared, rule));
}

void test_nested_encapsulation_limits() {
    const auto valid = netsec::parse_ipv6_extension_chain(nested_packet(2));
    REQUIRE(valid.ok());
    REQUIRE(valid.nesting_depth == 2);
    REQUIRE(valid.ipv6_layers.size() == 3);
    REQUIRE(valid.transport_offset.has_value());
    REQUIRE(*valid.transport_offset == 120);
    REQUIRE(valid.destination_port == 8080);

    netsec::ParsePolicy policy;
    policy.max_nesting_depth = 1;
    const auto limited = netsec::parse_ipv6_extension_chain(
        nested_packet(2), policy);
    REQUIRE(limited.status == netsec::ParseStatus::NestingLimitExceeded);
    REQUIRE(limited.error_offset == 80);
    REQUIRE(!limited.transport_offset.has_value());
}

void test_fragment_rules() {
    const auto first = netsec::parse_ipv6_extension_chain(
        fragment_packet(0, true, true));
    REQUIRE(first.ok());
    REQUIRE(first.fragment.has_value());
    REQUIRE(first.fragment->fragment_offset == 0);
    REQUIRE(first.fragment->more_fragments);
    REQUIRE(first.transport_offset.has_value());
    REQUIRE(*first.transport_offset == 48);
    REQUIRE(first.destination_port == 8080);

    const auto later = netsec::parse_ipv6_extension_chain(
        fragment_packet(8, true, false));
    REQUIRE(later.ok());
    REQUIRE(later.fragment.has_value());
    REQUIRE(later.fragment->fragment_offset == 8);
    REQUIRE(later.payload_kind == netsec::PayloadKind::NonInitialFragment);
    REQUIRE(later.payload_offset == 48);
    REQUIRE(!later.transport_offset.has_value());
    REQUIRE(!later.has_ports());

    netsec::TransportRule port_rule;
    port_rule.protocol = netsec::kNextHeaderUdp;
    port_rule.destination_port = 8080;
    REQUIRE(!netsec::rule_matches(later, port_rule));
}

void test_esp_is_opaque_and_terminal() {
    const auto esp = netsec::parse_ipv6_extension_chain(esp_packet());
    REQUIRE(esp.ok());
    REQUIRE(esp.payload_kind == netsec::PayloadKind::EncryptedEsp);
    REQUIRE(esp.esp_header_offset == 40);
    REQUIRE(esp.upper_layer_protocol == netsec::kNextHeaderEsp);
    REQUIRE(!esp.transport_offset.has_value());
    REQUIRE(!esp.has_ports());

    netsec::TransportRule port_rule;
    port_rule.protocol = netsec::kNextHeaderUdp;
    port_rule.destination_port = 8080;
    REQUIRE(!netsec::rule_matches(esp, port_rule));

    netsec::ParsePolicy policy;
    policy.max_extension_headers = 0;
    const auto limited =
        netsec::parse_ipv6_extension_chain(esp_packet(), policy);
    REQUIRE(limited.status == netsec::ParseStatus::ExtensionChainTooLong);
}

void test_walker_and_bad_fixed_header() {
    netsec::Ipv6ExtensionWalker walker;
    const auto result = walker.walk(full_extension_chain_packet());
    REQUIRE(result.ok());
    REQUIRE(result.destination_port == 8080);

    Bytes bad_version = full_extension_chain_packet();
    bad_version[0] = 0x40;
    REQUIRE(netsec::parse_ipv6_extension_chain(bad_version).status ==
            netsec::ParseStatus::InvalidVersion);

    const auto truncated =
        netsec::parse_ipv6_extension_chain(Bytes(20, 0));
    REQUIRE(truncated.status == netsec::ParseStatus::TruncatedPacket);
}

}  // namespace

int main() {
    try {
        test_legal_chain_and_rule_match();
        test_unknown_extension_is_rejected_instead_of_skipped();
        test_chain_limits();
        test_malformed_lengths_and_truncation();
        test_nested_encapsulation_limits();
        test_fragment_rules();
        test_esp_is_opaque_and_terminal();
        test_walker_and_bad_fixed_header();
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
