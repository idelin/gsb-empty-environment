// Ad-hoc fuzz smoke test (not part of the shipped test binary).
#include <cstdint>
#include <cstdio>
#include <random>
#include <vector>

#include "dtls13/dtls13.hpp"

using namespace dtls13;

int main() {
    std::mt19937_64 rng(0x91471337ULL);
    RecordLayer layer;
    HandshakeReassembler reassembler;

    for (int iteration = 0; iteration < 20000; ++iteration) {
        const std::size_t length = rng() % 96;
        std::vector<std::uint8_t> datagram(length);
        // Bias bytes toward valid content types and unified-header flags.
        for (std::size_t i = 0; i < length; ++i) {
            if (i == 0) {
                static const std::uint8_t types[] = {20, 21, 22, 23, 25,
                                                      0, 64, 255};
                datagram[i] = types[rng() % 8];
            } else if (i == 1) {
                datagram[i] = static_cast<std::uint8_t>(
                    0x20 | (rng() & 0x1F));
            } else {
                datagram[i] = static_cast<std::uint8_t>(rng() & 0xFF);
            }
        }
        (void)layer.ingest(ByteView(datagram));

        if (length >= kHandshakeHeaderSize) {
            auto parsed = parseHandshakeFragment(ByteView(datagram));
            if (parsed.ok()) {
                (void)reassembler.addFragment(parsed.value);
            }
        }
    }
    std::printf("fuzz smoke done, open slots=%zu\n",
                reassembler.openMessageCount());
    return 0;
}
