#pragma once

#include <cstdint>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace raypoly {

// Minimal arbitrary-precision signed integer (sign + magnitude, base 2^32).
// Supports the exact arithmetic needed by the geometry layer.
class BigInt {
public:
    BigInt() : sign_(0) {}
    BigInt(long long value) { // NOLINT(google-explicit-constructor)
        if (value == 0) {
            sign_ = 0;
            return;
        }
        unsigned long long u =
            value < 0 ? 0ULL - static_cast<unsigned long long>(value)
                      : static_cast<unsigned long long>(value);
        sign_ = value < 0 ? -1 : 1;
        while (u != 0) {
            mag_.push_back(static_cast<uint32_t>(u & 0xffffffffULL));
            u >>= 32;
        }
    }

    int sign() const { return sign_; }
    bool is_zero() const { return sign_ == 0; }

    static BigInt abs(BigInt v) {
        if (v.sign_ < 0) v.sign_ = 1;
        return v;
    }

    friend BigInt operator-(const BigInt& a) {
        BigInt r = a;
        r.sign_ = -r.sign_;
        return r;
    }

    friend BigInt operator+(const BigInt& a, const BigInt& b) {
        if (a.sign_ == 0) return b;
        if (b.sign_ == 0) return a;
        if (a.sign_ == b.sign_) {
            BigInt r;
            r.sign_ = a.sign_;
            r.mag_ = add_mag(a.mag_, b.mag_);
            return r;
        }
        int cmp = cmp_mag(a.mag_, b.mag_);
        if (cmp == 0) return BigInt();
        BigInt r;
        if (cmp > 0) {
            r.sign_ = a.sign_;
            r.mag_ = sub_mag(a.mag_, b.mag_);
        } else {
            r.sign_ = b.sign_;
            r.mag_ = sub_mag(b.mag_, a.mag_);
        }
        return r;
    }

    friend BigInt operator-(const BigInt& a, const BigInt& b) { return a + (-b); }

    friend BigInt operator*(const BigInt& a, const BigInt& b) {
        if (a.sign_ == 0 || b.sign_ == 0) return BigInt();
        BigInt r;
        r.sign_ = a.sign_ * b.sign_;
        r.mag_ = mul_mag(a.mag_, b.mag_);
        return r;
    }

    // Truncated division (quotient rounds toward zero).
    friend BigInt operator/(const BigInt& a, const BigInt& b) {
        if (b.sign_ == 0) throw std::domain_error("BigInt division by zero");
        if (a.sign_ == 0) return BigInt();
        std::vector<uint32_t> q, r;
        divmod_mag(a.mag_, b.mag_, q, r);
        BigInt result;
        result.mag_ = std::move(q);
        result.finish();
        result.sign_ = result.mag_.empty() ? 0 : a.sign_ * b.sign_;
        return result;
    }

    friend BigInt operator%(const BigInt& a, const BigInt& b) {
        if (b.sign_ == 0) throw std::domain_error("BigInt modulo by zero");
        std::vector<uint32_t> q, r;
        divmod_mag(a.mag_, b.mag_, q, r);
        BigInt result;
        result.mag_ = std::move(r);
        result.finish();
        result.sign_ = result.mag_.empty() ? 0 : a.sign_;
        return result;
    }

    friend bool operator==(const BigInt& a, const BigInt& b) {
        return a.sign_ == b.sign_ && a.mag_ == b.mag_;
    }
    friend bool operator!=(const BigInt& a, const BigInt& b) { return !(a == b); }
    friend bool operator<(const BigInt& a, const BigInt& b) {
        if (a.sign_ != b.sign_) return a.sign_ < b.sign_;
        if (a.sign_ == 0) return false;
        int c = cmp_mag(a.mag_, b.mag_);
        return a.sign_ > 0 ? c < 0 : c > 0;
    }
    friend bool operator>(const BigInt& a, const BigInt& b) { return b < a; }
    friend bool operator<=(const BigInt& a, const BigInt& b) { return !(b < a); }
    friend bool operator>=(const BigInt& a, const BigInt& b) { return !(a < b); }

    static BigInt gcd(BigInt a, BigInt b) {
        a = abs(std::move(a));
        b = abs(std::move(b));
        while (!b.is_zero()) {
            BigInt r = a % b;
            a = b;
            b = r;
        }
        return a;
    }

    std::string to_string() const {
        if (sign_ == 0) return "0";
        static const std::vector<uint32_t> kBase{1000000000u};
        std::vector<uint32_t> rem = mag_;
        std::string digits;
        while (!rem.empty()) {
            std::vector<uint32_t> q, r;
            divmod_mag(rem, kBase, q, r);
            uint32_t chunk = r.empty() ? 0 : r[0];
            if (q.empty()) {
                digits = std::to_string(chunk) + digits;
            } else {
                std::string part = std::to_string(chunk);
                digits = std::string(9 - part.size(), '0') + part + digits;
            }
            rem = std::move(q);
        }
        return sign_ < 0 ? "-" + digits : digits;
    }

private:
    int sign_;                  // -1, 0, +1
    std::vector<uint32_t> mag_; // little-endian limbs, no trailing zeros

