// Test-only builder that serialises hand-crafted NTFS FILE records. It is not
// part of the shipped library and intentionally exposes raw mutation hooks so
// tests can simulate on-disk corruption.
#pragma once

#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

namespace mft_test {

inline void putU16(std::vector<uint8_t>& b, size_t off, uint16_t v) {
    b[off] = static_cast<uint8_t>(v);
    b[off + 1] = static_cast<uint8_t>(v >> 8);
}

inline void putU32(std::vector<uint8_t>& b, size_t off, uint32_t v) {
    for (size_t i = 0; i < 4; ++i) {
        b[off + i] = static_cast<uint8_t>(v >> (8U * i));
    }
}

inline void putU64(std::vector<uint8_t>& b, size_t off, uint64_t v) {
    for (size_t i = 0; i < 8; ++i) {
        b[off + i] = static_cast<uint8_t>(v >> (8U * i));
    }
}

inline void putBytes(std::vector<uint8_t>& b, size_t off,
                     const std::vector<uint8_t>& bytes) {
    for (size_t i = 0; i < bytes.size(); ++i) {
        b[off + i] = bytes[i];
    }
}

inline std::vector<uint8_t> utf16(const std::u16string& text) {
    std::vector<uint8_t> out;
    out.reserve(text.size() * 2U);
    for (char16_t unit : text) {
        out.push_back(static_cast<uint8_t>(unit));
        out.push_back(static_cast<uint8_t>(static_cast<uint16_t>(unit) >> 8));
    }
    return out;
}

inline size_t align8(size_t value) { return (value + 7U) & ~size_t{7U}; }

class MftBuilder {
public:
    static constexpr size_t kFirstAttrOffset = 56;

    explicit MftBuilder(size_t recordSize = 1024)
        : record_(recordSize, 0), recordSize_(recordSize) {
        std::memcpy(record_.data(), "FILE", 4);
        // USA starts right after the fixed 42-byte header: sequence number
        // plus one replacement pair per 512-byte sector.
        putU16(record_, 0x04, 42);
        putU16(record_, 0x06,
               static_cast<uint16_t>(recordSize / 512U + 1U));
        putU16(record_, 0x14, static_cast<uint16_t>(kFirstAttrOffset));
        putU16(record_, 0x16, 0x0001);  // in-use flag
        putU32(record_, 0x1C, static_cast<uint32_t>(recordSize));
        putU32(record_, 0x2C, 42);      // record number
    }

    // Applies the update-sequence fixup: writes a chosen sequence number to
    // every sector end and stores the real replacement bytes in the USA.
    void applyFixup(uint16_t sequence = 0x4242) {
        const size_t sectors = recordSize_ / 512U;
        putU16(record_, 42, sequence);
        for (size_t s = 0; s < sectors; ++s) {
            const size_t end = s * 512U + 510U;
            const size_t pair = 44U + s * 2U;
            // Preserve whatever payload bytes currently sit at the sector
            // end, then overwrite the sector end with the sequence number.
            putU16(record_, pair,
                   static_cast<uint16_t>(
                       static_cast<uint16_t>(record_[end]) |
                       (static_cast<uint16_t>(record_[end + 1]) << 8)));
            putU16(record_, end, sequence);
        }
    }

    std::vector<uint8_t>& bytes() { return record_; }
    size_t size() const { return recordSize_; }

    size_t appendResident(uint32_t type,
                          const std::vector<uint8_t>& content,
                          const std::vector<uint8_t>& name = {}) {
        const size_t attrStart = cursor_;
        const uint8_t nameLength =
            static_cast<uint8_t>(name.empty() ? 0U : name.size() / 2U);
        size_t headerSize = 24U;
        if (!name.empty()) {
            headerSize = align8(headerSize + name.size());
        }
        const size_t contentOffset = align8(headerSize);
        const size_t totalLength = align8(contentOffset + content.size());

        putU32(record_, attrStart, type);
        putU32(record_, attrStart + 4, static_cast<uint32_t>(totalLength));
        record_[attrStart + 8] = 0;  // resident
        record_[attrStart + 9] = nameLength;
        if (!name.empty()) {
            putU16(record_, attrStart + 10, 24);
            putBytes(record_, attrStart + 24, name);
        }
        putU16(record_, attrStart + 14, 0);       // attribute id
        putU32(record_, attrStart + 16,
               static_cast<uint32_t>(content.size()));
        putU16(record_, attrStart + 20,
               static_cast<uint16_t>(contentOffset));
        putBytes(record_, attrStart + contentOffset, content);
        cursor_ = attrStart + totalLength;
        return attrStart;
    }

