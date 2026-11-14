#include "detail/crypto.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstring>
#include <limits>
#include <random>
#include <stdexcept>
#include <string>

#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
#include <cstdlib>
#elif defined(__linux__)
#include <sys/random.h>
#endif

namespace netsec::detail {
namespace {

constexpr std::uint32_t rotr32(std::uint32_t value, unsigned amount) {
  return (value >> amount) | (value << (32U - amount));
}

constexpr std::uint32_t rotl32(std::uint32_t value, unsigned amount) {
  return (value << amount) | (value >> (32U - amount));
}

std::uint32_t load_be32(const Byte* p) {
  return (static_cast<std::uint32_t>(p[0]) << 24U) |
         (static_cast<std::uint32_t>(p[1]) << 16U) |
         (static_cast<std::uint32_t>(p[2]) << 8U) |
         static_cast<std::uint32_t>(p[3]);
}

void store_be32(Byte* p, std::uint32_t value) {
  p[0] = static_cast<Byte>(value >> 24U);
  p[1] = static_cast<Byte>(value >> 16U);
  p[2] = static_cast<Byte>(value >> 8U);
  p[3] = static_cast<Byte>(value);
}

std::uint32_t load_le32(const Byte* p) {
  return static_cast<std::uint32_t>(p[0]) |
         (static_cast<std::uint32_t>(p[1]) << 8U) |
         (static_cast<std::uint32_t>(p[2]) << 16U) |
         (static_cast<std::uint32_t>(p[3]) << 24U);
}

void store_le32(Byte* p, std::uint32_t value) {
  p[0] = static_cast<Byte>(value);
  p[1] = static_cast<Byte>(value >> 8U);
  p[2] = static_cast<Byte>(value >> 16U);
  p[3] = static_cast<Byte>(value >> 24U);
}

std::uint64_t load_le64(const Byte* p) {
  std::uint64_t value = 0;
  for (unsigned i = 0; i < 8; ++i) {
    value |= static_cast<std::uint64_t>(p[i]) << (8U * i);
  }
  return value;
}

void store_le64(Byte* p, std::uint64_t value) {
  for (unsigned i = 0; i < 8; ++i) {
    p[i] = static_cast<Byte>(value >> (8U * i));
  }
}

}  // namespace

Sha256::Sha256()
    : state_{0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
             0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U} {}

void Sha256::transform(const Byte block[64]) {
  static constexpr std::uint32_t k[64] = {
      0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
      0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
      0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
      0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
      0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
      0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
      0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
      0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
      0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
      0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
      0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
      0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
      0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

  std::array<std::uint32_t, 64> w{};
  for (unsigned i = 0; i < 16; ++i) {
    w[i] = load_be32(block + 4U * i);
  }
  for (unsigned i = 16; i < 64; ++i) {
    const std::uint32_t s0 =
        rotr32(w[i - 15], 7) ^ rotr32(w[i - 15], 18) ^ (w[i - 15] >> 3U);
    const std::uint32_t s1 =
        rotr32(w[i - 2], 17) ^ rotr32(w[i - 2], 19) ^ (w[i - 2] >> 10U);
    w[i] = w[i - 16] + s0 + w[i - 7] + s1;
  }

  std::uint32_t a = state_[0];
  std::uint32_t b = state_[1];
  std::uint32_t c = state_[2];
  std::uint32_t d = state_[3];
  std::uint32_t e = state_[4];
  std::uint32_t f = state_[5];
  std::uint32_t g = state_[6];
  std::uint32_t h = state_[7];

  for (unsigned i = 0; i < 64; ++i) {
    const std::uint32_t s1 = rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25);
    const std::uint32_t ch = (e & f) ^ (~e & g);
    const std::uint32_t temp1 = h + s1 + ch + k[i] + w[i];
    const std::uint32_t s0 = rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22);
    const std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
    const std::uint32_t temp2 = s0 + maj;

    h = g;
    g = f;
    f = e;
    e = d + temp1;
    d = c;
    c = b;
    b = a;
    a = temp1 + temp2;
  }

