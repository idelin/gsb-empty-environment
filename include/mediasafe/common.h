// SPDX-License-Identifier: MIT
// mediasafe - raw byte-level structural security audit for PNG / TIFF.
//
// Embeddable, zero-external-dependency C++17 public interface.
#ifndef MEDIASAFE_COMMON_H
#define MEDIASAFE_COMMON_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace mediasafe {

enum class MediaFormat {
    Png,
    Tiff
};

// Host supplied resource ceilings. A value of 0 means "use the library
// default"; any other value is enforced strictly with checked 64-bit math.
struct AuditLimits {
    // Maximum permitted size of a fully decoded pixel buffer (bytes).
    uint64_t max_pixel_bytes = 256ULL * 1024ULL * 1024ULL; // 256 MiB
    // Maximum permitted raw container size (bytes).
    uint64_t max_file_bytes = 64ULL * 1024ULL * 1024ULL;   // 64 MiB
    // Maximum permitted IFD nesting depth (SubIFD / ExifIFD / GPSIFD).
    uint32_t max_ifd_depth = 16;
};

struct AuditResult {
    bool ok = false;
    MediaFormat format = MediaFormat::Png;
    std::string error;
    // Audit bookkeeping (always populated on success).
    uint64_t pixel_bytes = 0; // worst-case decoded pixel payload
    uint64_t width = 0;
    uint64_t height = 0;
    uint32_t ifd_count = 0;   // number of distinct IFDs visited (TIFF)
    uint32_t max_depth = 0;   // deepest IFD nesting observed (TIFF)

    static AuditResult pass(MediaFormat fmt) {
        AuditResult r;
        r.ok = true;
        r.format = fmt;
        return r;
    }
    static AuditResult fail(MediaFormat fmt, std::string msg) {
        AuditResult r;
        r.ok = false;
        r.format = fmt;
        r.error = std::move(msg);
        return r;
    }
};

// Portable unsigned overflow helpers (no compiler built-ins, no UB).
inline bool add_overflow_u64(uint64_t a, uint64_t b, uint64_t& out) {
    out = a + b;
    return out < a;
}

inline bool mul_overflow_u64(uint64_t a, uint64_t b, uint64_t& out) {
    if (a == 0 || b == 0) {
        out = 0;
        return false;
    }
    if (a > (UINT64_MAX / b)) {
        out = 0;
        return true;
    }
    out = a * b;
    return false;
}

// IEEE 802.3 CRC-32 (reflected, poly 0xEDB88320). This is the raw running
// state used by zlib crc32(): it initializes from 0xFFFFFFFF but does NOT
// apply the final XOR. To obtain the stored/wire CRC-32 of a buffer, XOR
// the returned value with 0xFFFFFFFF. Chunked continuation simply passes
// the previous returned state back in.
uint32_t crc32_ieee(const uint8_t* data, std::size_t length,
                    uint32_t crc = 0xFFFFFFFFu);

// PNG audit. Never throws on malformed input; all structural violations are
// reported through AuditResult::ok / error.
AuditResult audit_png(const uint8_t* data, std::size_t size,
                      const AuditLimits& limits = AuditLimits{});

// TIFF audit (II/MM classic 32-bit and BigTIFF).
AuditResult audit_tiff(const uint8_t* data, std::size_t size,
                       const AuditLimits& limits = AuditLimits{});

// Dispatch helper that sniffs the leading signature bytes.
AuditResult audit(const uint8_t* data, std::size_t size,
                  const AuditLimits& limits = AuditLimits{});

inline AuditResult audit(const std::vector<uint8_t>& data,
                         const AuditLimits& limits = AuditLimits{}) {
    return audit(data.data(), data.size(), limits);
}

} // namespace mediasafe

#endif // MEDIASAFE_COMMON_H
