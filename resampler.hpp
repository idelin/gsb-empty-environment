// resampler.hpp
//
// 有理倍率整数音频重采样库（C++17，仅头文件）。
//
// 数学模型（标准多相重采样）：
//   输入整数采样 x[0..N-1]，先按 P 零插值上采样，经长度 L 的 FIR 核 h
//  （核定义在上采样率上，系数为有理数），再按 Q 抽取下采样：
//       y[m] = sum_i x[i] * h[m*Q - i*P]      （h 下标越界按 0 处理）
//
// 精确性保证：
//   - 核系数先约分再通分到公分母 D，滤波累加在任意精度有符号整数上进行，
//     全程无浮点、无中间舍入；仅在最终输出时做一次舍入
//    （最近舍入，绝对值恰为一半时向远离零方向舍入）。
//   - 相位（下一个输出采样索引）由绝对索引维护，与输入如何切块完全无关：
//     任意切块（含空块、单样本块）与一次性处理逐样本一致。
//   - 倍率 P/Q 在构造时约分，分子分母不互质（如 2/4）与互质形式（1/2）
//     行为完全一致。
//
// 尾部冲洗：
//   输入结束（共 N 个采样）后调用 flush()，以零填充计算尾部输出，
//   总输出数 M = floor(((N-1)*P + L - 1) / Q) + 1（N >= 1；N == 0 时 M = 0）。
//
// 限制：输入/输出采样索引与倍率使用 int64_t；核系数通分后的公分母与
// 分子须落在 int64_t 内（否则抛 std::overflow_error）；最终舍入结果须
// 落在 int64_t 内（否则抛 std::overflow_error）。

#ifndef RESAMPLER_HPP
#define RESAMPLER_HPP

#include <cstdint>
#include <deque>
#include <numeric>
#include <stdexcept>
#include <vector>

namespace resample {

namespace detail {

// 任意精度有符号整数：仅实现本库所需的 += a*b（int64 乘积）与
// 除以正 int64 的舍入除法。小端 limbs，sign 属于 {-1, 0, 1}。
class BigInt {
public:
    BigInt() = default;

    void addProduct(int64_t a, int64_t b) {
        if (a == 0 || b == 0) return;
        const int productSign = ((a < 0) != (b < 0)) ? -1 : 1;
        const unsigned __int128 p =
            static_cast<unsigned __int128>(absu(a)) * absu(b);
        std::vector<uint64_t> m;
        m.push_back(static_cast<uint64_t>(p));
        const uint64_t hi = static_cast<uint64_t>(p >> 64);
        if (hi != 0) m.push_back(hi);
        addSigned(productSign, m);
    }

    // this / d 的最近舍入（|余数|*2 >= d 时向远离零进位），d > 0。
    int64_t divRoundHalfAway(int64_t d) const {
        if (sign_ == 0) return 0;
        // 长除法（高位 limb 到低位）。
        std::vector<uint64_t> q(mag_.size(), 0);
        unsigned __int128 rem = 0;
        for (std::size_t i = mag_.size(); i-- > 0;) {
            rem = (rem << 64) | mag_[i];
            q[i] = static_cast<uint64_t>(rem / static_cast<uint64_t>(d));
            rem %= static_cast<uint64_t>(d);
        }
        while (!q.empty() && q.back() == 0) q.pop_back();
        if (q.size() > 1 || (!q.empty() && q[0] > static_cast<uint64_t>(INT64_MAX)))
            throw std::overflow_error("resample: rounded output out of int64 range");
        uint64_t uq = q.empty() ? 0 : q[0];
        if (rem * 2 >= static_cast<unsigned __int128>(d)) {
            if (uq >= static_cast<uint64_t>(INT64_MAX))
                throw std::overflow_error("resample: rounded output out of int64 range");
            ++uq;
        }
        const int64_t v = static_cast<int64_t>(uq);
        return sign_ > 0 ? v : -v;
    }

private:
    static uint64_t absu(int64_t v) {
        return v < 0 ? static_cast<uint64_t>(-(v + 1)) + 1
                     : static_cast<uint64_t>(v);
    }

    static int cmpMag(const std::vector<uint64_t>& a,
                      const std::vector<uint64_t>& b) {
        if (a.size() != b.size()) return a.size() < b.size() ? -1 : 1;
        for (std::size_t i = a.size(); i-- > 0;)
            if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
        return 0;
    }

