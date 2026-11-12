#include "glyf_budget.hpp"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

using namespace netsec::glyf;

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                       \
    do {                                                                  \
        ++g_checks;                                                       \
        if (!(cond)) {                                                    \
            ++g_failures;                                                 \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << " - " \
                      << #cond << "\n";                                   \
        }                                                                 \
    } while (0)

void appendBe16(std::vector<uint8_t>& out, uint32_t value) {
    out.push_back(static_cast<uint8_t>((value >> 8) & 0xFFu));
    out.push_back(static_cast<uint8_t>(value & 0xFFu));
}

void appendBe32(std::vector<uint8_t>& out, uint32_t value) {
    out.push_back(static_cast<uint8_t>((value >> 24) & 0xFFu));
    out.push_back(static_cast<uint8_t>((value >> 16) & 0xFFu));
    out.push_back(static_cast<uint8_t>((value >> 8) & 0xFFu));
    out.push_back(static_cast<uint8_t>(value & 0xFFu));
}

// Builds a glyf table blob and loca offsets from independently encoded glyphs.
class FontBuilder {
public:
    explicit FontBuilder(uint32_t numGlyphs)
        : numGlyphs_(numGlyphs), glyphs_(numGlyphs) {}

    void setGlyph(uint32_t index, std::vector<uint8_t> blob) {
        glyphs_[index] = std::move(blob);
    }

    std::vector<uint32_t> buildGlyf() {
        glyf_.clear();
        std::vector<uint32_t> offsets(numGlyphs_ + 1, 0);
        for (uint32_t i = 0; i < numGlyphs_; ++i) {
            offsets[i] = static_cast<uint32_t>(glyf_.size());
            glyf_.insert(glyf_.end(), glyphs_[i].begin(), glyphs_[i].end());
            if ((glyf_.size() % 2) != 0) glyf_.push_back(0u);  // pad to even
        }
        offsets[numGlyphs_] = static_cast<uint32_t>(glyf_.size());
        return offsets;
    }

    std::vector<uint8_t> locaLong(const std::vector<uint32_t>& offsets) {
        std::vector<uint8_t> out;
        for (uint32_t offset : offsets) appendBe32(out, offset);
        return out;
    }

    std::vector<uint8_t> locaShort(const std::vector<uint32_t>& offsets) {
        std::vector<uint8_t> out;
        for (uint32_t offset : offsets) appendBe16(out, offset / 2);
        return out;
    }

    const std::vector<uint8_t>& glyf() const { return glyf_; }

private:
    uint32_t numGlyphs_;
    std::vector<std::vector<uint8_t>> glyphs_;
    std::vector<uint8_t> glyf_;
};

// A simple glyph with one contour holding `pointCount` points (all coordinates
// encoded as zero/delta-0) and `instructionBytes` of instruction data.
std::vector<uint8_t> simpleGlyph(uint16_t pointCount,
                                 uint16_t instructionBytes = 0) {
    std::vector<uint8_t> out;
    appendBe16(out, 1);                              // numberOfContours
    appendBe16(out, static_cast<uint16_t>(pointCount - 1));  // endPtsOfContours
    appendBe16(out, instructionBytes);               // instructionLength
    for (uint16_t i = 0; i < instructionBytes; ++i) out.push_back(0u);
    // Flags: on-curve (0x01), x/y not short but SAME (0x10|0x20) -> zero
    // delta, so no coordinate bytes follow.
    for (uint16_t i = 0; i < pointCount; ++i) out.push_back(0x31u);
    appendBe16(out, 0);                              // xMin
    appendBe16(out, 0);                              // yMin
    appendBe16(out, 0);                              // xMax
    appendBe16(out, 0);                              // yMax
    return out;
}

struct Component {
    uint16_t glyphIndex;
    uint16_t extraFlags = 0;
};

