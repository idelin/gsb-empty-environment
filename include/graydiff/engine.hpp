#pragma once

// 逐行往返（serpentine）扫描的精确误差扩散引擎。
//
// 精确累计模型：
//   数学上累积值的分母会沿扫描顺序增长（den^k），因此本引擎采用全局公分母
//       D = den^(W*H) * (levels-1)
//   所有累积量都是整数（单位 1/D），全程无浮点、无中间舍入。
//   可证明：第 r 个被处理像素的误差 E 必含因子 den^(N-r)（N=W*H），
//   因此分配时 E/den 恒为精确整除（divSmallExact 若失败即内部 bug）。
//
// 最终取整规则（只在量化时发生一次）：
//   k = #{ j in [1, levels-1] : 2*(levels-1)*A >= (2j-1)*maxValue*D }
//   即 round-half-up：恰好落在两级正中间时取较高一级；越界自动钳制到
//   [0, levels-1]。该判定用预计算阈值精确完成。
//
// 边界丢弃：落出图像的分配份额累加到 discarded（单位 1/D），单独计量。

#include <deque>
#include <stdexcept>
#include <utility>
#include <vector>

#include "bigint.hpp"
#include "kernel.hpp"

namespace graydiff {

enum class ScanDirection { LeftToRight, RightToLeft };

struct Options {
    Kernel kernel = Kernel::floydSteinberg();
    int levels = 2;       // 量化级数，[2, 256]
    int maxValue = 255;   // 输入灰度最大值，[1, 65535]
    ScanDirection startDirection = ScanDirection::LeftToRight;  // 第 0 行方向
    bool trace = false;   // 记录每像素累计值/误差（验证用，内存开销大）
};

// 最终取整规则的独立函数形式（便于单元测试）。
inline int decideLevel(const BigInt& accumUnits, const BigInt& denominator, int levels,
                       int maxValue) {
    BigInt left = accumUnits;
    left.mulSmall(uint32_t(2 * (levels - 1)));
    int k = 0;
    for (int j = 1; j <= levels - 1; ++j) {
        BigInt right = denominator;
        right.mulSmall(uint32_t(int64_t(2 * j - 1) * maxValue));
        if (BigInt::compare(left, right) >= 0)
            k = j;
        else
            break;
    }
    return k;
}

struct Trace {
    std::vector<BigInt> accumUnits;  // 行优先，每像素量化前的累计值（单位 1/D）
    std::vector<BigInt> errorUnits;  // 行优先，每像素的量化误差（单位 1/D）
};

struct Result {
    int width = 0, height = 0, levels = 0, maxValue = 0;
    std::vector<int> levelIndex;  // 行优先，每像素的量化级 [0, levels-1]
    BigInt discardedNum;          // 边界丢弃误差分子（单位 1/denominator）
    BigInt denominator;           // 全局公分母 D
    Trace trace;

    // 丢弃误差的近似浮点值（仅供显示；精确值用 discardedNum/denominator）
    double discardedErrorApprox() const { return BigInt::approxRatio(discardedNum, denominator); }

    // 仅供显示的 8 位灰度：round-half-up(level*255/(levels-1))
    int gray8Of(int level) const {
        return int((2 * level * 255 + (levels - 1)) / (2 * (levels - 1)));
    }
};

class Engine {
public:
    Engine(int width, int height, Options opt) : w_(width), h_(height), opt_(std::move(opt)) {
        if (width < 1 || height < 1) throw std::invalid_argument("image dimensions must be >= 1");
        if (opt_.levels < 2 || opt_.levels > 256)
            throw std::invalid_argument("levels must be in [2, 256]");
        if (opt_.maxValue < 1 || opt_.maxValue > 65535)
            throw std::invalid_argument("maxValue must be in [1, 65535]");
        opt_.kernel.validate();
        den_ = opt_.kernel.den;
        rows_.resize(size_t(opt_.kernel.maxDy()) + 1, std::vector<BigInt>(size_t(w_)));
        D_ = BigInt::powSmall(den_, uint64_t(w_) * uint64_t(h_));
        D_.mulSmall(uint32_t(opt_.levels - 1));
        dlev_ = D_;
        dlev_.divSmallExact(uint32_t(opt_.levels - 1));  // = den^(W*H)
        leftFactor_ = uint32_t(2 * (opt_.levels - 1));
        for (int j = 1; j <= opt_.levels - 1; ++j) {
            BigInt t = D_;
            t.mulSmall(uint32_t(int64_t(2 * j - 1) * opt_.maxValue));
            thresholds_.push_back(std::move(t));
        }
        x_ = rowDirection(0) == ScanDirection::LeftToRight ? 0 : w_ - 1;
        out_.resize(size_t(w_) * size_t(h_), -1);
        if (opt_.trace) {
            trace_.accumUnits.resize(size_t(w_) * size_t(h_));
            trace_.errorUnits.resize(size_t(w_) * size_t(h_));
        }
    }

