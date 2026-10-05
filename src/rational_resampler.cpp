#include "rational_resampler.hpp"

#include <numeric>
#include <stdexcept>

namespace rrs {
namespace {

using i64 = std::int64_t;
using i128 = __int128_t;

i64 gcd_i64(i64 a, i64 b) {
    if (a < 0) a = -a;
    if (b < 0) b = -b;
    while (b != 0) {
        i64 t = a % b;
        a = b;
        b = t;
    }
    return a;
}

// ceil(a / b) for b > 0, exact for any sign of a.
i64 ceil_div(i128 a, i128 b) {
    if (a >= 0) return static_cast<i64>((a + b - 1) / b);
    return static_cast<i64>(-((-a) / b));
}

// Round numerator/denominator (den > 0) to nearest integer,
// ties away from zero.
i64 round_div(i128 num, i64 den) {
    i128 d = den;
    i128 half = d / 2;
    i128 r = num >= 0 ? (num + half) / d : -((-num + half) / d);
    return static_cast<i64>(r);
}

}  // namespace

RationalResampler::RationalResampler(i64 up_factor,
                                     i64 down_factor,
                                     std::vector<Rational> kernel) {
    if (up_factor <= 0 || down_factor <= 0)
        throw std::invalid_argument("resampling factors must be positive");
    if (kernel.empty())
        throw std::invalid_argument("kernel must have at least one tap");

    // Reduce coefficients and find the common denominator.
    i128 common = 1;
    for (const auto& c : kernel) {
        if (c.den <= 0)
            throw std::invalid_argument("kernel denominators must be positive");
        i64 g = gcd_i64(c.num, c.den);
        i64 reduced_den = c.den / g;
        i128 l = common / gcd_i64(static_cast<i64>(common), reduced_den) *
                 static_cast<i128>(reduced_den);
        if (l > std::numeric_limits<i64>::max())
            throw std::overflow_error("kernel common denominator overflow");
        common = l;
    }
    denom_ = static_cast<i64>(common);

    std::vector<i64> scaled(kernel.size());
    for (size_t i = 0; i < kernel.size(); ++i) {
        i64 g = gcd_i64(kernel[i].num, kernel[i].den);
        i128 v = static_cast<i128>(kernel[i].num / g) *
                 (common / (kernel[i].den / g));
        scaled[i] = static_cast<i64>(v);
    }

    // Normalize a non-coprime ratio: with g = gcd(P, Q), only every g-th tap
    // is ever used, so decimating the kernel is mathematically identical.
    i64 g = gcd_i64(up_factor, down_factor);
    up_ = up_factor / g;
    down_ = down_factor / g;
    for (size_t i = 0; i < scaled.size(); i += static_cast<size_t>(g))
        taps_.push_back(scaled[i]);
}

i64 RationalResampler::emit(i64 n) const {
    i128 nq = static_cast<i128>(n) * down_;
    i64 k_hi = static_cast<i64>(nq / up_);        // floor, nq >= 0
    // During flush the newest samples may not exist for this phase; clamp to
    // the newest available sample and adjust the kernel index accordingly.
    i64 k_avail = hist_base_ + static_cast<i64>(hist_.size()) - 1;
    if (k_hi > k_avail) k_hi = k_avail;
    i64 idx = static_cast<i64>(nq - static_cast<i128>(k_hi) * up_);
    i128 acc = 0;
    for (i64 k = k_hi; idx < static_cast<i64>(taps_.size()) && k >= 0;
         --k, idx += up_) {
        if (k < hist_base_) break;  // sample already discarded (cannot happen)
        acc += static_cast<i128>(hist_[static_cast<size_t>(k - hist_base_)]) *
               taps_[static_cast<size_t>(idx)];
    }
    return round_div(acc, denom_);
}

void RationalResampler::emit_available(std::vector<i64>& out) {
    // Output n is determined once floor(n*Q/P) <= in_count_ - 1,
    // i.e. n*Q < in_count_*P. It must also lie inside the convolution
    // support of the samples seen so far (relevant when L < P); both bounds
    // are monotone in in_count_, so the emitted prefix is chunking-independent.
    i64 limit = output_size_for(in_count_);
    while (next_out_ < limit &&
           static_cast<i128>(next_out_) * down_ <
               static_cast<i128>(in_count_) * up_) {
        out.push_back(emit(next_out_));
        ++next_out_;
    }
    // Drop history no future output can reference:
    // the next output needs k >= ceil((next_out_*Q - (L-1)) / P).
    i64 keep_from = ceil_div(static_cast<i128>(next_out_) * down_ -
                                 (static_cast<i64>(taps_.size()) - 1),
                             up_);
    while (hist_base_ < keep_from && !hist_.empty()) {
        hist_.pop_front();
        ++hist_base_;
    }
}

std::vector<i64> RationalResampler::process(const std::vector<i64>& chunk) {
    if (flushed_)
        throw std::logic_error("process() called after flush()");
    for (i64 s : chunk) hist_.push_back(s);
    in_count_ += static_cast<i64>(chunk.size());
    std::vector<i64> out;
    emit_available(out);
    return out;
}

std::vector<i64> RationalResampler::flush() {
    if (flushed_) throw std::logic_error("flush() called twice");
    flushed_ = true;
    std::vector<i64> out;
    i64 total = output_size_for(in_count_);
    while (next_out_ < total) {
        out.push_back(emit(next_out_));
        ++next_out_;
    }
    hist_.clear();
    return out;
}

i64 RationalResampler::output_size_for(i64 input_size) const {
    if (input_size <= 0) return 0;
    // Last n with n*Q <= (N-1)*P + (L-1), plus one.
    i128 last = (static_cast<i128>(input_size - 1) * up_ +
                 (static_cast<i64>(taps_.size()) - 1)) /
                down_;
    return static_cast<i64>(last) + 1;
}

std::vector<i64> RationalResampler::resample(const std::vector<i64>& input,
                                             i64 up_factor,
                                             i64 down_factor,
                                             const std::vector<Rational>& kernel) {
    RationalResampler r(up_factor, down_factor, kernel);
    std::vector<i64> out = r.process(input);
    std::vector<i64> tail = r.flush();
    out.insert(out.end(), tail.begin(), tail.end());
    return out;
}

}  // namespace rrs
