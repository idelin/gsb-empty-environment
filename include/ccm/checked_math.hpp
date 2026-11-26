// Overflow-checked integer primitives.  Every span computation that crosses an
// attacker-controlled boundary goes through these helpers, so a wrap-around can
// never smuggle an out-of-range pointer past the auditor.
#ifndef CCM_CHECKED_MATH_HPP
#define CCM_CHECKED_MATH_HPP

#include <limits>

#include "ccm/types.hpp"

namespace ccm {

// Returns true when base + delta overflows T; otherwise writes the exact
// result to *out.
template <typename T>
bool checked_add(T base, T delta, T* out) {
#if defined(__GNUC__) || defined(__clang__)
    return __builtin_add_overflow(base, delta, out);
#else
    if (delta > (std::numeric_limits<T>::max() - base)) {
        return true;
    }
    *out = static_cast<T>(base + delta);
    return false;
#endif
}

// Returns true when a * b overflows T; otherwise writes the exact result.
template <typename T>
bool checked_mul(T a, T b, T* out) {
#if defined(__GNUC__) || defined(__clang__)
    return __builtin_mul_overflow(a, b, out);
#else
    if (a != 0 && b > (std::numeric_limits<T>::max() / a)) {
        return true;
    }
    *out = static_cast<T>(a * b);
    return false;
#endif
}

// Convenience wrapper for the common "pointer + span" audit.  Returns true
// when [base, base + size) cannot be represented.
inline bool checked_span_end(addr_t base, len_t size, addr_t* end) {
    return checked_add<addr_t>(base, static_cast<addr_t>(size), end);
}

}  // namespace ccm

#endif  // CCM_CHECKED_MATH_HPP
