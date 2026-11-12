#include "netsec.hpp"

#include <cstdint>
#include <cstring>
#include <iostream>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(cond)                                                      \
  do {                                                                   \
    if (!(cond)) {                                                       \
      std::cerr << "FAIL: " #cond " at " << __FILE__ << ':' << __LINE__  \
                << '\n';                                                 \
      ++g_failures;                                                      \
    }                                                                    \
  } while (0)

SipKey FixedKey() {
  SipKey key;
  for (int i = 0; i < 16; ++i)
    key.bytes[i] = static_cast<std::uint8_t>(i);
  return key;
}

FiveTuple V4Tuple(std::uint16_t src_port = 12345,
                  std::uint16_t dst_port = 443) {
  FiveTuple t;
  t.family = IpFamily::kV4;
  t.protocol = 6;  // TCP
  t.src_port = src_port;
  t.dst_port = dst_port;
  const std::uint8_t src[4] = {10, 0, 0, 1};
  const std::uint8_t dst[4] = {198, 51, 100, 7};
  std::memcpy(t.src_addr, src, 4);
  std::memcpy(t.dst_addr, dst, 4);
  return t;
}

FiveTuple V6Tuple() {
  FiveTuple t;
  t.family = IpFamily::kV6;
  t.protocol = 17;  // UDP
  t.src_port = 5000;
  t.dst_port = 53;
  const std::uint8_t src[16] = {0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
                                0,    0,    0,    0,    0, 0, 0, 1};
  const std::uint8_t dst[16] = {0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
                                0,    0,    0,    0,    0, 0, 0, 2};
  std::memcpy(t.src_addr, src, 16);
  std::memcpy(t.dst_addr, dst, 16);
  return t;
}

// Simulated packet: front scratch area followed by a packet payload marker.
struct PacketBuf {
  static constexpr std::size_t kFront = 256;
  static constexpr std::size_t kPayload = 64;
  std::vector<std::uint8_t> data =
      std::vector<std::uint8_t>(kFront + kPayload, 0xA5);

  std::uint8_t* data_start() { return data.data() + kFront; }
};

void TestSipHashVectors() {
  const SipKey key = FixedKey();
  CHECK(SipHash24(key, nullptr, 0) == 0x726fdb47dd0e0e31ull);

  const std::uint8_t one[1] = {0};
  CHECK(SipHash24(key, one, 1) == 0x74f839c593dc67fdull);

  std::uint8_t fifteen[15];
  for (int i = 0; i < 15; ++i)
    fifteen[i] = static_cast<std::uint8_t>(i);
  CHECK(SipHash24(key, fifteen, 15) == 0xa129ca6149be45e5ull);

  std::uint8_t sixty_three[63];
  for (int i = 0; i < 63; ++i)
    sixty_three[i] = static_cast<std::uint8_t>(i);
  CHECK(SipHash24(key, sixty_three, 63) == 0x958a324ceb064572ull);
}

void TestBootKeyRandomness() {
  const SipKey a = GenerateBootKey();
  const SipKey b = GenerateBootKey();
  bool nonzero = false;
  for (std::uint8_t byte : a.bytes)
    if (byte != 0) nonzero = true;
  CHECK(nonzero);
  CHECK(std::memcmp(a.bytes, b.bytes, sizeof(a.bytes)) != 0);
}

void TestRoundTrip() {
  const SipKey key = FixedKey();
  const FiveTuple tuple = V4Tuple();

  const std::size_t payload_lens[] = {1, 3, 4, 5, 7, 16, 31};
  for (std::size_t plen : payload_lens) {
    PacketBuf pkt;
    std::vector<std::uint8_t> payload(plen);
    for (std::size_t i = 0; i < plen; ++i)
      payload[i] = static_cast<std::uint8_t>(i + 1);

    std::uint8_t* begin = nullptr;
    std::size_t meta_len = 0;
    CHECK(WriteMetadata(key, 0x2001, payload.data(),
                        static_cast<std::uint16_t>(plen), tuple,
                        pkt.data_start(), PacketBuf::kFront, &begin,
                        &meta_len));
    CHECK(begin == pkt.data_start() - meta_len);
    CHECK(meta_len ==
          kHeaderSize + ((plen + 3) & ~std::size_t(3)) + kTagSize);
    CHECK(begin + meta_len == pkt.data_start());

    MetaView view;
    const TrustStatus st =
        ReadMetadata(key, begin, pkt.data_start(), tuple, &view);
    CHECK(st == TrustStatus::kOk);
    CHECK(IsTrusted(st));
    CHECK(view.payload_len == plen);
    CHECK(view.stage == 0x2001);
    CHECK(view.version == kVersion);
    CHECK(std::memcmp(view.payload, payload.data(), plen) == 0);
  }
}

