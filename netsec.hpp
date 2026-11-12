#pragma once

#include <cstddef>
#include <cstdint>

namespace netsec {

// On-wire metadata layout, stored immediately before the packet data start
// (XDP data_meta area) in little-endian byte order:
//
//   offset 0  : uint32 magic
//   offset 4  : uint16 version
//   offset 6  : uint16 write_stage
//   offset 8  : uint16 payload_len
//   offset 10 : uint16 total_len   (whole metadata record incl. tag)
//   offset 12 : uint8  payload[payload_len], zero-padded to 4-byte alignment
//   then      : uint64 SipHash-2-4 tag
//
// The tag authenticates the whole record together with a SipHash of the
// packet five-tuple, so metadata cannot be moved onto a different flow and a
// forwarded packet cannot carry a record forged without the per-boot key.

inline constexpr uint32_t kMagic = 0x444d534eu;  // "NSMD"
inline constexpr uint16_t kVersion = 1u;
inline constexpr std::size_t kHeaderSize = 12u;
inline constexpr std::size_t kTagSize = 8u;
inline constexpr std::size_t kMaxPayloadLen = 65535u;

enum class IpFamily : std::uint8_t {
  kV4 = 4,
  kV6 = 6,
};

struct FiveTuple {
  IpFamily family = IpFamily::kV4;
  std::uint8_t protocol = 0;
  std::uint16_t src_port = 0;  // host order
  std::uint16_t dst_port = 0;  // host order
  std::uint8_t src_addr[16] = {};
  std::uint8_t dst_addr[16] = {};
};

struct SipKey {
  std::uint8_t bytes[16] = {};
};

enum class TrustStatus : std::uint8_t {
  kOk = 0,
  kUntrustedTooSmall,
  kUntrustedBadMagic,
  kUntrustedBadVersion,
  kUntrustedBadLength,
  kUntrustedBadTag,
};

struct MetaView {
  const std::uint8_t* payload = nullptr;
  std::uint16_t payload_len = 0;
  std::uint16_t stage = 0;
  std::uint16_t version = 0;
};

bool IsTrusted(TrustStatus status) noexcept;
const char* ToString(TrustStatus status) noexcept;

// Fresh random 128-bit SipHash key for this process boot.
SipKey GenerateBootKey() noexcept;

// Canonical SipHash-2-4 over raw bytes.
std::uint64_t SipHash24(const SipKey& key, const std::uint8_t* data,
                        std::size_t len) noexcept;

// Domain-separated SipHash of the packet five-tuple.
std::uint64_t HashFiveTuple(const SipKey& key, const FiveTuple& tuple) noexcept;

// Writes the record into the front space ending at data_start.
// front_space spans [data_start - front_size, data_start); the record is
// placed at the end of that region and never crosses the data start. Returns
// false when the payload or whole record does not fit. On success *meta_begin
// points at the first record byte and *meta_len holds total_len.
bool WriteMetadata(const SipKey& key, std::uint16_t stage,
                   const std::uint8_t* payload, std::uint16_t payload_len,
                   const FiveTuple& tuple, std::uint8_t* data_start,
                   std::size_t front_size, std::uint8_t** meta_begin = nullptr,
                   std::size_t* meta_len = nullptr) noexcept;

// Validates [meta, data_start): any magic/version/tag/length mismatch is
// reported as an untrusted status and the caller must drop any trust decision.
TrustStatus ReadMetadata(const SipKey& key, const std::uint8_t* meta,
                         const std::uint8_t* data_start,
                         const FiveTuple& tuple, MetaView* out) noexcept;

}  // namespace netsec
