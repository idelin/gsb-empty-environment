#pragma once

#include <cstdint>
#include <ostream>
#include <stdexcept>

namespace fem {

// Exact rational number with normalized sign and reduced numerator/denominator.
// Intermediate arithmetic uses 128-bit integers to delay overflow.
class Rational {
public:
    Rational() : num_(0), den_(1) {}
    Rational(std::int64_t n) : num_(n), den_(1) {}
    Rational(std::int64_t n, std::int64_t d) { *this = reduce(n, d); }

    std::int64_t num() const { return num_; }
    std::int64_t den() const { return den_; }

    bool isZero() const { return num_ == 0; }

    Rational& operator+=(const Rational& o) { return *this = *this + o; }
    Rational& operator-=(const Rational& o) { return *this = *this - o; }
    Rational& operator*=(const Rational& o) { return *this = *this * o; }
    Rational& operator/=(const Rational& o) { return *this = *this / o; }

    friend Rational operator+(const Rational& a, const Rational& b) {
        return reduce(static_cast<__int128>(a.num_) * b.den_ +
                          static_cast<__int128>(b.num_) * a.den_,
                      static_cast<__int128>(a.den_) * b.den_);
    }
    friend Rational operator-(const Rational& a, const Rational& b) {
        return reduce(static_cast<__int128>(a.num_) * b.den_ -
                          static_cast<__int128>(b.num_) * a.den_,
                      static_cast<__int128>(a.den_) * b.den_);
    }
    friend Rational operator*(const Rational& a, const Rational& b) {
        return reduce(static_cast<__int128>(a.num_) * b.num_,
                      static_cast<__int128>(a.den_) * b.den_);
    }
    friend Rational operator/(const Rational& a, const Rational& b) {
        if (b.num_ == 0) throw std::domain_error("Rational: division by zero");
        const __int128 n = static_cast<__int128>(a.num_) * b.den_;
        const __int128 d = static_cast<__int128>(a.den_) * b.num_;
        return reduce(n, d);
    }
    Rational operator-() const { return Rational(-num_, den_); }

    friend bool operator==(const Rational& a, const Rational& b) {
        return a.num_ == b.num_ && a.den_ == b.den_;
    }
    friend bool operator!=(const Rational& a, const Rational& b) { return !(a == b); }
    friend bool operator<(const Rational& a, const Rational& b) {
        return static_cast<__int128>(a.num_) * b.den_ <
               static_cast<__int128>(b.num_) * a.den_;
    }

    friend std::ostream& operator<<(std::ostream& os, const Rational& r) {
        os << r.num_;
        if (r.den_ != 1) os << '/' << r.den_;
        return os;
    }

private:
    static __int128 gcd128(__int128 a, __int128 b) {
        if (a < 0) a = -a;
        if (b < 0) b = -b;
        while (b != 0) {
            __int128 t = a % b;
            a = b;
            b = t;
        }
        return a == 0 ? 1 : a;
    }

    static Rational reduce(__int128 n, __int128 d) {
        if (d == 0) throw std::domain_error("Rational: zero denominator");
        if (d < 0) {
            n = -n;
            d = -d;
        }
        const __int128 g = gcd128(n, d);
        n /= g;
        d /= g;
        if (n > INT64_MAX || n < INT64_MIN || d > INT64_MAX)
            throw std::overflow_error("Rational: value out of int64 range");
        Rational r;
        r.num_ = static_cast<std::int64_t>(n);
        r.den_ = static_cast<std::int64_t>(d);
        return r;
    }

    std::int64_t num_;
    std::int64_t den_;
};

} // namespace fem