    static void addTo(std::vector<uint64_t>& a, const std::vector<uint64_t>& b) {
        if (a.size() < b.size()) a.resize(b.size(), 0);
        uint64_t carry = 0;
        for (std::size_t i = 0; i < b.size(); ++i) {
            const uint64_t s = a[i] + b[i];
            const uint64_t c1 = (s < a[i]) ? 1 : 0;
            a[i] = s + carry;
            carry = c1 | ((a[i] < s) ? 1 : 0);
        }
        for (std::size_t i = b.size(); carry && i < a.size(); ++i) {
            a[i] += carry;
            carry = (a[i] == 0) ? 1 : 0;
        }
        if (carry) a.push_back(1);
    }

    // 前置条件：|a| >= |b|。a -= b。
    static void subFrom(std::vector<uint64_t>& a, const std::vector<uint64_t>& b) {
        uint64_t borrow = 0;
        for (std::size_t i = 0; i < b.size(); ++i) {
            const uint64_t bi = b[i] + borrow;
            borrow = (bi < borrow || a[i] < bi) ? 1 : 0;
            a[i] -= bi;
        }
        for (std::size_t i = b.size(); borrow && i < a.size(); ++i) {
            borrow = (a[i] == 0) ? 1 : 0;
            a[i] -= 1;
        }
        while (!a.empty() && a.back() == 0) a.pop_back();
    }

    void addSigned(int s, const std::vector<uint64_t>& m) {
        if (sign_ == 0) {
            sign_ = s;
            mag_ = m;
            return;
        }
        if (sign_ == s) {
            addTo(mag_, m);
            return;
        }
        const int c = cmpMag(mag_, m);
        if (c == 0) {
            mag_.clear();
            sign_ = 0;
        } else if (c > 0) {
            subFrom(mag_, m);
        } else {
            std::vector<uint64_t> t = m;
            subFrom(t, mag_);
            mag_ = t;
            sign_ = s;
        }
    }

    std::vector<uint64_t> mag_;
    int sign_ = 0;
};

inline int64_t ceilDivNonNeg(int64_t a, int64_t b) { // b > 0, a 任意
    return a >= 0 ? (a + b - 1) / b : -((-a) / b);
}

} // namespace detail

class RationalResampler {
public:
    struct Coeff {
        int64_t num;
        int64_t den; // 不得为 0
    };

    // up/down：重采样倍率（输出率 = 输入率 * up / down），均须 > 0，
    // 构造时自动约分。kernel：FIR 核（定义在 up 倍上采样率上），非空。
    RationalResampler(int64_t up, int64_t down, const std::vector<Coeff>& kernel)
        : in_count_(0), out_next_(0), buf_base_(0), flushed_(false) {
        if (up <= 0 || down <= 0)
            throw std::invalid_argument("resample: ratio must be positive");
        if (kernel.empty())
            throw std::invalid_argument("resample: kernel must be non-empty");
        const int64_t g = std::gcd(up, down);
        P_ = up / g;
        Q_ = down / g;
        L_ = static_cast<int64_t>(kernel.size());

        // 系数约分并通分到公分母 kden_。
        std::vector<int64_t> nums(kernel.size());
        std::vector<int64_t> dens(kernel.size());
        int64_t lcm = 1;
        for (std::size_t i = 0; i < kernel.size(); ++i) {
            int64_t n = kernel[i].num;
            int64_t d = kernel[i].den;
            if (d == 0)
                throw std::invalid_argument("resample: zero coefficient denominator");
            if (d < 0) {
                if (d == INT64_MIN || n == INT64_MIN)
                    throw std::overflow_error("resample: coefficient out of range");
                d = -d;
                n = -n;
            }
            const int64_t cg = std::gcd(n < 0 ? -(n + 1) + 1 : n, d);
            n /= cg;
            d /= cg;
            nums[i] = n;
            dens[i] = d;
            const int64_t lg = std::gcd(lcm, d);
            const __int128 nl = static_cast<__int128>(lcm) / lg * d;
            if (nl > INT64_MAX)
                throw std::overflow_error("resample: common denominator overflow");
            lcm = static_cast<int64_t>(nl);
        }
        kden_ = lcm;
        knum_.resize(kernel.size());
        for (std::size_t i = 0; i < kernel.size(); ++i) {
            const __int128 v = static_cast<__int128>(nums[i]) * (kden_ / dens[i]);
            if (v > INT64_MAX || v < INT64_MIN)
                throw std::overflow_error("resample: normalized numerator overflow");
            knum_[i] = static_cast<int64_t>(v);
        }
    }

