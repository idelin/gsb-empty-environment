#include "estop_integrity.hpp"

#include <cstring>

namespace netsec::estop {
namespace {

class Sha256 {
 public:
  Sha256() { reset(); }

  void reset() {
    state_[0] = 0x6a09e667u;
    state_[1] = 0xbb67ae85u;
    state_[2] = 0x3c6ef372u;
    state_[3] = 0xa54ff53au;
    state_[4] = 0x510e527fu;
    state_[5] = 0x9b05688cu;
    state_[6] = 0x1f83d9abu;
    state_[7] = 0x5be0cd19u;
    bit_length_ = 0;
    buffer_size_ = 0;
  }

  void update(const std::uint8_t* data, std::size_t length) {
    bit_length_ += static_cast<std::uint64_t>(length) * 8u;
    while (length > 0) {
      const std::size_t take =
          length < 64 - buffer_size_ ? length : 64 - buffer_size_;
      std::memcpy(buffer_.data() + buffer_size_, data, take);
      buffer_size_ += take;
      data += take;
      length -= take;
      if (buffer_size_ == 64) {
        compress(buffer_.data());
        buffer_size_ = 0;
      }
    }
  }

  HmacTag final() {
    buffer_[buffer_size_++] = 0x80u;
    if (buffer_size_ > 56) {
      while (buffer_size_ < 64) buffer_[buffer_size_++] = 0;
      compress(buffer_.data());
      buffer_size_ = 0;
    }
    while (buffer_size_ < 56) buffer_[buffer_size_++] = 0;
    for (int i = 7; i >= 0; --i) {
      buffer_[buffer_size_++] =
          static_cast<std::uint8_t>(bit_length_ >> (i * 8));
    }
    compress(buffer_.data());
    HmacTag out{};
    for (int i = 0; i < 8; ++i) {
      out[i * 4] = static_cast<std::uint8_t>(state_[i] >> 24);
      out[i * 4 + 1] = static_cast<std::uint8_t>(state_[i] >> 16);
      out[i * 4 + 2] = static_cast<std::uint8_t>(state_[i] >> 8);
      out[i * 4 + 3] = static_cast<std::uint8_t>(state_[i]);
    }
    return out;
  }

 private:
  static std::uint32_t rotr(std::uint32_t x, std::uint32_t n) {
    return (x >> n) | (x << (32 - n));
  }

