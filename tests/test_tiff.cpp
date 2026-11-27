#include "mediasafe/common.h"

#include <cstdint>
#include <cstring>
#include <iostream>
#include <vector>

using mediasafe::AuditLimits;
using mediasafe::AuditResult;

namespace {

int& tiff_checks() {
    static int n = 0;
    return n;
}

#define EXPECT_TIFF_OK(buf, limits)                                         \
    do {                                                                    \
        ++tiff_checks();                                                     \
        AuditResult r_ = mediasafe::audit_tiff((buf).data(), (buf).size(),  \
                                               (limits));                   \
        if (!r_.ok) {                                                       \
            std::cerr << "FAIL expected ok: " << r_.error                   \
                      << " (" << __FILE__ << ":" << __LINE__ << ")\n";      \
            std::abort();                                                   \
        }                                                                   \
    } while (0)

#define EXPECT_TIFF_REJECT(buf, limits, needle)                             \
    do {                                                                    \
        ++tiff_checks();                                                     \
        AuditResult r_ = mediasafe::audit_tiff((buf).data(), (buf).size(),  \
                                               (limits));                   \
        if (r_.ok || r_.error.find(needle) == std::string::npos) {         \
            std::cerr << "FAIL expected rejection containing '" << needle   \
                      << "', got ok=" << r_.ok << " err='" << r_.error      \
                      << "' (" << __FILE__ << ":" << __LINE__ << ")\n";     \
            std::abort();                                                   \
        }                                                                   \
    } while (0)

// Minimal hand-rolled TIFF/BigTIFF container builder. Offsets are managed
// explicitly so malicious samples (cycles, OOB pointers) can be forged.
class TiffBuf {
public:
    explicit TiffBuf(bool little, bool big = false)
        : le_(little), big_(big) {}

    std::size_t size() const { return data_.size(); }
    const std::vector<uint8_t>& buffer() const { return data_; }

    void reserve(std::size_t n) { data_.reserve(n); }

    void u8(uint8_t v) { data_.push_back(v); }

    void u16(uint16_t v) {
        if (le_) {
            data_.push_back(static_cast<uint8_t>(v & 0xFFu));
            data_.push_back(static_cast<uint8_t>((v >> 8) & 0xFFu));
        } else {
            data_.push_back(static_cast<uint8_t>((v >> 8) & 0xFFu));
            data_.push_back(static_cast<uint8_t>(v & 0xFFu));
        }
    }

    void u32(uint32_t v) {
        for (int i = 0; i < 4; ++i) {
            const int shift = le_ ? (i * 8) : (24 - i * 8);
            data_.push_back(static_cast<uint8_t>((v >> shift) & 0xFFu));
        }
    }

    void u64(uint64_t v) {
        for (int i = 0; i < 8; ++i) {
            const int shift = le_ ? (i * 8) : (56 - i * 8);
            data_.push_back(static_cast<uint8_t>((v >> shift) & 0xFFu));
        }
    }

    // Write a small inline value left-justified inside a value slot of
    // `slot` bytes, zero-padding the remainder (TIFF6 left-justification).
    void write_inline(uint64_t v, std::size_t value_bytes,
                      std::size_t slot) {
        for (std::size_t i = 0; i < value_bytes; ++i) {
            const int shift = le_
                ? static_cast<int>(i * 8u)
                : static_cast<int>((value_bytes - 1u - i) * 8u);
            data_.push_back(
                static_cast<uint8_t>((v >> shift) & 0xFFu));
        }
        for (std::size_t i = value_bytes; i < slot; ++i) {
            data_.push_back(0u);
        }
    }

    void pad(std::size_t target) {
        while (data_.size() < target) data_.push_back(0u);
    }

    void patch32(std::size_t pos, uint32_t v) {
        for (int i = 0; i < 4; ++i) {
            const int shift = le_ ? (i * 8) : (24 - i * 8);
            data_[pos + static_cast<std::size_t>(i)] =
                static_cast<uint8_t>((v >> shift) & 0xFFu);
        }
    }

    void patch64(std::size_t pos, uint64_t v) {
        for (int i = 0; i < 8; ++i) {
            const int shift = le_ ? (i * 8) : (56 - i * 8);
            data_[pos + static_cast<std::size_t>(i)] =
                static_cast<uint8_t>((v >> shift) & 0xFFu);
        }
    }