    // 送入一块输入采样（可为空块），追加产生的输出采样到 out。
    void process(const int64_t* samples, std::size_t count,
                 std::vector<int64_t>& out) {
        if (flushed_)
            throw std::logic_error("resample: process() after flush()");
        for (std::size_t k = 0; k < count; ++k) {
            buf_.push_back(samples[k]);
            ++in_count_;
        }
        // 可确定的输出 m 需同时满足：
        //   1) 所需输入已全部到达：m*Q < in_count*P；
        //   2) 落在当前总输出数定义内：m < floor(((n-1)*P + L-1)/Q) + 1
        //     （当 L < P 时，超出该范围的尾部位置在流结束时不属于输出流）。
        const __int128 n = in_count_;
        const __int128 arrived = (n * P_ + Q_ - 1) / Q_;
        const __int128 defined = ((n - 1) * P_ + (L_ - 1)) / Q_ + 1;
        emitUntil(static_cast<int64_t>(arrived < defined ? arrived : defined),
                  out);
    }

    void process(const std::vector<int64_t>& samples, std::vector<int64_t>& out) {
        process(samples.data(), samples.size(), out);
    }

    // 尾部冲洗：以零填充计算剩余全部输出。幂等，重复调用不再产生输出。
    void flush(std::vector<int64_t>& out) {
        if (flushed_) return;
        flushed_ = true;
        const int64_t n = in_count_;
        const int64_t total =
            n == 0 ? 0
                   : static_cast<int64_t>(
                         (static_cast<__int128>(n - 1) * P_ + (L_ - 1)) / Q_) +
                         1;
        emitUntil(total, out);
    }

    int64_t inputCount() const { return in_count_; }
    int64_t outputCount() const { return out_next_; }
    bool flushed() const { return flushed_; }

private:
    // 产生所有索引 < limit 的输出（缺少的输入按 0 处理）。
    void emitUntil(int64_t limit, std::vector<int64_t>& out) {
        while (out_next_ < limit) {
            const int64_t m = out_next_;
            const int64_t mQ = m * Q_;
            detail::BigInt acc;
            for (int64_t j = 0; j < L_; ++j) {
                const int64_t t = mQ - j; // 上采样域索引
                if (t < 0) break;
                if (t % P_ != 0) continue;
                const int64_t i = t / P_; // 输入采样索引
                if (i >= in_count_) continue; // 尾部零填充
                acc.addProduct(buf_[static_cast<std::size_t>(i - buf_base_)],
                               knum_[static_cast<std::size_t>(j)]);
            }
            out.push_back(acc.divRoundHalfAway(kden_));
            ++out_next_;
        }
        // 丢弃不再需要的输入：后续输出 m >= out_next_ 只访问
        // i >= ceil((out_next_*Q - (L-1)) / P)。
        int64_t lo = detail::ceilDivNonNeg(out_next_ * Q_ - (L_ - 1), P_);
        if (lo < 0) lo = 0;
        while (buf_base_ < lo && !buf_.empty()) {
            buf_.pop_front();
            ++buf_base_;
        }
    }

    int64_t P_, Q_;   // 约分后的倍率
    int64_t L_;       // 核长度
    int64_t kden_;    // 核公分母（> 0）
    std::vector<int64_t> knum_; // 通分后的核分子
    int64_t in_count_;          // 已接收输入采样总数
    int64_t out_next_;          // 下一个待输出索引（相位状态）
    std::deque<int64_t> buf_;   // 输入滑动窗口
    int64_t buf_base_;          // buf_ 队首的绝对索引
    bool flushed_;
};

// 一次性便捷接口：等价于 process(全部) + flush()。
inline std::vector<int64_t> resample(const std::vector<int64_t>& in, int64_t up,
                                     int64_t down,
                                     const std::vector<RationalResampler::Coeff>& kernel) {
    RationalResampler r(up, down, kernel);
    std::vector<int64_t> out;
    r.process(in, out);
    r.flush(out);
    return out;
}

} // namespace resample

#endif // RESAMPLER_HPP
