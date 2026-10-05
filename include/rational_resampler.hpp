// Rational-ratio FIR resampler for integer sample streams.
//
// Mathematical model (polyphase view of upsample -> FIR -> downsample):
//   y[n] = sum_k x[k] * h[n*Q - k*P],   0 <= n*Q - k*P < L
// where P/Q is the resampling ratio and h[0..L-1] the FIR kernel.
//
// Guarantees:
//  * Kernel coefficients are exact rationals; all filtering is done in exact
//    integer arithmetic over a common denominator. Rounding to integer happens
//    exactly once, when a sample is emitted (round-half-away-from-zero).
//  * Output is a pure function of the global input stream: feeding the same
//    samples in any chunking (including empty and single-sample chunks)
//    yields sample-identical output to one-shot processing.
//  * flush() emits the filter tail; after flush() the stream is complete.
//  * Non-coprime P/Q are normalized by gcd(P, Q); the kernel is decimated
//    accordingly, which is mathematically identical (only every gcd-th tap is
//    ever reachable).
#pragma once

#include <cstdint>
#include <deque>
#include <vector>

namespace rrs {

struct Rational {
    std::int64_t num;
    std::int64_t den;  // must be > 0
};

class RationalResampler {
public:
    // up_factor / down_factor: resampling ratio, both > 0 (need not be coprime).
    // kernel: FIR coefficients, at least one tap.
    RationalResampler(std::int64_t up_factor,
                      std::int64_t down_factor,
                      std::vector<Rational> kernel);

    // Feed the next chunk of input samples (may be empty). Returns the output
    // samples that become determined by the input received so far.
    // Must not be called after flush().
    std::vector<std::int64_t> process(const std::vector<std::int64_t>& chunk);

    // Signal end of input and emit the remaining tail. May be called only
    // once; the resampler must not be used afterwards.
    std::vector<std::int64_t> flush();

    // One-shot convenience: process all input, then flush.
    static std::vector<std::int64_t> resample(const std::vector<std::int64_t>& input,
                                              std::int64_t up_factor,
                                              std::int64_t down_factor,
                                              const std::vector<Rational>& kernel);

    // Total number of output samples for a complete stream of `input_size`
    // samples (after flush).
    std::int64_t output_size_for(std::int64_t input_size) const;

    std::int64_t up_factor() const { return up_; }
    std::int64_t down_factor() const { return down_; }

private:
    std::int64_t up_;                 // normalized (coprime) upsample factor P
    std::int64_t down_;               // normalized (coprime) downsample factor Q
    std::vector<std::int64_t> taps_;  // kernel scaled to common denominator
    std::int64_t denom_;              // common denominator of kernel coefficients

    std::deque<std::int64_t> hist_;   // recent input samples
    std::int64_t hist_base_ = 0;      // absolute index of hist_.front()
    std::int64_t in_count_ = 0;       // total input samples received
    std::int64_t next_out_ = 0;       // absolute index of next output sample
    bool flushed_ = false;

    std::int64_t emit(std::int64_t n) const;
    // Emit every output whose last needed input sample is already available.
    void emit_available(std::vector<std::int64_t>& out);
};

}  // namespace rrs