// A composite glyph referencing the given components. The final component
// clears MORE_COMPONENTS; earlier ones set it.
std::vector<uint8_t> compositeGlyph(const std::vector<Component>& components,
                                    uint16_t instructionBytes = 0,
                                    bool wordArgs = false) {
    std::vector<uint8_t> out;
    appendBe16(out, 0xFFFF);  // numberOfContours = -1
    appendBe16(out, 0);       // xMin
    appendBe16(out, 0);       // yMin
    appendBe16(out, 0);       // xMax
    appendBe16(out, 0);       // yMax
    for (size_t i = 0; i < components.size(); ++i) {
        uint16_t flags = components[i].extraFlags;
        if (wordArgs) flags |= 0x0001;       // ARG_1_AND_2_ARE_WORDS
        if (i + 1 < components.size()) flags |= 0x0020;  // MORE_COMPONENTS
        if (instructionBytes != 0 && i + 1 == components.size())
            flags |= 0x0100;                 // WE_HAVE_INSTRUCTIONS
        appendBe16(out, flags);
        appendBe16(out, components[i].glyphIndex);
        if (wordArgs) {
            appendBe16(out, 0);  // arg1
            appendBe16(out, 0);  // arg2
        } else {
            out.push_back(0u);   // arg1 (byte)
            out.push_back(0u);   // arg2 (byte)
        }
        if ((flags & 0x0008) != 0) {       // WE_HAVE_A_SCALE: one F2Dot14
            appendBe16(out, 0x4000);       // 1.0
        } else if ((flags & 0x0040) != 0) {  // WE_HAVE_AN_X_AND_Y_SCALE
            appendBe16(out, 0x4000);
            appendBe16(out, 0x4000);
        } else if ((flags & 0x0080) != 0) {  // WE_HAVE_A_TWO_BY_TWO
            appendBe16(out, 0x4000);
            appendBe16(out, 0);
            appendBe16(out, 0);
            appendBe16(out, 0x4000);
        }
    }
    if (instructionBytes != 0) {
        appendBe16(out, instructionBytes);
        for (uint16_t i = 0; i < instructionBytes; ++i) out.push_back(0u);
    }
    return out;
}

// 1. A benign font: .notdef empty plus simple and valid composite glyphs.
void testValidFont() {
    FontBuilder builder(4);
    builder.setGlyph(1, simpleGlyph(4));
    builder.setGlyph(2, simpleGlyph(6));
    builder.setGlyph(3, compositeGlyph({{1}, {2}}));
    auto offsets = builder.buildGlyf();

    GlyfStats stats;
    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 4, true,
                     GlyfBudget{}, &stats);
    CHECK(result.error == GlyfError::Ok);
    CHECK(result.glyphIndex == 0);
    CHECK(stats.glyphsChecked == 3);
    CHECK(stats.maxDepthObserved == 2);
    CHECK(stats.totalPoints == 4 + 6 + 4 + 6);

    // Same offsets must validate through the short (16-bit) loca format too.
    GlyfStats shortStats;
    GlyfResult shortResult =
        validateGlyf(builder.glyf(), builder.locaShort(offsets), 4, false,
                     GlyfBudget{}, &shortStats);
    CHECK(shortResult.error == GlyfError::Ok);
    CHECK(shortStats.totalPoints == stats.totalPoints);
}

// 2. Self-referencing composite glyph: glyph #2 points at itself.
void testSelfReferenceCycle() {
    FontBuilder builder(3);
    builder.setGlyph(1, simpleGlyph(3));
    builder.setGlyph(2, compositeGlyph({{2}}));
    auto offsets = builder.buildGlyf();

    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true);
    CHECK(result.error == GlyfError::CompositeCycle);
    CHECK(result.glyphIndex == 2);
}

// 3. Two glyphs referencing each other: #2 -> #3 -> #2.
void testMutualReferenceCycle() {
    FontBuilder builder(4);
    builder.setGlyph(1, simpleGlyph(2));
    builder.setGlyph(2, compositeGlyph({{3}}));
    builder.setGlyph(3, compositeGlyph({{2}}));
    auto offsets = builder.buildGlyf();

    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 4, true);
    CHECK(result.error == GlyfError::CompositeCycle);
    CHECK(result.glyphIndex == 2);
}