    // 第 y 行的扫描方向（逐行往返）
    ScanDirection rowDirection(int y) const {
        if ((y & 1) == 0) return opt_.startDirection;
        return opt_.startDirection == ScanDirection::LeftToRight ? ScanDirection::RightToLeft
                                                                 : ScanDirection::LeftToRight;
    }

    // 按 serpentine 扫描顺序送入一个像素，返回其量化级。
    int feedPixel(int32_t value) {
        if (done()) throw std::logic_error("Engine::feedPixel: all pixels already fed");
        if (value < 0 || value > opt_.maxValue)
            throw std::out_of_range("Engine::feedPixel: pixel value out of [0, maxValue]");
        const int x = x_, y = y_;

        BigInt accum = std::move(rows_[0][size_t(x)]);  // 已收齐全部上游贡献
        {
            BigInt vd = D_;
            vd.mulSmall(uint32_t(value));
            accum += vd;
        }

        const int level = decide(accum);

        BigInt err = accum;
        if (level != 0) {
            BigInt q = dlev_;  // Q = level*maxValue*den^(W*H)，单位 1/D
            q.mulSmall(uint32_t(int64_t(level) * opt_.maxValue));
            err -= q;
        }

        BigInt step = err;
        step.divSmallExact(den_);  // 精确性由数学不变量保证

        const ScanDirection dir = rowDirection(y);
        for (const Tap& t : opt_.kernel.taps) {
            const int adx = dir == ScanDirection::LeftToRight ? t.dx : -t.dx;
            const int tx = x + adx, ty = y + t.dy;
            BigInt c = step;
            c.mulSigned(t.num);
            if (tx < 0 || tx >= w_ || ty >= h_)
                discarded_ += c;  // 边界丢弃，单独计量
            else
                rows_[size_t(t.dy)][size_t(tx)] += c;
        }

        const size_t idx = size_t(y) * size_t(w_) + size_t(x);
        out_[idx] = level;
        if (opt_.trace) {
            trace_.accumUnits[idx] = accum;
            trace_.errorUnits[idx] = err;
        }
        advance();
        return level;
    }

    bool done() const { return y_ >= h_; }
    void finish() {
        if (!done()) throw std::logic_error("Engine::finish: not all pixels were fed");
    }

    const std::vector<int>& levelIndex() const { return out_; }
    const BigInt& discardedUnits() const { return discarded_; }
    const BigInt& denominator() const { return D_; }
    const Trace& trace() const { return trace_; }

    Result extractResult() {
        Result r;
        r.width = w_;
        r.height = h_;
        r.levels = opt_.levels;
        r.maxValue = opt_.maxValue;
        r.levelIndex = std::move(out_);
        r.discardedNum = std::move(discarded_);
        r.denominator = std::move(D_);
        r.trace = std::move(trace_);
        return r;
    }

private:
    int decide(const BigInt& accum) const {
        BigInt left = accum;
        left.mulSmall(leftFactor_);
        int k = 0;
        for (const BigInt& th : thresholds_) {
            if (BigInt::compare(left, th) >= 0)
                ++k;
            else
                break;
        }
        return k;
    }

    void advance() {
        if (rowDirection(y_) == ScanDirection::LeftToRight) {
            if (++x_ >= w_) {
                x_ = w_ - 1;
                ++y_;
                endRow();
            }
        } else {
            if (--x_ < 0) {
                x_ = 0;
                ++y_;
                endRow();
            }
        }
    }

    void endRow() {
        rows_.pop_front();
        rows_.push_back(std::vector<BigInt>(size_t(w_)));
    }

    int w_, h_;
    Options opt_;
    uint32_t den_ = 1;
    uint32_t leftFactor_ = 2;
    BigInt D_;                          // 全局公分母
    BigInt dlev_;                       // D/(levels-1)
    std::vector<BigInt> thresholds_;    // D*(2j-1)*maxValue, j=1..levels-1
    std::deque<std::vector<BigInt>> rows_;  // 滚动累积缓冲（maxDy+1 行）
    std::vector<int> out_;
    BigInt discarded_;
    Trace trace_;
    int x_ = 0, y_ = 0;
};

}  // namespace graydiff
