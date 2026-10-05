#pragma once

// Minimal arbitrary-precision signed integer (sign + magnitude, base 2^32).
// Only the operations needed by the triangulation checker are provided:
// construction, comparison, +, -, *, decimal parsing/printing.

#include <cstdint>
#include <cstdio>
#include <stdexcept>
#include <string>
#include <vector>

class BigInt {
public:
    BigInt() = default;

    BigInt(long long v) {
        if (v == 0) return;
        sign_ = v < 0 ? -1 : 1;
        unsigned long long u = v < 0 ? ~static_cast<unsigned long long>(v) + 1
                                     : static_cast<unsigned long long>(v);
        while (u) {
            mag_.push_back(static_cast<uint32_t>(u & 0xffffffffu));
            u >>= 32;
        }
    }

    static BigInt fromDecimalString(const std::string& s) {
        size_t i = 0;
        bool neg = false;
        if (i < s.size() && (s[i] == '-' || s[i] == '+')) {
            neg = (s[i] == '-');
            ++i;
        }
        if (i == s.size()) throw std::runtime_error("bad integer: '" + s + "'");
        BigInt r;
        for (; i < s.size(); ++i) {
            if (s[i] < '0' || s[i] > '9')
                throw std::runtime_error("bad integer: '" + s + "'");
            r.mulSmall(10);
            r.addSmall(static_cast<uint32_t>(s[i] - '0'));
        }
        if (neg) r.sign_ = -r.sign_;
        return r;
    }

    int sign() const { return sign_; }
    bool isZero() const { return sign_ == 0; }

    BigInt operator-() const {
        BigInt r = *this;
        r.sign_ = -r.sign_;
        return r;
    }

    friend int compare(const BigInt& a, const BigInt& b) {
        if (a.sign_ != b.sign_) return a.sign_ < b.sign_ ? -1 : 1;
        if (a.sign_ == 0) return 0;
        int c = compareAbs(a, b);
        return a.sign_ > 0 ? c : -c;
    }

    friend BigInt operator+(const BigInt& a, const BigInt& b) {
        if (a.sign_ == 0) return b;
        if (b.sign_ == 0) return a;
        if (a.sign_ == b.sign_) return addAbs(a, b, a.sign_);
        int c = compareAbs(a, b);
        if (c == 0) return BigInt();
        if (c > 0) return subAbs(a, b, a.sign_);
        return subAbs(b, a, b.sign_);
    }

    friend BigInt operator-(const BigInt& a, const BigInt& b) { return a + (-b); }

    friend BigInt operator*(const BigInt& a, const BigInt& b) {
        if (a.sign_ == 0 || b.sign_ == 0) return BigInt();
        BigInt r;
        r.mag_.assign(a.mag_.size() + b.mag_.size(), 0);
        for (size_t i = 0; i < a.mag_.size(); ++i) {
            uint64_t carry = 0;
            for (size_t j = 0; j < b.mag_.size(); ++j) {
                uint64_t cur = r.mag_[i + j] +
                               static_cast<uint64_t>(a.mag_[i]) * b.mag_[j] + carry;
                r.mag_[i + j] = static_cast<uint32_t>(cur);
                carry = cur >> 32;
            }
            size_t k = i + b.mag_.size();
            while (carry) {
                if (k == r.mag_.size()) r.mag_.push_back(0);
                uint64_t cur = static_cast<uint64_t>(r.mag_[k]) + carry;
                r.mag_[k] = static_cast<uint32_t>(cur);
                carry = cur >> 32;
                ++k;
            }
        }
        r.sign_ = a.sign_ * b.sign_;
        r.trim();
        return r;
    }

    BigInt& operator+=(const BigInt& o) { *this = *this + o; return *this; }
    BigInt& operator-=(const BigInt& o) { *this = *this - o; return *this; }
    BigInt& operator*=(const BigInt& o) { *this = *this * o; return *this; }

    friend bool operator==(const BigInt& a, const BigInt& b) { return compare(a, b) == 0; }
    friend bool operator!=(const BigInt& a, const BigInt& b) { return compare(a, b) != 0; }
    friend bool operator<(const BigInt& a, const BigInt& b) { return compare(a, b) < 0; }
    friend bool operator<=(const BigInt& a, const BigInt& b) { return compare(a, b) <= 0; }
    friend bool operator>(const BigInt& a, const BigInt& b) { return compare(a, b) > 0; }
    friend bool operator>=(const BigInt& a, const BigInt& b) { return compare(a, b) >= 0; }

    bool isEven() const { return mag_.empty() || (mag_[0] & 1u) == 0; }

