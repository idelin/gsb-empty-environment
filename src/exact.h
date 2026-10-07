// Exact arithmetic: arbitrary precision integer + rational number.
// Self-contained (no external dependencies).
#pragma once
#include <cstdint>
#include <string>
#include <vector>
#include <stdexcept>
#include <algorithm>

class BigInt {
public:
    // magnitude stored little-endian, no trailing zero limbs; sign in {-1,0,1}
    std::vector<uint32_t> mag;
    int sign = 0;

    BigInt() {}
    BigInt(long long v) { *this = fromLL(v); }
    static BigInt fromLL(long long v) {
        BigInt r;
        if (v == 0) return r;
        unsigned long long u;
        if (v < 0) { r.sign = -1; u = (unsigned long long)(-(v + 1)) + 1; }
        else { r.sign = 1; u = (unsigned long long)v; }
        while (u) { r.mag.push_back((uint32_t)(u & 0xffffffffu)); u >>= 32; }
        return r;
    }
    static BigInt fromString(const std::string& s) {
        size_t i = 0; bool neg = false;
        if (i < s.size() && (s[i] == '-' || s[i] == '+')) { neg = (s[i] == '-'); ++i; }
        BigInt r;
        for (; i < s.size(); ++i) {
            if (s[i] < '0' || s[i] > '9') throw std::runtime_error("bad integer: " + s);
            r = r * BigInt(10) + BigInt(s[i] - '0');
        }
        if (neg) r.sign = -r.sign;
        return r;
    }
    bool isZero() const { return sign == 0; }
    void trim() { while (!mag.empty() && mag.back() == 0) mag.pop_back(); if (mag.empty()) sign = 0; }
    BigInt operator-() const { BigInt r = *this; r.sign = -r.sign; return r; }
    BigInt abs() const { BigInt r = *this; if (r.sign < 0) r.sign = -r.sign; return r; }

    static int cmpAbs(const BigInt& a, const BigInt& b) {
        if (a.mag.size() != b.mag.size()) return a.mag.size() < b.mag.size() ? -1 : 1;
        for (size_t i = a.mag.size(); i-- > 0;)
            if (a.mag[i] != b.mag[i]) return a.mag[i] < b.mag[i] ? -1 : 1;
        return 0;
    }
    static int cmp(const BigInt& a, const BigInt& b) {
        if (a.sign != b.sign) return a.sign < b.sign ? -1 : 1;
        if (a.sign == 0) return 0;
        int c = cmpAbs(a, b);
        return a.sign > 0 ? c : -c;
    }
    static BigInt addAbs(const BigInt& a, const BigInt& b) {
        BigInt r; r.sign = 1; r.mag.resize(std::max(a.mag.size(), b.mag.size()) + 1, 0);
        uint64_t carry = 0;
        for (size_t i = 0; i < r.mag.size(); ++i) {
            uint64_t s = carry;
            if (i < a.mag.size()) s += a.mag[i];
            if (i < b.mag.size()) s += b.mag[i];
            r.mag[i] = (uint32_t)(s & 0xffffffffu); carry = s >> 32;
        }
        r.trim(); return r;
    }
    // requires |a| >= |b|
    static BigInt subAbs(const BigInt& a, const BigInt& b) {
        BigInt r; r.sign = 1; r.mag.resize(a.mag.size(), 0);
        long long borrow = 0;
        for (size_t i = 0; i < a.mag.size(); ++i) {
            long long d = (long long)a.mag[i] - borrow - (i < b.mag.size() ? b.mag[i] : 0);
            if (d < 0) { d += (1LL << 32); borrow = 1; } else borrow = 0;
            r.mag[i] = (uint32_t)d;
        }
        r.trim(); return r;
    }
    BigInt operator+(const BigInt& o) const {
        if (sign == 0) return o;
        if (o.sign == 0) return *this;
        if (sign == o.sign) { BigInt r = addAbs(*this, o); r.sign = sign; r.trim(); return r; }
        int c = cmpAbs(*this, o);
        if (c == 0) return BigInt();
        if (c > 0) { BigInt r = subAbs(*this, o); r.sign = sign; r.trim(); return r; }
        BigInt r = subAbs(o, *this); r.sign = o.sign; r.trim(); return r;
    }
    BigInt operator-(const BigInt& o) const { return *this + (-o); }
    BigInt operator*(const BigInt& o) const {
        if (sign == 0 || o.sign == 0) return BigInt();
        BigInt r; r.sign = sign * o.sign; r.mag.assign(mag.size() + o.mag.size(), 0);
        for (size_t i = 0; i < mag.size(); ++i) {
            uint64_t carry = 0;
            for (size_t j = 0; j < o.mag.size(); ++j) {
                uint64_t cur = r.mag[i + j] + (uint64_t)mag[i] * o.mag[j] + carry;
                r.mag[i + j] = (uint32_t)(cur & 0xffffffffu); carry = cur >> 32;
            }
            size_t k = i + o.mag.size();
            while (carry) {
                uint64_t cur = (uint64_t)r.mag[k] + carry;
                r.mag[k] = (uint32_t)(cur & 0xffffffffu); carry = cur >> 32; ++k;
                if (k >= r.mag.size() && carry) r.mag.push_back(0);
            }
        }
        r.trim(); return r;
    }
    // unsigned division of magnitudes: returns {quotient, remainder}, both non-negative
    static std::pair<BigInt, BigInt> divmodAbs(const BigInt& a, const BigInt& b) {
        if (b.isZero()) throw std::runtime_error("division by zero");
        if (cmpAbs(a, b) < 0) return { BigInt(), a.abs() };
        BigInt q; q.sign = 1; q.mag.assign(a.mag.size(), 0);
        BigInt r; // remainder
        auto bitLen = [](const BigInt& x) {
            if (x.mag.empty()) return (size_t)0;
            uint32_t top = x.mag.back(); size_t bits = (x.mag.size() - 1) * 32;
            while (top) { ++bits; top >>= 1; }
            return bits;
        };
        size_t n = bitLen(a);
        for (size_t i = n; i-- > 0;) {
            // r <<= 1
            uint32_t carry = 0;
            for (size_t k = 0; k < r.mag.size(); ++k) {
                uint32_t nc = r.mag[k] >> 31; r.mag[k] = (r.mag[k] << 1) | carry; carry = nc;
            }
            if (carry) r.mag.push_back(carry);
            // r |= bit i of a
            if ((a.mag[i / 32] >> (i % 32)) & 1u) {
                if (r.mag.empty()) r.mag.push_back(1); else r.mag[0] |= 1u;
            }
            r.sign = r.mag.empty() ? 0 : 1;
            if (cmpAbs(r, b) >= 0) { r = subAbs(r, b); r.sign = r.mag.empty() ? 0 : 1; q.mag[i / 32] |= (1u << (i % 32)); }
        }
        q.trim(); r.trim();
        return { q, r };
    }
    // truncated division; signs: quotient sign = a.sign*b.sign, remainder follows a
    BigInt operator/(const BigInt& o) const {
        auto qr = divmodAbs(this->abs(), o.abs());
        qr.first.sign = (qr.first.isZero()) ? 0 : sign * o.sign;
        return qr.first;
    }
    BigInt operator%(const BigInt& o) const {
        auto qr = divmodAbs(this->abs(), o.abs());
        qr.second.sign = (qr.second.isZero()) ? 0 : sign;
        return qr.second;
    }
    static BigInt gcd(BigInt a, BigInt b) {
        a = a.abs(); b = b.abs();
        while (!b.isZero()) { BigInt t = a % b; a = b; b = t; }
        return a;
    }
    bool operator==(const BigInt& o) const { return sign == o.sign && mag == o.mag; }
    bool operator!=(const BigInt& o) const { return !(*this == o); }
    bool operator<(const BigInt& o) const { return cmp(*this, o) < 0; }
    bool operator<=(const BigInt& o) const { return cmp(*this, o) <= 0; }
    bool operator>(const BigInt& o) const { return cmp(*this, o) > 0; }
    std::string toString() const {
        if (sign == 0) return "0";
        // repeatedly divide by 10^9
        BigInt a = this->abs();
        std::string s;
        BigInt base(1000000000);
        while (!a.isZero()) {
            auto qr = divmodAbs(a, base);
            a = qr.first;
            unsigned long long chunk = qr.second.mag.empty() ? 0 : qr.second.mag[0];
            std::string part = std::to_string(chunk);
            if (!a.isZero()) s = std::string(9 - part.size(), '0') + part + s;
            else s = part + s;
        }
        if (sign < 0) s = "-" + s;
        return s;
    }
};

