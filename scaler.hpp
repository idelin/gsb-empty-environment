// scaler.hpp -- Exact area-weighted integer image scaler (C++17, header-only).
//
// Each output pixel is the coverage-area weighted average of the source
// pixels it overlaps. Weights are exact rationals: for output index j the
// covered source interval is [j*src/dst, (j+1)*src/dst), and each weight is
// stored as an integer numerator with shared denominator dst. The numerators
// of any output sample sum to src, so a weighted average is exactly
// sum(num_i * v_i) / src with no rounding inside a pass.
//
// The horizontal pass keeps exact integer numerators (denominator srcW).
// The vertical pass accumulates those numerators with exact integer weights,
// giving a final denominator of srcW*srcH. Rounding happens exactly once,
// when an output row is emitted -- never between the two passes.
//
// Rows are consumed and produced in streaming order. Only the source rows
// inside the current vertical filter window are kept in the row cache.
//
// Integer range note: accumulators are int64_t; the largest value is
// maxPixel * srcW * srcH, so source dimensions up to ~1e6 are safe for
// 8-bit pixels.
#pragma once

#include <cstdint>
#include <deque>
#include <limits>
#include <stdexcept>
#include <vector>

namespace imgs {

struct Filter1D {
    struct Span {
        int32_t first;             // index of first contributing source sample
        std::vector<int32_t> num;  // weight numerators, shared denominator = dstLen
    };
    int32_t srcLen = 0;
    int32_t dstLen = 0;
    std::vector<Span> spans;       // one per output sample
};

inline Filter1D makeFilter1D(int32_t srcLen, int32_t dstLen) {
    if (srcLen <= 0 || dstLen <= 0)
        throw std::invalid_argument("makeFilter1D: dimensions must be positive");
    Filter1D f;
    f.srcLen = srcLen;
    f.dstLen = dstLen;
    f.spans.reserve(static_cast<size_t>(dstLen));
    for (int64_t j = 0; j < dstLen; ++j) {
        // Output sample j covers [lo, hi) in units of 1/dstLen.
        const int64_t lo = j * srcLen;
        const int64_t hi = (j + 1) * srcLen;
        const int32_t first = static_cast<int32_t>(lo / dstLen);
        const int32_t last = static_cast<int32_t>((hi - 1) / dstLen);
        Filter1D::Span span;
        span.first = first;
        span.num.reserve(static_cast<size_t>(last - first + 1));
        for (int32_t i = first; i <= last; ++i) {
            const int64_t a = lo > static_cast<int64_t>(i) * dstLen
                                  ? lo
                                  : static_cast<int64_t>(i) * dstLen;
            const int64_t b = hi < static_cast<int64_t>(i + 1) * dstLen
                                  ? hi
                                  : static_cast<int64_t>(i + 1) * dstLen;
            span.num.push_back(static_cast<int32_t>(b - a));
        }
        f.spans.push_back(std::move(span));
    }
    return f;
}

template <typename Pixel = uint8_t>
class StreamScaler {
public:
    StreamScaler(int32_t srcW, int32_t srcH, int32_t dstW, int32_t dstH,
                 int32_t channels = 1)
        : fx_(makeFilter1D(srcW, dstW)),
          fy_(makeFilter1D(srcH, dstH)),
          srcW_(srcW), srcH_(srcH), dstW_(dstW), dstH_(dstH), ch_(channels) {
        if (channels <= 0)
            throw std::invalid_argument("StreamScaler: channels must be positive");
    }

    int32_t srcWidth() const { return srcW_; }
    int32_t srcHeight() const { return srcH_; }
    int32_t dstWidth() const { return dstW_; }
    int32_t dstHeight() const { return dstH_; }

    // Push one source row (srcW*channels pixels). Any output rows that become
    // computable are written to `out` (dstW*channels pixels each, room for
    // maxRows). Returns the number of output rows written.
    size_t pushRow(const Pixel* src, Pixel* out, size_t maxRows) {
        if (nextSrcRow_ >= srcH_)
            throw std::logic_error("StreamScaler: more rows pushed than srcH");
        cache_.push_back(horizontal(src));
        ++nextSrcRow_;
        return drain(out, maxRows);
    }

