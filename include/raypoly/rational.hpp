#pragma once

#include <stdexcept>
#include <string>
#include <utility>

#include "bigint.hpp"

namespace raypoly {

// Exact rational number, kept normalized: denominator > 0, gcd(|num|, den) == 1.
class Rational {
public:
    Rational() : num_(0), den_(1) {}
    Rational(long long n) : num_(n), den_(1) {} // NOLINT(google-explicit-constructor)
    Rational(long long n, long long d) : num_(n), den_(d) { normalize(); }
    Rational(BigInt n, BigInt d) : num_(std::move(n)), den_(std::move(d)) { normalize(); }

    const BigInt& num() const { return num_; }
    const BigInt& den() const { return den_; }
    int sign() const { return num_.sign(); }

    friend Rational operator-(const Rational& a) { return Rational(-a.num_, a.den_); }

    friend Rational operator+(const Rational& a, const Rational& b) {
        return Rational(a.num_ * b.den_ + b.num_ * a.den_, a.den_ * b.den_);
    }
    friend Rational operator-(const Rational& a, const Rational& b) { return a + (-b); }
    friend Rational operator*(const Rational& a, const Rational& b) {
        return Rational(a.num_ * b.num_, a.den_ * b.den_);
    }
    friend Rational operator/(const Rational& a, const Rational& b) {
        if (b.num_.sign() == 0) throw std::domain_error("Rational division by zero");
        return Rational(a.num_ * b.den_, a.den_ * b.num_);
    }

    friend bool operator==(const Rational& a, const Rational& b) {
        return a.num_ == b.num_ && a.den_ == b.den_;
    }
    friend bool operator!=(const Rational& a, const Rational& b) { return !(a == b); }
    friend bool operator<(const Rational& a, const Rational& b) {
        return a.num_ * b.den_ < b.num_ * a.den_;
    }
    friend bool operator>(const Rational& a, const Rational& b) { return b < a; }
    friend bool operator<=(const Rational& a, const Rational& b) { return !(b < a); }
    friend bool operator>=(const Rational& a, const Rational& b) { return !(a < b); }

    std::string to_string() const {
        if (den_ == BigInt(1)) return num_.to_string();
        return num_.to_string() + "/" + den_.to_string();
    }

private:
    BigInt num_, den_;

    void normalize() {
        if (den_.sign() == 0) throw std::invalid_argument("Rational with zero denominator");
        if (den_.sign() < 0) {
            num_ = -num_;
            den_ = -den_;
        }
        if (num_.sign() == 0) {
            den_ = BigInt(1);
            return;
        }
        BigInt g = BigInt::gcd(num_, den_);
        if (!(g == BigInt(1))) {
            num_ = num_ / g;
            den_ = den_ / g;
        }
    }
};

} // namespace raypoly
