#pragma once
// Exact arbitrary-precision signed integer (base 2^32, little-endian magnitude).
// Self-contained replacement for boost::multiprecision::cpp_int, providing the
// operations needed by exact rational geometry: +, -, *, /, %, gcd, compare.

#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>
#include <algorithm>
#include <stdexcept>

namespace exactclip {

namespace detail {

using Mag = std::vector<uint32_t>; // little-endian limbs, no trailing zero limbs

inline void trim(Mag& m) {
    while (!m.empty() && m.back() == 0) m.pop_back();
}

inline int cmpMag(const Mag& a, const Mag& b) {
    if (a.size() != b.size()) return a.size() < b.size() ? -1 : 1;
    for (size_t i = a.size(); i-- > 0;)
        if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
    return 0;
}

inline Mag addMag(const Mag& a, const Mag& b) {
    Mag r;
    r.reserve(std::max(a.size(), b.size()) + 1);
    uint64_t carry = 0;
    for (size_t i = 0; i < std::max(a.size(), b.size()) || carry; ++i) {
        uint64_t v = carry + (i < a.size() ? a[i] : 0) + (i < b.size() ? b[i] : 0);
        r.push_back(static_cast<uint32_t>(v));
        carry = v >> 32;
    }
    return r;
}

// a - b, requires a >= b (magnitudes)
inline Mag subMag(const Mag& a, const Mag& b) {
    Mag r;
    r.reserve(a.size());
    int64_t borrow = 0;
    for (size_t i = 0; i < a.size(); ++i) {
        int64_t v = static_cast<int64_t>(a[i]) - borrow - (i < b.size() ? b[i] : 0);
        if (v < 0) { v += (int64_t{1} << 32); borrow = 1; } else { borrow = 0; }
        r.push_back(static_cast<uint32_t>(v));
    }
    trim(r);
    return r;
}

inline Mag mulMag(const Mag& a, const Mag& b) {
    if (a.empty() || b.empty()) return {};
    Mag r(a.size() + b.size(), 0);
    for (size_t i = 0; i < a.size(); ++i) {
        uint64_t carry = 0;
        for (size_t j = 0; j < b.size(); ++j) {
            uint64_t v = static_cast<uint64_t>(a[i]) * b[j] + r[i + j] + carry;
            r[i + j] = static_cast<uint32_t>(v);
            carry = v >> 32;
        }
        for (size_t k = i + b.size(); carry; ++k) {
            uint64_t v = static_cast<uint64_t>(r[k]) + carry;
            r[k] = static_cast<uint32_t>(v);
            carry = v >> 32;
        }
    }
    trim(r);
    return r;
}

inline void shl1(Mag& a) {
    if (a.empty()) return;
    uint32_t carry = 0;
    for (auto& w : a) {
        uint32_t c = w >> 31;
        w = (w << 1) | carry;
        carry = c;
    }
    if (carry) a.push_back(carry);
}

inline void shr1(Mag& a) {
    uint32_t carry = 0;
    for (size_t i = a.size(); i-- > 0;) {
        uint32_t c = a[i] & 1u;
        a[i] = (a[i] >> 1) | (carry << 31);
        carry = c;
    }
    trim(a);
}

inline bool isOdd(const Mag& a) { return !a.empty() && (a[0] & 1u); }

// q = a / b, r = a % b on magnitudes (b nonzero). Binary long division.
inline void divmodMag(const Mag& a, const Mag& b, Mag& q, Mag& r) {
    q.clear();
    r.clear();
    if (cmpMag(a, b) < 0) { r = a; return; }
    q.assign(a.size(), 0);
    for (size_t i = a.size() * 32; i-- > 0;) {
        shl1(r);
        if ((a[i >> 5] >> (i & 31)) & 1u) {
            if (r.empty()) r.push_back(1u);
            else r[0] |= 1u;
        }
        if (cmpMag(r, b) >= 0) {
            r = subMag(r, b);
            q[i >> 5] |= (1u << (i & 31));
        }
    }
    trim(q);
}

// a = a / d, returns remainder (d nonzero)
inline uint32_t divmodSmall(Mag& a, uint32_t d) {
    uint64_t rem = 0;
    for (size_t i = a.size(); i-- > 0;) {
        uint64_t cur = (rem << 32) | a[i];
        a[i] = static_cast<uint32_t>(cur / d);
        rem = cur % d;
    }
    trim(a);
    return static_cast<uint32_t>(rem);
}

} // namespace detail

class BigInt {
public:
    BigInt() : sign_(0) {}
    BigInt(long long v) {
        if (v == 0) { sign_ = 0; return; }
        sign_ = v > 0 ? 1 : -1;
        unsigned long long u = v > 0 ? static_cast<unsigned long long>(v)
                                     : static_cast<unsigned long long>(-(v + 1)) + 1;
        while (u) { mag_.push_back(static_cast<uint32_t>(u & 0xffffffffu)); u >>= 32; }
    }

