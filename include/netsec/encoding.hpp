#pragma once

#include <cstdint>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

[[nodiscard]] std::string to_hex(std::span<const std::uint8_t> bytes);

[[nodiscard]] std::vector<std::uint8_t> from_hex(std::string_view hex);

[[nodiscard]] std::string to_base64(std::span<const std::uint8_t> bytes);

[[nodiscard]] std::vector<std::uint8_t> from_base64(std::string_view base64);

}  // namespace netsec
