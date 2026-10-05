#pragma once

// 最小精确大整数：符号-数值表示，32 位小端 limbs。
// 只实现误差扩散引擎需要的运算：加、减、比较、乘/除小整数、十进制输出。

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <stdexcept>
#include <string>
#include <vector>

namespace graydiff {

class BigInt {
public:
    BigInt() = default;

    BigInt(int64_t value) {
        if (value == 0) return;
        sign_ = value < 0 ? -1 : 1;
        uint64_t a = value < 0 ? uint64_t(-(value + 1)) + 1 : uint64_t(value);
        while (a) {
            mag_.push_back(uint32_t(a & 0xffffffffu));
            a >>= 32;
        }
    }

    // base^exp（重复乘小整数）
    static BigInt powSmall(uint32_t base, uint64_t exp) {
        if (base == 0) return exp == 0 ? BigInt(1) : BigInt(0);
        BigInt r(1);
        for (uint64_t i = 0; i < exp; ++i) r.mulSmall(base);
        return r;
    }

    bool isZero() const { return sign_ == 0; }
    int sign() const { return sign_; }

    static int compare(const BigInt& a, const BigInt& b) {
        if (a.sign_ != b.sign_) return a.sign_ < b.sign_ ? -1 : 1;
        if (a.sign_ == 0) return 0;
        int c = cmpMag(a.mag_, b.mag_);
        return a.sign_ > 0 ? c : -c;
    }

    friend bool operator==(const BigInt& a, const BigInt& b) { return compare(a, b) == 0; }
    friend bool operator!=(const BigInt& a, const BigInt& b) { return compare(a, b) != 0; }
    friend bool operator<(const BigInt& a, const BigInt& b) { return compare(a, b) < 0; }
    friend bool operator<=(const BigInt& a, const BigInt& b) { return compare(a, b) <= 0; }
    friend bool operator>(const BigInt& a, const BigInt& b) { return compare(a, b) > 0; }
    friend bool operator>=(const BigInt& a, const BigInt& b) { return compare(a, b) >= 0; }

    BigInt& operator+=(const BigInt& o) {
        if (o.sign_ == 0) return *this;
        if (sign_ == 0) { *this = o; return *this; }
        if (sign_ == o.sign_) { addMagTo(mag_, o.mag_); return *this; }
        int c = cmpMag(mag_, o.mag_);
        if (c == 0) {
            mag_.clear();
            sign_ = 0;
        } else if (c > 0) {
            subMagFrom(mag_, o.mag_);
        } else {
            std::vector<uint32_t> r = o.mag_;
            subMagFrom(r, mag_);
            mag_ = std::move(r);
            sign_ = o.sign_;
        }
        return *this;
    }

    BigInt& operator-=(const BigInt& o) {
        BigInt t = o;
        t.sign_ = -t.sign_;
        return *this += t;
    }

    friend BigInt operator+(BigInt a, const BigInt& b) { a += b; return a; }
    friend BigInt operator-(BigInt a, const BigInt& b) { a -= b; return a; }

    BigInt operator-() const {
        BigInt r = *this;
        r.sign_ = -r.sign_;
        return r;
    }

    // *= m（无符号小整数）
    BigInt& mulSmall(uint32_t m) {
        if (sign_ == 0 || m == 1) return *this;
        if (m == 0) {
            mag_.clear();
            sign_ = 0;
            return *this;
        }
        uint64_t carry = 0;
        for (auto& limb : mag_) {
            uint64_t cur = uint64_t(limb) * m + carry;
            limb = uint32_t(cur);
            carry = cur >> 32;
        }
        while (carry) {
            mag_.push_back(uint32_t(carry));
            carry >>= 32;
        }
        return *this;
    }

    // *= m（带符号小整数）
    BigInt& mulSigned(int32_t m) {
        if (m < 0) {
            sign_ = -sign_;
            mulSmall(uint32_t(-(int64_t)m));
        } else {
            mulSmall(uint32_t(m));
        }
        return *this;
    }

    // 精确除以 d；若不能整除则抛出（违反引擎内部不变量，属于 bug）
    BigInt& divSmallExact(uint32_t d) {
        if (d == 0) throw std::invalid_argument("BigInt::divSmallExact: divide by zero");
        if (divmodSmall(d) != 0)
            throw std::logic_error("BigInt::divSmallExact: inexact division");
        return *this;
    }