    void fill(std::size_t n, uint8_t v) {
        data_.insert(data_.end(), n, v);
    }

    // TIFF field type constants.
    static constexpr uint16_t T_SHORT = 3;
    static constexpr uint16_t T_LONG = 4;
    static constexpr uint16_t T_LONG8 = 16;

    // Tag constants.
    static constexpr uint16_t TAG_WIDTH = 256;
    static constexpr uint16_t TAG_LENGTH = 257;
    static constexpr uint16_t TAG_BPS = 258;
    static constexpr uint16_t TAG_STRIP_OFF = 273;
    static constexpr uint16_t TAG_SPP = 277;
    static constexpr uint16_t TAG_STRIP_CNT = 279;
    static constexpr uint16_t TAG_SUBIFD = 330;
    static constexpr uint16_t TAG_EXIF = 34665;

    std::size_t header() {
        const std::size_t start = data_.size();
        if (le_) {
            u8(0x49u);
            u8(0x49u);
        } else {
            u8(0x4Du);
            u8(0x4Du);
        }
        u16(big_ ? 43u : 42u);
        if (big_) {
            u16(8); // offset size
            u16(0); // reserved
            u64(0); // IFD0 placeholder
        } else {
            u32(0); // IFD0 placeholder
        }
        return start;
    }

    // One entry; value is placed inline when it fits, else written raw as
    // the 4/8-byte value slot (caller decides whether it is a pointer).
    void entry(uint16_t tag, uint16_t type, uint64_t count,
               uint64_t value_slot) {
        u16(tag);
        u16(type);
        const uint16_t elem = (type == T_SHORT) ? 2u
                            : (type == T_LONG8) ? 8u : 4u;
        const std::size_t slot = big_ ? 8u : 4u;
        if (big_) {
            u64(count);
            if (static_cast<std::size_t>(elem) >= 8u) {
                u64(value_slot);
            } else {
                write_inline(value_slot, elem, slot);
            }
        } else {
            u32(static_cast<uint32_t>(count));
            if (static_cast<std::size_t>(elem) >= 4u) {
                u32(static_cast<uint32_t>(value_slot));
            } else {
                write_inline(value_slot, elem, slot);
            }
        }
    }

    std::size_t ifd_begin(uint16_t entries) {
        const std::size_t pos = data_.size();
        if (big_) {
            u64(entries);
        } else {
            u16(entries);
        }
        return pos;
    }

    void ifd_end(uint64_t next_ifd) {
        if (big_) {
            u64(next_ifd);
        } else {
            u32(static_cast<uint32_t>(next_ifd));
        }
    }

private:
    bool le_;
    bool big_;
    std::vector<uint8_t> data_;
};

// Build a valid classic TIFF with two strips (grayscale 8-bit, 8x8).
std::vector<uint8_t> valid_classic_tiff(bool little) {
    TiffBuf t(little);
    t.reserve(256);
    const std::size_t hdr = t.header();
    const std::size_t ifd0 = t.size();
    // 8x8, 1 sample/pixel, 8 bits, two strips of 32 bytes each.
    const uint16_t n_entries = 6;
    t.ifd_begin(n_entries);
    t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_SHORT, 1, 8);
    t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_SHORT, 1, 8);
    t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, 1, 8);
    t.entry(TiffBuf::TAG_SPP, TiffBuf::T_SHORT, 1, 1);
    t.entry(TiffBuf::TAG_STRIP_OFF, TiffBuf::T_LONG, 2, 0); // patched below
    const std::size_t off_patch = t.size() - 4u;
    t.entry(TiffBuf::TAG_STRIP_CNT, TiffBuf::T_LONG, 2, 0); // patched below
    const std::size_t cnt_patch = t.size() - 4u;
    t.ifd_end(0);

    // Out-of-line arrays come first, then the strip payloads. Each array's
    // declared range (count * type_size = 2 * 4 = 8 bytes) must itself lie
    // fully inside the file, and every strip range must be fully contained.
    const std::size_t off_array = t.size();
    const std::size_t cnt_array = off_array + 8u;
    const std::size_t strip1 = cnt_array + 8u;
    const std::size_t strip2 = strip1 + 32u;
    t.u32(static_cast<uint32_t>(strip1));
    t.u32(static_cast<uint32_t>(strip2));
    t.u32(32);
    t.u32(32);
    t.fill(32, 0xAAu);
    t.fill(32, 0xBBu);
    t.patch32(off_patch, static_cast<uint32_t>(off_array));
    t.patch32(cnt_patch, static_cast<uint32_t>(cnt_array));
    t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
    (void)off_patch;
    return t.buffer();
}

