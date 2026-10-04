#include "ediff/quantize.h"

#include <algorithm>
#include <stdexcept>

namespace ediff {

Kernel floydSteinberg() {
    return Kernel{{{1, 0, 7}, {-1, 1, 3}, {0, 1, 5}, {1, 1, 1}}, 16};
}

Image::Image(int w, int h, std::vector<int> data) : width(w), height(h), px(std::move(data)) {
    if (w < 0 || h < 0 || px.size() != static_cast<size_t>(w) * static_cast<size_t>(h))
        throw std::invalid_argument("Image: size mismatch");
}

int nearestLevel(int64_t value_num, int64_t den, const std::vector<int>& levels) {
    // 最近灰度级；恰好中点（2*v == (a+b)*den）时取较高级。
    int q = levels.front();
    for (size_t i = 1; i < levels.size(); ++i) {
        __int128 lhs = static_cast<__int128>(2) * value_num;
        __int128 rhs = static_cast<__int128>(levels[i - 1] + levels[i]) * den;
        if (lhs >= rhs)
            q = levels[i];
        else
            break;
    }
    return q;
}

namespace {

void validate(const Image& img, const Options& opt) {
    if (img.width <= 0 || img.height <= 0)
        throw std::invalid_argument("empty image");
    if (opt.levels.size() < 2 || !std::is_sorted(opt.levels.begin(), opt.levels.end()))
        throw std::invalid_argument("levels must be sorted, size >= 2");
    if (opt.kernel.den <= 0)
        throw std::invalid_argument("kernel denominator must be > 0");
    for (const Tap& t : opt.kernel.taps) {
        if (t.dy < 0 || (t.dy == 0 && t.dx <= 0))
            throw std::invalid_argument("tap must target unprocessed pixels (dy>0 or dy==0,dx>0)");
    }
}

int maxDy(const Kernel& k) {
    int m = 0;
    for (const Tap& t : k.taps) m = std::max(m, t.dy);
    return m;
}

// 顺序处理行 [y0, y1)，携带误差行缓冲 state（dy_max+1 行）。
// 这是唯一的扫描实现；quantize 与 quantizeTiled 只是分块不同。
void processRows(const Image& img, const Options& opt, int y0, int y1,
                 std::vector<std::vector<int64_t>>& state, Result& res) {
    const int w = img.width;
    const int64_t den = opt.kernel.den;
    const int dy_max = maxDy(opt.kernel);

    for (int y = y0; y < y1; ++y) {
        const bool forward = (y % 2 == 0) == (opt.start == StartDir::LeftToRight);
        const int x_begin = forward ? 0 : w - 1;
        const int x_end = forward ? w : -1;
        const int x_step = forward ? 1 : -1;

        for (int x = x_begin; x != x_end; x += x_step) {
            const int64_t value_num = static_cast<int64_t>(img.at(x, y)) * den + state[0][x];
            const int q = nearestLevel(value_num, den, opt.levels);
            res.out[static_cast<size_t>(y) * w + x] = q;
            const int64_t err_num = value_num - static_cast<int64_t>(q) * den;

            for (const Tap& t : opt.kernel.taps) {
                const int tx = x + (forward ? t.dx : -t.dx);  // 反向扫描时权重水平镜像
                const int ty = y + t.dy;
                const int64_t share = err_num * t.num;  // 分子，分母仍为 den
                if (tx >= 0 && tx < w && ty < img.height)
                    state[t.dy][tx] += share;
                else
                    res.discarded_num += share;  // 边界外：丢弃并单独计量
            }
        }
        // 行结束：误差缓冲整体下移一行，末行清零。
        for (int d = 0; d < dy_max; ++d) state[d].swap(state[d + 1]);
        std::fill(state[dy_max].begin(), state[dy_max].end(), 0);
    }
}

}  // namespace

Result quantize(const Image& img, const Options& opt) {
    validate(img, opt);
    Result res;
    res.out.assign(static_cast<size_t>(img.width) * img.height, 0);
    res.discarded_den = opt.kernel.den;
    std::vector<std::vector<int64_t>> state(maxDy(opt.kernel) + 1,
                                            std::vector<int64_t>(img.width, 0));
    processRows(img, opt, 0, img.height, state, res);
    return res;
}

Result quantizeTiled(const Image& img, const Options& opt, int rows_per_tile) {
    validate(img, opt);
    if (rows_per_tile < 1)
        throw std::invalid_argument("rows_per_tile must be >= 1");
    Result res;
    res.out.assign(static_cast<size_t>(img.width) * img.height, 0);
    res.discarded_den = opt.kernel.den;
    std::vector<std::vector<int64_t>> state(maxDy(opt.kernel) + 1,
                                            std::vector<int64_t>(img.width, 0));
    for (int y0 = 0; y0 < img.height; y0 += rows_per_tile) {
        const int y1 = std::min(y0 + rows_per_tile, img.height);
        processRows(img, opt, y0, y1, state, res);
    }
    return res;
}

Image mirrorHorizontal(const Image& img) {
    return Image(img.width, img.height, mirrorHorizontal(img.px, img.width, img.height));
}

std::vector<int> mirrorHorizontal(const std::vector<int>& row_major, int w, int h) {
    std::vector<int> out(row_major.size());
    for (int y = 0; y < h; ++y)
        for (int x = 0; x < w; ++x)
            out[static_cast<size_t>(y) * w + (w - 1 - x)] = row_major[static_cast<size_t>(y) * w + x];
    return out;
}

}  // namespace ediff