  void compress(const std::uint8_t* block) {
    static constexpr std::uint32_t k[64] = {
        0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu,
        0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u,
        0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u,
        0xc19bf174u, 0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
        0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau, 0x983e5152u,
        0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
        0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu,
        0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
        0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u,
        0xd6990624u, 0xf40e3585u, 0x106aa070u, 0x19a4c116u, 0x1e376c08u,
        0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu,
        0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
        0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u};
    std::uint32_t w[64];
    for (int i = 0; i < 16; ++i) {
      w[i] = (static_cast<std::uint32_t>(block[i * 4]) << 24) |
             (static_cast<std::uint32_t>(block[i * 4 + 1]) << 16) |
             (static_cast<std::uint32_t>(block[i * 4 + 2]) << 8) |
             static_cast<std::uint32_t>(block[i * 4 + 3]);
    }
    for (int i = 16; i < 64; ++i) {
      const std::uint32_t s0 =
          rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
      const std::uint32_t s1 =
          rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    std::uint32_t a = state_[0], b = state_[1], c = state_[2], d = state_[3];
    std::uint32_t e = state_[4], f = state_[5], g = state_[6], h = state_[7];
    for (int i = 0; i < 64; ++i) {
      const std::uint32_t s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const std::uint32_t ch = (e & f) ^ (~e & g);
      const std::uint32_t t1 = h + s1 + ch + k[i] + w[i];
      const std::uint32_t s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
      const std::uint32_t t2 = s0 + maj;
      h = g;
      g = f;
      f = e;
      e = d + t1;
      d = c;
      c = b;
      b = a;
      a = t1 + t2;
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

  std::uint32_t state_[8]{};
  std::array<std::uint8_t, 64> buffer_{};
  std::size_t buffer_size_ = 0;
  std::uint64_t bit_length_ = 0;
};

bool tag_equals(const HmacTag& a, const HmacTag& b) {
  std::uint8_t diff = 0;
  for (std::size_t i = 0; i < a.size(); ++i) diff |= a[i] ^ b[i];
  return diff == 0;
}

}  // namespace

HmacTag hmac_sha256(const SecretKey& key, const void* data,
                    std::size_t length) {
  std::array<std::uint8_t, 64> ipad{};
  std::array<std::uint8_t, 64> opad{};
  for (std::size_t i = 0; i < 64; ++i) {
    const std::uint8_t kbyte = i < key.size() ? key[i] : 0;
    ipad[i] = kbyte ^ 0x36u;
    opad[i] = kbyte ^ 0x5cu;
  }
  Sha256 inner;
  inner.update(ipad.data(), ipad.size());
  inner.update(static_cast<const std::uint8_t*>(data), length);
  const HmacTag inner_hash = inner.final();
  Sha256 outer;
  outer.update(opad.data(), opad.size());
  outer.update(inner_hash.data(), inner_hash.size());
  return outer.final();
}

HmacTag sign_frame(const SecretKey& key, std::uint64_t sequence,
                   BusCommand command) {
  std::uint8_t payload[9];
  for (int i = 0; i < 8; ++i) {
    payload[i] = static_cast<std::uint8_t>(sequence >> (i * 8));
  }
  payload[8] = static_cast<std::uint8_t>(command);
  return hmac_sha256(key, payload, sizeof(payload));
}

HmacTag sign_reset(const SecretKey& key, std::uint64_t nonce) {
  std::uint8_t payload[13];
  payload[0] = 'R';
  payload[1] = 'E';
  payload[2] = 'S';
  payload[3] = 'E';
  payload[4] = 'T';
  for (int i = 0; i < 8; ++i) {
    payload[5 + i] = static_cast<std::uint8_t>(nonce >> (i * 8));
  }
  return hmac_sha256(key, payload, sizeof(payload));
}

EstopIntegrity::EstopIntegrity(SecretKey key,
                               std::chrono::milliseconds discrepancy_window)
    : key_(key), discrepancy_window_(discrepancy_window) {}

void EstopIntegrity::set_hardwired_estop(bool active) {
  hardwired_ = active;
  if (active) latch_stop(StopReason::kHardwired);
}

bool EstopIntegrity::process_frame(const BusFrame& frame, TimePoint now) {
  // 屏蔽急停与总线清除急停为被禁止命令:即使签名合法也拒绝并闭锁。
  if (frame.command == BusCommand::kMaskEstop ||
      frame.command == BusCommand::kClearEstop) {
    latch_stop(StopReason::kSecurityViolation);
    return false;
  }
  // 防重放:序号必须严格递增。
  if (frame.sequence <= last_sequence_) {
    latch_stop(StopReason::kSecurityViolation);
    return false;
  }
  // 完整性校验:签名不合法即伪造,拒绝并闭锁。
  const HmacTag expected = sign_frame(key_, frame.sequence, frame.command);
  if (!tag_equals(expected, frame.tag)) {
    latch_stop(StopReason::kSecurityViolation);
    return false;
  }
  last_sequence_ = frame.sequence;
  switch (frame.command) {
    case BusCommand::kChannelAAsserted:
      channel_a_ = true;
      break;
    case BusCommand::kChannelAReleased:
      channel_a_ = false;
      break;
    case BusCommand::kChannelBAsserted:
      channel_b_ = true;
      break;
    case BusCommand::kChannelBReleased:
      channel_b_ = false;
      break;
    default:
      break;
  }
  evaluate_discrepancy(now);
  if (stop_reason_ == StopReason::kNone && channel_a_ && channel_b_) {
    latch_stop(StopReason::kEstopLoop);
  }
  return true;
}

void EstopIntegrity::tick(TimePoint now) { evaluate_discrepancy(now); }

ResetResult EstopIntegrity::request_reset(const ResetRequest& request) {
  if (stop_reason_ == StopReason::kNone) {
    return ResetResult::kRejectedNotStopped;
  }
  if (hardwired_) {
    return ResetResult::kRejectedHardwiredActive;
  }
  if (!loop_healthy()) {
    return ResetResult::kRejectedLoopActive;
  }
  if (!request.physical_confirmed) {
    return ResetResult::kRejectedNoPhysicalConfirm;
  }
  if (request.nonce != reset_nonce_) {
    return ResetResult::kRejectedBadNonce;
  }
  const HmacTag expected = sign_reset(key_, request.nonce);
  if (!tag_equals(expected, request.signature)) {
    return ResetResult::kRejectedBadSignature;
  }
  stop_reason_ = StopReason::kNone;
  ++reset_nonce_;
  return ResetResult::kAccepted;
}

void EstopIntegrity::latch_stop(StopReason reason) {
  // 硬接线优先:已因硬接线停机时不允许被其他原因替换。
  if (stop_reason_ == StopReason::kHardwired) return;
  stop_reason_ = reason;
}

void EstopIntegrity::evaluate_discrepancy(TimePoint now) {
  if (channel_a_ == channel_b_) {
    discrepancy_pending_ = false;
    return;
  }
  if (!discrepancy_pending_) {
    discrepancy_pending_ = true;
    discrepancy_since_ = now;
    return;
  }
  if (now - discrepancy_since_ > discrepancy_window_) {
    latch_stop(StopReason::kDiscrepancy);
  }
}

bool EstopIntegrity::loop_healthy() const {
  return !channel_a_ && !channel_b_;
}

}  // namespace netsec::estop
