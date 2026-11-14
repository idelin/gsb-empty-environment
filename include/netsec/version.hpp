#pragma once

#include <compare>
#include <cstdint>
#include <string>
#include <string_view>

namespace netsec {

class ParseError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

struct Version {
    std::uint32_t major{0};
    std::uint32_t minor{0};
    std::uint32_t patch{0};

    constexpr auto operator<=>(const Version&) const noexcept = default;

    [[nodiscard]] std::string to_string() const;

    static Version parse(std::string_view text);
};

std::string to_string(const Version& version);

}  // namespace netsec