    size_t appendNonResident(uint32_t type,
                             const std::vector<uint8_t>& runList,
                             uint64_t realSize,
                             const std::vector<uint8_t>& name = {}) {
        // Reconstruct the cluster count encoded in the run list so the
        // header's last-VCN field stays internally consistent.
        uint64_t clusterCount = 0;
        size_t scan = 0;
        while (scan < runList.size()) {
            const uint8_t header = runList[scan++];
            const unsigned lenN = header & 0x0FU;
            const unsigned offN = (header >> 4U) & 0x0FU;
            if (lenN == 0U ||
                scan + static_cast<size_t>(lenN + offN) > runList.size()) {
                break;  // deliberately malformed fixture; header VCN stays 0
            }
            uint64_t runLength = 0;
            for (unsigned i = 0; i < lenN; ++i) {
                runLength |= static_cast<uint64_t>(runList[scan + i])
                             << (8U * i);
            }
            clusterCount += runLength;
            scan += static_cast<size_t>(lenN + offN);
        }
        const uint64_t lastVcn =
            clusterCount == 0U ? 0U : clusterCount - 1U;
        const size_t attrStart = cursor_;
        const uint8_t nameLength =
            static_cast<uint8_t>(name.empty() ? 0U : name.size() / 2U);
        size_t headerSize = 64U;
        if (!name.empty()) {
            headerSize = align8(headerSize + name.size());
        }
        const size_t runOffset = align8(headerSize);
        const size_t totalLength =
            align8(runOffset + runList.size() + 1U);

        putU32(record_, attrStart, type);
        putU32(record_, attrStart + 4, static_cast<uint32_t>(totalLength));
        record_[attrStart + 8] = 1;  // non-resident
        record_[attrStart + 9] = nameLength;
        if (!name.empty()) {
            putU16(record_, attrStart + 10, 64);
            putBytes(record_, attrStart + 64, name);
        }
        putU16(record_, attrStart + 14, 0);       // attribute id
        putU64(record_, attrStart + 16, 0);       // start VCN
        putU64(record_, attrStart + 24, lastVcn);
        putU16(record_, attrStart + 32,
               static_cast<uint16_t>(runOffset));
        putU64(record_, attrStart + 40,
               align8(realSize));                 // allocated size
        putU64(record_, attrStart + 48, realSize);
        putBytes(record_, attrStart + runOffset, runList);
        record_[attrStart + runOffset + runList.size()] = 0;  // terminator
        cursor_ = attrStart + totalLength;
        return attrStart;
    }

    void appendEndMarker() {
        putU32(record_, cursor_, 0xFFFFFFFFU);
        cursor_ += 4U;
    }

