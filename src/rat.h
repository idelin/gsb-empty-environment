#pragma once

// Exact rational number: num/den with den > 0. Values are intentionally NOT
// reduced (no gcd needed); comparisons cross-multiply. All geometric
// predicates in the checker only need exact signs and equality.

#include "bigint.h"

struct Rat {
    BigInt num, den; // invariant: den > 0

    Rat() : num(BigInt(0)), den(BigInt(1)) {}
    Rat(long long v) : num(BigInt(v)), den(BigInt(1)) {}
    Rat(BigInt n, BigInt d) : num(std::move(n)), den(std::move(d)) {
        if (den.isZero()) throw std::runtime_error("zero denominator");
        if (den.sign() < 0) {
            num = -num;
            den = -den;
        }
    }

    static Rat ratio(long long n, long long d) { return Rat(BigInt(n), BigInt(d)); }

    int sign() const { return num.sign(); }
    bool isZero() const { return num.isZero(); }

    Rat operator-() const { return Rat(-num, den); }

    std::string toString() const {
        if (den == BigInt(1)) return num.toString();
        return num.toString() + "/" + den.toString();
    }
};

inline Rat operator+(const Rat& a, const Rat& b) {
    return Rat(a.num * b.den + b.num * a.den, a.den * b.den);
}
inline Rat operator-(const Rat& a, const Rat& b) { return a + (-b); }
inline Rat operator*(const Rat& a, const Rat& b) {
    return Rat(a.num * b.num, a.den * b.den);
}
inline Rat operator/(const Rat& a, const Rat& b) {
    if (b.isZero()) throw std::runtime_error("division by zero");
    return Rat(a.num * b.den, a.den * b.num);
}

inline int compare(const Rat& a, const Rat& b) {
    return compare(a.num * b.den, b.num * a.den);
}
inline bool operator==(const Rat& a, const Rat& b) { return compare(a, b) == 0; }
inline bool operator!=(const Rat& a, const Rat& b) { return compare(a, b) != 0; }
inline bool operator<(const Rat& a, const Rat& b) { return compare(a, b) < 0; }
inline bool operator<=(const Rat& a, const Rat& b) { return compare(a, b) <= 0; }
inline bool operator>(const Rat& a, const Rat& b) { return compare(a, b) > 0; }
inline bool operator>=(const Rat& a, const Rat& b) { return compare(a, b) >= 0; }

inline Rat absRat(const Rat& r) { return r.sign() < 0 ? -r : r; }

// Exact r/2, keeping the denominator small when num is even.
inline Rat halveRat(const Rat& r) {
    if (r.num.isEven()) return Rat(r.num.half(), r.den);
    return Rat(r.num, r.den * BigInt(2));
}