class Rat {
public:
    BigInt num, den; // den > 0, gcd(|num|, den) == 1
    Rat() : num(0), den(1) {}
    Rat(long long v) : num(v), den(1) {}
    Rat(BigInt n, BigInt d) {
        if (d.isZero()) throw std::runtime_error("zero denominator");
        if (d.sign < 0) { n = -n; d = -d; }
        BigInt g = BigInt::gcd(n, d);
        if (g.isZero()) g = BigInt(1);
        num = n / g; den = d / g;
    }
    static Rat parse(const std::string& s) {
        auto slash = s.find('/');
        if (slash != std::string::npos)
            return Rat(BigInt::fromString(s.substr(0, slash)), BigInt::fromString(s.substr(slash + 1)));
        auto dot = s.find('.');
        if (dot != std::string::npos) {
            std::string ip = s.substr(0, dot), fp = s.substr(dot + 1);
            bool neg = false;
            if (!ip.empty() && ip[0] == '-') { neg = true; ip = ip.substr(1); }
            if (ip.empty()) ip = "0";
            BigInt n = BigInt::fromString(ip);
            BigInt d(1);
            for (size_t i = 0; i < fp.size(); ++i) { n = n * BigInt(10) + BigInt(fp[i] - '0'); d = d * BigInt(10); }
            if (neg) n = -n;
            return Rat(n, d);
        }
        return Rat(BigInt::fromString(s), BigInt(1));
    }
    int sign() const { return num.sign; }
    bool isZero() const { return num.isZero(); }
    Rat operator-() const { return Rat(-num, den); }
    Rat operator+(const Rat& o) const { return Rat(num * o.den + o.num * den, den * o.den); }
    Rat operator-(const Rat& o) const { return Rat(num * o.den - o.num * den, den * o.den); }
    Rat operator*(const Rat& o) const { return Rat(num * o.num, den * o.den); }
    Rat operator/(const Rat& o) const {
        if (o.isZero()) throw std::runtime_error("rat division by zero");
        BigInt d = den * o.num;
        if (d.sign < 0) return Rat(-(num * o.den), -d);
        return Rat(num * o.den, d);
    }
    static int cmp(const Rat& a, const Rat& b) { return BigInt::cmp(a.num * b.den, b.num * a.den); }
    bool operator==(const Rat& o) const { return num == o.num && den == o.den; }
    bool operator!=(const Rat& o) const { return !(*this == o); }
    bool operator<(const Rat& o) const { return cmp(*this, o) < 0; }
    bool operator<=(const Rat& o) const { return cmp(*this, o) <= 0; }
    bool operator>(const Rat& o) const { return cmp(*this, o) > 0; }
    Rat abs() const { return Rat(num.abs(), den); }
    std::string toString() const {
        if (den == BigInt(1)) return num.toString();
        return num.toString() + "/" + den.toString();
    }
};