    BigInt half() const { // truncating division by 2
        BigInt r = *this;
        r.sign_ = 1;
        r.divModSmall(2);
        r.sign_ = sign_ == 0 || r.mag_.empty() ? 0 : sign_;
        return r;
    }

    std::string toString() const {
        if (sign_ == 0) return "0";
        BigInt t = *this;
        t.sign_ = 1;
        std::string out;
        bool first = true;
        while (!t.isZero()) {
            uint32_t rem = t.divModSmall(1000000000u);
            char buf[16];
            if (first) {
                std::snprintf(buf, sizeof(buf), "%u", rem);
                first = false;
            } else {
                std::snprintf(buf, sizeof(buf), "%09u", rem);
            }
            out = std::string(buf) + out;
        }
        if (sign_ < 0) out = "-" + out;
        return out;
    }

private:
    std::vector<uint32_t> mag_; // little-endian, no trailing zero limbs
    int sign_ = 0;              // -1, 0, +1

    void trim() {
        while (!mag_.empty() && mag_.back() == 0) mag_.pop_back();
        if (mag_.empty()) sign_ = 0;
    }

    void mulSmall(uint32_t m) {
        if (sign_ == 0) return;
        if (m == 0) {
            mag_.clear();
            sign_ = 0;
            return;
        }
        uint64_t carry = 0;
        for (auto& d : mag_) {
            uint64_t cur = static_cast<uint64_t>(d) * m + carry;
            d = static_cast<uint32_t>(cur);
            carry = cur >> 32;
        }
        while (carry) {
            mag_.push_back(static_cast<uint32_t>(carry));
            carry >>= 32;
        }
    }

    void addSmall(uint32_t a) { // assumes *this >= 0 (used during parsing)
        if (sign_ == 0) {
            if (a) {
                mag_.push_back(a);
                sign_ = 1;
            }
            return;
        }
        uint64_t carry = a;
        for (size_t i = 0; carry && i < mag_.size(); ++i) {
            uint64_t cur = static_cast<uint64_t>(mag_[i]) + carry;
            mag_[i] = static_cast<uint32_t>(cur);
            carry = cur >> 32;
        }
        if (carry) mag_.push_back(static_cast<uint32_t>(carry));
    }

    uint32_t divModSmall(uint32_t m) {
        uint64_t rem = 0;
        for (size_t i = mag_.size(); i-- > 0;) {
            uint64_t cur = (rem << 32) | mag_[i];
            mag_[i] = static_cast<uint32_t>(cur / m);
            rem = cur % m;
        }
        trim();
        return static_cast<uint32_t>(rem);
    }

    static int compareAbs(const BigInt& a, const BigInt& b) {
        if (a.mag_.size() != b.mag_.size())
            return a.mag_.size() < b.mag_.size() ? -1 : 1;
        for (size_t i = a.mag_.size(); i-- > 0;)
            if (a.mag_[i] != b.mag_[i]) return a.mag_[i] < b.mag_[i] ? -1 : 1;
        return 0;
    }

    static BigInt addAbs(const BigInt& a, const BigInt& b, int sign) {
        BigInt r;
        r.sign_ = sign;
        r.mag_.resize(a.mag_.size() > b.mag_.size() ? a.mag_.size() : b.mag_.size(), 0);
        uint64_t carry = 0;
        for (size_t i = 0; i < r.mag_.size(); ++i) {
            uint64_t cur = carry;
            if (i < a.mag_.size()) cur += a.mag_[i];
            if (i < b.mag_.size()) cur += b.mag_[i];
            r.mag_[i] = static_cast<uint32_t>(cur);
            carry = cur >> 32;
        }
        if (carry) r.mag_.push_back(static_cast<uint32_t>(carry));
        return r;
    }

    static BigInt subAbs(const BigInt& a, const BigInt& b, int sign) { // |a| > |b|
        BigInt r;
        r.sign_ = sign;
        r.mag_.resize(a.mag_.size(), 0);
        int64_t borrow = 0;
        for (size_t i = 0; i < a.mag_.size(); ++i) {
            int64_t cur = static_cast<int64_t>(a.mag_[i]) - borrow -
                          (i < b.mag_.size() ? static_cast<int64_t>(b.mag_[i]) : 0);
            if (cur < 0) {
                cur += (int64_t)1 << 32;
                borrow = 1;
            } else {
                borrow = 0;
            }
            r.mag_[i] = static_cast<uint32_t>(cur);
        }
        r.trim();
        if (r.mag_.empty()) r.sign_ = 0;
        return r;
    }
};