  state_[0] += a;
  state_[1] += b;
  state_[2] += c;
  state_[3] += d;
  state_[4] += e;
  state_[5] += f;
  state_[6] += g;
  state_[7] += h;
}

void Sha256::update(std::span<const Byte> data) {
  if (finished_) {
    throw std::logic_error("SHA-256 update after finish");
  }
  total_bytes_ += data.size();
  std::size_t offset = 0;
  if (buffered_ != 0) {
    const std::size_t take = std::min<std::size_t>(64 - buffered_, data.size());
    std::memcpy(buffer_.data() + buffered_, data.data(), take);
    buffered_ += take;
    offset += take;
    if (buffered_ == 64) {
      transform(buffer_.data());
      buffered_ = 0;
    }
  }
  while (offset + 64 <= data.size()) {
    transform(data.data() + offset);
    offset += 64;
  }
  if (offset < data.size()) {
    buffered_ = data.size() - offset;
    std::memcpy(buffer_.data(), data.data() + offset, buffered_);
  }
}

Digest Sha256::finish() {
  if (finished_) {
    throw std::logic_error("SHA-256 finish called twice");
  }
  finished_ = true;
  const std::uint64_t bit_length = total_bytes_ * 8U;
  buffer_[buffered_++] = 0x80U;
  if (buffered_ > 56) {
    std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffered_),
              buffer_.end(), 0);
    transform(buffer_.data());
    buffered_ = 0;
  }
  std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffered_),
            buffer_.begin() + 56, 0);
  for (unsigned i = 0; i < 8; ++i) {
    buffer_[56 + i] = static_cast<Byte>(bit_length >> (56U - 8U * i));
  }
  transform(buffer_.data());

  Digest out{};
  for (unsigned i = 0; i < 8; ++i) {
    store_be32(out.data() + 4U * i, state_[i]);
  }
  return out;
}

Digest sha256(std::span<const Byte> data) {
  Sha256 hash;
  hash.update(data);
  return hash.finish();
}

Digest hmac_sha256(std::span<const Byte> key, std::span<const Byte> data) {
  std::array<Byte, 64> normalized{};
  if (key.size() > normalized.size()) {
    const Digest hashed = sha256(key);
    std::copy(hashed.begin(), hashed.end(), normalized.begin());
  } else {
    std::copy(key.begin(), key.end(), normalized.begin());
  }

  std::array<Byte, 64> inner{};
  std::array<Byte, 64> outer{};
  for (std::size_t i = 0; i < normalized.size(); ++i) {
    inner[i] = static_cast<Byte>(normalized[i] ^ 0x36U);
    outer[i] = static_cast<Byte>(normalized[i] ^ 0x5cU);
  }

  Sha256 first;
  first.update(inner);
  first.update(data);
  const Digest inner_digest = first.finish();

  Sha256 second;
  second.update(outer);
  second.update(inner_digest);
  return second.finish();
}

std::vector<Byte> hkdf_expand(std::span<const Byte> prk,
                              std::span<const Byte> info,
                              std::size_t length) {
  if (length > 255U * 32U) {
    throw std::invalid_argument("HKDF output too long");
  }
  std::vector<Byte> output;
  output.reserve(length);
  Bytes previous;
  for (std::uint8_t counter = 1; output.size() < length; ++counter) {
    Bytes input = previous;
    input.insert(input.end(), info.begin(), info.end());
    input.push_back(counter);
    const Digest block = hmac_sha256(prk, input);
    previous.assign(block.begin(), block.end());
    const std::size_t take = std::min<std::size_t>(32, length - output.size());
    output.insert(output.end(), previous.begin(),
                  previous.begin() + static_cast<std::ptrdiff_t>(take));
  }
  return output;
}