void run_tiff_tests() {
    const AuditLimits lim{};

    // --- Valid samples ---------------------------------------------------
    EXPECT_TIFF_OK(valid_classic_tiff(true), lim);  // little-endian II
    EXPECT_TIFF_OK(valid_classic_tiff(false), lim); // big-endian MM

    // Valid BigTIFF (single strip, inline 8-byte offsets).
    {
        TiffBuf t(true, true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(6);
        const std::size_t strip =
            ifd0 + 8u + 6u * 20u + 8u;
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_LONG8, 1, 4);
        t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_LONG8, 1, 4);
        t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_SPP, TiffBuf::T_SHORT, 1, 1);
        t.entry(TiffBuf::TAG_STRIP_OFF, TiffBuf::T_LONG8, 1,
                static_cast<uint64_t>(strip));
        t.entry(TiffBuf::TAG_STRIP_CNT, TiffBuf::T_LONG8, 1, 16);
        t.ifd_end(0);
        t.fill(16, 0xCCu);
        t.patch64(hdr + 8u, static_cast<uint64_t>(ifd0));
        EXPECT_TIFF_OK(t.buffer(), lim);
    }

    // --- Bad magic -------------------------------------------------------
    {
        std::vector<uint8_t> b = valid_classic_tiff(true);
        b[0] = 0x00u;
        EXPECT_TIFF_REJECT(b, lim, "magic");
    }

    // --- IFD self-referencing cycle (infinite loop DoS) ------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(1);
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_SHORT, 1, 8);
        t.ifd_end(static_cast<uint64_t>(ifd0)); // points back to itself
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "cycle");
    }

    // --- Out-of-bounds SubIFD pointer ------------------------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(1);
        t.entry(TiffBuf::TAG_SUBIFD, TiffBuf::T_LONG, 1,
                static_cast<uint64_t>(t.size() + 4096u)); // beyond EOF
        t.ifd_end(0);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "outside file");
    }

    // --- ExifIFD pointing before the header ------------------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(1);
        t.entry(TiffBuf::TAG_EXIF, TiffBuf::T_LONG, 1, 2u);
        t.ifd_end(0);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "outside file");
    }

    // --- Excessive SubIFD nesting depth ----------------------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t first = t.size();
        constexpr int chain = 40;
        // Layout chain linearly; each IFD has one SubIFD pointer to the
        // next IFD position.
        std::vector<std::size_t> ifd_pos;
        for (int i = 0; i < chain; ++i) {
            ifd_pos.push_back(t.size());
            t.ifd_begin(1);
            t.entry(TiffBuf::TAG_SUBIFD, TiffBuf::T_LONG, 1, 0); // patch
            t.ifd_end(0);
        }
        for (int i = 0; i < chain; ++i) {
            const std::size_t entry_value = ifd_pos[static_cast<std::size_t>(i)]
                                            + 2u + 8u;
            if (i + 1 < chain) {
                t.patch32(entry_value,
                          static_cast<uint32_t>(
                              ifd_pos[static_cast<std::size_t>(i + 1)]));
            }
        }
        t.patch32(hdr + 4u, static_cast<uint32_t>(first));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "depth");
    }

    // --- Main IFD chain cycle: IFD0 -> IFD1 -> IFD0 ----------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(1);
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_SHORT, 1, 8);
        const std::size_t ifd1_pos_placeholder = t.size();
        t.ifd_end(0);
        (void)ifd1_pos_placeholder;
        const std::size_t ifd1 = t.size();
        t.ifd_begin(1);
        t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_SHORT, 1, 8);
        t.ifd_end(static_cast<uint64_t>(ifd0)); // loop back
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        t.patch32(ifd0 + 2u + 12u, static_cast<uint32_t>(ifd1));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "cycle");
    }

    // --- Out-of-bounds tag value pointer ---------------------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(1);
        // 10 SHORTs = 20 bytes, cannot fit inline, pointer past EOF.
        t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, 10,
                static_cast<uint64_t>(t.size() + 2048u));
        t.ifd_end(0);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "outside file");
    }

    // --- count * type_size overflow (huge count) -------------------------
    {
        TiffBuf t(true, true); // BigTIFF so an 8-byte count can be encoded
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(1);
        t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, UINT64_MAX, 0);
        t.ifd_end(0);
        t.patch64(hdr + 8u, static_cast<uint64_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "overflow");
    }

    // --- StripOffsets / StripByteCounts count mismatch -------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(3);
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_STRIP_OFF, TiffBuf::T_LONG, 1,
                static_cast<uint64_t>(ifd0 + 64u));
        t.ifd_end(0);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        t.fill(32, 0x01u);
        EXPECT_TIFF_REJECT(t.buffer(), lim, "must pair");
    }

    // --- Strip range beyond EOF ------------------------------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(5);
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_STRIP_OFF, TiffBuf::T_LONG, 1,
                static_cast<uint64_t>(ifd0 + 60u));
        t.entry(TiffBuf::TAG_STRIP_CNT, TiffBuf::T_LONG, 1, 1024u);
        t.ifd_end(0);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "beyond file");
    }

    // --- Overlapping strips (Strip Overlap) ------------------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(6);
        const std::size_t strip = ifd0 + 2u + 6u * 12u + 4u;
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_STRIP_OFF, TiffBuf::T_LONG, 2, 0);
        const std::size_t off_patch = t.size() - 4u;
        t.entry(TiffBuf::TAG_STRIP_CNT, TiffBuf::T_LONG, 2, 0);
        const std::size_t cnt_patch = t.size() - 4u;
        t.entry(TiffBuf::TAG_SPP, TiffBuf::T_SHORT, 1, 1);
        t.ifd_end(0);
        t.fill(64, 0xDDu);
        const std::size_t off_array = t.size();
        t.u32(static_cast<uint32_t>(strip));
        t.u32(static_cast<uint32_t>(strip + 8u)); // overlaps first strip
        const std::size_t cnt_array = t.size();
        t.u32(32);
        t.u32(32);
        t.patch32(off_patch, static_cast<uint32_t>(off_array));
        t.patch32(cnt_patch, static_cast<uint32_t>(cnt_array));
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "overlap");
    }

    // --- Pixel budget: 64-bit multiplication overflow --------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(5);
        const std::size_t strip = ifd0 + 2u + 5u * 12u + 4u;
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_LONG, 1, 0x40000000u);
        t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_LONG, 1, 0x40000000u);
        t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_STRIP_OFF, TiffBuf::T_LONG, 1,
                static_cast<uint64_t>(strip));
        t.entry(TiffBuf::TAG_STRIP_CNT, TiffBuf::T_LONG, 1, 8u);
        t.ifd_end(0);
        t.fill(8, 0xEEu);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "overflow");
    }

    // --- Pixel budget: no wrap, but exceeds the host ceiling -------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(5);
        const std::size_t strip = ifd0 + 2u + 5u * 12u + 4u;
        t.entry(TiffBuf::TAG_WIDTH, TiffBuf::T_LONG, 1, 20000u);
        t.entry(TiffBuf::TAG_LENGTH, TiffBuf::T_LONG, 1, 20000u);
        t.entry(TiffBuf::TAG_BPS, TiffBuf::T_SHORT, 1, 8);
        t.entry(TiffBuf::TAG_STRIP_OFF, TiffBuf::T_LONG, 1,
                static_cast<uint64_t>(strip));
        t.entry(TiffBuf::TAG_STRIP_CNT, TiffBuf::T_LONG, 1, 8u);
        t.ifd_end(0);
        t.fill(8, 0xEEu);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "host limit");
    }

    // --- ExifIFD cycle: Exif IFD points back to IFD0 ---------------------
    {
        TiffBuf t(true);
        const std::size_t hdr = t.header();
        const std::size_t ifd0 = t.size();
        t.ifd_begin(1);
        t.entry(TiffBuf::TAG_EXIF, TiffBuf::T_LONG, 1,
                static_cast<uint64_t>(ifd0));
        t.ifd_end(0);
        t.patch32(hdr + 4u, static_cast<uint32_t>(ifd0));
        EXPECT_TIFF_REJECT(t.buffer(), lim, "cycle");
    }
}

} // namespace

void test_tiff() {
    run_tiff_tests();
}