void TestEmptyMetadata() {
  const SipKey key = FixedKey();
  const FiveTuple tuple = V4Tuple();
  PacketBuf pkt;

  std::uint8_t* begin = nullptr;
  std::size_t meta_len = 0;
  CHECK(WriteMetadata(key, 7, nullptr, 0, tuple, pkt.data_start(),
                      PacketBuf::kFront, &begin, &meta_len));
  CHECK(meta_len == kHeaderSize + kTagSize);

  MetaView view;
  CHECK(ReadMetadata(key, begin, pkt.data_start(), tuple, &view) ==
        TrustStatus::kOk);
  CHECK(view.payload_len == 0);
  CHECK(view.stage == 7);

  // Completely absent metadata region is untrusted.
  CHECK(!IsTrusted(ReadMetadata(key, pkt.data_start(), pkt.data_start(),
                                tuple, &view)));
}

void TestForgedTag() {
  const SipKey key = FixedKey();
  const FiveTuple tuple = V4Tuple();
  PacketBuf pkt;
  const std::uint8_t payload[4] = {1, 2, 3, 4};

  std::uint8_t* begin = nullptr;
  std::size_t meta_len = 0;
  CHECK(WriteMetadata(key, 1, payload, 4, tuple, pkt.data_start(),
                      PacketBuf::kFront, &begin, &meta_len));

  // Flip one tag byte: attacker cannot recompute without the boot key.
  PacketBuf forged = pkt;
  std::uint8_t* fbegin =
      forged.data.data() + (begin - pkt.data.data());
  fbegin[meta_len - 1] ^= 0x01;
  MetaView view;
  CHECK(ReadMetadata(key, fbegin, forged.data_start(), tuple, &view) ==
        TrustStatus::kUntrustedBadTag);

  // Record forged under a different key is rejected.
  SipKey other = key;
  other.bytes[0] ^= 0xFF;
  CHECK(ReadMetadata(other, begin, pkt.data_start(), tuple, &view) ==
        TrustStatus::kUntrustedBadTag);

  // Tampering with the payload itself breaks the tag as well.
  PacketBuf tampered = pkt;
  std::uint8_t* tbegin =
      tampered.data.data() + (begin - pkt.data.data());
  tbegin[kHeaderSize + 1] ^= 0x80;
  CHECK(ReadMetadata(key, tbegin, tampered.data_start(), tuple, &view) ==
        TrustStatus::kUntrustedBadTag);
}

void TestFiveTupleSwap() {
  const SipKey key = FixedKey();
  const FiveTuple tuple = V4Tuple();
  PacketBuf pkt;
  const std::uint8_t payload[8] = {9, 8, 7, 6, 5, 4, 3, 2};

  std::uint8_t* begin = nullptr;
  CHECK(WriteMetadata(key, 1, payload, 8, tuple, pkt.data_start(),
                      PacketBuf::kFront, &begin, nullptr));

  MetaView view;
  // Move the record onto another flow (different dst port).
  FiveTuple moved = V4Tuple(12345, 8443);
  CHECK(ReadMetadata(key, begin, pkt.data_start(), moved, &view) ==
        TrustStatus::kUntrustedBadTag);

  // Swap src/dst ports (NAT/forwarding confusion).
  FiveTuple swapped = V4Tuple(443, 12345);
  CHECK(ReadMetadata(key, begin, pkt.data_start(), swapped, &view) ==
        TrustStatus::kUntrustedBadTag);

  // Original flow still validates.
  CHECK(ReadMetadata(key, begin, pkt.data_start(), tuple, &view) ==
        TrustStatus::kOk);

  // IPv6 tuple round trip plus mismatch.
  const FiveTuple v6 = V6Tuple();
  PacketBuf v6pkt;
  std::uint8_t* v6begin = nullptr;
  CHECK(WriteMetadata(key, 3, payload, 8, v6, v6pkt.data_start(),
                      PacketBuf::kFront, &v6begin, nullptr));
  CHECK(ReadMetadata(key, v6begin, v6pkt.data_start(), v6, &view) ==
        TrustStatus::kOk);
  FiveTuple v6moved = v6;
  v6moved.dst_port = 54;
  CHECK(ReadMetadata(key, v6begin, v6pkt.data_start(), v6moved, &view) ==
        TrustStatus::kUntrustedBadTag);
}