Bytes secure_random(std::size_t length) {
  Bytes output(length);
  if (length == 0) {
    return output;
  }
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
  arc4random_buf(output.data(), output.size());
#elif defined(__linux__)
  std::size_t offset = 0;
  while (offset < output.size()) {
    const ssize_t got =
        getrandom(output.data() + offset, output.size() - offset, 0);
    if (got <= 0) {
      throw std::runtime_error("getrandom failed");
    }
    offset += static_cast<std::size_t>(got);
  }
#else
  std::random_device device;
  for (Byte& byte : output) {
    byte = static_cast<Byte>(device());
  }
#endif
  return output;
}

bool constant_time_equal(std::span<const Byte> lhs,
                         std::span<const Byte> rhs) {
  if (lhs.size() != rhs.size()) {
    return false;
  }
  Byte difference = 0;
  for (std::size_t i = 0; i < lhs.size(); ++i) {
    difference = static_cast<Byte>(difference | (lhs[i] ^ rhs[i]));
  }
  return difference == 0;
}

namespace {

constexpr std::uint64_t kFeMask = (1ULL << 51U) - 1U;

struct Fe {
  std::uint64_t limb[5]{};
};

Fe fe_zero() { return Fe{}; }

Fe fe_one() {
  Fe out{};
  out.limb[0] = 1;
  return out;
}

void fe_carry(Fe& value) {
  for (unsigned i = 0; i < 4; ++i) {
    const std::uint64_t carry = value.limb[i] >> 51U;
    value.limb[i] &= kFeMask;
    value.limb[i + 1] += carry;
  }
  const std::uint64_t carry = value.limb[4] >> 51U;
  value.limb[4] &= kFeMask;
  value.limb[0] += carry * 19U;
  const std::uint64_t carry0 = value.limb[0] >> 51U;
  value.limb[0] &= kFeMask;
  value.limb[1] += carry0;
}

Fe fe_add(const Fe& a, const Fe& b) {
  Fe out{};
  for (unsigned i = 0; i < 5; ++i) {
    out.limb[i] = a.limb[i] + b.limb[i];
  }
  fe_carry(out);
  return out;
}

Fe fe_sub(const Fe& a, const Fe& b) {
  constexpr std::uint64_t p0 = (1ULL << 51U) - 19U;
  constexpr std::uint64_t p1 = (1ULL << 51U) - 1U;
  Fe out{};
  out.limb[0] = a.limb[0] + 2U * p0 - b.limb[0];
  out.limb[1] = a.limb[1] + 2U * p1 - b.limb[1];
  out.limb[2] = a.limb[2] + 2U * p1 - b.limb[2];
  out.limb[3] = a.limb[3] + 2U * p1 - b.limb[3];
  out.limb[4] = a.limb[4] + 2U * p1 - b.limb[4];
  fe_carry(out);
  return out;
}

Fe fe_mul(const Fe& a, const Fe& b) {
  const unsigned __int128 a0 = a.limb[0];
  const unsigned __int128 a1 = a.limb[1];
  const unsigned __int128 a2 = a.limb[2];
  const unsigned __int128 a3 = a.limb[3];
  const unsigned __int128 a4 = a.limb[4];
  const unsigned __int128 b0 = b.limb[0];
  const unsigned __int128 b1 = b.limb[1];
  const unsigned __int128 b2 = b.limb[2];
  const unsigned __int128 b3 = b.limb[3];
  const unsigned __int128 b4 = b.limb[4];

  unsigned __int128 t0 = a0 * b0 + 19U * (a1 * b4 + a2 * b3 + a3 * b2 + a4 * b1);
  unsigned __int128 t1 = a0 * b1 + a1 * b0 + 19U * (a2 * b4 + a3 * b3 + a4 * b2);
  unsigned __int128 t2 = a0 * b2 + a1 * b1 + a2 * b0 + 19U * (a3 * b4 + a4 * b3);
  unsigned __int128 t3 = a0 * b3 + a1 * b2 + a2 * b1 + a3 * b0 + 19U * (a4 * b4);
  unsigned __int128 t4 = a0 * b4 + a1 * b3 + a2 * b2 + a3 * b1 + a4 * b0;

  std::uint64_t carry = static_cast<std::uint64_t>(t0 >> 51U);
  const std::uint64_t r0 = static_cast<std::uint64_t>(t0) & kFeMask;
  t1 += carry;
  carry = static_cast<std::uint64_t>(t1 >> 51U);
  const std::uint64_t r1 = static_cast<std::uint64_t>(t1) & kFeMask;
  t2 += carry;
  carry = static_cast<std::uint64_t>(t2 >> 51U);
  const std::uint64_t r2 = static_cast<std::uint64_t>(t2) & kFeMask;
  t3 += carry;
  carry = static_cast<std::uint64_t>(t3 >> 51U);
  const std::uint64_t r3 = static_cast<std::uint64_t>(t3) & kFeMask;
  t4 += carry;
  carry = static_cast<std::uint64_t>(t4 >> 51U);
  const std::uint64_t r4 = static_cast<std::uint64_t>(t4) & kFeMask;

  Fe out{};
  out.limb[0] = r0 + carry * 19U;
  out.limb[1] = r1;
  out.limb[2] = r2;
  out.limb[3] = r3;
  out.limb[4] = r4;
  fe_carry(out);
  return out;
}

Fe fe_square(const Fe& value) { return fe_mul(value, value); }

Fe fe_decode32(const Byte input[32]) {
  std::array<Byte, 32> copy{};
  std::copy(input, input + 32, copy.begin());
  copy[31] &= 0x7fU;
  Fe out{};
  out.limb[0] = load_le64(copy.data()) & kFeMask;
  out.limb[1] = (load_le64(copy.data() + 6) >> 3U) & kFeMask;
  out.limb[2] = (load_le64(copy.data() + 12) >> 6U) & kFeMask;
  out.limb[3] = (load_le64(copy.data() + 19) >> 1U) & kFeMask;
  out.limb[4] = (load_le64(copy.data() + 24) >> 12U) & kFeMask;
  return out;
}

void fe_encode32(Fe value, Byte out[32]) {
  fe_carry(value);
  fe_carry(value);

  const std::uint64_t w0 = value.limb[0] | (value.limb[1] << 51U);
  const std::uint64_t w1 = (value.limb[1] >> 13U) | (value.limb[2] << 38U);
  const std::uint64_t w2 = (value.limb[2] >> 26U) | (value.limb[3] << 25U);
  const std::uint64_t w3 = (value.limb[3] >> 39U) | (value.limb[4] << 12U);

  std::array<std::uint64_t, 4> words{w0, w1, w2, w3};
  constexpr std::array<std::uint64_t, 4> mod{
      0xffffffffffffffedULL, 0xffffffffffffffffULL,
      0xffffffffffffffffULL, 0x7fffffffffffffffULL};
  bool ge = false;
  for (int i = 3; i >= 0; --i) {
    if (words[static_cast<std::size_t>(i)] != mod[static_cast<std::size_t>(i)]) {
      ge = words[static_cast<std::size_t>(i)] >
           mod[static_cast<std::size_t>(i)];
      break;
    }
    if (i == 0) {
      ge = true;
    }
  }
  if (ge) {
    unsigned __int128 borrow = 0;
    for (unsigned i = 0; i < 4; ++i) {
      const unsigned __int128 subtrahend =
          static_cast<unsigned __int128>(mod[i]) + borrow;
      const std::uint64_t old = words[i];
      words[i] = old - static_cast<std::uint64_t>(subtrahend);
      borrow = static_cast<unsigned __int128>(old) < subtrahend ? 1U : 0U;
    }
  }
  for (unsigned i = 0; i < 4; ++i) {
    store_le64(out + 8U * i, words[i]);
  }
}

void fe_cswap(std::uint64_t swap, Fe& a, Fe& b) {
  const std::uint64_t mask = 0U - swap;
  for (unsigned i = 0; i < 5; ++i) {
    const std::uint64_t diff = (a.limb[i] ^ b.limb[i]) & mask;
    a.limb[i] ^= diff;
    b.limb[i] ^= diff;
  }
}

Fe fe_invert(const Fe& value) {
  constexpr std::array<Byte, 32> exponent{
      0xebU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU,
      0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU,
      0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU,
      0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0xffU, 0x7fU};
  Fe result = fe_one();
  Fe base = value;
  for (int bit = 254; bit >= 0; --bit) {
    result = fe_square(result);
    if (((exponent[static_cast<std::size_t>(bit / 8)] >>
          (static_cast<unsigned>(bit) & 7U)) &
         1U) != 0U) {
      result = fe_mul(result, base);
    }
  }
  return result;
}

}  // namespace

