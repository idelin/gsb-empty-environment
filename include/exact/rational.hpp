// Exact rational number: num/den kept in lowest terms, den > 0.
#pragma once

#include "bigint.hpp"
#include <string>

namespace exact {

class Rational {
public:
    Rational() : n_(0), d_(1) {}
    Rational(long long v) : n_(v), d_(1) {}
    Rational(BigInt num, BigInt den) : n_(std::move(num)), d_(std::move(den)) { normalize(); }

    static Rational fromString(const std::string& s) {
        auto slash = s.find('/');
        if (slash == std::string::npos) return Rational(BigInt::fromString(s), BigInt(1));
        return Rational(BigInt::fromString(s.substr(0, slash)),
                        BigInt::fromString(s.substr(slash + 1)));
    }

    const BigInt& num() const { return n_; }
    const BigInt& den() const { return d_; }
    int sign() const { return n_.sign(); }
    bool isZero() const { return n_.isZero(); }

    Rational operator-() const { return Rational(-n_, d_); }

    friend Rational operator+(const Rational& a, const Rational& b) {
        return Rational(a.n_ * b.d_ + b.n_ * a.d_, a.d_ * b.d_);
    }
    friend Rational operator-(const Rational& a, const Rational& b) {
        return Rational(a.n_ * b.d_ - b.n_ * a.d_, a.d_ * b.d_);
    }
    friend Rational operator*(const Rational& a, const Rational& b) {
        return Rational(a.n_ * b.n_, a.d_ * b.d_);
    }
    friend Rational operator/(const Rational& a, const Rational& b) {
        if (b.isZero()) throw std::domain_error("Rational: division by zero");
        return Rational(a.n_ * b.d_, a.d_ * b.n_);
    }
    Rational& operator+=(const Rational& o) { return *this = *this + o; }
    Rational& operator-=(const Rational& o) { return *this = *this - o; }
    Rational& operator*=(const Rational& o) { return *this = *this * o; }
    Rational& operator/=(const Rational& o) { return *this = *this / o; }

    friend bool operator==(const Rational& a, const Rational& b) { return a.n_ == b.n_ && a.d_ == b.d_; }
    friend bool operator!=(const Rational& a, const Rational& b) { return !(a == b); }
    friend bool operator<(const Rational& a, const Rational& b) { return a.n_ * b.d_ < b.n_ * a.d_; }
    friend bool operator<=(const Rational& a, const Rational& b) { return !(b < a); }
    friend bool operator>(const Rational& a, const Rational& b) { return b < a; }
    friend bool operator>=(const Rational& a, const Rational& b) { return !(a < b); }

    std::string toString() const {
        if (d_ == BigInt(1)) return n_.toString();
        return n_.toString() + "/" + d_.toString();
    }

private:
    BigInt n_, d_;

    void normalize() {
        if (d_.isZero()) throw std::domain_error("Rational: zero denominator");
        if (d_.isNegative()) { n_ = -n_; d_ = -d_; }
        if (n_.isZero()) { d_ = BigInt(1); return; }
        BigInt g = BigInt::gcd(n_, d_);
        if (g != BigInt(1)) { n_ = n_ / g; d_ = d_ / g; }
    }
};

} // namespace exact