    void finish() {
        while (!mag_.empty() && mag_.back() == 0) mag_.pop_back();
        if (mag_.empty()) sign_ = 0;
    }

    static void trim(std::vector<uint32_t>& v) {
        while (!v.empty() && v.back() == 0) v.pop_back();
    }

    static int cmp_mag(const std::vector<uint32_t>& a, const std::vector<uint32_t>& b) {
        if (a.size() != b.size()) return a.size() < b.size() ? -1 : 1;
        for (std::size_t i = a.size(); i-- > 0;) {
            if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
        }
        return 0;
    }

    static std::vector<uint32_t> add_mag(const std::vector<uint32_t>& a,
                                         const std::vector<uint32_t>& b) {
        std::vector<uint32_t> out;
        out.reserve(std::max(a.size(), b.size()) + 1);
        uint64_t carry = 0;
        for (std::size_t i = 0; i < a.size() || i < b.size() || carry; ++i) {
            uint64_t sum = carry;
            if (i < a.size()) sum += a[i];
            if (i < b.size()) sum += b[i];
            out.push_back(static_cast<uint32_t>(sum & 0xffffffffULL));
            carry = sum >> 32;
        }
        return out;
    }

    // Requires a >= b.
    static std::vector<uint32_t> sub_mag(const std::vector<uint32_t>& a,
                                         const std::vector<uint32_t>& b) {
        std::vector<uint32_t> out;
        out.reserve(a.size());
        int64_t borrow = 0;
        for (std::size_t i = 0; i < a.size(); ++i) {
            int64_t diff = static_cast<int64_t>(a[i]) - borrow;
            if (i < b.size()) diff -= b[i];
            if (diff < 0) {
                diff += 1LL << 32;
                borrow = 1;
            } else {
                borrow = 0;
            }
            out.push_back(static_cast<uint32_t>(diff));
        }
        trim(out);
        return out;
    }

    static std::vector<uint32_t> mul_mag(const std::vector<uint32_t>& a,
                                         const std::vector<uint32_t>& b) {
        std::vector<uint32_t> out(a.size() + b.size(), 0);
        for (std::size_t i = 0; i < a.size(); ++i) {
            uint64_t carry = 0;
            for (std::size_t j = 0; j < b.size() || carry; ++j) {
                uint64_t cur = out[i + j] + carry;
                if (j < b.size())
                    cur += static_cast<uint64_t>(a[i]) * static_cast<uint64_t>(b[j]);
                out[i + j] = static_cast<uint32_t>(cur & 0xffffffffULL);
                carry = cur >> 32;
            }
        }
        trim(out);
        return out;
    }

    static std::size_t bit_length(const std::vector<uint32_t>& v) {
        if (v.empty()) return 0;
        uint32_t top = v.back();
        std::size_t bits = (v.size() - 1) * 32;
        while (top != 0) {
            ++bits;
            top >>= 1;
        }
        return bits;
    }

    static bool get_bit(const std::vector<uint32_t>& v, std::size_t i) {
        std::size_t limb = i / 32;
        return limb < v.size() && ((v[limb] >> (i % 32)) & 1u) != 0;
    }

    static void set_bit(std::vector<uint32_t>& v, std::size_t i) {
        std::size_t limb = i / 32;
        if (v.size() <= limb) v.resize(limb + 1, 0);
        v[limb] |= 1u << (i % 32);
    }

    static void shl1(std::vector<uint32_t>& v) {
        uint32_t carry = 0;
        for (auto& limb : v) {
            uint32_t next = limb >> 31;
            limb = (limb << 1) | carry;
            carry = next;
        }
        if (carry) v.push_back(carry);
    }

    // Binary long division on magnitudes: q = a / b, r = a % b (b non-zero).
    static void divmod_mag(const std::vector<uint32_t>& a, const std::vector<uint32_t>& b,
                           std::vector<uint32_t>& q, std::vector<uint32_t>& r) {
        q.clear();
        r.clear();
        if (cmp_mag(a, b) < 0) {
            r = a;
            return;
        }
        std::size_t nbits = bit_length(a);
        q.assign((nbits + 31) / 32, 0);
        std::vector<uint32_t> rem;
        for (std::size_t i = nbits; i-- > 0;) {
            shl1(rem);
            if (get_bit(a, i)) {
                if (rem.empty()) rem.push_back(0);
                rem[0] |= 1u;
            }
            if (cmp_mag(rem, b) >= 0) {
                rem = sub_mag(rem, b);
                set_bit(q, i);
            }
        }
        trim(q);
        r = std::move(rem);
    }
};

} // namespace raypoly
