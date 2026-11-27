#include "mediasafe/common.h"

#include <cstddef>
#include <cstdint>

namespace mediasafe {

AuditResult audit(const uint8_t* data, std::size_t size,
                  const AuditLimits& limits) {
    static const uint8_t png_sig[8] = {
        0x89u, 0x50u, 0x4Eu, 0x47u, 0x0Du, 0x0Au, 0x1Au, 0x0Au};

    if (data == nullptr || size == 0) {
        return AuditResult::fail(MediaFormat::Png, "audit: empty input");
    }
    if (size >= 8) {
        bool png = true;
        for (std::size_t i = 0; i < 8; ++i) {
            if (data[i] != png_sig[i]) png = false;
        }
        if (png) {
            return audit_png(data, size, limits);
        }
    }
    if ((data[0] == 0x49u && data[1] == 0x49u) ||
        (data[0] == 0x4Du && data[1] == 0x4Du)) {
        return audit_tiff(data, size, limits);
    }
    return AuditResult::fail(MediaFormat::Png,
                             "audit: unrecognized media signature");
}

} // namespace mediasafe
