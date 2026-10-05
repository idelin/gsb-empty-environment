#pragma once

// 误差扩散核：一组有理权重，坐标以"扫描前进方向"为基准。
//   dy > 0            —— 下方第 dy 行（尚未处理）
//   dy == 0 且 dx > 0 —— 同一行中尚未处理的像素
// 权重 = num / den。反向行（往返扫描的回程）处理时 dx 自动取反，
// 因此核只需按前进方向描述一次。

#include <algorithm>
#include <cstdint>
#include <stdexcept>
#include <vector>

namespace graydiff {

struct Tap {
    int dx;
    int dy;
    int32_t num;
};

struct Kernel {
    std::vector<Tap> taps;
    uint32_t den = 1;

    void validate() const {
        if (den == 0) throw std::invalid_argument("kernel denominator must be positive");
        for (const Tap& t : taps) {
            if (!(t.dy > 0 || (t.dy == 0 && t.dx > 0)))
                throw std::invalid_argument(
                    "kernel tap must point to an unprocessed pixel (dy>0 or dy==0,dx>0)");
        }
    }

    int maxDy() const {
        int m = 0;
        for (const Tap& t : taps) m = std::max(m, t.dy);
        return m;
    }

    // 权重和等于 1（sum(num)==den）时误差完全守恒：全部被后续像素接收或计入边界丢弃。

    static Kernel floydSteinberg() {
        return {{{1, 0, 7}, {-1, 1, 3}, {0, 1, 5}, {1, 1, 1}}, 16};
    }
    static Kernel jarvisJudiceNinke() {
        return {{{1, 0, 7},  {2, 0, 5},  {-2, 1, 3}, {-1, 1, 5},
                 {0, 1, 7},  {1, 1, 5},  {2, 1, 3},  {-2, 2, 1},
                 {-1, 2, 3}, {0, 2, 5},  {1, 2, 3},  {2, 2, 1}},
                48};
    }
    static Kernel stucki() {
        return {{{1, 0, 8},  {2, 0, 4},  {-2, 1, 2}, {-1, 1, 4},
                 {0, 1, 8},  {1, 1, 4},  {2, 1, 2},  {-2, 2, 1},
                 {-1, 2, 2}, {0, 2, 4},  {1, 2, 2},  {2, 2, 1}},
                42};
    }
    // Atkinson 故意只扩散 6/8 的误差（sum(num)=6 < den=8），是合法的守恒外核。
    static Kernel atkinson() {
        return {{{1, 0, 1}, {2, 0, 1}, {-1, 1, 1}, {0, 1, 1}, {1, 1, 1}, {0, 2, 1}}, 8};
    }
    // 最小核：右邻 1/2，便于手算验证。
    static Kernel rightHalf() { return {{{1, 0, 1}}, 2}; }
};

}  // namespace graydiff
