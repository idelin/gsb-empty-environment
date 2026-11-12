#include "glyf_budget.hpp"

#include <cstdint>
#include <iostream>
#include <vector>

using namespace netsec::glyf;

namespace {

// Deterministic xorshift so the run is reproducible without <random> I/O.
uint32_t rngState = 0x12345678u;
uint32_t nextRng() {
    uint32_t x = rngState;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    rngState = x;
    return x;
}

}  // namespace

// Feeds thousands of arbitrary byte strings as glyf/loca to the validator. The
// validator must always return cleanly (accept or reject) without reading out
// of bounds. Run under ASan/UBSan to turn any mis-read into a failure.
int main() {
    for (int iteration = 0; iteration < 20000; ++iteration) {
        uint32_t numGlyphs = 1 + (nextRng() % 8);
        bool longLoca = (nextRng() & 1u) != 0;

        std::vector<uint8_t> loca;
        uint32_t entryCount = numGlyphs + 1;
        if (longLoca) {
            loca.resize(entryCount * 4);
            for (uint32_t i = 0; i < entryCount; ++i) {
                uint32_t value = nextRng() % 96;
                loca[i * 4 + 0] = static_cast<uint8_t>(value >> 24);
                loca[i * 4 + 1] = static_cast<uint8_t>(value >> 16);
                loca[i * 4 + 2] = static_cast<uint8_t>(value >> 8);
                loca[i * 4 + 3] = static_cast<uint8_t>(value);
            }
        } else {
            loca.resize(entryCount * 2);
            for (uint32_t i = 0; i < entryCount; ++i) {
                uint16_t value = static_cast<uint16_t>(nextRng() % 48);
                loca[i * 2 + 0] = static_cast<uint8_t>(value >> 8);
                loca[i * 2 + 1] = static_cast<uint8_t>(value);
            }
        }

        std::vector<uint8_t> glyf(nextRng() % 80);
        for (uint8_t& byte : glyf) {
            byte = static_cast<uint8_t>(nextRng() & 0xFFu);
        }

        GlyfBudget budget;
        budget.maxDepth = 1 + (nextRng() % 4);
        budget.maxTotalPoints = 1 + (nextRng() % 64);
        budget.maxInstructionBytes = 1 + (nextRng() % 64);
        budget.maxComponentsPerGlyph = 1 + (nextRng() % 8);

        GlyfStats stats;
        GlyfResult result =
            validateGlyf(glyf, loca, numGlyphs, longLoca, budget, &stats);

        if (result.error != GlyfError::Ok) {
            if (result.glyphIndex >= numGlyphs &&
                result.error != GlyfError::LocaTruncated &&
                result.error != GlyfError::LocaOffsetOutOfRange) {
                std::cerr << "fuzz: glyph index out of expected range\n";
                return 1;
            }
        }
    }

    std::cout << "fuzz smoke: 20000 random inputs handled cleanly\n";
    return 0;
}