    // 除以 d 并返回余数
    uint32_t divmodSmall(uint32_t d) {
        if (d == 0) throw std::invalid_argument("BigInt::divmodSmall: divide by zero");
        uint64_t rem = 0;
        for (size_t i = mag_.size(); i-- > 0;) {
            uint64_t cur = (rem << 32) | mag_[i];
            mag_[i] = uint32_t(cur / d);
            rem = cur % d;
        }
        trim(mag_);
        if (mag_.empty()) sign_ = 0;
        return uint32_t(rem);
    }

    std::string toString() const {
        if (sign_ == 0) return "0";
        BigInt tmp;
        tmp.sign_ = 1;
        tmp.mag_ = mag_;
        std::vector<uint32_t> chunks;
        while (!tmp.mag_.empty()) chunks.push_back(tmp.divmodSmall(1000000000u));
        std::string s = (sign_ < 0) ? "-" : "";
        s += std::to_string(chunks.back());
        for (size_t i = chunks.size() - 1; i-- > 0;) {
            char buf[16];
            std::snprintf(buf, sizeof buf, "%09u", chunks[i]);
            s += buf;
        }
        return s;
    }

    // 近似值 = mant * 2^exp2（相对误差约 2^-53），仅供诊断显示
    void approxParts(double& mant, int& exp2) const {
        mant = 0.0;
        exp2 = 0;
        size_t i = mag_.size();
        int cnt = 0;
        while (i > 0 && cnt < 2) {
            mant = mant * 4294967296.0 + mag_[--i];
            ++cnt;
        }
        exp2 = 32 * int(i);
    }

    double toDoubleApprox() const {
        double m;
        int e;
        approxParts(m, e);
        return std::ldexp(m, e) * sign_;
    }

    // num/den 的近似值（两者可以是天文数字）
    static double approxRatio(const BigInt& num, const BigInt& den) {
        if (den.isZero()) throw std::invalid_argument("BigInt::approxRatio: zero denominator");
        if (num.isZero()) return 0.0;
        double mn, md;
        int en, ed;
        num.approxParts(mn, en);
        den.approxParts(md, ed);
        return std::ldexp((mn / md) * double(num.sign_ * den.sign_), en - ed);
    }

private:
    std::vector<uint32_t> mag_;  // 小端，无前导零；空表示 0
    int sign_ = 0;               // -1, 0, +1

    static void trim(std::vector<uint32_t>& m) {
        while (!m.empty() && m.back() == 0) m.pop_back();
    }

    static int cmpMag(const std::vector<uint32_t>& a, const std::vector<uint32_t>& b) {
        if (a.size() != b.size()) return a.size() < b.size() ? -1 : 1;
        for (size_t i = a.size(); i-- > 0;)
            if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
        return 0;
    }

    static void addMagTo(std::vector<uint32_t>& a, const std::vector<uint32_t>& b) {
        uint64_t carry = 0;
        size_t i = 0;
        for (; i < b.size(); ++i) {
            uint64_t cur = (i < a.size() ? a[i] : 0u) + uint64_t(b[i]) + carry;
            if (i < a.size()) a[i] = uint32_t(cur);
            else a.push_back(uint32_t(cur));
            carry = cur >> 32;
        }
        for (; carry && i < a.size(); ++i) {
            uint64_t cur = uint64_t(a[i]) + carry;
            a[i] = uint32_t(cur);
            carry = cur >> 32;
        }
        if (carry) a.push_back(uint32_t(carry));
    }

    // 要求 |a| >= |b|；结果会去掉前导零
    static void subMagFrom(std::vector<uint32_t>& a, const std::vector<uint32_t>& b) {
        int64_t borrow = 0;
        for (size_t i = 0; i < a.size(); ++i) {
            int64_t cur = int64_t(a[i]) - (i < b.size() ? int64_t(b[i]) : 0) - borrow;
            if (cur < 0) {
                cur += int64_t(1) << 32;
                borrow = 1;
            } else {
                borrow = 0;
            }
            a[i] = uint32_t(cur);
        }
        trim(a);
    }
};

}  // namespace graydiff
