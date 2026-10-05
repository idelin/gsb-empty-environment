// 演示：生成合成灰度图，用 Floyd-Steinberg 核做二值抖动，输出 PGM。
// 用法: graydiff_demo [宽 高 级数] [输出.pgm]

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <iostream>
#include <string>

#include "graydiff/quantize.hpp"

using namespace graydiff;

int main(int argc, char** argv) {
    int w = argc > 1 ? std::stoi(argv[1]) : 256;
    int h = argc > 2 ? std::stoi(argv[2]) : 160;
    int levels = argc > 3 ? std::stoi(argv[3]) : 2;
    std::string outPath = argc > 4 ? argv[4] : "out.pgm";

    Image img(w, h);
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            double v = 127.5 + 110.0 * std::sin(x * 0.06) * std::cos(y * 0.045) +
                       40.0 * (x - w / 2.0) / w;
            img.at(x, y) = std::max(0, std::min(255, int(std::lround(v))));
        }
    }

    Options opt;
    opt.kernel = Kernel::floydSteinberg();
    opt.levels = levels;
    Result r = quantize(img, opt);

    FILE* f = std::fopen(outPath.c_str(), "w");
    if (!f) {
        std::perror(outPath.c_str());
        return 1;
    }
    std::fprintf(f, "P2\n%d %d\n255\n", w, h);
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x)
            std::fprintf(f, "%d ", r.gray8Of(r.levelIndex[size_t(y) * w + x]));
        std::fprintf(f, "\n");
    }
    std::fclose(f);

    std::cout << "wrote " << outPath << " (" << w << "x" << h << ", levels=" << levels
              << ")\n";
    std::cout << "boundary discarded error ~= " << r.discardedErrorApprox() << "\n";
    return 0;
}
