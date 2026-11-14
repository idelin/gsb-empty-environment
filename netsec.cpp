#include "netsec.hpp"

#include <algorithm>
#include <charconv>
#include <cstring>
#include <stdexcept>

namespace netsec {
namespace {

// 自包含 SHA-256（用于 HMAC，避免外部依赖）。
class Sha256 {
public:
  Sha256() { reset(); }

  void reset() {
    state_ = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
              0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
    buffer_len_ = 0;
    total_len_ = 0;
  }

  void update(std::span<const std::uint8_t> data) {
    total_len_ += data.size();
    while (!data.empty()) {
      const std::size_t take =
          std::min(data.size(), sizeof(buffer_) - buffer_len_);
      std::memcpy(buffer_.data() + buffer_len_, data.data(), take);
      buffer_len_ += take;
      data = data.subspan(take);
      if (buffer_len_ == sizeof(buffer_)) {
        compress(buffer_.data());
        buffer_len_ = 0;
      }
    }
  }

  std::array<std::uint8_t, 32> finish() {
    const std::uint64_t bit_len = total_len_ * 8;
    const std::uint8_t pad = 0x80;
    update({&pad, 1});
    const std::uint8_t zero = 0;
    while (buffer_len_ != 56) update({&zero, 1});
    std::array<std::uint8_t, 8> len_bytes{};
    for (int i = 0; i < 8; ++i)
      len_bytes[i] = static_cast<std::uint8_t>(bit_len >> (56 - 8 * i));
    update(len_bytes);
    std::array<std::uint8_t, 32> out{};
    for (int i = 0; i < 8; ++i)
      for (int j = 0; j < 4; ++j)
        out[i * 4 + j] =
            static_cast<std::uint8_t>(state_[i] >> (24 - 8 * j));
    return out;
  }

private:
  static std::uint32_t rotr(std::uint32_t x, unsigned n) {
    return (x >> n) | (x << (32 - n));
  }

