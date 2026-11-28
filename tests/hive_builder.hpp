#ifndef REGHIVE_AUDIT_TEST_HIVE_BUILDER_HPP
#define REGHIVE_AUDIT_TEST_HIVE_BUILDER_HPP

#include <cstdint>
#include <cstring>
#include <vector>

// Minimal, dependency-free Windows Registry Hive image builder used only by
// the test suite.  Produces little-endian regf/hbin/nk/vk/lf/lh/li/ri/db
// byte images.  Cell offsets are reserved at add_cell() time so references
// can be patched before build() performs the two-phase physical layout.

namespace reghive_audit_test {

constexpr uint32_t NONE = 0xFFFFFFFFu;

inline void put16(std::vector<uint8_t>& b, size_t off, uint16_t v) {
    b[off] = static_cast<uint8_t>(v & 0xFFu);
    b[off + 1] = static_cast<uint8_t>((v >> 8) & 0xFFu);
}

inline void put32(std::vector<uint8_t>& b, size_t off, uint32_t v) {
    b[off] = static_cast<uint8_t>(v & 0xFFu);
    b[off + 1] = static_cast<uint8_t>((v >> 8) & 0xFFu);
    b[off + 2] = static_cast<uint8_t>((v >> 16) & 0xFFu);
    b[off + 3] = static_cast<uint8_t>((v >> 24) & 0xFFu);
}

inline uint32_t get32(const std::vector<uint8_t>& b, size_t off) {
    return static_cast<uint32_t>(b[off]) |
           (static_cast<uint32_t>(b[off + 1]) << 8) |
           (static_cast<uint32_t>(b[off + 2]) << 16) |
           (static_cast<uint32_t>(b[off + 3]) << 24);
}

inline uint32_t compute_regf_checksum(const std::vector<uint8_t>& b) {
    uint32_t acc = 0;
    for (size_t i = 0; i < 508; i += 4) {
        acc ^= get32(b, i);
    }
    return acc;
}

inline size_t align8(size_t n) { return (n + 7u) & ~static_cast<size_t>(7u); }

class HiveBuilder {
public:
    struct PlacedCell {
        uint32_t hive_offset = NONE;
    };

    // Append a raw payload (without the 4-byte cell size).  The returned
    // descriptor already carries the final hive-relative offset, so it is
    // safe to embed in other payloads immediately.
    PlacedCell add_cell(std::vector<uint8_t> payload) {
        size_t need = align8(4u + payload.size());
        size_t block = cursor_ / 0x1000;
        size_t within = cursor_ % 0x1000;
        if (within < 0x20) {
            cursor_ = static_cast<uint32_t>(block * 0x1000 + 0x20);
            within = 0x20;
        }
        if (within + need > 0x1000) {
            // Move to the first cell slot of the next 4096-byte hbin.
            cursor_ = static_cast<uint32_t>((block + 1u) * 0x1000 + 0x20);
        }
        PlacedCell cell;
        cell.hive_offset = cursor_;
        cursor_ += static_cast<uint32_t>(need);
        cells_.push_back(Cell{cell.hive_offset, need, std::move(payload)});
        return cell;
    }

    static std::vector<uint8_t> make_nk(
        const std::vector<uint8_t>& name,
        uint32_t parent = NONE,
        uint32_t stable_list = NONE, uint32_t volatile_list = NONE,
        uint32_t stable_count = 0, uint32_t volatile_count = 0,
        uint32_t value_list = NONE, uint32_t value_count = 0) {
        std::vector<uint8_t> p(0x50, 0);
        p[0] = 'n';
        p[1] = 'k';
        put32(p, 0x14, parent);
        put32(p, 0x18, stable_list);
        put32(p, 0x1C, volatile_list);
        put32(p, 0x20, stable_count);
        put32(p, 0x24, volatile_count);
        put32(p, 0x28, value_list);
        put32(p, 0x2C, value_count);
        put16(p, 0x4C, static_cast<uint16_t>(name.size()));
        p.insert(p.end(), name.begin(), name.end());
        return p;
    }

    // short_count = true for lf/lh (u16 count), false for li/ri (u32).
    static std::vector<uint8_t> make_list(
        char a, char b, const std::vector<uint32_t>& refs,
        bool short_count, bool wide_items) {
        size_t fixed = short_count ? 6u : 8u;
        size_t item = wide_items ? 8u : 4u;
        std::vector<uint8_t> p(fixed, 0);
        p[0] = static_cast<uint8_t>(a);
        p[1] = static_cast<uint8_t>(b);
        if (short_count) {
            put16(p, 4, static_cast<uint16_t>(refs.size()));
        } else {
            put32(p, 4, static_cast<uint32_t>(refs.size()));
        }
        for (uint32_t ref : refs) {
            size_t at = p.size();
            p.resize(at + item, 0);
            put32(p, at, ref);
        }
        return p;
    }

