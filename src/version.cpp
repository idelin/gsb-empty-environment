#include "netsec/version.hpp"

#include <charconv>
#include <stdexcept>

namespace netsec {

namespace {

std::uint32_t parse_component(std::string_view part) {
    if (part.empty()) {
        throw ParseError("empty version component");
    }
    for (char ch : part) {
        if (ch < '0' || ch > '9') {
            throw ParseError("non-numeric version component");
        }
    }
    std::uint32_t value = 0;
    const auto* begin = part.data();
    const auto* end = begin + part.size();
    const auto [ptr, ec] = std::from_chars(begin, end, value);
    if (ec != std::errc{} || ptr != end) {
        throw ParseError("invalid version component");
    }
    return value;
}

}  // namespace

std::string Version::to_string() const {
    return netsec::to_string(*this);
}

Version Version::parse(std::string_view text) {
    const std::size_t first = text.find('.');
    if (first == std::string_view::npos) {
        throw ParseError("version must be MAJOR.MINOR.PATCH");
    }
    const std::size_t second = text.find('.', first + 1);
    if (second == std::string_view::npos || text.find('.', second + 1) != std::string_view::npos) {
        throw ParseError("version must be MAJOR.MINOR.PATCH");
    }

    Version result;
    result.major = parse_component(text.substr(0, first));
    result.minor = parse_component(text.substr(first + 1, second - first - 1));
    result.patch = parse_component(text.substr(second + 1));
    return result;
}

std::string to_string(const Version& version) {
    return std::to_string(version.major) + '.' + std::to_string(version.minor) + '.' +
           std::to_string(version.patch);
}

}  // namespace netsec
