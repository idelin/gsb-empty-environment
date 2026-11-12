#pragma once

// netsec::glyf - embeddable TrueType glyf/loca budget validator.
//
// Single header, C++17, no dependencies beyond the standard library. Drop the
// file into a font pipeline and feed it the raw bytes of the 'glyf' and 'loca'
// tables together with maxp.numGlyphs and head.indexToLocFormat.
//
// The validator:
//   * checks that loca has enough entries, every offset stays inside glyf and
//     offsets are monotonically non-decreasing;
//   * fully walks every non-empty glyph, decoding simple glyph flag/coordinate
//     streams and composite component records with strict bounds checking;
//   * expands composite glyphs while bounding recursion depth, the cumulative
//     number of contour points and the number of instruction bytes;
//   * rejects reference cycles, out-of-range component indices and every
//     over-budget condition, reporting the offending glyph number.

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace netsec::glyf {

enum class GlyfError {
    Ok = 0,
    LocaTruncated,             // loca table is too short for numGlyphs
    LocaOffsetOutOfRange,      // a loca offset points past the glyf table
    LocaNotMonotonic,          // a loca offset is smaller than the previous one
    GlyphTruncated,            // a glyph runs past its loca window / glyf table
    GlyphMalformed,            // structurally invalid glyph data
    GlyphIndexOutOfRange,      // composite component index >= numGlyphs
    CompositeCycle,            // composite references an ancestor glyph
    DepthExceeded,             // composite nesting deeper than the budget
    PointBudgetExceeded,       // cumulative contour points over the budget
    InstructionBudgetExceeded, // cumulative instruction bytes over the budget
    ComponentCountExceeded     // too many component records in one composite
};

struct GlyfBudget {
    // Maximum composite nesting depth (the root glyph itself counts as 1).
    uint32_t maxDepth = 32;
    // Maximum contour points accumulated while expanding one root glyph.
    // A component referenced several times pays its points each time.
    uint32_t maxTotalPoints = 65535;
    // Maximum instruction bytes accumulated while expanding one root glyph.
    uint32_t maxInstructionBytes = 65535;
    // Structural safety cap on component records inside a single composite.
    uint32_t maxComponentsPerGlyph = 1024;
};

struct GlyfStats {
    uint32_t glyphsChecked = 0;         // non-empty root glyphs walked
    uint32_t maxDepthObserved = 0;      // deepest composite nesting seen
    uint32_t totalPoints = 0;           // points summed over all root glyphs
    uint32_t totalInstructionBytes = 0; // instructions summed over all roots
};

struct GlyfResult {
    GlyfError error = GlyfError::Ok;
    // Primary offending glyph number.
    uint32_t glyphIndex = 0;
    // Secondary glyph number (e.g. the referenced component); equals
    // glyphIndex when not applicable.
    uint32_t detailIndex = 0;
    // Human-readable description, always mentioning the glyph number(s).
    std::string message;

    explicit operator bool() const noexcept {
        return error == GlyfError::Ok;
    }
};

inline const char* errorName(GlyfError error) noexcept {
    switch (error) {
        case GlyfError::Ok: return "Ok";
        case GlyfError::LocaTruncated: return "LocaTruncated";
        case GlyfError::LocaOffsetOutOfRange: return "LocaOffsetOutOfRange";
        case GlyfError::LocaNotMonotonic: return "LocaNotMonotonic";
        case GlyfError::GlyphTruncated: return "GlyphTruncated";
        case GlyfError::GlyphMalformed: return "GlyphMalformed";
        case GlyfError::GlyphIndexOutOfRange: return "GlyphIndexOutOfRange";
        case GlyfError::CompositeCycle: return "CompositeCycle";
        case GlyfError::DepthExceeded: return "DepthExceeded";
        case GlyfError::PointBudgetExceeded: return "PointBudgetExceeded";
        case GlyfError::InstructionBudgetExceeded:
            return "InstructionBudgetExceeded";
        case GlyfError::ComponentCountExceeded:
            return "ComponentCountExceeded";
    }
    return "Unknown";
}

namespace detail {

inline uint16_t readBe16(const uint8_t* p) noexcept {
    return static_cast<uint16_t>((static_cast<uint16_t>(p[0]) << 8) | p[1]);
}

inline uint32_t readBe32(const uint8_t* p) noexcept {
    return (static_cast<uint32_t>(p[0]) << 24) |
           (static_cast<uint32_t>(p[1]) << 16) |
           (static_cast<uint32_t>(p[2]) << 8) |
           static_cast<uint32_t>(p[3]);
}

// Bounds-checked reader over a glyph's [start, end) loca window.
class Cursor {
public:
    Cursor(const uint8_t* data, std::size_t size) noexcept
        : data_(data), size_(size) {}

    bool ok() const noexcept { return ok_; }
    std::size_t pos() const noexcept { return pos_; }
    std::size_t size() const noexcept { return size_; }

    bool require(std::size_t count) noexcept {
        if (!ok_) return false;
        if (count > size_ - pos_) {
            ok_ = false;
            return false;
        }
        return true;
    }

    uint8_t u8() noexcept {
        if (!require(1)) return 0;
        return data_[pos_++];
    }

    int8_t i8() noexcept {
        return static_cast<int8_t>(u8());
    }

    uint16_t u16() noexcept {
        if (!require(2)) return 0;
        uint16_t value = readBe16(data_ + pos_);
        pos_ += 2;
        return value;
    }

    int16_t i16() noexcept {
        return static_cast<int16_t>(u16());
    }

    void skip(std::size_t count) noexcept {
        if (require(count)) pos_ += count;
    }

private:
    const uint8_t* data_;
    std::size_t size_;
    std::size_t pos_ = 0;
    bool ok_ = true;
};

// Simple glyph flag bits.
inline constexpr uint8_t kFlagOnCurve = 0x01;
inline constexpr uint8_t kFlagXShort = 0x02;
inline constexpr uint8_t kFlagYShort = 0x04;
inline constexpr uint8_t kFlagRepeat = 0x08;
inline constexpr uint8_t kFlagXSame = 0x10;
inline constexpr uint8_t kFlagYSame = 0x20;

// Composite glyph flag bits.
inline constexpr uint16_t kCompArgWords = 0x0001;
inline constexpr uint16_t kCompArgsAreXy = 0x0002;
inline constexpr uint16_t kCompRoundToGrid = 0x0004;
inline constexpr uint16_t kCompScale = 0x0008;
inline constexpr uint16_t kCompMoreComponents = 0x0020;
inline constexpr uint16_t kCompXyScale = 0x0040;
inline constexpr uint16_t kCompTwoByTwo = 0x0080;
inline constexpr uint16_t kCompHaveInstructions = 0x0100;

}  // namespace detail

class GlyfValidator {
public:
    GlyfResult run(const uint8_t* glyf, std::size_t glyfSize,
                   const uint8_t* loca, std::size_t locaSize,
                   uint32_t numGlyphs, bool longLoca,
                   const GlyfBudget& budget, GlyfStats* statsOut) {
        glyf_ = glyf;
        glyfSize_ = glyfSize;
        numGlyphs_ = numGlyphs;
        budget_ = &budget;

        GlyfStats localStats;
        GlyfStats& stats = statsOut ? *statsOut : localStats;
        stats = GlyfStats{};

        if ((glyf_ == nullptr && glyfSize_ != 0) ||
            (loca == nullptr && locaSize != 0)) {
            return fail(GlyfError::GlyphMalformed, 0, 0,
                        "null table buffer with non-zero size");
        }
        if (numGlyphs > 65535) {
            return fail(GlyfError::GlyphMalformed, 0, 0,
                        "numGlyphs " + std::to_string(numGlyphs) +
                        " exceeds the TrueType maximum of 65535");
        }

        GlyfResult locaResult = parseLoca(loca, locaSize, numGlyphs, longLoca);
        if (locaResult.error != GlyfError::Ok) return locaResult;

        onPath_.assign(numGlyphs, 0);

        for (uint32_t glyph = 0; glyph < numGlyphs; ++glyph) {
            uint32_t start = offsets_[glyph];
            uint32_t end = offsets_[glyph + 1];
            if (start == end) continue;  // empty glyph (e.g. .notdef)

            stats.glyphsChecked++;
            uint32_t points = 0;
            uint32_t instructionBytes = 0;
            GlyfResult result =
                expand(glyph, 1, points, instructionBytes, stats);
            if (result.error != GlyfError::Ok) {
                // Always name the root glyph being validated as the primary
                // index; the exact offending component stays in detailIndex.
                if (result.glyphIndex != glyph) {
                    result.detailIndex = result.glyphIndex;
                    result.message += " (root glyph #" +
                                      std::to_string(glyph) +
                                      ", offending glyph #" +
                                      std::to_string(result.detailIndex) + ")";
                }
                result.glyphIndex = glyph;
                return result;
            }
            stats.totalPoints += points;
            stats.totalInstructionBytes += instructionBytes;
        }

        return GlyfResult{};
    }

private:
    const uint8_t* glyf_ = nullptr;
    std::size_t glyfSize_ = 0;
    uint32_t numGlyphs_ = 0;
    const GlyfBudget* budget_ = nullptr;
    std::vector<uint32_t> offsets_;
    std::vector<unsigned char> onPath_;

    GlyfResult fail(GlyfError error, uint32_t glyph, uint32_t detail,
                    std::string message) {
        GlyfResult result;
        result.error = error;
        result.glyphIndex = glyph;
        result.detailIndex = detail;
        result.message = std::move(message);
        return result;
    }

    GlyfResult truncated(uint32_t glyph) {
        return fail(GlyfError::GlyphTruncated, glyph, glyph,
                    "glyph #" + std::to_string(glyph) +
                    " data is truncated within its loca window");
    }

    GlyfResult parseLoca(const uint8_t* loca, std::size_t locaSize,
                         uint32_t numGlyphs, bool longLoca) {
        const std::size_t entrySize = longLoca ? 4u : 2u;
        const std::size_t entries = static_cast<std::size_t>(numGlyphs) + 1;
        if (locaSize < entries * entrySize) {
            return fail(GlyfError::LocaTruncated, 0, 0,
                        "loca table needs " +
                        std::to_string(entries * entrySize) +
                        " bytes but only " + std::to_string(locaSize) +
                        " are available");
        }

        offsets_.resize(entries);
        for (std::size_t i = 0; i < entries; ++i) {
            if (longLoca) {
                offsets_[i] = detail::readBe32(loca + i * 4);
            } else {
                offsets_[i] =
                    static_cast<uint32_t>(detail::readBe16(loca + i * 2)) * 2u;
            }
        }

        for (uint32_t i = 0; i <= numGlyphs; ++i) {
            if (offsets_[i] > glyfSize_) {
                return fail(GlyfError::LocaOffsetOutOfRange, i, i,
                            "loca offset " + std::to_string(offsets_[i]) +
                            " for glyph #" + std::to_string(i) +
                            " exceeds glyf size " +
                            std::to_string(glyfSize_));
            }
            if (i > 0 && offsets_[i] < offsets_[i - 1]) {
                return fail(GlyfError::LocaNotMonotonic, i - 1, i,
                            "loca offset regresses at glyph #" +
                            std::to_string(i - 1) + ": " +
                            std::to_string(offsets_[i - 1]) + " -> " +
                            std::to_string(offsets_[i]));
            }
        }
        return GlyfResult{};
    }

    GlyfResult addInstructionBytes(uint32_t glyph, uint32_t count,
                                   uint32_t& instructionBytes) {
        uint64_t total = static_cast<uint64_t>(instructionBytes) + count;
        if (total > budget_->maxInstructionBytes) {
            return fail(GlyfError::InstructionBudgetExceeded, glyph, glyph,
                        "instruction budget exceeded at glyph #" +
                        std::to_string(glyph) + " (" +
                        std::to_string(total) + " > " +
                        std::to_string(budget_->maxInstructionBytes) + ")");
        }
        instructionBytes += count;
        return GlyfResult{};
    }

    GlyfResult addPoints(uint32_t glyph, uint32_t count, uint32_t& points) {
        uint64_t total = static_cast<uint64_t>(points) + count;
        if (total > budget_->maxTotalPoints) {
            return fail(GlyfError::PointBudgetExceeded, glyph, glyph,
                        "point budget exceeded at glyph #" +
                        std::to_string(glyph) + " (" +
                        std::to_string(total) + " > " +
                        std::to_string(budget_->maxTotalPoints) + ")");
        }
        points += count;
        return GlyfResult{};
    }

    GlyfResult expand(uint32_t glyph, uint32_t depth, uint32_t& points,
                      uint32_t& instructionBytes, GlyfStats& stats) {
        if (onPath_[glyph] != 0) {
            return fail(GlyfError::CompositeCycle, glyph, glyph,
                        "composite reference cycle detected at glyph #"+
                        std::to_string(glyph));
        }
        if (depth > budget_->maxDepth) {
            return fail(GlyfError::DepthExceeded, glyph, glyph,
                        "composite recursion depth " + std::to_string(depth) +
                        " exceeds budget " +
                        std::to_string(budget_->maxDepth) +
                        " at glyph #" + std::to_string(glyph));
        }
        if (depth > stats.maxDepthObserved) stats.maxDepthObserved = depth;

        // Empty glyphs are only valid as standalone roots (skipped in run).
        // A composite pointing at one has no contours to contribute.
        if (offsets_[glyph] == offsets_[glyph + 1]) {
            return fail(GlyfError::GlyphMalformed, glyph, glyph,
                        "composite references empty glyph #" +
                        std::to_string(glyph));
        }

        struct PathGuard {
            std::vector<unsigned char>& onPath;
            uint32_t id;
            PathGuard(std::vector<unsigned char>& path, uint32_t glyphId)
                : onPath(path), id(glyphId) {
                onPath[id] = 1;
            }
            ~PathGuard() { onPath[id] = 0; }
        } guard(onPath_, glyph);

        detail::Cursor cursor(glyf_ + offsets_[glyph],
                              offsets_[glyph + 1] - offsets_[glyph]);
        int16_t contourCount = cursor.i16();
        if (!cursor.ok()) return truncated(glyph);

        if (contourCount >= 0) {
            // Simple glyph layout: numberOfContours then immediately the
            // endPtsOfContours array (no bounding-box skip here).
            return parseSimple(cursor, glyph, contourCount, points,
                               instructionBytes);
        }
        if (contourCount == -1) {
            // Skip the fixed bounding box (xMin, yMin, xMax, yMax) before the
            // component records. Simple glyphs instead continue directly with
            // endPtsOfContours at this cursor position.
            cursor.skip(8);
            if (!cursor.ok()) return truncated(glyph);
            return parseComposite(cursor, glyph, depth, points,
                                  instructionBytes, stats);
        }
        return fail(GlyfError::GlyphMalformed, glyph, glyph,
                    "glyph #" + std::to_string(glyph) +
                    " has invalid numberOfContours " +
                    std::to_string(contourCount));
    }

    GlyfResult parseSimple(detail::Cursor& cursor, uint32_t glyph,
                           int16_t contourCount, uint32_t& points,
                           uint32_t& instructionBytes) {
        if (contourCount > 32767) {
            return fail(GlyfError::GlyphMalformed, glyph, glyph,
                        "glyph #" + std::to_string(glyph) +
                        " contour count " + std::to_string(contourCount) +
                        " exceeds 32767");
        }

        uint32_t contourCountU = static_cast<uint32_t>(contourCount);
        uint32_t glyphPoints = 0;
        bool firstContour = true;
        uint16_t previousEnd = 0;
        for (uint32_t i = 0; i < contourCountU; ++i) {
            uint16_t endPoint = cursor.u16();
            if (!cursor.ok()) return truncated(glyph);
            if (!firstContour && endPoint <= previousEnd) {
                return fail(GlyfError::GlyphMalformed, glyph, glyph,
                            "glyph #" + std::to_string(glyph) +
                            " endPtsOfContours not increasing");
            }
            firstContour = false;
            previousEnd = endPoint;
            glyphPoints = static_cast<uint32_t>(endPoint) + 1;
        }
        if (glyphPoints > 65535) {
            return fail(GlyfError::GlyphMalformed, glyph, glyph,
                        "glyph #" + std::to_string(glyph) +
                        " point count " + std::to_string(glyphPoints) +
                        " exceeds 65535");
        }

        uint16_t instructionLength = cursor.u16();
        if (!cursor.ok()) return truncated(glyph);
        GlyfResult budget =
            addInstructionBytes(glyph, instructionLength, instructionBytes);
        if (budget.error != GlyfError::Ok) return budget;
        cursor.skip(instructionLength);
        if (!cursor.ok()) return truncated(glyph);

        // Decode the flag run. Flags may be repeated via the repeat count.
        std::vector<uint8_t> flags;
        flags.reserve(glyphPoints);
        while (flags.size() < glyphPoints) {
            uint8_t flag = cursor.u8();
            if (!cursor.ok()) return truncated(glyph);
            uint32_t repeat = 1;
            if ((flag & detail::kFlagRepeat) != 0) {
                repeat = cursor.u8();
                if (!cursor.ok()) return truncated(glyph);
                if (repeat == 0) {
                    return fail(GlyfError::GlyphMalformed, glyph, glyph,
                                "glyph #" + std::to_string(glyph) +
                                " has zero flag repeat count");
                }
            }
            if (repeat > glyphPoints - flags.size()) {
                return fail(GlyfError::GlyphMalformed, glyph, glyph,
                            "glyph #" + std::to_string(glyph) +
                            " flag run covers more than the point count");
            }
            for (uint32_t i = 0; i < repeat; ++i) flags.push_back(flag);
        }

        // Validate the x-coordinate stream length.
        for (uint8_t flag : flags) {
            if ((flag & detail::kFlagXShort) != 0) {
                cursor.skip(1);
            } else if ((flag & detail::kFlagXSame) == 0) {
                cursor.skip(2);
            }
            if (!cursor.ok()) return truncated(glyph);
        }
        // Validate the y-coordinate stream length.
        for (uint8_t flag : flags) {
            if ((flag & detail::kFlagYShort) != 0) {
                cursor.skip(1);
            } else if ((flag & detail::kFlagYSame) == 0) {
                cursor.skip(2);
            }
            if (!cursor.ok()) return truncated(glyph);
        }

        budget = addPoints(glyph, glyphPoints, points);
        if (budget.error != GlyfError::Ok) return budget;

        // Remaining bytes inside the loca window are the required even-length
        // glyf padding (loca offsets are always even); every required field was
        // bounds-checked above, so trailing padding is benign.
        return GlyfResult{};
    }

    GlyfResult parseComposite(detail::Cursor& cursor, uint32_t glyph,
                              uint32_t depth, uint32_t& points,
                              uint32_t& instructionBytes,
                              GlyfStats& stats) {
        uint16_t flags = 0;
        uint32_t componentCount = 0;
        bool haveInstructions = false;

        do {
            flags = cursor.u16();
            uint16_t componentIndex = cursor.u16();
            if (!cursor.ok()) return truncated(glyph);

            // Argument record: two int16 (word) or two uint8 (byte).
            cursor.skip((flags & detail::kCompArgWords) != 0 ? 4 : 2);

            // Exactly one transform kind may be present.
            int transformKinds = ((flags & detail::kCompScale) != 0) +
                                 ((flags & detail::kCompXyScale) != 0) +
                                 ((flags & detail::kCompTwoByTwo) != 0);
            if (transformKinds > 1) {
                return fail(GlyfError::GlyphMalformed, glyph, componentIndex,
                            "glyph #" + std::to_string(glyph) +
                            " component has multiple transform flags");
            }
            if ((flags & detail::kCompScale) != 0) {
                cursor.skip(2);
            } else if ((flags & detail::kCompXyScale) != 0) {
                cursor.skip(4);
            } else if ((flags & detail::kCompTwoByTwo) != 0) {
                cursor.skip(8);
            }
            if (!cursor.ok()) return truncated(glyph);

            if (++componentCount > budget_->maxComponentsPerGlyph) {
                return fail(GlyfError::ComponentCountExceeded, glyph,
                            componentIndex,
                            "glyph #" + std::to_string(glyph) +
                            " has more than " +
                            std::to_string(budget_->maxComponentsPerGlyph) +
                            " components");
            }
            if (componentIndex >= numGlyphs_) {
                return fail(GlyfError::GlyphIndexOutOfRange, glyph,
                            componentIndex,
                            "glyph #" + std::to_string(glyph) +
                            " references out-of-range component #" +
                            std::to_string(componentIndex));
            }

            haveInstructions =
                (flags & detail::kCompHaveInstructions) != 0;

            GlyfResult child = expand(componentIndex, depth + 1, points,
                                      instructionBytes, stats);
            if (child.error != GlyfError::Ok) return child;
        } while ((flags & detail::kCompMoreComponents) != 0);

        // Composite instruction stream follows the last component record.
        if (haveInstructions) {
            uint16_t instructionLength = cursor.u16();
            if (!cursor.ok()) return truncated(glyph);
            GlyfResult budget = addInstructionBytes(
                glyph, instructionLength, instructionBytes);
            if (budget.error != GlyfError::Ok) return budget;
            cursor.skip(instructionLength);
            if (!cursor.ok()) return truncated(glyph);
        }

        // Any remaining bytes are even-length glyf padding, not an error.
        return GlyfResult{};
    }
};

// Validate raw glyf/loca table bytes.
//
// glyf/loca point at the table payloads, numGlyphs is maxp.numGlyphs and
// longLoca is (head.indexToLocFormat == 1). Stats are optional.
inline GlyfResult validateGlyf(const uint8_t* glyf, std::size_t glyfSize,
                               const uint8_t* loca, std::size_t locaSize,
                               uint32_t numGlyphs, bool longLoca,
                               const GlyfBudget& budget = GlyfBudget{},
                               GlyfStats* statsOut = nullptr) {
    GlyfValidator validator;
    return validator.run(glyf, glyfSize, loca, locaSize, numGlyphs, longLoca,
                         budget, statsOut);
}

inline GlyfResult validateGlyf(const std::vector<uint8_t>& glyf,
                               const std::vector<uint8_t>& loca,
                               uint32_t numGlyphs, bool longLoca,
                               const GlyfBudget& budget = GlyfBudget{},
                               GlyfStats* statsOut = nullptr) {
    return validateGlyf(glyf.data(), glyf.size(), loca.data(), loca.size(),
                        numGlyphs, longLoca, budget, statsOut);
}

}  // namespace netsec::glyf
