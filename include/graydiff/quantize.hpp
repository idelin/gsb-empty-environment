#pragma once

// 图像级驱动：整幅处理与分块处理共用同一个流式引擎。
// 分块只是把同一条 serpentine 像素流切成不同片段送入，
// 因此任意分块方式产生逐位相同的输出与边界丢弃计量。

#include <algorithm>
#include <cstdint>
#include <stdexcept>
#include <vector>

#include "engine.hpp"

namespace graydiff {

struct Image {
    int width = 0, height = 0;
    std::vector<int32_t> pixels;  // 行优先，取值 [0, maxValue]

    Image() = default;
    Image(int w, int h, int32_t fill = 0) : width(w), height(h), pixels(size_t(w) * size_t(h), fill) {}

    int32_t at(int x, int y) const { return pixels[size_t(y) * size_t(width) + size_t(x)]; }
    int32_t& at(int x, int y) { return pixels[size_t(y) * size_t(width) + size_t(x)]; }
};

inline Result quantize(const Image& img, Options opt) {
    Engine eng(img.width, img.height, std::move(opt));
    for (int y = 0; y < img.height; ++y) {
        if (eng.rowDirection(y) == ScanDirection::LeftToRight) {
            for (int x = 0; x < img.width; ++x) eng.feedPixel(img.at(x, y));
        } else {
            for (int x = img.width - 1; x >= 0; --x) eng.feedPixel(img.at(x, y));
        }
    }
    eng.finish();
    return eng.extractResult();
}

// 分块处理：按 tileH 高的条带自上而下；条带内逐全局行，按该行的扫描方向
// 依次送入各 tile 的行段。任何 (tileW, tileH) 都等价于整幅处理。
inline Result quantizeTiled(const Image& img, Options opt, int tileW, int tileH) {
    if (tileW < 1 || tileH < 1) throw std::invalid_argument("tile size must be >= 1");
    Engine eng(img.width, img.height, std::move(opt));
    for (int bandY = 0; bandY < img.height; bandY += tileH) {
        const int yEnd = std::min(bandY + tileH, img.height);
        for (int y = bandY; y < yEnd; ++y) {
            if (eng.rowDirection(y) == ScanDirection::LeftToRight) {
                for (int tx = 0; tx < img.width; tx += tileW) {
                    const int xe = std::min(tx + tileW, img.width);
                    for (int x = tx; x < xe; ++x) eng.feedPixel(img.at(x, y));
                }
            } else {
                const int last = ((img.width - 1) / tileW) * tileW;
                for (int tx = last; tx >= 0; tx -= tileW) {
                    const int xe = std::min(tx + tileW, img.width);
                    for (int x = xe - 1; x >= tx; --x) eng.feedPixel(img.at(x, y));
                }
            }
        }
    }
    eng.finish();
    return eng.extractResult();
}

}  // namespace graydiff
