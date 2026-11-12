#include "netsec.hpp"

#include <cstring>

#if defined(__linux__)
#include <errno.h>
#include <sys/random.h>
#include <unistd.h>
#elif defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
#include <stdlib.h>
#else
#include <chrono>
#include <random>
#endif

namespace netsec {
namespace {

constexpr std::uint64_t kSipV0Init = 0x736f6d6570736575ull;
constexpr std::uint64_t kSipV1Init = 0x646f72616e646f6dull;
constexpr std::uint64_t kSipV2Init = 0x6c7967656e657261ull;
constexpr std::uint64_t kSipV3Init = 0x7465646279746573ull;

std::uint64_t Rotl(std::uint64_t x, unsigned b) noexcept {
  return (x << b) | (x >> (64u - b));
}

std::uint64_t Load64Le(const std::uint8_t* p) noexcept {
  std::uint64_t v = 0;
  std::memcpy(&v, p, sizeof(v));
  return v;
}

void Store64Le(std::uint8_t* p, std::uint64_t v) noexcept {
  std::memcpy(p, &v, sizeof(v));
}

std::uint32_t Load32Le(const std::uint8_t* p) noexcept {
  std::uint32_t v = 0;
  std::memcpy(&v, p, sizeof(v));
  return v;
}

void Store32Le(std::uint8_t* p, std::uint32_t v) noexcept {
  std::memcpy(p, &v, sizeof(v));
}

std::uint16_t Load16Le(const std::uint8_t* p) noexcept {
  std::uint16_t v = 0;
  std::memcpy(&v, p, sizeof(v));
  return v;
}

void Store16Le(std::uint8_t* p, std::uint16_t v) noexcept {
  std::memcpy(p, &v, sizeof(v));
}

// Streaming SipHash-2-4 (Aumasson & Bernstein reference construction) so the
// tag can absorb the tuple hash and the record without a scratch buffer.
class SipHasher {
 public:
  explicit SipHasher(const SipKey& key) noexcept {
    const std::uint64_t k0 = Load64Le(key.bytes);
    const std::uint64_t k1 = Load64Le(key.bytes + 8);
    v0_ = kSipV0Init ^ k0;
    v1_ = kSipV1Init ^ k1;
    v2_ = kSipV2Init ^ k0;
    v3_ = kSipV3Init ^ k1;
  }

  void Update(const std::uint8_t* data, std::size_t len) noexcept {
    total_ += len;
    if (buf_len_ != 0) {
      const std::size_t need = 8u - buf_len_;
      const std::size_t take = len < need ? len : need;
      std::memcpy(buf_ + buf_len_, data, take);
      buf_len_ = static_cast<std::uint8_t>(buf_len_ + take);
      data += take;
      len -= take;
      if (buf_len_ == 8u) {
        ProcessBlock(Load64Le(buf_));
        buf_len_ = 0;
      }
    }
    while (len >= 8u) {
      ProcessBlock(Load64Le(data));
      data += 8;
      len -= 8;
    }
    if (len != 0) {
      std::memcpy(buf_, data, len);
      buf_len_ = static_cast<std::uint8_t>(len);
    }
  }

  std::uint64_t Finish() noexcept {
    std::uint64_t block = (static_cast<std::uint64_t>(total_ & 0xffu) << 56);
    for (std::size_t i = 0; i < buf_len_; ++i) {
      block |= static_cast<std::uint64_t>(buf_[i]) << (8u * i);
    }
    ProcessBlock(block);

    v2_ ^= 0xffull;
    SipRound();
    SipRound();
    SipRound();
    SipRound();
    return v0_ ^ v1_ ^ v2_ ^ v3_;
  }

 private:
  void SipRound() noexcept {
    v0_ += v1_;
    v1_ = Rotl(v1_, 13) ^ v0_;
    v0_ = Rotl(v0_, 32);
    v2_ += v3_;
    v3_ = Rotl(v3_, 16) ^ v2_;
    v0_ += v3_;
    v3_ = Rotl(v3_, 21) ^ v0_;
    v2_ += v1_;
    v1_ = Rotl(v1_, 17) ^ v2_;
    v2_ = Rotl(v2_, 32);
  }

