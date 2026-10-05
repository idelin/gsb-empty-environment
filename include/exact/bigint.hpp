// Exact arbitrary-precision signed integer (header-only, C++17).
// Magnitude stored little-endian in base 2^32; zero has empty magnitude.
#pragma once

#include <cstdint>
#include <string>
#include <vector>
#include <stdexcept>
#include <algorithm>

namespace exact {

class BigInt {
public:
    BigInt() = default;
    BigInt(long long v) {
        if (v < 0) { neg_ = true; }
        unsigned long long u = v < 0 ? (~(unsigned long long)v) + 1ULL : (unsigned long long)v;
        while (u) { mag_.push_back((uint32_t)(u & 0xffffffffULL)); u >>= 32; }
    }

    static BigInt fromString(const std::string& s) {
        size_t i = 0;
        bool neg = false;
        if (i < s.size() && (s[i] == '-' || s[i] == '+')) { neg = (s[i] == '-'); ++i; }
        BigInt r;
        for (; i < s.size(); ++i) {
            if (s[i] < '0' || s[i] > '9') throw std::invalid_argument("BigInt::fromString");
            r = r * BigInt(10) + BigInt(s[i] - '0');
        }
        if (neg && !r.isZero()) r.neg_ = true;
        return r;
    }

    bool isZero() const { return mag_.empty(); }
    bool isNegative() const { return neg_; }
    int sign() const { return mag_.empty() ? 0 : (neg_ ? -1 : 1); }

    BigInt operator-() const { BigInt r = *this; if (!r.isZero()) r.neg_ = !r.neg_; return r; }
    BigInt abs() const { BigInt r = *this; r.neg_ = false; return r; }

    friend bool operator==(const BigInt& a, const BigInt& b) { return a.neg_ == b.neg_ && a.mag_ == b.mag_; }
    friend bool operator!=(const BigInt& a, const BigInt& b) { return !(a == b); }
    friend bool operator<(const BigInt& a, const BigInt& b) {
        if (a.neg_ != b.neg_) return a.neg_;
        int c = cmpMag(a.mag_, b.mag_);
        return a.neg_ ? c > 0 : c < 0;
    }
    friend bool operator<=(const BigInt& a, const BigInt& b) { return !(b < a); }
    friend bool operator>(const BigInt& a, const BigInt& b) { return b < a; }
    friend bool operator>=(const BigInt& a, const BigInt& b) { return !(a < b); }

    BigInt& operator+=(const BigInt& o) {
        if (o.isZero()) return *this;
        if (isZero()) { *this = o; return *this; }
        if (neg_ == o.neg_) { mag_ = addMag(mag_, o.mag_); }
        else {
            int c = cmpMag(mag_, o.mag_);
            if (c == 0) { mag_.clear(); neg_ = false; }
            else if (c > 0) mag_ = subMag(mag_, o.mag_);
            else { mag_ = subMag(o.mag_, mag_); neg_ = o.neg_; }
        }
        return *this;
    }
    BigInt& operator-=(const BigInt& o) { return *this += (-o); }
    friend BigInt operator+(BigInt a, const BigInt& b) { return a += b; }
    friend BigInt operator-(BigInt a, const BigInt& b) { return a -= b; }

    BigInt& operator*=(const BigInt& o) {
        if (isZero() || o.isZero()) { mag_.clear(); neg_ = false; return *this; }
        std::vector<uint32_t> r(mag_.size() + o.mag_.size(), 0);
        for (size_t i = 0; i < mag_.size(); ++i) {
            uint64_t carry = 0;
            for (size_t j = 0; j < o.mag_.size(); ++j) {
                uint64_t cur = r[i + j] + (uint64_t)mag_[i] * o.mag_[j] + carry;
                r[i + j] = (uint32_t)(cur & 0xffffffffULL);
                carry = cur >> 32;
            }
            size_t k = i + o.mag_.size();
            while (carry) {
                uint64_t cur = (uint64_t)r[k] + carry;
                r[k] = (uint32_t)(cur & 0xffffffffULL);
                carry = cur >> 32;
                ++k;
            }
        }
        mag_ = std::move(r);
        trim();
        neg_ = (neg_ != o.neg_);
        return *this;
    }
    friend BigInt operator*(BigInt a, const BigInt& b) { return a *= b; }