    // Flush remaining output rows after all srcH source rows were pushed.
    size_t finish(Pixel* out, size_t maxRows) {
        if (nextSrcRow_ != srcH_)
            throw std::logic_error("StreamScaler: finish() before all rows pushed");
        return drain(out, maxRows);
    }

    bool done() const { return nextDstRow_ == dstH_; }

private:
    static constexpr int64_t kMax =
        static_cast<int64_t>(std::numeric_limits<Pixel>::max());

    // Exact horizontal pass: result numerators have denominator srcW_.
    std::vector<int64_t> horizontal(const Pixel* row) const {
        std::vector<int64_t> acc(static_cast<size_t>(dstW_) * ch_);
        for (int32_t x = 0; x < dstW_; ++x) {
            const Filter1D::Span& s = fx_.spans[static_cast<size_t>(x)];
            for (int32_t c = 0; c < ch_; ++c) {
                int64_t sum = 0;
                for (size_t k = 0; k < s.num.size(); ++k)
                    sum += static_cast<int64_t>(s.num[k]) *
                           row[static_cast<size_t>(s.first + static_cast<int32_t>(k)) * ch_ + c];
                acc[static_cast<size_t>(x) * ch_ + c] = sum;
            }
        }
        return acc;
    }

    size_t drain(Pixel* out, size_t maxRows) {
        size_t produced = 0;
        while (nextDstRow_ < dstH_ && produced < maxRows) {
            const Filter1D::Span& s = fy_.spans[static_cast<size_t>(nextDstRow_)];
            const int32_t need = s.first + static_cast<int32_t>(s.num.size());
            if (need > nextSrcRow_)
                break;  // wait for more source rows
            // Evict cached rows the sliding window can never touch again.
            while (cacheBase_ < s.first) {
                cache_.pop_front();
                ++cacheBase_;
            }
            std::vector<int64_t> acc(static_cast<size_t>(dstW_) * ch_, 0);
            for (size_t k = 0; k < s.num.size(); ++k) {
                const std::vector<int64_t>& hrow =
                    cache_[static_cast<size_t>(s.first + static_cast<int32_t>(k) - cacheBase_)];
                const int64_t w = s.num[k];
                for (size_t i = 0; i < acc.size(); ++i)
                    acc[i] += w * hrow[i];
            }
            // The single rounding step of the whole pipeline.
            const int64_t den = static_cast<int64_t>(srcW_) * srcH_;
            Pixel* o = out + produced * static_cast<size_t>(dstW_) * ch_;
            for (size_t i = 0; i < acc.size(); ++i) {
                int64_t v = (acc[i] + den / 2) / den;  // round half up
                if (v < 0) v = 0;
                if (v > kMax) v = kMax;
                o[i] = static_cast<Pixel>(v);
            }
            ++nextDstRow_;
            ++produced;
        }
        return produced;
    }

    Filter1D fx_, fy_;
    int32_t srcW_, srcH_, dstW_, dstH_, ch_;
    std::deque<std::vector<int64_t>> cache_;  // horizontally scaled rows
    int32_t cacheBase_ = 0;                   // source row index of cache_.front()
    int32_t nextSrcRow_ = 0;
    int32_t nextDstRow_ = 0;
};

// One-shot convenience wrapper around StreamScaler.
template <typename Pixel = uint8_t>
std::vector<Pixel> scale(const Pixel* src, int32_t srcW, int32_t srcH,
                         int32_t dstW, int32_t dstH, int32_t channels = 1) {
    StreamScaler<Pixel> sc(srcW, srcH, dstW, dstH, channels);
    std::vector<Pixel> out(static_cast<size_t>(dstW) * dstH * channels);
    const size_t srcStride = static_cast<size_t>(srcW) * channels;
    const size_t dstStride = static_cast<size_t>(dstW) * channels;
    size_t row = 0;
    for (int32_t y = 0; y < srcH; ++y)
        row += sc.pushRow(src + static_cast<size_t>(y) * srcStride,
                          out.data() + row * dstStride, dstH - row);
    row += sc.finish(out.data() + row * dstStride, dstH - row);
    if (row != static_cast<size_t>(dstH) || !sc.done())
        throw std::logic_error("scale: internal row count mismatch");
    return out;
}

}  // namespace imgs