// 4. A long acyclic composite chain must trip the depth budget.
void testDeepComposite() {
    const uint32_t numGlyphs = 40;  // glyph 0 empty, 1 simple, 2..39 chain
    FontBuilder builder(numGlyphs);
    builder.setGlyph(1, simpleGlyph(2));
    for (uint32_t i = 2; i < numGlyphs; ++i)
        builder.setGlyph(i, compositeGlyph({{static_cast<uint16_t>(i - 1)}}));
    auto offsets = builder.buildGlyf();

    // Default depth (32) rejects the 38-deep chain and names the deepest glyph.
    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), numGlyphs,
                     true);
    CHECK(result.error == GlyfError::DepthExceeded);
    CHECK(result.glyphIndex == 33);  // first root whose chain exceeds depth 32
    CHECK(result.detailIndex == 1);  // the simple leaf reached at depth 33

    // A generous depth budget makes the same font valid.
    GlyfBudget generous;
    generous.maxDepth = 64;
    GlyfStats stats;
    GlyfResult ok =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), numGlyphs,
                     true, generous, &stats);
    CHECK(ok.error == GlyfError::Ok);
    CHECK(stats.maxDepthObserved == 39);  // root #39 down to simple #1
}

// 5. A loca offset that moves backwards must be rejected.
void testLocaRegression() {
    FontBuilder builder(3);
    builder.setGlyph(0, simpleGlyph(2));  // non-empty so offsets[1] > 0
    builder.setGlyph(1, simpleGlyph(3));
    builder.setGlyph(2, simpleGlyph(3));
    auto offsets = builder.buildGlyf();
    offsets[2] = offsets[1] - 2;   // glyph #2 start moves backwards

    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true);
    CHECK(result.error == GlyfError::LocaNotMonotonic);
    CHECK(result.glyphIndex == 1);
    CHECK(result.detailIndex == 2);
}

// 6. A loca offset past the end of glyf must be reported with the glyph number.
void testLocaOffsetOutOfRange() {
    FontBuilder builder(2);
    builder.setGlyph(1, simpleGlyph(3));
    auto offsets = builder.buildGlyf();
    offsets[2] = static_cast<uint32_t>(builder.glyf().size()) + 10;

    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 2, true);
    CHECK(result.error == GlyfError::LocaOffsetOutOfRange);
    CHECK(result.glyphIndex == 2);
}

// 7. A truncated loca table (too few entries) is rejected before parsing.
void testLocaTruncated() {
    FontBuilder builder(3);
    builder.setGlyph(1, simpleGlyph(2));
    auto offsets = builder.buildGlyf();
    std::vector<uint8_t> loca = builder.locaLong(offsets);
    loca.resize(loca.size() - 4);  // drop the final offset entry

    GlyfResult result =
        validateGlyf(builder.glyf(), loca, 3, true);
    CHECK(result.error == GlyfError::LocaTruncated);
}

// 8. Repeatedly referenced components must make the point total explode.
void testPointBudget() {
    FontBuilder builder(3);
    builder.setGlyph(1, simpleGlyph(10));
    builder.setGlyph(2,
                     compositeGlyph({{1}, {1}, {1}, {1}, {1}}));  // 50 points
    auto offsets = builder.buildGlyf();

    GlyfBudget tight;
    tight.maxTotalPoints = 40;
    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true,
                     tight);
    CHECK(result.error == GlyfError::PointBudgetExceeded);
    CHECK(result.glyphIndex == 2);

    GlyfBudget roomy;
    roomy.maxTotalPoints = 64;
    GlyfStats stats;
    GlyfResult ok =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true,
                     roomy, &stats);
    CHECK(ok.error == GlyfError::Ok);
    CHECK(stats.totalPoints == 10 + 50);
}

