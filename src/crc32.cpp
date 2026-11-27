#include "mediasafe/common.h"

#include <array>

namespace mediasafe {

namespace {

const std::array<uint32_t, 256>& crc_table() {
    static const std::array<uint32_t, 256> table = [] {
        std::array<uint32_t, 256> t{};
        for (uint32_t n = 0; n < 256u; ++n) {
            uint32_t c = n;
            for (int k = 0; k < 8; ++k) {
                c = (c & 1u) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
            }
            t[n] = c;
        }
        return t;
    }();
    return table;
}

} // namespace

uint32_t crc32_ieee(const uint8_t* data, std::size_t length, uint32_t crc) {
    const auto& table = crc_table();
    for (std::size_t i = 0; i < length; ++i) {
        const uint8_t byte = data[i];
        const uint32_t index =
            static_cast<uint32_t>((crc ^ byte) & 0xFFu);
        crc = table[index] ^ (crc >> 8);
    }
    return crc;
}

} // namespace mediasafe