    // Raw accessor used by corruption tests.
    uint8_t& at(size_t offset) { return record_[offset]; }

private:
    std::vector<uint8_t> record_;
    size_t recordSize_;
    size_t cursor_ = kFirstAttrOffset;
};

inline std::vector<uint8_t> standardInformationContent() {
    std::vector<uint8_t> c(48, 0);
    putU64(c, 0, 0x01D123456789ABCDULL);  // creation
    putU64(c, 8, 0x01D123456789ABCEULL);  // modification
    putU64(c, 16, 0x01D123456789ABCFULL); // MFT change
    putU64(c, 24, 0x01D123456789ABD0ULL); // access
    putU32(c, 32, 0x00000020U);            // archive attribute
    return c;
}

inline std::vector<uint8_t> fileNameContent(
    const std::u16string& name = u"report.txt",
    uint64_t realSize = 128) {
    std::vector<uint8_t> c;
    c.resize(66U + name.size() * 2U, 0);
    putU64(c, 0, 0x0005000000000005ULL);  // parent dir ref
    putU64(c, 40, 0);                     // allocated size
    putU64(c, 48, realSize);
    putU32(c, 56, 0x00000020U);           // flags
    c[64] = static_cast<uint8_t>(name.size());
    c[65] = 3;                            // WIN32 namespace
    const std::vector<uint8_t> raw = utf16(name);
    putBytes(c, 66, raw);
    return c;
}

// Builds a Microsoft reparse block (symlink or mount point) including the
// resident content header (ReparseDataLength + ReparseTag).
inline std::vector<uint8_t> reparseContent(
    uint32_t tag, const std::u16string& substitute,
    const std::u16string& print) {
    const std::vector<uint8_t> sub = utf16(substitute);
    const std::vector<uint8_t> pri = utf16(print);
    // Symlink adds a 4-byte flags word after the four offset fields; mount
    // points do not.
    const uint16_t extra = (tag == 0xA000000CU) ? 4U : 0U;
    const uint16_t subOffset = extra;
    const uint16_t printOffset =
        static_cast<uint16_t>(subOffset + sub.size());
    const uint16_t pathBytes =
        static_cast<uint16_t>(sub.size() + pri.size());
    const uint32_t dataLength =
        8U + extra + static_cast<uint32_t>(pathBytes);

    std::vector<uint8_t> c(8U + dataLength, 0);
    putU32(c, 0, dataLength);
    putU32(c, 4, tag);
    const size_t block = 8;
    putU16(c, block + 0, subOffset);
    putU16(c, block + 2, static_cast<uint16_t>(sub.size()));
    putU16(c, block + 4, printOffset);
    putU16(c, block + 6, static_cast<uint16_t>(pri.size()));
    putBytes(c, block + 8U + subOffset, sub);
    putBytes(c, block + 8U + printOffset, pri);
    return c;
}

// Encodes a sequence of (lengthClusters, deltaLcn) run descriptors. A
// negative delta exercises the signed LCN difference encoding.
inline std::vector<uint8_t> encodeRuns(
    const std::vector<std::pair<uint64_t, int64_t>>& descriptors) {
    std::vector<uint8_t> out;
    int64_t previous = 0;
    for (const auto& [length, lcn] : descriptors) {
        unsigned lenN = 1;
        while ((lenN < 8U) &&
               (length >= (uint64_t{1} << (8U * lenN)))) {
            ++lenN;
        }
        if (lcn < 0) {
            // Sparse run: offset nibble zero.
            out.push_back(static_cast<uint8_t>(lenN));
            for (unsigned i = 0; i < lenN; ++i) {
                out.push_back(static_cast<uint8_t>(length >> (8U * i)));
            }
            continue;
        }
        int64_t delta = lcn - previous;
        unsigned offN = 1;
        if (delta >= 0) {
            while (offN < 8U &&
                   (delta >= (int64_t{1} << (8U * offN - 1)))) {
                ++offN;
            }
        } else {
            while (offN < 8U &&
                   (delta < -(int64_t{1} << (8U * offN - 1)) - 1)) {
                ++offN;
            }
        }
        out.push_back(static_cast<uint8_t>(lenN | (offN << 4U)));
        for (unsigned i = 0; i < lenN; ++i) {
            out.push_back(static_cast<uint8_t>(length >> (8U * i)));
        }
        uint64_t raw = static_cast<uint64_t>(delta);
        for (unsigned i = 0; i < offN; ++i) {
            out.push_back(static_cast<uint8_t>(raw >> (8U * i)));
        }
        previous = lcn;
    }
    return out;
}

// Encodes runs from explicit signed LCN deltas, allowing tests to craft
// deltas that resolve to a negative absolute LCN (hostile on-disk input).
inline std::vector<uint8_t> encodeRunsFromDeltas(
    const std::vector<std::pair<uint64_t, int64_t>>& lengthDeltaPairs) {
    std::vector<uint8_t> out;
    for (const auto& [length, delta] : lengthDeltaPairs) {
        unsigned lenN = 1;
        while ((lenN < 8U) &&
               (length >= (uint64_t{1} << (8U * lenN)))) {
            ++lenN;
        }
        unsigned offN = 1;
        if (delta >= 0) {
            while (offN < 8U &&
                   (delta >= (int64_t{1} << (8U * offN - 1)))) {
                ++offN;
            }
        } else {
            while (offN < 8U &&
                   (delta < -(int64_t{1} << (8U * offN - 1)) - 1)) {
                ++offN;
            }
        }
        out.push_back(static_cast<uint8_t>(lenN | (offN << 4U)));
        for (unsigned i = 0; i < lenN; ++i) {
            out.push_back(static_cast<uint8_t>(length >> (8U * i)));
        }
        const uint64_t raw = static_cast<uint64_t>(delta);
        for (unsigned i = 0; i < offN; ++i) {
            out.push_back(static_cast<uint8_t>(raw >> (8U * i)));
        }
    }
    return out;
}

}  // namespace mft_test
