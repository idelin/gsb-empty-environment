#include "frame_decoder.hpp"

#include <cstdint>
#include <cstdio>
#include <vector>

using framed::DecodeError;
using framed::Frame;
using framed::FrameDecoder;

namespace {

int g_failures = 0;

#define CHECK(cond)                                                     \
  do {                                                                  \
    if (!(cond)) {                                                      \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__,      \
                   #cond);                                              \
      ++g_failures;                                                     \
    }                                                                   \
  } while (0)

std::vector<std::uint8_t> EncodeVarint(std::uint32_t value) {
  std::vector<std::uint8_t> out;
  do {
    std::uint8_t byte = value & 0x7Fu;
    value >>= 7;
    if (value != 0) {
      byte |= 0x80u;
    }
    out.push_back(byte);
  } while (value != 0);
  return out;
}

void Append(std::vector<std::uint8_t>* dst,
            const std::vector<std::uint8_t>& src) {
  dst->insert(dst->end(), src.begin(), src.end());
}

std::vector<std::uint8_t> MakeFrame(std::uint8_t type,
                                    const std::vector<std::uint8_t>& payload) {
  std::vector<std::uint8_t> out;
  out.push_back(type);
  Append(&out, EncodeVarint(static_cast<std::uint32_t>(payload.size())));
  Append(&out, payload);
  return out;
}

std::vector<std::uint8_t> MakeControlFrame(std::uint32_t new_max) {
  return MakeFrame(framed::kControlFrameType, EncodeVarint(new_max));
}

// Core property test: for every pair of cut points 0 <= i <= j <= n, feeding
// the sample as three chunks [0,i) [i,j) [j,n) must succeed and produce the
// same frames (and final limit) as feeding the whole buffer at once.
void CheckAllTwoPointSplits(const std::vector<std::uint8_t>& sample,
                            std::uint32_t initial_max) {
  FrameDecoder whole(initial_max);
  std::vector<Frame> expected;
  CHECK(whole.feed(sample, &expected));
  CHECK(whole.ok());

  const std::size_t n = sample.size();
  for (std::size_t i = 0; i <= n; ++i) {
    for (std::size_t j = i; j <= n; ++j) {
      FrameDecoder dec(initial_max);
      std::vector<Frame> got;
      const bool ok1 = dec.feed(sample.data(), i, &got);
      const bool ok2 = dec.feed(sample.data() + i, j - i, &got);
      const bool ok3 = dec.feed(sample.data() + j, n - j, &got);
      if (!(ok1 && ok2 && ok3 && dec.ok() && got == expected &&
            dec.max_frame_length() == whole.max_frame_length())) {
        std::fprintf(stderr,
                     "FAIL split mismatch: sample_size=%zu i=%zu j=%zu\n", n,
                     i, j);
        ++g_failures;
        return;
      }
    }
  }
}

void CheckByteByByte(const std::vector<std::uint8_t>& sample,
                     std::uint32_t initial_max) {
  FrameDecoder whole(initial_max);
  std::vector<Frame> expected;
  CHECK(whole.feed(sample, &expected));

  FrameDecoder dec(initial_max);
  std::vector<Frame> got;
  for (std::uint8_t byte : sample) {
    CHECK(dec.feed(&byte, 1, &got));
  }
  CHECK(dec.ok());
  CHECK(got == expected);
}

// An error must latch: further feeds fail and emit nothing until reset().
void CheckErrorSticksUntilReset(const std::vector<std::uint8_t>& bad,
                                DecodeError want,
                                std::uint32_t initial_max =
                                    framed::kDefaultMaxFrameLength) {
  FrameDecoder dec(initial_max);
  std::vector<Frame> out;
  CHECK(!dec.feed(bad, &out));
  CHECK(dec.error() == want);
  CHECK(!dec.ok());

  const std::vector<std::uint8_t> good = MakeFrame(0x01, {0xAA});
  const std::size_t frames_before = out.size();
  CHECK(!dec.feed(good, &out));
  CHECK(out.size() == frames_before);
  CHECK(dec.error() == want);

  dec.reset();
  CHECK(dec.ok());
  CHECK(dec.error() == DecodeError::kNone);
  CHECK(dec.feed(good, &out));
  CHECK(out.size() == frames_before + 1);
  CHECK(out.back().type == 0x01);
  CHECK(out.back().payload == std::vector<std::uint8_t>{0xAA});
}

std::vector<std::vector<std::uint8_t>> BuildValidSamples() {
  std::vector<std::vector<std::uint8_t>> samples;

  // Empty stream.
  samples.push_back({});

  // Single frame with empty payload (length 0, single-byte varint).
  samples.push_back(MakeFrame(0x01, {}));

  // Assorted data frames exercising 1- and 2-byte length encodings.
  {
    std::vector<std::uint8_t> s;
    Append(&s, MakeFrame(0x01, {1, 2, 3}));
    Append(&s, MakeFrame(0x7F, std::vector<std::uint8_t>(127, 0xAB)));
    Append(&s, MakeFrame(0x02, std::vector<std::uint8_t>(128, 0xCD)));
    Append(&s, MakeFrame(0x03, std::vector<std::uint8_t>(300, 0xEF)));
    Append(&s, MakeFrame(0x04, {}));
    samples.push_back(s);
  }

  // Control frames changing the limit, including 3- and 5-byte varints in
  // control payloads.
  {
    std::vector<std::uint8_t> s;
    Append(&s, MakeFrame(0x10, {9, 9, 9}));
    Append(&s, MakeControlFrame(4));  // subsequent payloads must be <= 4
    Append(&s, MakeFrame(0x11, {1, 2, 3, 4}));
    Append(&s, MakeFrame(0x12, {}));
    Append(&s, MakeControlFrame(100000));  // 3-byte varint payload
    Append(&s, MakeFrame(0x13, std::vector<std::uint8_t>(200, 7)));
    Append(&s, MakeControlFrame(0x10000000u));  // 5-byte varint payload
    Append(&s, MakeFrame(0x14, {42}));
    samples.push_back(s);
  }

  // Limit reduced to zero: only empty frames may follow (and the limit can
  // never be raised again, because a non-empty control payload would itself
  // exceed the limit).
  {
    std::vector<std::uint8_t> s;
    Append(&s, MakeControlFrame(0));
    Append(&s, MakeFrame(0x20, {}));
    Append(&s, MakeFrame(0x21, {}));
    samples.push_back(s);
  }

  // Limit reduced to one: control frames with 1-byte payloads still work.
  {
    std::vector<std::uint8_t> s;
    Append(&s, MakeControlFrame(1));
    Append(&s, MakeFrame(0x22, {7}));
    Append(&s, MakeControlFrame(5));
    Append(&s, MakeFrame(0x23, {1, 2, 3, 4, 5}));
    samples.push_back(s);
  }

  return samples;
}

void TestValidSamples() {
  const auto samples = BuildValidSamples();
  for (const auto& sample : samples) {
    CheckAllTwoPointSplits(sample, framed::kDefaultMaxFrameLength);
    CheckByteByByte(sample, framed::kDefaultMaxFrameLength);
  }
  // Also with a small initial limit for the samples that fit under it.
  CheckAllTwoPointSplits(samples[0], 0);
  CheckAllTwoPointSplits(samples[1], 0);
  CheckAllTwoPointSplits(samples[4], 16);
}

void TestLengthErrors() {
  // Non-shortest length: value 0 encoded in two bytes.
  CheckErrorSticksUntilReset({0x01, 0x80, 0x00},
                             DecodeError::kLengthNotShortest);
  // Non-shortest length: value 1 encoded in two bytes.
  CheckErrorSticksUntilReset({0x01, 0x81, 0x00},
                             DecodeError::kLengthNotShortest);
  // Six-byte varint (5th byte still has the continuation bit set).
  CheckErrorSticksUntilReset({0x01, 0x80, 0x80, 0x80, 0x80, 0x80},
                             DecodeError::kLengthTooLong);
  // Five bytes but the value needs more than 32 bits.
  CheckErrorSticksUntilReset({0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x10},
                             DecodeError::kLengthOverflow);
  CheckErrorSticksUntilReset({0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F},
                             DecodeError::kLengthOverflow);
}

void TestFrameTooLarge() {
  // Above the default 1 MiB limit (header alone triggers the error).
  std::vector<std::uint8_t> big = {0x01};
  Append(&big, EncodeVarint(framed::kDefaultMaxFrameLength + 1));
  CheckErrorSticksUntilReset(big, DecodeError::kFrameTooLarge);

  // Above a limit installed by a control frame.
  std::vector<std::uint8_t> s;
  Append(&s, MakeControlFrame(2));
  Append(&s, MakeFrame(0x20, {1, 2, 3}));
  CheckErrorSticksUntilReset(s, DecodeError::kFrameTooLarge);

  // Above a small initial limit passed to the constructor.
  CheckErrorSticksUntilReset(MakeFrame(0x01, {1, 2}),
                             DecodeError::kFrameTooLarge,
                             /*initial_max=*/1);
}

void TestInvalidControlPayload() {
  // Empty payload: no varint at all.
  CheckErrorSticksUntilReset(MakeFrame(framed::kControlFrameType, {}),
                             DecodeError::kInvalidControlPayload);
  // Non-shortest varint payload.
  CheckErrorSticksUntilReset({0x00, 0x02, 0x80, 0x00},
                             DecodeError::kInvalidControlPayload);
  // Trailing garbage after a valid varint.
  CheckErrorSticksUntilReset({0x00, 0x02, 0x01, 0x01},
                             DecodeError::kInvalidControlPayload);
  // Truncated varint payload.
  CheckErrorSticksUntilReset({0x00, 0x01, 0x80},
                             DecodeError::kInvalidControlPayload);
  // Six-byte varint payload.
  CheckErrorSticksUntilReset(
      {0x00, 0x06, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01},
      DecodeError::kInvalidControlPayload);
}

void TestControlTakesEffectForSubsequentFramesOnly() {
  // The control frame itself is checked against the *old* limit; the new
  // limit applies from the next frame on.
  std::vector<std::uint8_t> s;
  Append(&s, MakeControlFrame(1));  // payload is 1 byte, allowed under old max
  Append(&s, MakeFrame(0x30, {7}));
  FrameDecoder dec;
  std::vector<Frame> out;
  CHECK(dec.feed(s, &out));
  CHECK(out.size() == 2);
  CHECK(dec.max_frame_length() == 1);
}

void TestResetClearsPartialFrame() {
  // Half of a frame is consumed, then reset: the leftover bytes must not
  // leak into the next stream.
  FrameDecoder dec;
  std::vector<Frame> out;
  const std::vector<std::uint8_t> partial = {0x01, 0x05, 0xAA};
  CHECK(dec.feed(partial, &out));
  CHECK(out.empty());
  dec.reset();
  const std::vector<std::uint8_t> good = MakeFrame(0x02, {0xBB});
  CHECK(dec.feed(good, &out));
  CHECK(out.size() == 1);
  CHECK(out[0].type == 0x02);
  CHECK(out[0].payload == std::vector<std::uint8_t>{0xBB});
}

}  // namespace

int main() {
  TestValidSamples();
  TestLengthErrors();
  TestFrameTooLarge();
  TestInvalidControlPayload();
  TestControlTakesEffectForSubsequentFramesOnly();
  TestResetClearsPartialFrame();

  if (g_failures == 0) {
    std::printf("all tests passed\n");
    return 0;
  }
  std::fprintf(stderr, "%d check(s) failed\n", g_failures);
  return 1;
}