    // Truncating division; remainder has the sign of the dividend (like C++).
    static void divmod(const BigInt& a, const BigInt& b, BigInt& q, BigInt& r) {
        if (b.isZero()) throw std::domain_error("BigInt: division by zero");
        if (cmpMag(a.mag_, b.mag_) < 0) { q = BigInt(0); r = a; return; }
        std::vector<uint32_t> qm, rm;
        divmodMag(a.mag_, b.mag_, qm, rm);
        q.mag_ = std::move(qm); q.trim();
        q.neg_ = !q.isZero() && (a.neg_ != b.neg_);
        r.mag_ = std::move(rm); r.trim();
        r.neg_ = !r.isZero() && a.neg_;
    }
    friend BigInt operator/(const BigInt& a, const BigInt& b) { BigInt q, r; divmod(a, b, q, r); return q; }
    friend BigInt operator%(const BigInt& a, const BigInt& b) { BigInt q, r; divmod(a, b, q, r); return r; }

    static BigInt gcd(BigInt a, BigInt b) {
        a.neg_ = false; b.neg_ = false;
        while (!b.isZero()) {
            BigInt q, r;
            divmod(a, b, q, r);
            a = b; b = r;
        }
        return a;
    }

    std::string toString() const {
        if (isZero()) return "0";
        const BigInt base(1000000000LL);
        BigInt t = *this; t.neg_ = false;
        std::string s;
        while (!t.isZero()) {
            BigInt q, r;
            divmod(t, base, q, r);
            long long chunk = r.isZero() ? 0 : (long long)r.mag_[0];
            if (!q.isZero()) {
                std::string part = std::to_string(chunk);
                s = std::string(9 - part.size(), '0') + part + s;
            } else {
                s = std::to_string(chunk) + s;
            }
            t = q;
        }
        return neg_ ? "-" + s : s;
    }

private:
    std::vector<uint32_t> mag_;
    bool neg_ = false;

    void trim() {
        while (!mag_.empty() && mag_.back() == 0) mag_.pop_back();
        if (mag_.empty()) neg_ = false;
    }

    static int cmpMag(const std::vector<uint32_t>& a, const std::vector<uint32_t>& b) {
        if (a.size() != b.size()) return a.size() < b.size() ? -1 : 1;
        for (size_t i = a.size(); i-- > 0;)
            if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
        return 0;
    }

    static std::vector<uint32_t> addMag(const std::vector<uint32_t>& a, const std::vector<uint32_t>& b) {
        std::vector<uint32_t> r;
        r.reserve(std::max(a.size(), b.size()) + 1);
        uint64_t carry = 0;
        for (size_t i = 0; i < std::max(a.size(), b.size()); ++i) {
            uint64_t cur = carry;
            if (i < a.size()) cur += a[i];
            if (i < b.size()) cur += b[i];
            r.push_back((uint32_t)(cur & 0xffffffffULL));
            carry = cur >> 32;
        }
        if (carry) r.push_back((uint32_t)carry);
        return r;
    }

    // Requires a >= b (magnitude).
    static std::vector<uint32_t> subMag(const std::vector<uint32_t>& a, const std::vector<uint32_t>& b) {
        std::vector<uint32_t> r(a.size());
        int64_t borrow = 0;
        for (size_t i = 0; i < a.size(); ++i) {
            int64_t cur = (int64_t)a[i] - borrow - (i < b.size() ? (int64_t)b[i] : 0);
            if (cur < 0) { cur += (int64_t)1 << 32; borrow = 1; } else borrow = 0;
            r[i] = (uint32_t)cur;
        }
        while (!r.empty() && r.back() == 0) r.pop_back();
        return r;
    }

    static int bitLength(const std::vector<uint32_t>& m) {
        if (m.empty()) return 0;
        uint32_t top = m.back();
        int bits = 0;
        while (top) { ++bits; top >>= 1; }
        return (int)(m.size() - 1) * 32 + bits;
    }

    static bool getBit(const std::vector<uint32_t>& m, int i) {
        return (m[(size_t)i / 32] >> (i % 32)) & 1u;
    }

    static void shl1(std::vector<uint32_t>& m) {
        uint32_t carry = 0;
        for (auto& w : m) {
            uint32_t next = w >> 31;
            w = (w << 1) | carry;
            carry = next;
        }
        if (carry) m.push_back(carry);
    }

    // Binary long division on magnitudes: q = floor(a/b), r = a - q*b.
    static void divmodMag(const std::vector<uint32_t>& a, const std::vector<uint32_t>& b,
                          std::vector<uint32_t>& q, std::vector<uint32_t>& r) {
        q.assign(a.size(), 0);
        r.clear();
        int n = bitLength(a);
        for (int i = n - 1; i >= 0; --i) {
            shl1(r);
            if (getBit(a, i)) {
                if (r.empty()) r.push_back(1); else r[0] |= 1u;
            }
            if (cmpMag(r, b) >= 0) {
                r = subMag(r, b);
                q[(size_t)i / 32] |= (1u << (i % 32));
            }
        }
        while (!q.empty() && q.back() == 0) q.pop_back();
    }
};

} // namespace exact