    int sign() const { return sign_; }
    bool isZero() const { return sign_ == 0; }

    BigInt abs() const { BigInt r(*this); if (r.sign_ < 0) r.sign_ = 1; return r; }
    BigInt operator-() const { BigInt r(*this); r.sign_ = -r.sign_; return r; }

    friend bool operator==(const BigInt& a, const BigInt& b) {
        return a.sign_ == b.sign_ && a.mag_ == b.mag_;
    }
    friend bool operator!=(const BigInt& a, const BigInt& b) { return !(a == b); }

    friend int cmp(const BigInt& a, const BigInt& b) {
        if (a.sign_ != b.sign_) return a.sign_ < b.sign_ ? -1 : 1;
        int c = detail::cmpMag(a.mag_, b.mag_);
        return a.sign_ < 0 ? -c : c;
    }
    friend bool operator<(const BigInt& a, const BigInt& b) { return cmp(a, b) < 0; }
    friend bool operator<=(const BigInt& a, const BigInt& b) { return cmp(a, b) <= 0; }
    friend bool operator>(const BigInt& a, const BigInt& b) { return cmp(a, b) > 0; }
    friend bool operator>=(const BigInt& a, const BigInt& b) { return cmp(a, b) >= 0; }

    BigInt& operator+=(const BigInt& o) {
        if (o.sign_ == 0) return *this;
        if (sign_ == 0) { *this = o; return *this; }
        if (sign_ == o.sign_) { mag_ = detail::addMag(mag_, o.mag_); return *this; }
        int c = detail::cmpMag(mag_, o.mag_);
        if (c == 0) { sign_ = 0; mag_.clear(); return *this; }
        if (c > 0) { mag_ = detail::subMag(mag_, o.mag_); }
        else { mag_ = detail::subMag(o.mag_, mag_); sign_ = o.sign_; }
        return *this;
    }
    BigInt& operator-=(const BigInt& o) { return *this += (-o); }
    BigInt& operator*=(const BigInt& o) {
        if (sign_ == 0 || o.sign_ == 0) { sign_ = 0; mag_.clear(); return *this; }
        mag_ = detail::mulMag(mag_, o.mag_);
        sign_ *= o.sign_;
        return *this;
    }
    // Truncated division (quotient rounds toward zero).
    BigInt& operator/=(const BigInt& o) {
        if (o.sign_ == 0) throw std::domain_error("BigInt division by zero");
        detail::Mag q, r;
        detail::divmodMag(mag_, o.mag_, q, r);
        mag_ = std::move(q);
        sign_ = mag_.empty() ? 0 : sign_ * o.sign_;
        return *this;
    }
    BigInt& operator%=(const BigInt& o) {
        if (o.sign_ == 0) throw std::domain_error("BigInt modulo by zero");
        detail::Mag q, r;
        detail::divmodMag(mag_, o.mag_, q, r);
        mag_ = std::move(r);
        sign_ = mag_.empty() ? 0 : sign_;
        return *this;
    }

    friend BigInt operator+(BigInt a, const BigInt& b) { return a += b; }
    friend BigInt operator-(BigInt a, const BigInt& b) { return a -= b; }
    friend BigInt operator*(BigInt a, const BigInt& b) { return a *= b; }
    friend BigInt operator/(BigInt a, const BigInt& b) { return a /= b; }
    friend BigInt operator%(BigInt a, const BigInt& b) { return a %= b; }

    // Greatest common divisor of |a| and |b|; gcd(0,0) == 0. Binary GCD.
    static BigInt gcd(BigInt a, BigInt b) {
        a = a.abs();
        b = b.abs();
        if (a.isZero()) return b;
        if (b.isZero()) return a;
        unsigned shift = 0;
        while (!detail::isOdd(a.mag_) && !detail::isOdd(b.mag_)) {
            detail::shr1(a.mag_);
            detail::shr1(b.mag_);
            ++shift;
        }
        while (!detail::isOdd(a.mag_)) detail::shr1(a.mag_);
        while (!b.isZero()) {
            while (!detail::isOdd(b.mag_)) detail::shr1(b.mag_);
            if (detail::cmpMag(a.mag_, b.mag_) > 0) std::swap(a, b);
            b.mag_ = detail::subMag(b.mag_, a.mag_);
            if (b.mag_.empty()) b.sign_ = 0;
        }
        while (shift--) detail::shl1(a.mag_);
        return a;
    }

    std::string toString() const {
        if (sign_ == 0) return "0";
        detail::Mag m = mag_;
        std::string s;
        while (!m.empty()) {
            uint32_t rem = detail::divmodSmall(m, 1000000000u);
            char buf[16];
            std::snprintf(buf, sizeof buf, m.empty() ? "%u" : "%09u", rem);
            s = std::string(buf) + s;
        }
        return (sign_ < 0 ? "-" : "") + s;
    }

private:
    int sign_;         // -1, 0, +1
    detail::Mag mag_;  // magnitude, empty iff sign_ == 0
};

} // namespace exactclip
