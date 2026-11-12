#include "netsec.hpp"

#include <algorithm>

namespace netsec {
namespace {

constexpr int64_t kSMin = INT64_MIN;
constexpr int64_t kSMax = INT64_MAX;
constexpr uint64_t kUMax = UINT64_MAX;

bool addOverflowsSigned(int64_t a, int64_t b) {
    return (b > 0 && a > kSMax - b) || (b < 0 && a < kSMin - b);
}

bool subOverflowsSigned(int64_t a, int64_t b) {
    return (b < 0 && a > kSMax + b) || (b > 0 && a < kSMin + b);
}

// Bits that are 1 in every value of the unsigned interval [lo, hi]:
// exactly the bits above the highest bit where lo and hi differ.
uint64_t fixedOnes(uint64_t lo, uint64_t hi) {
    uint64_t diff = lo ^ hi;
    if (diff == 0) return lo;
    diff |= diff >> 1;
    diff |= diff >> 2;
    diff |= diff >> 4;
    diff |= diff >> 8;
    diff |= diff >> 16;
    diff |= diff >> 32;
    return lo & ~diff;
}

// Well-defined arithmetic right shift (avoids implementation-defined
// right shift of negative signed values).
int64_t shiftRightArithmetic(int64_t v, unsigned s) {
    if (s == 0) return v;
    uint64_t uv = static_cast<uint64_t>(v) >> s;
    if (v < 0) uv |= ~0ULL << (64 - s);
    return static_cast<int64_t>(uv);
}

}  // namespace

ScalarRange::ScalarRange() : smin_(kSMin), smax_(kSMax), umin_(0), umax_(kUMax) {}

ScalarRange ScalarRange::full() { return ScalarRange(); }

ScalarRange ScalarRange::constant(uint64_t value) {
    ScalarRange r;
    r.umin_ = r.umax_ = value;
    r.smin_ = r.smax_ = static_cast<int64_t>(value);
    return r;
}

ScalarRange ScalarRange::raw(int64_t smin, int64_t smax, uint64_t umin, uint64_t umax) {
    ScalarRange r;
    r.smin_ = smin;
    r.smax_ = smax;
    r.umin_ = umin;
    r.umax_ = umax;
    r.normalize();
    return r;
}

bool ScalarRange::isEmpty() const { return umin_ > umax_; }

bool ScalarRange::isConstant() const { return !isEmpty() && umin_ == umax_; }

uint64_t ScalarRange::constValue() const { return umin_; }

bool ScalarRange::contains(uint64_t value) const {
    if (isEmpty()) return false;
    int64_t sv = static_cast<int64_t>(value);
    return value >= umin_ && value <= umax_ && sv >= smin_ && sv <= smax_;
}

void ScalarRange::setEmpty() {
    umin_ = 1;
    umax_ = 0;
    smin_ = 1;
    smax_ = 0;
}

// Cross-deduces bounds between the signed and unsigned intervals and
// collapses the range to empty when the two contradict each other.
void ScalarRange::normalize() {
    if (umin_ > umax_ || smin_ > smax_) {
        setEmpty();
        return;
    }
    // Unsigned interval -> signed interval. Only single-sided unsigned
    // intervals (all non-negative or all negative as i64) transfer.
    if (umax_ <= static_cast<uint64_t>(kSMax)) {
        smin_ = std::max(smin_, static_cast<int64_t>(umin_));
        smax_ = std::min(smax_, static_cast<int64_t>(umax_));
    } else if (umin_ > static_cast<uint64_t>(kSMax)) {
        smin_ = std::max(smin_, static_cast<int64_t>(umin_));
        smax_ = std::min(smax_, static_cast<int64_t>(umax_));
    }
    // Signed interval -> unsigned interval.
    if (smin_ >= 0) {
        umin_ = std::max(umin_, static_cast<uint64_t>(smin_));
        umax_ = std::min(umax_, static_cast<uint64_t>(smax_));
    } else if (smax_ < 0) {
        umin_ = std::max(umin_, static_cast<uint64_t>(smin_));
        umax_ = std::min(umax_, static_cast<uint64_t>(smax_));
    }
    if (umin_ > umax_ || smin_ > smax_) setEmpty();
}

void ScalarRange::add(const ScalarRange& rhs) {
    if (isEmpty() || rhs.isEmpty()) {
        setEmpty();
        return;
    }
    // Wrap on either end degrades the unsigned interval to full domain.
    if (umin_ + rhs.umin_ < umin_ || umax_ + rhs.umax_ < umax_) {
        umin_ = 0;
        umax_ = kUMax;
    } else {
        umin_ += rhs.umin_;
        umax_ += rhs.umax_;
    }
    if (addOverflowsSigned(smin_, rhs.smin_) || addOverflowsSigned(smax_, rhs.smax_)) {
        smin_ = kSMin;
        smax_ = kSMax;
    } else {
        smin_ += rhs.smin_;
        smax_ += rhs.smax_;
    }
    normalize();
}

void ScalarRange::sub(const ScalarRange& rhs) {
    if (isEmpty() || rhs.isEmpty()) {
        setEmpty();
        return;
    }
    if (umin_ < rhs.umax_) {  // unsigned underflow possible
        umin_ = 0;
        umax_ = kUMax;
    } else {
        uint64_t lo = umin_ - rhs.umax_;
        umax_ -= rhs.umin_;
        umin_ = lo;
    }
    if (subOverflowsSigned(smin_, rhs.smax_) || subOverflowsSigned(smax_, rhs.smin_)) {
        smin_ = kSMin;
        smax_ = kSMax;
    } else {
        int64_t lo = smin_ - rhs.smax_;
        smax_ -= rhs.smin_;
        smin_ = lo;
    }
    normalize();
}

void ScalarRange::bitwiseAnd(const ScalarRange& rhs) {
    if (isEmpty() || rhs.isEmpty()) {
        setEmpty();
        return;
    }
    // Result bits can only be set where both operands have a fixed 1;
    // the result never exceeds either operand's unsigned maximum.
    umin_ = fixedOnes(umin_, umax_) & fixedOnes(rhs.umin_, rhs.umax_);
    umax_ = std::min(umax_, rhs.umax_);
    smin_ = kSMin;
    smax_ = kSMax;
    normalize();
}

void ScalarRange::shl(const ScalarRange& shift) {
    if (isEmpty() || shift.isEmpty()) {
        setEmpty();
        return;
    }
    if (shift.umax_ >= 64 || umax_ > (kUMax >> shift.umax_)) {
        // Shift may be >= 64 or may overflow: result is unknown.
        umin_ = 0;
        umax_ = kUMax;
    } else {
        umin_ <<= shift.umin_;
        umax_ <<= shift.umax_;
    }
    smin_ = kSMin;
    smax_ = kSMax;
    normalize();
}

void ScalarRange::lshr(const ScalarRange& shift) {
    if (isEmpty() || shift.isEmpty()) {
        setEmpty();
        return;
    }
    if (shift.umax_ >= 64) {
        umin_ = 0;
        umax_ = kUMax;
    } else {
        umin_ >>= shift.umax_;
        umax_ >>= shift.umin_;
    }
    smin_ = kSMin;
    smax_ = kSMax;
    normalize();
}

void ScalarRange::ashr(const ScalarRange& shift) {
    if (isEmpty() || shift.isEmpty()) {
        setEmpty();
        return;
    }
    if (shift.umax_ >= 64) {
        umin_ = 0;
        umax_ = kUMax;
        smin_ = kSMin;
        smax_ = kSMax;
    } else {
        smin_ = shiftRightArithmetic(smin_, static_cast<unsigned>(shift.umax_));
        smax_ = shiftRightArithmetic(smax_, static_cast<unsigned>(shift.umin_));
        umin_ = 0;
        umax_ = kUMax;
    }
    normalize();
}

void ScalarRange::intersect(ScalarRange& a, ScalarRange& b) {
    uint64_t umin = std::max(a.umin_, b.umin_);
    uint64_t umax = std::min(a.umax_, b.umax_);
    int64_t smin = std::max(a.smin_, b.smin_);
    int64_t smax = std::min(a.smax_, b.smax_);
    a.umin_ = b.umin_ = umin;
    a.umax_ = b.umax_ = umax;
    a.smin_ = b.smin_ = smin;
    a.smax_ = b.smax_ = smax;
}

// Refines `a` (and symmetrically `b`) under `a != b`. Only singleton
// comparisons allow sound narrowing for inequality.
void ScalarRange::narrowNe(ScalarRange& a, ScalarRange& b) {
    if (b.isConstant()) {
        uint64_t c = b.constValue();
        if (a.isConstant() && a.constValue() == c) {
            a.setEmpty();
            b.setEmpty();
            return;
        }
        if (a.umin_ == c && a.umin_ < a.umax_) {
            ++a.umin_;
        } else if (a.umax_ == c && a.umin_ < a.umax_) {
            --a.umax_;
        }
        int64_t sc = static_cast<int64_t>(c);
        if (a.smin_ == sc && a.smin_ < a.smax_) {
            ++a.smin_;
        } else if (a.smax_ == sc && a.smin_ < a.smax_) {
            --a.smax_;
        }
    } else if (a.isConstant()) {
        narrowNe(b, a);
    }
}

void ScalarRange::ltU(ScalarRange& a, ScalarRange& b) {
    if (b.umax_ == 0 || a.umin_ == kUMax) {
        a.setEmpty();
        b.setEmpty();
        return;
    }
    a.umax_ = std::min(a.umax_, b.umax_ - 1);
    b.umin_ = std::max(b.umin_, a.umin_ + 1);
}

void ScalarRange::leU(ScalarRange& a, ScalarRange& b) {
    a.umax_ = std::min(a.umax_, b.umax_);
    b.umin_ = std::max(b.umin_, a.umin_);
}

void ScalarRange::gtU(ScalarRange& a, ScalarRange& b) {
    if (a.umax_ == 0 || b.umin_ == kUMax) {
        a.setEmpty();
        b.setEmpty();
        return;
    }
    a.umin_ = std::max(a.umin_, b.umin_ + 1);
    b.umax_ = std::min(b.umax_, a.umax_ - 1);
}

void ScalarRange::geU(ScalarRange& a, ScalarRange& b) {
    a.umin_ = std::max(a.umin_, b.umin_);
    b.umax_ = std::min(b.umax_, a.umax_);
}

void ScalarRange::ltS(ScalarRange& a, ScalarRange& b) {
    if (b.smax_ == kSMin || a.smin_ == kSMax) {
        a.setEmpty();
        b.setEmpty();
        return;
    }
    a.smax_ = std::min(a.smax_, b.smax_ - 1);
    b.smin_ = std::max(b.smin_, a.smin_ + 1);
}

void ScalarRange::leS(ScalarRange& a, ScalarRange& b) {
    a.smax_ = std::min(a.smax_, b.smax_);
    b.smin_ = std::max(b.smin_, a.smin_);
}

void ScalarRange::gtS(ScalarRange& a, ScalarRange& b) {
    if (a.smax_ == kSMin || b.smin_ == kSMax) {
        a.setEmpty();
        b.setEmpty();
        return;
    }
    a.smin_ = std::max(a.smin_, b.smin_ + 1);
    b.smax_ = std::min(b.smax_, a.smax_ - 1);
}

void ScalarRange::geS(ScalarRange& a, ScalarRange& b) {
    a.smin_ = std::max(a.smin_, b.smin_);
    b.smax_ = std::min(b.smax_, a.smax_);
}

RangeBranch ScalarRange::narrowBranch(JumpOp op, const ScalarRange& lhs,
                                      const ScalarRange& rhs) {
    RangeBranch br{lhs, rhs, lhs, rhs};
    switch (op) {
        case JumpOp::Eq:
            intersect(br.takenLeft, br.takenRight);
            narrowNe(br.fallLeft, br.fallRight);
            break;
        case JumpOp::Ne:
            narrowNe(br.takenLeft, br.takenRight);
            intersect(br.fallLeft, br.fallRight);
            break;
        case JumpOp::ULt:
            ltU(br.takenLeft, br.takenRight);
            geU(br.fallLeft, br.fallRight);
            break;
        case JumpOp::ULe:
            leU(br.takenLeft, br.takenRight);
            gtU(br.fallLeft, br.fallRight);
            break;
        case JumpOp::UGt:
            gtU(br.takenLeft, br.takenRight);
            leU(br.fallLeft, br.fallRight);
            break;
        case JumpOp::UGe:
            geU(br.takenLeft, br.takenRight);
            ltU(br.fallLeft, br.fallRight);
            break;
        case JumpOp::SLt:
            ltS(br.takenLeft, br.takenRight);
            geS(br.fallLeft, br.fallRight);
            break;
        case JumpOp::SLe:
            leS(br.takenLeft, br.takenRight);
            gtS(br.fallLeft, br.fallRight);
            break;
        case JumpOp::SGt:
            gtS(br.takenLeft, br.takenRight);
            leS(br.fallLeft, br.fallRight);
            break;
        case JumpOp::SGe:
            geS(br.takenLeft, br.takenRight);
            ltS(br.fallLeft, br.fallRight);
            break;
    }
    br.takenLeft.normalize();
    br.takenRight.normalize();
    br.fallLeft.normalize();
    br.fallRight.normalize();
    return br;
}

bool checkMapValueAccess(const ScalarRange& offset, uint64_t accessWidth, uint64_t valueSize) {
    if (offset.isEmpty()) return false;
    if (accessWidth > valueSize) return false;
    // The offset interval must not contain negative values...
    if (offset.smin() < 0) return false;
    // ...and [offset, offset + accessWidth) must fit inside the value.
    // Formulated without overflow: offset.umax() <= valueSize - width.
    if (offset.umax() > valueSize - accessWidth) return false;
    return true;
}

}  // namespace netsec