    static std::vector<uint8_t> make_vk(
        const std::vector<uint8_t>& name, uint32_t data_size_raw,
        uint32_t data_off, uint16_t name_off) {
        std::vector<uint8_t> p(0x18, 0);
        p[0] = 'v';
        p[1] = 'k';
        put16(p, 0x06, name_off);
        put32(p, 0x08, data_size_raw);
        put32(p, 0x0C, data_off);
        p.insert(p.end(), name.begin(), name.end());
        return p;
    }

    static std::vector<uint8_t> make_value_list(
        const std::vector<uint32_t>& vk_refs) {
        std::vector<uint8_t> p;
        p.resize(vk_refs.size() * 4u);
        for (size_t i = 0; i < vk_refs.size(); ++i) {
            put32(p, i * 4u, vk_refs[i]);
        }
        return p;
    }

    static std::vector<uint8_t> make_plain_data(
        const std::vector<uint8_t>& bytes) {
        return bytes;
    }

    static std::vector<uint8_t> make_db(uint16_t segment_count,
                                        uint32_t segment_list) {
        std::vector<uint8_t> p(0x0E, 0);
        p[0] = 'd';
        p[1] = 'b';
        put16(p, 0x08, segment_count);
        put32(p, 0x0A, segment_list);
        return p;
    }

    static std::vector<uint8_t> make_segment_list(
        const std::vector<uint32_t>& refs) {
        return make_value_list(refs);
    }

    // Build the full hive image.  Physical hbin placement preserves the
    // hive-relative offsets reserved by add_cell(); a cell that does not fit
    // the remaining space of one hbin starts in the next and the gap becomes
    // a positive-length free cell.
    std::vector<uint8_t> build(const PlacedCell& root,
                               uint32_t sequence = 1) {
        size_t hbin_count = 1u;
        for (const Cell& cell : cells_) {
            size_t block = cell.hive_offset / 0x1000;
            if (block + 1u > hbin_count) {
                hbin_count = block + 1u;
            }
        }

        std::vector<uint8_t> image(
            0x1000u + hbin_count * 0x1000u, 0);

        for (const Cell& cell : cells_) {
            size_t block = cell.hive_offset / 0x1000;
            size_t within = cell.hive_offset % 0x1000;
            size_t at = 0x1000u + block * 0x1000u + within;
            put32(image, at,
                  static_cast<uint32_t>(0x80000000u | cell.total_size));
            std::memcpy(image.data() + at + 4u, cell.payload.data(),
                        cell.payload.size());
        }

        // Fill every hbin header plus positive free cells over the gaps.
        for (size_t h = 0; h < hbin_count; ++h) {
            size_t base = 0x1000u + h * 0x1000u;
            image[base] = 'h';
            image[base + 1] = 'b';
            image[base + 2] = 'i';
            image[base + 3] = 'n';
            put32(image, base + 0x04, static_cast<uint32_t>(h) * 0x1000u);
            put32(image, base + 0x08, 0x1000u);

            size_t range_lo = h * 0x1000 + 0x20;
            size_t range_hi = (h + 1u) * 0x1000;
            size_t scan = range_lo;
            for (const Cell& cell : cells_) {
                if (cell.hive_offset < range_lo ||
                    cell.hive_offset >= range_hi) {
                    continue;
                }
                if (cell.hive_offset > scan) {
                    size_t gap = cell.hive_offset - scan;
                    size_t gap_at = base + (scan - h * 0x1000);
                    put32(image, gap_at, static_cast<uint32_t>(gap));
                }
                scan = cell.hive_offset + cell.total_size;
            }
            if (scan < range_hi) {
                size_t gap = range_hi - scan;
                size_t gap_at = base + (scan - h * 0x1000);
                put32(image, gap_at, static_cast<uint32_t>(gap));
            }
        }

        image[0] = 'r';
        image[1] = 'e';
        image[2] = 'g';
        image[3] = 'f';
        put32(image, 0x04, sequence);
        put32(image, 0x08, sequence);
        put32(image, 0x24, root.hive_offset);
        put32(image, 0x28,
              static_cast<uint32_t>(hbin_count) * 0x1000u);
        uint32_t checksum = compute_regf_checksum(image);
        put32(image, 0x1FC, checksum);
        return image;
    }

private:
    struct Cell {
        uint32_t hive_offset = 0;
        size_t total_size = 0;
        std::vector<uint8_t> payload;
    };

    std::vector<Cell> cells_;
    uint32_t cursor_ = 0x20;  // cell area of the first hbin (header-aware)
};

}  // namespace reghive_audit_test

#endif  // REGHIVE_AUDIT_TEST_HIVE_BUILDER_HPP