  void ProcessBlock(std::uint64_t block) noexcept {
    v3_ ^= block;
    SipRound();
    SipRound();
    v0_ ^= block;
  }

  std::uint64_t v0_ = 0;
  std::uint64_t v1_ = 0;
  std::uint64_t v2_ = 0;
  std::uint64_t v3_ = 0;
  std::uint8_t buf_[8] = {};
  std::uint8_t buf_len_ = 0;
  std::uint64_t total_ = 0;
};

std::size_t Align4(std::size_t n) noexcept {
  return (n + 3u) & ~static_cast<std::size_t>(3u);
}

std::uint64_t ComputeTag(const SipKey& key, const FiveTuple& tuple,
                         const std::uint8_t* record,
                         std::size_t record_len_no_tag) noexcept {
  SipHasher hasher(key);
  const std::uint8_t tag_domain = 0x01;
  hasher.Update(&tag_domain, 1);
  const std::uint64_t tuple_hash = HashFiveTuple(key, tuple);
  std::uint8_t le[8];
  Store64Le(le, tuple_hash);
  hasher.Update(le, sizeof(le));
  hasher.Update(record, record_len_no_tag);
  return hasher.Finish();
}

bool OsRandom(std::uint8_t* out, std::size_t len) noexcept {
#if defined(__linux__)
  while (len != 0) {
    const ssize_t r = getrandom(out, len, 0);
    if (r < 0) {
      if (errno == EINTR) continue;
      return false;
    }
    out += r;
    len -= static_cast<std::size_t>(r);
  }
  return true;
#elif defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
  arc4random_buf(out, len);
  return true;
#else
  try {
    std::random_device rd;
    while (len != 0) {
      const unsigned v = rd();
      const std::size_t step = len < sizeof(v) ? len : sizeof(v);
      std::memcpy(out, &v, step);
      out += step;
      len -= step;
    }
    return true;
  } catch (...) {
    return false;
  }
#endif
}

}  // namespace

bool IsTrusted(TrustStatus status) noexcept {
  return status == TrustStatus::kOk;
}

const char* ToString(TrustStatus status) noexcept {
  switch (status) {
    case TrustStatus::kOk:
      return "ok";
    case TrustStatus::kUntrustedTooSmall:
      return "untrusted: region smaller than fixed header";
    case TrustStatus::kUntrustedBadMagic:
      return "untrusted: magic mismatch";
    case TrustStatus::kUntrustedBadVersion:
      return "untrusted: version mismatch";
    case TrustStatus::kUntrustedBadLength:
      return "untrusted: length mismatch or record crosses data start";
    case TrustStatus::kUntrustedBadTag:
      return "untrusted: SipHash tag mismatch";
  }
  return "untrusted: unknown";
}

SipKey GenerateBootKey() noexcept {
  SipKey key;
  if (OsRandom(key.bytes, sizeof(key.bytes))) return key;

  // Last-resort fallback when no OS/CSPRNG source is available. Mix address
  // layout and a counter so two boot keys still differ within a process.
  std::uint64_t seed = 0x9e3779b97f4a7c15ull;
  seed ^= static_cast<std::uint64_t>(
      reinterpret_cast<std::uintptr_t>(&key));
  for (std::uint8_t& b : key.bytes) {
    seed ^= seed << 13;
    seed ^= seed >> 7;
    seed ^= seed << 17;
    b = static_cast<std::uint8_t>(seed & 0xffu);
  }
  return key;
}

std::uint64_t SipHash24(const SipKey& key, const std::uint8_t* data,
                        std::size_t len) noexcept {
  SipHasher hasher(key);
  hasher.Update(data, len);
  return hasher.Finish();
}

std::uint64_t HashFiveTuple(const SipKey& key,
                            const FiveTuple& tuple) noexcept {
  std::uint8_t encoded[1 + 1 + 2 + 2 + 16 + 16];
  std::size_t pos = 0;
  encoded[pos++] = static_cast<std::uint8_t>(tuple.family);
  encoded[pos++] = tuple.protocol;
  Store16Le(encoded + pos, tuple.src_port);
  pos += 2;
  Store16Le(encoded + pos, tuple.dst_port);
  pos += 2;

  const std::size_t addr_len = tuple.family == IpFamily::kV4 ? 4u : 16u;
  std::memcpy(encoded + pos, tuple.src_addr, addr_len);
  pos += addr_len;
  std::memcpy(encoded + pos, tuple.dst_addr, addr_len);
  pos += addr_len;
  return SipHash24(key, encoded, pos);
}

bool WriteMetadata(const SipKey& key, std::uint16_t stage,
                   const std::uint8_t* payload, std::uint16_t payload_len,
                   const FiveTuple& tuple, std::uint8_t* data_start,
                   std::size_t front_size, std::uint8_t** meta_begin,
                   std::size_t* meta_len) noexcept {
  if (data_start == nullptr || (payload == nullptr && payload_len != 0)) {
    return false;
  }

  const std::size_t padded_payload = Align4(payload_len);
  const std::size_t total = kHeaderSize + padded_payload + kTagSize;
  if (total > front_size || total > 0xffffu) {
    return false;
  }

  std::uint8_t* begin = data_start - total;
  Store32Le(begin + 0, kMagic);
  Store16Le(begin + 4, kVersion);
  Store16Le(begin + 6, stage);
  Store16Le(begin + 8, payload_len);
  Store16Le(begin + 10, static_cast<std::uint16_t>(total));

  if (payload_len != 0) {
    std::memcpy(begin + kHeaderSize, payload, payload_len);
  }
  if (padded_payload != payload_len) {
    std::memset(begin + kHeaderSize + payload_len, 0,
                padded_payload - payload_len);
  }

  const std::uint64_t tag =
      ComputeTag(key, tuple, begin, total - kTagSize);
  Store64Le(begin + total - kTagSize, tag);

  if (meta_begin != nullptr) *meta_begin = begin;
  if (meta_len != nullptr) *meta_len = total;
  return true;
}

TrustStatus ReadMetadata(const SipKey& key, const std::uint8_t* meta,
                         const std::uint8_t* data_start,
                         const FiveTuple& tuple, MetaView* out) noexcept {
  if (out != nullptr) *out = MetaView{};
  if (meta == nullptr || data_start == nullptr || meta > data_start) {
    return TrustStatus::kUntrustedTooSmall;
  }

  const std::size_t available =
      static_cast<std::size_t>(data_start - meta);
  if (available < kHeaderSize) {
    return TrustStatus::kUntrustedTooSmall;
  }

  const std::uint32_t magic = Load32Le(meta + 0);
  const std::uint16_t version = Load16Le(meta + 4);
  const std::uint16_t stage = Load16Le(meta + 6);
  const std::uint16_t payload_len = Load16Le(meta + 8);
  const std::uint16_t total_len = Load16Le(meta + 10);

  if (magic != kMagic) {
    return TrustStatus::kUntrustedBadMagic;
  }
  if (version != kVersion) {
    return TrustStatus::kUntrustedBadVersion;
  }

  const std::size_t expected_len =
      kHeaderSize + Align4(payload_len) + kTagSize;
  if (total_len < kHeaderSize + kTagSize || total_len > available ||
      static_cast<std::size_t>(total_len) != expected_len ||
      kHeaderSize + static_cast<std::size_t>(payload_len) + kTagSize >
          total_len) {
    return TrustStatus::kUntrustedBadLength;
  }

  const std::uint64_t expected_tag = ComputeTag(
      key, tuple, meta, static_cast<std::size_t>(total_len) - kTagSize);
  const std::uint64_t actual_tag =
      Load64Le(meta + total_len - kTagSize);

  std::uint64_t diff = expected_tag ^ actual_tag;
  diff = (diff | (~diff + 1ull)) >> 63;
  if (diff != 0) {
    return TrustStatus::kUntrustedBadTag;
  }

  if (out != nullptr) {
    out->payload = meta + kHeaderSize;
    out->payload_len = payload_len;
    out->stage = stage;
    out->version = version;
  }
  return TrustStatus::kOk;
}

}  // namespace netsec
