#pragma once

#include "pki/x509.hpp"

#include <cstdint>
#include <string>
#include <vector>

namespace pki::names {

using x509::GeneralName;
using x509::GeneralNameKind;

struct IpCidr {
    std::vector<std::uint8_t> address;
    std::vector<std::uint8_t> mask;
    int prefix = 0;
};

[[nodiscard]] std::string ascii_lower(std::string value);
[[nodiscard]] bool looks_like_dns_name(const std::string& value);

[[nodiscard]] bool dns_matches_constraint(const std::string& name,
                                          const std::string& constraint);
[[nodiscard]] bool email_matches_constraint(const std::string& email,
                                            const std::string& constraint);
[[nodiscard]] bool ip_matches_constraint(const std::vector<std::uint8_t>& address,
                                         const std::vector<std::uint8_t>& constraint_address,
                                         const std::vector<std::uint8_t>& mask);
[[nodiscard]] bool ip_matches_constraint(const der::Bytes& address,
                                         const GeneralName& constraint);
[[nodiscard]] bool uri_matches_constraint(const std::string& uri,
                                          const std::string& constraint);
[[nodiscard]] bool directory_matches_constraint(const der::Bytes& directory_name,
                                                const der::Bytes& constraint_name);
[[nodiscard]] bool general_name_matches(const GeneralName& name,
                                        const GeneralName& constraint);
[[nodiscard]] std::optional<GeneralName> intersect_general_constraints(
    const GeneralName& left, const GeneralName& right);

[[nodiscard]] bool is_permitted(const std::vector<GeneralName>& names,
                                const std::vector<GeneralName>& permitted);
[[nodiscard]] bool is_excluded(const std::vector<GeneralName>& names,
                               const std::vector<GeneralName>& excluded);

[[nodiscard]] IpCidr parse_ip_cidr(const std::string& cidr);
[[nodiscard]] std::vector<std::uint8_t> parse_ip_address(const std::string& text);
[[nodiscard]] std::vector<std::uint8_t> ip_mask(std::size_t address_bytes, int prefix);

} // namespace pki::names
