#pragma once
// Exact rational number: num/den with den > 0 and gcd(|num|, den) == 1.
// All arithmetic is exact; no floating point is ever involved.

#include "bigint.hpp"

namespace exactclip {

class Rational {
public:
    Rational() : num_(0), den_(1) {}
    Rational(long long n) : num_(n), den_(1) {}
    Rational(long long n, long long d) { init(BigInt(n), BigInt(d)); }
    Rational(BigInt n, BigInt d) { init(std::move(n), std::move(d)); }

    const BigInt& num() const { return num_; }
    const BigInt& den() const { return den_; }
    int sign() const { return num_.sign(); }
    bool isZero() const { return num_.isZero(); }

    friend Rational operator+(const Rational& a, const Rational& b) {
        return Rational(a.num_ * b.den_ + b.num_ * a.den_, a.den_ * b.den_);
    }
    friend Rational operator-(const Rational& a, const Rational& b) {
        return Rational(a.num_ * b.den_ - b.num_ * a.den_, a.den_ * b.den_);
    }
    friend Rational operator*(const Rational& a, const Rational& b) {
        return Rational(a.num_ * b.num_, a.den_ * b.den_);
    }
    friend Rational operator/(const Rational& a, const Rational& b) {
        if (b.isZero()) throw std::domain_error("Rational division by zero");
        return Rational(a.num_ * b.den_, a.den_ * b.num_);
    }
    Rational operator-() const { return Rational(-num_, den_); }

    friend bool operator==(const Rational& a, const Rational& b) {
        return a.num_ == b.num_ && a.den_ == b.den_;
    }
    friend bool operator!=(const Rational& a, const Rational& b) { return !(a == b); }
    friend int cmp(const Rational& a, const Rational& b) {
        return cmp(a.num_ * b.den_, b.num_ * a.den_);
    }
    friend bool operator<(const Rational& a, const Rational& b) { return cmp(a, b) < 0; }
    friend bool operator<=(const Rational& a, const Rational& b) { return cmp(a, b) <= 0; }
    friend bool operator>(const Rational& a, const Rational& b) { return cmp(a, b) > 0; }
    friend bool operator>=(const Rational& a, const Rational& b) { return cmp(a, b) >= 0; }

    std::string toString() const {
        if (den_ == BigInt(1)) return num_.toString();
        return num_.toString() + "/" + den_.toString();
    }

private:
    void init(BigInt n, BigInt d) {
        if (d.isZero()) throw std::invalid_argument("Rational with zero denominator");
        if (d.sign() < 0) { n = -n; d = -d; }
        if (n.isZero()) { num_ = BigInt(0); den_ = BigInt(1); return; }
        BigInt g = BigInt::gcd(n, d);
        num_ = n / g;
        den_ = d / g;
    }

    BigInt num_, den_; // den_ > 0, gcd(|num_|, den_) == 1
};

} // namespace exactclip