std::array<Byte, 32> x25519(std::span<const Byte> private_key,
                            std::span<const Byte> peer_public_key) {
  if (private_key.size() != 32 || peer_public_key.size() != 32) {
    throw std::invalid_argument("X25519 requires 32-byte inputs");
  }
  std::array<Byte, 32> scalar{};
  std::copy(private_key.begin(), private_key.end(), scalar.begin());
  scalar[0] &= 248U;
  scalar[31] &= 127U;
  scalar[31] |= 64U;

  const Fe x1 = fe_decode32(peer_public_key.data());
  Fe x2 = fe_one();
  Fe z2 = fe_zero();
  Fe x3 = x1;
  Fe z3 = fe_one();
  std::uint64_t swap = 0;

  const Fe a24{};
  Fe a24_value = a24;
  a24_value.limb[0] = 121665U;

  for (int position = 254; position >= 0; --position) {
    const std::uint64_t bit =
        (scalar[static_cast<std::size_t>(position / 8)] >>
         (static_cast<unsigned>(position) & 7U)) &
        1U;
    swap ^= bit;
    fe_cswap(swap, x2, x3);
    fe_cswap(swap, z2, z3);
    swap = bit;

    const Fe a = fe_add(x2, z2);
    const Fe aa = fe_square(a);
    const Fe b = fe_sub(x2, z2);
    const Fe bb = fe_square(b);
    const Fe e = fe_sub(aa, bb);
    const Fe c = fe_add(x3, z3);
    const Fe d = fe_sub(x3, z3);
    const Fe da = fe_mul(d, a);
    const Fe cb = fe_mul(c, b);
    x3 = fe_square(fe_add(da, cb));
    z3 = fe_mul(x1, fe_square(fe_sub(da, cb)));
    x2 = fe_mul(aa, bb);
    z2 = fe_mul(e, fe_add(aa, fe_mul(a24_value, e)));
  }
  fe_cswap(swap, x2, x3);
  fe_cswap(swap, z2, z3);

  const Fe result = fe_mul(x2, fe_invert(z2));
  std::array<Byte, 32> output{};
  fe_encode32(result, output.data());
  return output;
}

