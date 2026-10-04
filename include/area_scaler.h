#pragma once

// Area-weighted image scaler with exact rational weights.
//
// Each output pixel is the area-weighted average of the source pixels it
// covers. All arithmetic is exact (integer numerators over a common
// denominator); rounding happens exactly once, at the very end:
//
//     out(x,y) = round( sum_{sy,sx} wy(y,sy) * wx(x,sx) * src(sy,sx)
//                       / (srcH * srcW) )
//
// where wx(x,sx)/srcW and wy(y,sy)/srcH are the exact coverage fractions.
// The horizontal pass therefore produces un-rounded rational row sums, and
// the vertical pass accumulates those rationals -- never rounded rows.
//
// Streaming interface: source rows are pushed one at a time (top to bottom)
// via pushRow(); completed output rows are returned as soon as every source
// row they touch has been seen. Memory is O(active output rows * dstW),
// independent of srcH.

#include <algorithm>
#include <cassert>
#include <cstdint>
#include <deque>
#include <utility>
#include <vector>

namespace area_scale {

// Exact 1-D coverage weights for one axis.
//
// Coordinates are rescaled so source pixel i spans [i*dstN, (i+1)*dstN) and
// output pixel j spans [j*srcN, (j+1)*srcN). The overlap length is then an
// integer, and for every output j the numerators sum to srcN (== denom).
struct AxisWeights {
    int denom = 1;                // common denominator (== source length)
    std::vector<int> offset;      // per output: start index into srcIndex/numer
    std::vector<int> count;       // per output: number of contributing inputs
    std::vector<int> srcIndex;    // flattened contributing source indices
    std::vector<int> numer;       // flattened numerators
};

inline AxisWeights computeAxisWeights(int srcN, int dstN) {
    assert(srcN > 0 && dstN > 0);
    AxisWeights w;
    w.denom = srcN;
    w.offset.resize(dstN);
    w.count.resize(dstN);
    w.srcIndex.reserve(srcN + dstN);
    w.numer.reserve(srcN + dstN);
    for (int j = 0; j < dstN; ++j) {
        const long long left  = (long long)j * srcN;        // in 1/dstN source px
        const long long right = (long long)(j + 1) * srcN;
        const int first = (int)(left / dstN);
        const int last  = (int)((right - 1) / dstN);
        w.offset[j] = (int)w.srcIndex.size();
        w.count[j]  = last - first + 1;
        for (int i = first; i <= last; ++i) {
            const long long lo = std::max(left, (long long)i * dstN);
            const long long hi = std::min(right, (long long)(i + 1) * dstN);
            w.srcIndex.push_back(i);
            w.numer.push_back((int)(hi - lo));
        }
    }
    return w;
}

// Streaming area scaler for a single-channel integer image of type Pixel.
template <typename Pixel>
class Scaler {
public:
    Scaler(int srcW, int srcH, int dstW, int dstH)
        : srcW_(srcW), srcH_(srcH), dstW_(dstW), dstH_(dstH),
          wx_(computeAxisWeights(srcW, dstW)),
          wy_(computeAxisWeights(srcH, dstH)),
          startsAt_(srcH), endsAt_(srcH) {
        assert(srcW > 0 && srcH > 0 && dstW > 0 && dstH > 0);
        for (int j = 0; j < dstH_; ++j) {
            const int first = wy_.srcIndex[wy_.offset[j]];
            const int last  = wy_.srcIndex[wy_.offset[j] + wy_.count[j] - 1];
            startsAt_[first].push_back(j);
            endsAt_[last].push_back(j);
        }
    }

    int srcW() const { return srcW_; }
    int srcH() const { return srcH_; }
    int dstW() const { return dstW_; }
    int dstH() const { return dstH_; }

    // Push the next source row (exactly srcH() pushes, top to bottom).
    // `row` must point to srcW() pixels. Returns every output row completed
    // by this push, as (outY, pixels) pairs in increasing outY order.
    std::vector<std::pair<int, std::vector<Pixel>>> pushRow(const Pixel* row) {
        assert(nextSrc_ < srcH_ && "more source rows pushed than srcH");
        const int k = nextSrc_++;

        // 1) Horizontal pass: exact rational sums with denominator srcW_.
        //    Deliberately NOT rounded here.
        hval_.assign(dstW_, 0);
        for (int j = 0; j < dstW_; ++j) {
            long long acc = 0;
            const int off = wx_.offset[j], cnt = wx_.count[j];
            for (int t = 0; t < cnt; ++t)
                acc += (long long)wx_.numer[off + t] * row[wx_.srcIndex[off + t]];
            hval_[j] = acc;
        }

        // 2) Open accumulators for output rows whose coverage starts here.
        for (int j : startsAt_[k])
            active_.push_back({j, std::vector<long long>(dstW_, 0)});

        // 3) Add this row's exact contribution to every active output row.
        for (auto& entry : active_) {
            const int j = entry.first;
            const int first = wy_.srcIndex[wy_.offset[j]];
            const long long vnum = wy_.numer[wy_.offset[j] + (k - first)];
            for (int x = 0; x < dstW_; ++x)
                entry.second[x] += vnum * hval_[x];
        }

        // 4) Close output rows whose coverage ends here; round exactly once.
        std::vector<std::pair<int, std::vector<Pixel>>> done;
        const long long denom = (long long)srcW_ * srcH_;
        for (int j : endsAt_[k]) {
            assert(!active_.empty() && active_.front().first == j);
            std::vector<Pixel> out(dstW_);
            const std::vector<long long>& acc = active_.front().second;
            for (int x = 0; x < dstW_; ++x)
                out[x] = (Pixel)((acc[x] + denom / 2) / denom);  // round half up
            done.push_back({j, std::move(out)});
            active_.pop_front();
        }
        return done;
    }

    // Convenience: scale a whole image held in memory.
    static std::vector<Pixel> scale(const std::vector<Pixel>& src,
                                    int srcW, int srcH, int dstW, int dstH) {
        assert((int)src.size() >= srcW * srcH);
        Scaler s(srcW, srcH, dstW, dstH);
        std::vector<Pixel> dst((size_t)dstW * dstH);
        int produced = 0;
        for (int y = 0; y < srcH; ++y) {
            for (auto& p : s.pushRow(&src[(size_t)y * srcW])) {
                assert(p.first == produced);
                std::copy(p.second.begin(), p.second.end(),
                          dst.begin() + (size_t)p.first * dstW);
                ++produced;
            }
        }
        assert(produced == dstH);
        (void)produced;
        return dst;
    }

private:
    int srcW_, srcH_, dstW_, dstH_;
    AxisWeights wx_, wy_;
    std::vector<std::vector<int>> startsAt_, endsAt_;  // per source row
    std::vector<long long> hval_;                      // current row, x-pass sums
    std::deque<std::pair<int, std::vector<long long>>> active_;
    int nextSrc_ = 0;
};

}  // namespace area_scale
