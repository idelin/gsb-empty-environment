#pragma once

#include <cstdint>

namespace netsec {

// Conditional jump operations used to narrow scalar ranges on both
// sides of a branch, mirroring eBPF JMP/JMP32 condition codes.
enum class JumpOp {
    Eq,
    Ne,
    UGt,
    UGe,
    ULt,
    ULe,
    SGt,
    SGe,
    SLt,
    SLe,
};

struct RangeBranch;

// Tracks the set of possible 64-bit scalar values as the intersection of
// a signed interval [smin, smax] and an unsigned interval [umin, umax],
// like the eBPF verifier's register bounds. A range whose two intervals
// contradict each other becomes empty, i.e. the path is unreachable.
class ScalarRange {
public:
    ScalarRange();  // full domain: completely unknown scalar

    static ScalarRange full();
    static ScalarRange constant(uint64_t value);
    // Builds a range from raw bounds and normalizes it (cross-deducing
    // signed/unsigned bounds and detecting contradictions).
    static ScalarRange raw(int64_t smin, int64_t smax, uint64_t umin, uint64_t umax);

    bool isEmpty() const;  // contradictory bounds: unreachable path
    bool isConstant() const;
    uint64_t constValue() const;

    int64_t smin() const { return smin_; }
    int64_t smax() const { return smax_; }
    uint64_t umin() const { return umin_; }
    uint64_t umax() const { return umax_; }

    bool contains(uint64_t value) const;

    // Arithmetic wraps like u64/i64 hardware. Any addition or subtraction
    // that can overflow/underflow degrades the affected interval to the
    // full domain.
    void add(const ScalarRange& rhs);
    void sub(const ScalarRange& rhs);
    void bitwiseAnd(const ScalarRange& rhs);
    void shl(const ScalarRange& shift);
    void lshr(const ScalarRange& shift);
    void ashr(const ScalarRange& shift);

    static RangeBranch narrowBranch(JumpOp op, const ScalarRange& lhs,
                                    const ScalarRange& rhs);

private:
    int64_t smin_, smax_;
    uint64_t umin_, umax_;

    void normalize();  // cross-deduce bounds, collapse contradictions
    void setEmpty();

    static void intersect(ScalarRange& a, ScalarRange& b);
    static void narrowNe(ScalarRange& a, ScalarRange& b);
    static void ltU(ScalarRange& a, ScalarRange& b);
    static void leU(ScalarRange& a, ScalarRange& b);
    static void gtU(ScalarRange& a, ScalarRange& b);
    static void geU(ScalarRange& a, ScalarRange& b);
    static void ltS(ScalarRange& a, ScalarRange& b);
    static void leS(ScalarRange& a, ScalarRange& b);
    static void gtS(ScalarRange& a, ScalarRange& b);
    static void geS(ScalarRange& a, ScalarRange& b);
};

// Result of refining both operands on each side of a conditional jump
// `lhs op rhs`. An empty range marks an unreachable branch.
struct RangeBranch {
    ScalarRange takenLeft, takenRight;  // condition holds
    ScalarRange fallLeft, fallRight;    // condition does not hold
};

// Verifies that adding a scalar with the given range to a map-value
// pointer is safe for an access of `accessWidth` bytes into a value of
// `valueSize` bytes: the offset interval must contain no negative value
// and [offset, offset + accessWidth) must lie inside [0, valueSize).
bool checkMapValueAccess(const ScalarRange& offset, uint64_t accessWidth, uint64_t valueSize);

}  // namespace netsec