namespace {

void chacha20_block(const Byte key[32], std::uint32_t counter,
                    const Byte nonce[12], Byte output[64]) {
  std::array<std::uint32_t, 16> initial{};
  initial[0] = 0x61707865U;
  initial[1] = 0x3320646eU;
  initial[2] = 0x79622d32U;
  initial[3] = 0x6b206574U;
  for (unsigned i = 0; i < 8; ++i) {
    initial[4 + i] = load_le32(key + 4U * i);
  }
  initial[12] = counter;
  for (unsigned i = 0; i < 3; ++i) {
    initial[13 + i] = load_le32(nonce + 4U * i);
  }

  std::array<std::uint32_t, 16> x = initial;
  const auto quarter = [](std::uint32_t& a, std::uint32_t& b,
                          std::uint32_t& c, std::uint32_t& d) {
    a += b;
    d ^= a;
    d = rotl32(d, 16);
    c += d;
    b ^= c;
    b = rotl32(b, 12);
    a += b;
    d ^= a;
    d = rotl32(d, 8);
    c += d;
    b ^= c;
    b = rotl32(b, 7);
  };
  for (unsigned round = 0; round < 10; ++round) {
    quarter(x[0], x[4], x[8], x[12]);
    quarter(x[1], x[5], x[9], x[13]);
    quarter(x[2], x[6], x[10], x[14]);
    quarter(x[3], x[7], x[11], x[15]);
    quarter(x[0], x[5], x[10], x[15]);
    quarter(x[1], x[6], x[11], x[12]);
    quarter(x[2], x[7], x[8], x[13]);
    quarter(x[3], x[4], x[9], x[14]);
  }
  for (unsigned i = 0; i < 16; ++i) {
    store_le32(output + 4U * i, x[i] + initial[i]);
  }
}

struct Poly1305State {
  std::uint32_t r[5]{};
  std::uint32_t pad[4]{};
  std::uint32_t h[5]{};
};

void poly1305_init(Poly1305State& state, const Byte key[32]) {
  const std::uint32_t t0 = load_le32(key);
  const std::uint32_t t1 = load_le32(key + 4);
  const std::uint32_t t2 = load_le32(key + 8);
  const std::uint32_t t3 = load_le32(key + 12);
  state.r[0] = t0 & 0x3ffffffU;
  state.r[1] = ((t0 >> 26U) | (t1 << 6U)) & 0x3ffff03U;
  state.r[2] = ((t1 >> 20U) | (t2 << 12U)) & 0x3ffc0ffU;
  state.r[3] = ((t2 >> 14U) | (t3 << 18U)) & 0x3f03fffU;
  state.r[4] = (t3 >> 8U) & 0x00fffffU;
  for (unsigned i = 0; i < 4; ++i) {
    state.pad[i] = load_le32(key + 16U + 4U * i);
  }
  std::fill(std::begin(state.h), std::end(state.h), 0);
}

void poly1305_block(Poly1305State& state, const Byte block[16],
                    bool full_block) {
  constexpr std::uint32_t mask26 = 0x3ffffffU;
  const std::uint32_t r0 = state.r[0];
  const std::uint32_t r1 = state.r[1];
  const std::uint32_t r2 = state.r[2];
  const std::uint32_t r3 = state.r[3];
  const std::uint32_t r4 = state.r[4];
  const std::uint32_t s1 = r1 * 5U;
  const std::uint32_t s2 = r2 * 5U;
  const std::uint32_t s3 = r3 * 5U;
  const std::uint32_t s4 = r4 * 5U;

  std::uint64_t h0 = state.h[0] + (load_le32(block) & mask26);
  std::uint64_t h1 =
      state.h[1] + ((load_le32(block + 3) >> 2U) & mask26);
  std::uint64_t h2 =
      state.h[2] + ((load_le32(block + 6) >> 4U) & mask26);
  std::uint64_t h3 =
      state.h[3] + ((load_le32(block + 9) >> 6U) & mask26);
  const std::uint32_t final_bit = full_block ? (1U << 24U) : 0U;
  std::uint64_t h4 =
      state.h[4] + ((load_le32(block + 12) >> 8U) | final_bit);

  const std::uint64_t d0 =
      h0 * r0 + h1 * s4 + h2 * s3 + h3 * s2 + h4 * s1;
  const std::uint64_t d1 =
      h0 * r1 + h1 * r0 + h2 * s4 + h3 * s3 + h4 * s2;
  const std::uint64_t d2 =
      h0 * r2 + h1 * r1 + h2 * r0 + h3 * s4 + h4 * s3;
  const std::uint64_t d3 =
      h0 * r3 + h1 * r2 + h2 * r1 + h3 * r0 + h4 * s4;
  const std::uint64_t d4 =
      h0 * r4 + h1 * r3 + h2 * r2 + h3 * r1 + h4 * r0;

  std::uint64_t carry = d0 >> 26U;
  h0 = d0 & mask26;
  const std::uint64_t e1 = d1 + carry;
  carry = e1 >> 26U;
  h1 = e1 & mask26;
  const std::uint64_t e2 = d2 + carry;
  carry = e2 >> 26U;
  h2 = e2 & mask26;
  const std::uint64_t e3 = d3 + carry;
  carry = e3 >> 26U;
  h3 = e3 & mask26;
  const std::uint64_t e4 = d4 + carry;
  carry = e4 >> 26U;
  h4 = e4 & mask26;
  h0 += carry * 5U;
  carry = h0 >> 26U;
  h0 &= mask26;
  h1 += carry;

  state.h[0] = static_cast<std::uint32_t>(h0);
  state.h[1] = static_cast<std::uint32_t>(h1);
  state.h[2] = static_cast<std::uint32_t>(h2);
  state.h[3] = static_cast<std::uint32_t>(h3);
  state.h[4] = static_cast<std::uint32_t>(h4);
}

Digest poly1305_finish(Poly1305State& state) {
  constexpr std::uint32_t mask26 = 0x3ffffffU;
  std::uint64_t h0 = state.h[0];
  std::uint64_t h1 = state.h[1];
  std::uint64_t h2 = state.h[2];
  std::uint64_t h3 = state.h[3];
  std::uint64_t h4 = state.h[4];

  std::uint64_t carry = h1 >> 26U;
  h1 &= mask26;
  h2 += carry;
  carry = h2 >> 26U;
  h2 &= mask26;
  h3 += carry;
  carry = h3 >> 26U;
  h3 &= mask26;
  h4 += carry;
  carry = h4 >> 26U;
  h4 &= mask26;
  h0 += carry * 5U;
  carry = h0 >> 26U;
  h0 &= mask26;
  h1 += carry;

  std::uint64_t g0 = h0 + 5U;
  carry = g0 >> 26U;
  g0 &= mask26;
  std::uint64_t g1 = h1 + carry;
  carry = g1 >> 26U;
  g1 &= mask26;
  std::uint64_t g2 = h2 + carry;
  carry = g2 >> 26U;
  g2 &= mask26;
  std::uint64_t g3 = h3 + carry;
  carry = g3 >> 26U;
  g3 &= mask26;
  std::uint64_t g4 = h4 + carry - (1ULL << 26U);

  const std::uint64_t select_g = (g4 >> 63U) - 1U;
  const std::uint64_t select_h = ~select_g;
  h0 = (h0 & select_h) | (g0 & select_g);
  h1 = (h1 & select_h) | (g1 & select_g);
  h2 = (h2 & select_h) | (g2 & select_g);
  h3 = (h3 & select_h) | (g3 & select_g);
  h4 = (h4 & select_h) | (g4 & select_g);

  const std::uint64_t word0 =
      (h0 | (h1 << 26U)) & 0xffffffffULL;
  const std::uint64_t word1 =
      ((h1 >> 6U) | (h2 << 20U)) & 0xffffffffULL;
  const std::uint64_t word2 =
      ((h2 >> 12U) | (h3 << 14U)) & 0xffffffffULL;
  const std::uint64_t word3 =
      ((h3 >> 18U) | (h4 << 8U)) & 0xffffffffULL;
  const std::uint64_t f0 =
      word0 + static_cast<std::uint64_t>(state.pad[0]);
  const std::uint64_t f1 =
      word1 + static_cast<std::uint64_t>(state.pad[1]) + (f0 >> 32U);
  const std::uint64_t f2 =
      word2 + static_cast<std::uint64_t>(state.pad[2]) + (f1 >> 32U);
  const std::uint64_t f3 =
      word3 + static_cast<std::uint64_t>(state.pad[3]) + (f2 >> 32U);

  Digest tag{};
  store_le32(tag.data(), static_cast<std::uint32_t>(f0));
  store_le32(tag.data() + 4, static_cast<std::uint32_t>(f1));
  store_le32(tag.data() + 8, static_cast<std::uint32_t>(f2));
  store_le32(tag.data() + 12, static_cast<std::uint32_t>(f3));
  return tag;
}

Digest poly1305(std::span<const Byte> key, std::span<const Byte> message) {
  Poly1305State state;
  poly1305_init(state, key.data());
  std::size_t offset = 0;
  while (offset + 16 <= message.size()) {
    poly1305_block(state, message.data() + offset, true);
    offset += 16;
  }
  if (offset < message.size()) {
    std::array<Byte, 16> final{};
    const std::size_t remaining = message.size() - offset;
    std::copy(message.begin() + static_cast<std::ptrdiff_t>(offset),
              message.end(), final.begin());
    final[remaining] = 1U;
    poly1305_block(state, final.data(), false);
  }
  return poly1305_finish(state);
}

void chacha20_xor(const Byte key[32], std::uint32_t counter,
                  const Byte nonce[12], std::span<const Byte> input,
                  Byte* output) {
  std::array<Byte, 64> stream{};
  std::size_t offset = 0;
  while (offset < input.size()) {
    chacha20_block(key, counter++, nonce, stream.data());
    const std::size_t take = std::min<std::size_t>(64, input.size() - offset);
    for (std::size_t i = 0; i < take; ++i) {
      output[offset + i] =
          static_cast<Byte>(input[offset + i] ^ stream[i]);
    }
    offset += take;
  }
}

void append_u64_le(Bytes& out, std::uint64_t value) {
  std::array<Byte, 8> encoded{};
  store_le64(encoded.data(), value);
  out.insert(out.end(), encoded.begin(), encoded.end());
}

void append_padding(Bytes& out) {
  while ((out.size() & 15U) != 0U) {
    out.push_back(0);
  }
}

Digest aead_tag(const Byte poly_key[32], std::span<const Byte> aad,
                std::span<const Byte> ciphertext) {
  Bytes mac;
  mac.reserve(aad.size() + ciphertext.size() + 32);
  mac.insert(mac.end(), aad.begin(), aad.end());
  append_padding(mac);
  mac.insert(mac.end(), ciphertext.begin(), ciphertext.end());
  append_padding(mac);
  append_u64_le(mac, aad.size());
  append_u64_le(mac, ciphertext.size());
  return poly1305(std::span<const Byte>(poly_key, 32), mac);
}

}  // namespace