void TestTruncation() {
  const SipKey key = FixedKey();
  const FiveTuple tuple = V4Tuple();
  PacketBuf pkt;
  const std::uint8_t payload[5] = {1, 2, 3, 4, 5};

  std::uint8_t* begin = nullptr;
  std::size_t meta_len = 0;
  CHECK(WriteMetadata(key, 1, payload, 5, tuple, pkt.data_start(),
                      PacketBuf::kFront, &begin, &meta_len));

  MetaView view;
  // Region shorter than the fixed header.
  CHECK(ReadMetadata(key, begin, begin + 4, tuple, &view) ==
        TrustStatus::kUntrustedTooSmall);

  // Header present but region ends before total_len (tag cut off).
  CHECK(ReadMetadata(key, begin, begin + meta_len - 1, tuple, &view) ==
        TrustStatus::kUntrustedBadLength);

  // Claimed total_len lies beyond the data start (length field inflated).
  PacketBuf inflated = pkt;
  std::uint8_t* ibegin =
      inflated.data.data() + (begin - pkt.data.data());
  std::uint16_t claimed = static_cast<std::uint16_t>(meta_len + 4);
  std::memcpy(ibegin + 10, &claimed, sizeof(claimed));
  CHECK(ReadMetadata(key, ibegin, inflated.data_start(), tuple, &view) ==
        TrustStatus::kUntrustedBadLength);

  // Inconsistent payload_len vs total_len.
  PacketBuf inconsistent = pkt;
  std::uint8_t* xbegin =
      inconsistent.data.data() + (begin - pkt.data.data());
  const std::uint16_t bad_payload = 60000;
  std::memcpy(xbegin + 8, &bad_payload, sizeof(bad_payload));
  CHECK(ReadMetadata(key, xbegin, inconsistent.data_start(), tuple,
                     &view) == TrustStatus::kUntrustedBadLength);
}

void TestOldVersionAndMagic() {
  const SipKey key = FixedKey();
  const FiveTuple tuple = V4Tuple();
  PacketBuf pkt;
  const std::uint8_t payload[4] = {1, 2, 3, 4};

  std::uint8_t* begin = nullptr;
  CHECK(WriteMetadata(key, 1, payload, 4, tuple, pkt.data_start(),
                      PacketBuf::kFront, &begin, nullptr));

  PacketBuf old = pkt;
  std::uint8_t* obegin = old.data.data() + (begin - pkt.data.data());
  const std::uint16_t old_version = 0;
  std::memcpy(obegin + 4, &old_version, sizeof(old_version));
  MetaView view;
  CHECK(ReadMetadata(key, obegin, old.data_start(), tuple, &view) ==
        TrustStatus::kUntrustedBadVersion);

  PacketBuf badmagic = pkt;
  std::uint8_t* mbegin =
      badmagic.data.data() + (begin - pkt.data.data());
  mbegin[0] ^= 0xFF;
  CHECK(ReadMetadata(key, mbegin, badmagic.data_start(), tuple, &view) ==
        TrustStatus::kUntrustedBadMagic);
}

void TestFrontSpaceBounds() {
  const SipKey key = FixedKey();
  const FiveTuple tuple = V4Tuple();
  const std::uint8_t payload[10] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9};
  // total = 12 header + 12 padded payload + 8 tag = 32
  constexpr std::size_t kNeeded = 32;

  PacketBuf pkt;
  // Refused when front space is smaller than the record.
  CHECK(!WriteMetadata(key, 1, payload, 10, tuple, pkt.data_start(),
                       kNeeded - 1, nullptr, nullptr));

  // Fits exactly, and bytes ahead of the record stay untouched.
  std::uint8_t* begin = nullptr;
  std::size_t meta_len = 0;
  CHECK(WriteMetadata(key, 1, payload, 10, tuple, pkt.data_start(),
                      kNeeded, &begin, &meta_len));
  CHECK(meta_len == kNeeded);
  CHECK(begin == pkt.data_start() - kNeeded);
  for (std::uint8_t* p = pkt.data.data(); p < begin; ++p) {
    CHECK(*p == 0xA5);
  }
}

}  // namespace

int main() {
  TestSipHashVectors();
  TestBootKeyRandomness();
  TestRoundTrip();
  TestEmptyMetadata();
  TestForgedTag();
  TestFiveTupleSwap();
  TestTruncation();
  TestOldVersionAndMagic();
  TestFrontSpaceBounds();

  if (g_failures == 0) {
    std::cout << "all netsec tests passed\n";
    return 0;
  }
  std::cout << g_failures << " check(s) failed\n";
  return 1;
}