// 9. Instruction bytes from both simple and composite glyphs are bounded.
void testInstructionBudget() {
    FontBuilder builder(3);
    builder.setGlyph(1, simpleGlyph(2, 100));
    builder.setGlyph(2, compositeGlyph({{1}}, 100));
    auto offsets = builder.buildGlyf();

    GlyfBudget tight;
    tight.maxInstructionBytes = 150;
    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true,
                     tight);
    CHECK(result.error == GlyfError::InstructionBudgetExceeded);
    CHECK(result.glyphIndex == 2);  // 100 (component) + 100 (own) overflows

    GlyfBudget roomy;
    roomy.maxInstructionBytes = 512;
    GlyfStats stats;
    GlyfResult ok =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true,
                     roomy, &stats);
    CHECK(ok.error == GlyfError::Ok);
    CHECK(stats.totalInstructionBytes == 100 + 100 + 100);
}

// 10. A component index >= numGlyphs is an out-of-range reference.
void testComponentIndexOutOfRange() {
    FontBuilder builder(2);
    builder.setGlyph(1, compositeGlyph({{7}}));
    auto offsets = builder.buildGlyf();

    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 2, true);
    CHECK(result.error == GlyfError::GlyphIndexOutOfRange);
    CHECK(result.glyphIndex == 1);
    CHECK(result.detailIndex == 7);
}

// 11. A glyph whose loca window is too short for its declared data.
void testTruncatedGlyph() {
    FontBuilder builder(2);
    builder.setGlyph(1, simpleGlyph(4));
    auto offsets = builder.buildGlyf();
    std::vector<uint8_t> glyf = builder.glyf();
    // Shrink glyph #1's loca window so its trailing flag/coordinate bytes are
    // no longer addressable, exercising the bounds checks.
    offsets[2] = offsets[1] + 8;

    GlyfResult result =
        validateGlyf(glyf, builder.locaLong(offsets), 2, true);
    CHECK(result.error == GlyfError::GlyphTruncated);
    CHECK(result.glyphIndex == 1);
}

// 12. A composite that points at an empty glyph is malformed.
void testCompositeToEmptyGlyph() {
    FontBuilder builder(3);
    builder.setGlyph(1, compositeGlyph({{2}}));  // glyph #2 stays empty
    auto offsets = builder.buildGlyf();

    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true);
    CHECK(result.error == GlyfError::GlyphMalformed);
    CHECK(result.glyphIndex == 1);   // root glyph being validated
    CHECK(result.detailIndex == 2);  // the empty component it references
}

// 13. Word-argument composite records and a transform flag still parse.
void testCompositeWithWordArgsAndScale() {
    FontBuilder builder(3);
    builder.setGlyph(1, simpleGlyph(3));
    builder.setGlyph(2, compositeGlyph({{1, /*kCompScale*/ 0x0008}}, 0,
                                      /*wordArgs*/ true));
    auto offsets = builder.buildGlyf();

    GlyfStats stats;
    GlyfResult result =
        validateGlyf(builder.glyf(), builder.locaLong(offsets), 3, true,
                     GlyfBudget{}, &stats);
    CHECK(result.error == GlyfError::Ok);
    CHECK(stats.totalPoints == 3 + 3);
}

}  // namespace

int main() {
    testValidFont();
    testSelfReferenceCycle();
    testMutualReferenceCycle();
    testDeepComposite();
    testLocaRegression();
    testLocaOffsetOutOfRange();
    testLocaTruncated();
    testPointBudget();
    testInstructionBudget();
    testComponentIndexOutOfRange();
    testTruncatedGlyph();
    testCompositeToEmptyGlyph();
    testCompositeWithWordArgsAndScale();

    std::cout << g_checks - g_failures << "/" << g_checks
              << " checks passed\n";
    if (g_failures != 0) {
        std::cout << g_failures << " CHECK(S) FAILED\n";
        return 1;
    }
    std::cout << "All glyf budget tests passed\n";
    return g_failures == 0 ? 0 : 1;
}