Bytes aead_seal(std::span<const Byte> key, std::span<const Byte> nonce,
                std::span<const Byte> aad, std::span<const Byte> plaintext) {
  if (key.size() != 32 || nonce.size() != 12) {
    throw std::invalid_argument("ChaCha20-Poly1305 requires a 32-byte key and 12-byte nonce");
  }
  std::array<Byte, 64> first_block{};
  chacha20_block(key.data(), 0, nonce.data(), first_block.data());
  Bytes output(plaintext.size() + 16);
  chacha20_xor(key.data(), 1, nonce.data(), plaintext, output.data());
  const std::span<const Byte> ciphertext(output.data(), plaintext.size());
  const Digest tag = aead_tag(first_block.data(), aad, ciphertext);
  std::copy(tag.begin(), tag.begin() + 16,
            output.begin() + static_cast<std::ptrdiff_t>(plaintext.size()));
  return output;
}

std::optional<Bytes> aead_open(std::span<const Byte> key,
                               std::span<const Byte> nonce,
                               std::span<const Byte> aad,
                               std::span<const Byte> sealed) {
  if (key.size() != 32 || nonce.size() != 12 || sealed.size() < 16) {
    return std::nullopt;
  }
  const std::size_t ciphertext_size = sealed.size() - 16;
  const std::span<const Byte> ciphertext(sealed.data(), ciphertext_size);
  const std::span<const Byte> received_tag(sealed.data() + ciphertext_size, 16);

  std::array<Byte, 64> first_block{};
  chacha20_block(key.data(), 0, nonce.data(), first_block.data());
  const Digest expected = aead_tag(first_block.data(), aad, ciphertext);
  if (!constant_time_equal(std::span<const Byte>(expected).first(16),
                           received_tag)) {
    return std::nullopt;
  }

  Bytes plaintext(ciphertext_size);
  chacha20_xor(key.data(), 1, nonce.data(), ciphertext, plaintext.data());
  return plaintext;
}

}  // namespace netsec::detail