  void compress(const std::uint8_t* block) {
    static constexpr std::uint32_t k[64] = {
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b,
        0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01,
        0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7,
        0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
        0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152,
        0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
        0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc,
        0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
        0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819,
        0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08,
        0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f,
        0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
        0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};
    std::uint32_t w[64];
    for (int i = 0; i < 16; ++i)
      w[i] = (static_cast<std::uint32_t>(block[i * 4]) << 24) |
             (static_cast<std::uint32_t>(block[i * 4 + 1]) << 16) |
             (static_cast<std::uint32_t>(block[i * 4 + 2]) << 8) |
             static_cast<std::uint32_t>(block[i * 4 + 3]);
    for (int i = 16; i < 64; ++i) {
      const std::uint32_t s0 =
          rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
      const std::uint32_t s1 =
          rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    std::uint32_t a = state_[0], b = state_[1], c = state_[2],
                  d = state_[3], e = state_[4], f = state_[5],
                  g = state_[6], h = state_[7];
    for (int i = 0; i < 64; ++i) {
      const std::uint32_t s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const std::uint32_t ch = (e & f) ^ (~e & g);
      const std::uint32_t t1 = h + s1 + ch + k[i] + w[i];
      const std::uint32_t s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
      const std::uint32_t t2 = s0 + maj;
      h = g; g = f; f = e; e = d + t1;
      d = c; c = b; b = a; a = t1 + t2;
    }
    state_[0] += a; state_[1] += b; state_[2] += c; state_[3] += d;
    state_[4] += e; state_[5] += f; state_[6] += g; state_[7] += h;
  }

  std::array<std::uint32_t, 8> state_{};
  std::array<std::uint8_t, 64> buffer_{};
  std::size_t buffer_len_ = 0;
  std::uint64_t total_len_ = 0;
};

std::array<std::uint8_t, 32> hmac_sha256(std::span<const std::uint8_t> key,
                                         std::span<const std::uint8_t> msg) {
  std::array<std::uint8_t, 64> block{};
  if (key.size() > block.size()) {
    Sha256 h;
    h.update(key);
    const auto digest = h.finish();
    std::memcpy(block.data(), digest.data(), digest.size());
  } else {
    std::memcpy(block.data(), key.data(), key.size());
  }
  std::array<std::uint8_t, 64> inner{}, outer{};
  for (std::size_t i = 0; i < block.size(); ++i) {
    inner[i] = block[i] ^ 0x36;
    outer[i] = block[i] ^ 0x5c;
  }
  Sha256 h;
  h.update(inner);
  h.update(msg);
  const auto inner_digest = h.finish();
  h.reset();
  h.update(outer);
  h.update(inner_digest);
  return h.finish();
}

// 帧的规范化序列化（认证标签覆盖的字段）。
std::vector<std::uint8_t> canonical_bytes(const ManagementFrame& frame) {
  std::vector<std::uint8_t> out;
  out.reserve(32);
  out.push_back(static_cast<std::uint8_t>(frame.type));
  const auto push_mac = [&out](const MacAddress& mac) {
    out.insert(out.end(), mac.bytes.begin(), mac.bytes.end());
  };
  push_mac(frame.source);
  push_mac(frame.destination);
  const auto reason = static_cast<std::uint16_t>(frame.reason);
  out.push_back(static_cast<std::uint8_t>(reason >> 8));
  out.push_back(static_cast<std::uint8_t>(reason & 0xff));
  for (int i = 7; i >= 0; --i)
    out.push_back(static_cast<std::uint8_t>(frame.sequence >> (8 * i)));
  return out;
}

bool mic_equal(const std::array<std::uint8_t, 16>& a,
               const std::array<std::uint8_t, 16>& b) {
  std::uint8_t diff = 0;
  for (std::size_t i = 0; i < a.size(); ++i) diff |= a[i] ^ b[i];
  return diff == 0;
}

}  // namespace

std::string MacAddress::to_string() const {
  static constexpr char hex[] = "0123456789abcdef";
  std::string out;
  out.reserve(17);
  for (std::size_t i = 0; i < bytes.size(); ++i) {
    if (i) out.push_back(':');
    out.push_back(hex[bytes[i] >> 4]);
    out.push_back(hex[bytes[i] & 0xf]);
  }
  return out;
}

MacAddress MacAddress::from_string(const std::string& text) {
  MacAddress mac;
  if (text.size() != 17) throw std::invalid_argument("bad MAC length");
  for (std::size_t i = 0; i < 6; ++i) {
    unsigned value = 0;
    const char* begin = text.data() + i * 3;
    const auto [ptr, ec] = std::from_chars(begin, begin + 2, value, 16);
    if (ec != std::errc{} || ptr != begin + 2)
      throw std::invalid_argument("bad MAC octet");
    if (i < 5 && text[i * 3 + 2] != ':')
      throw std::invalid_argument("bad MAC separator");
    mac.bytes[i] = static_cast<std::uint8_t>(value);
  }
  return mac;
}

const char* to_string(FrameVerdict verdict) {
  switch (verdict) {
    case FrameVerdict::Accepted: return "Accepted";
    case FrameVerdict::DroppedUnprotected: return "DroppedUnprotected";
    case FrameVerdict::DroppedSourceMismatch: return "DroppedSourceMismatch";
    case FrameVerdict::DroppedSequenceRollback:
      return "DroppedSequenceRollback";
    case FrameVerdict::DroppedAuthenticationFailed:
      return "DroppedAuthenticationFailed";
    case FrameVerdict::DroppedReasonNotPermitted:
      return "DroppedReasonNotPermitted";
  }
  return "Unknown";
}

ProtectedAssociation::ProtectedAssociation(
    MacAddress self, MacAddress ap, std::span<const std::uint8_t> shared_key,
    std::set<ReasonCode> allowed_disconnect_reasons)
    : self_(self),
      ap_(ap),
      key_(shared_key.begin(), shared_key.end()),
      allowed_disconnect_reasons_(std::move(allowed_disconnect_reasons)) {
  if (key_.empty()) throw std::invalid_argument("shared key must not be empty");
}

std::array<std::uint8_t, 16> ProtectedAssociation::compute_mic(
    const ManagementFrame& frame) const {
  const auto bytes = canonical_bytes(frame);
  const auto digest = hmac_sha256(key_, bytes);
  std::array<std::uint8_t, 16> mic{};
  std::memcpy(mic.data(), digest.data(), mic.size());
  return mic;
}

ManagementFrame ProtectedAssociation::protect_outbound(FrameType type,
                                                       ReasonCode reason) {
  ManagementFrame frame;
  frame.type = type;
  frame.source = self_;
  frame.destination = ap_;
  frame.reason = reason;
  frame.sequence = ++tx_sequence_;
  frame.protected_frame = true;
  frame.mic = compute_mic(frame);
  return frame;
}

FrameVerdict ProtectedAssociation::process_inbound(
    const ManagementFrame& frame) {
  // 1) 未受保护的关键管理帧一律丢弃。
  if (!frame.protected_frame) return FrameVerdict::DroppedUnprotected;
  // 2) 来源必须属于当前关联的接入点。
  if (frame.source != ap_) return FrameVerdict::DroppedSourceMismatch;
  // 3) 序列号必须严格递增（防回退/重放）。
  if (rx_initialized_ && frame.sequence <= rx_sequence_)
    return FrameVerdict::DroppedSequenceRollback;
  // 4) 认证标签校验（双向认证的“入”方向）。
  if (!mic_equal(frame.mic, compute_mic(frame)))
    return FrameVerdict::DroppedAuthenticationFailed;
  // 5) 断开类帧的原因码必须落在允许集合内。
  if ((frame.type == FrameType::Deauthentication ||
       frame.type == FrameType::Disassociation) &&
      !disconnect_permitted(frame.reason))
    return FrameVerdict::DroppedReasonNotPermitted;

  rx_sequence_ = frame.sequence;
  rx_initialized_ = true;
  return FrameVerdict::Accepted;
}

bool ProtectedAssociation::disconnect_permitted(ReasonCode reason) const {
  return allowed_disconnect_reasons_.contains(reason);
}

const char* to_string(ReconnectDecision decision) {
  switch (decision) {
    case ReconnectDecision::Permitted: return "Permitted";
    case ReconnectDecision::DeniedUnknownBssid: return "DeniedUnknownBssid";
    case ReconnectDecision::DeniedAttemptsExhausted:
      return "DeniedAttemptsExhausted";
  }
  return "Unknown";
}

ReconnectController::ReconnectController(ReconnectPolicy policy)
    : policy_(std::move(policy)) {}

std::pair<ReconnectDecision, unsigned> ReconnectController::request(
    const MacAddress& bssid) {
  if (!policy_.allowed_bssids.contains(bssid))
    return {ReconnectDecision::DeniedUnknownBssid, 0};
  if (attempts_ >= policy_.max_attempts)
    return {ReconnectDecision::DeniedAttemptsExhausted, 0};
  // 指数退避：backoff_ms * 2^attempts。
  const unsigned wait = policy_.backoff_ms << attempts_;
  ++attempts_;
  return {ReconnectDecision::Permitted, wait};
}

void ReconnectController::reset() { attempts_ = 0; }

}  // namespace netsec
