#include <cstdio>
#include <string>
#include <utility>
#include <vector>

#include "frame_decoder.hpp"

namespace {

using framed::DecodeError;
using framed::FrameDecoder;

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                    \
  do {                                                                 \
    ++g_checks;                                                        \
    if (!(cond)) {                                                     \
      ++g_failures;                                                    \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);     \
    }                                                                  \
  } while (0)

struct Frame {
  uint8_t type;
  std::vector<uint8_t> payload;
  bool operator==(const Frame& other) const {
    return type == other.type && payload == other.payload;
  }
};

struct DecodeResult {
  std::vector<Frame> frames;
  DecodeError error;
};

DecodeResult decode_chunks(const std::vector<std::vector<uint8_t>>& chunks) {
  DecodeResult result{ {}, DecodeError::kNone };
  FrameDecoder decoder([&](uint8_t type, const uint8_t* payload, size_t length) {
    result.frames.push_back({ type, { payload, payload + length } });
  });
  for (const auto& chunk : chunks) {
    if (!decoder.feed(chunk.data(), chunk.size())) break;
  }
  result.error = decoder.error();
  return result;
}

DecodeResult decode_whole(const std::vector<uint8_t>& bytes) {
  return decode_chunks({ bytes });
}

std::vector<uint8_t> make_frame(uint8_t type, size_t payload_size, uint8_t fill = 0xAB) {
  std::vector<uint8_t> bytes;
  framed::encode_frame(type, std::vector<uint8_t>(payload_size, fill), bytes);
  return bytes;
}

void append(std::vector<uint8_t>& dst, const std::vector<uint8_t>& src) {
  dst.insert(dst.end(), src.begin(), src.end());
}

// For a valid sample, decode it whole, then enumerate every pair of cut
// points (i, j) splitting the input into three chunks and check that the
// decoded frames and the final state match the whole-input decode.
void check_all_two_cut_splits(const char* name, const std::vector<uint8_t>& sample) {
  const DecodeResult expected = decode_whole(sample);
  CHECK(expected.error == DecodeError::kNone);
  const size_t n = sample.size();
  for (size_t i = 0; i <= n; ++i) {
    for (size_t j = i; j <= n; ++j) {
      DecodeResult got = decode_chunks({
          { sample.begin(), sample.begin() + static_cast<ptrdiff_t>(i) },
          { sample.begin() + static_cast<ptrdiff_t>(i), sample.begin() + static_cast<ptrdiff_t>(j) },
          { sample.begin() + static_cast<ptrdiff_t>(j), sample.end() },
      });
      if (got.error != DecodeError::kNone || !(got.frames == expected.frames)) {
        ++g_checks;
        ++g_failures;
        std::printf("FAIL %s: split (%zu, %zu) of %zu bytes\n", name, i, j, n);
        return;
      }
      g_checks += 1;
    }
  }
  std::printf("ok: %s (%zu bytes, %zu splits)\n", name, n, (n + 1) * (n + 2) / 2);
}

void test_valid_samples() {
  // 1. Single small data frame.
  check_all_two_cut_splits("single frame", make_frame(0x01, 5));

  // 2. Control frame shrinks the limit, then frames at and under the limit.
  {
    std::vector<uint8_t> sample;
    append(sample, framed::make_control_frame(16));
    append(sample, make_frame(0x02, 16));
    append(sample, make_frame(0x03, 0));
    append(sample, make_frame(0x01, 7));
    check_all_two_cut_splits("control + frames", sample);
  }

  // 3. Payload with a multi-byte length (300 bytes -> 2-byte varint).
  check_all_two_cut_splits("two-byte length", make_frame(0x07, 300));

  // 4. Back-to-back empty frames and a control frame with a 5-byte payload.
  {
    std::vector<uint8_t> sample;
    append(sample, framed::make_control_frame(0x10000000));  // 5-byte varint payload
    append(sample, make_frame(0x01, 0));
    append(sample, make_frame(0x01, 0));
    append(sample, make_frame(0x09, 3));
    check_all_two_cut_splits("empty frames + 5-byte control", sample);
  }

  // 5. Control frame raising the limit again after it was lowered
  //    (control frames are exempt from the current limit).
  {
    std::vector<uint8_t> sample;
    append(sample, framed::make_control_frame(1));
    append(sample, make_frame(0x01, 1));
    append(sample, framed::make_control_frame(64));
    append(sample, make_frame(0x01, 64));
    check_all_two_cut_splits("limit lowered then raised", sample);
  }
}

void test_large_payload_chunked() {
  // 70000-byte payload (3-byte length), fed in irregular chunks.
  std::vector<uint8_t> sample;
  append(sample, framed::make_control_frame(100000));
  append(sample, make_frame(0x05, 70000, 0x7E));
  const DecodeResult expected = decode_whole(sample);
  CHECK(expected.error == DecodeError::kNone);
  CHECK(expected.frames.size() == 2);
  CHECK(expected.frames.size() == 2 && expected.frames[1].payload.size() == 70000);

  for (size_t chunk_size : { 1, 3, 4096, 65536 }) {
    std::vector<Frame> frames;
    FrameDecoder decoder([&](uint8_t type, const uint8_t* payload, size_t length) {
      frames.push_back({ type, { payload, payload + length } });
    });
    for (size_t off = 0; off < sample.size(); off += chunk_size) {
      const size_t len = std::min(chunk_size, sample.size() - off);
      CHECK(decoder.feed(sample.data() + off, len));
    }
    CHECK(decoder.ok());
    CHECK(frames == expected.frames);
  }
}

void test_five_byte_length_header() {
  // A 5-byte shortest length (0x10000000) with the limit raised: the header
  // alone must parse without error and simply wait for payload bytes.
  std::vector<uint8_t> bytes;
  append(bytes, framed::make_control_frame(0xFFFFFFFF));
  bytes.push_back(0x01);
  framed::encode_varint(0x10000000, bytes);  // 5 bytes
  DecodeResult result = decode_whole(bytes);
  CHECK(result.error == DecodeError::kNone);
  CHECK(result.frames.size() == 1);  // only the control frame so far
}

void expect_error(const char* name, const std::vector<uint8_t>& bytes, DecodeError want) {
  DecodeResult result = decode_whole(bytes);
  ++g_checks;
  if (result.error != want) {
    ++g_failures;
    std::printf("FAIL %s: got error %s, want %s\n", name,
                framed::error_name(result.error), framed::error_name(want));
  }
}

void test_errors() {
  // Non-minimal length: 0 encoded in two bytes.
  expect_error("non-minimal length", { 0x01, 0x80, 0x00 }, DecodeError::kLengthNotMinimal);
  // Non-minimal length: 300 encoded in three bytes.
  expect_error("non-minimal 3-byte", { 0x01, 0xAC, 0x82, 0x00 },
               DecodeError::kLengthNotMinimal);
  // Six-byte varint: continuation bit still set on the 5th byte.
  expect_error("length too long", { 0x01, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01 },
               DecodeError::kLengthTooLong);
  // 5th byte exceeds 0x0F: value does not fit in 32 bits.
  expect_error("length overflow", { 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F },
               DecodeError::kLengthOverflow);
  // Payload exceeds the default 1 MiB limit.
  {
    std::vector<uint8_t> bytes = { 0x01 };
    framed::encode_varint(framed::kDefaultMaxPayload + 1, bytes);
    expect_error("payload too large (default)", bytes, DecodeError::kPayloadTooLarge);
  }
  // Payload exceeds a limit set by a control frame.
  {
    std::vector<uint8_t> bytes;
    append(bytes, framed::make_control_frame(4));
    append(bytes, make_frame(0x01, 5));
    expect_error("payload too large (control)", bytes, DecodeError::kPayloadTooLarge);
  }
  // Malformed control payloads.
  expect_error("control empty", { 0x00, 0x00 }, DecodeError::kBadControlPayload);
  expect_error("control zero value", { 0x00, 0x01, 0x00 }, DecodeError::kBadControlPayload);
  expect_error("control non-minimal", { 0x00, 0x02, 0x80, 0x00 },
               DecodeError::kBadControlPayload);
  expect_error("control unterminated", { 0x00, 0x01, 0x80 },
               DecodeError::kBadControlPayload);
  expect_error("control too long", { 0x00, 0x06, 1, 2, 3, 4, 5, 6 },
               DecodeError::kBadControlPayload);
}

void test_error_is_sticky_until_reset() {
  std::vector<Frame> frames;
  FrameDecoder decoder([&](uint8_t type, const uint8_t* payload, size_t length) {
    frames.push_back({ type, { payload, payload + length } });
  });

  const std::vector<uint8_t> bad = { 0x01, 0x80, 0x00 };  // non-minimal length
  CHECK(!decoder.feed(bad.data(), bad.size()));
  CHECK(decoder.error() == DecodeError::kLengthNotMinimal);

  // Further feeds keep failing while the error is latched.
  const std::vector<uint8_t> good = make_frame(0x02, 3);
  CHECK(!decoder.feed(good.data(), good.size()));
  CHECK(decoder.error() == DecodeError::kLengthNotMinimal);
  CHECK(frames.empty());

  // Explicit reset recovers; the default limit is restored.
  decoder.reset();
  CHECK(decoder.ok());
  CHECK(decoder.max_payload() == framed::kDefaultMaxPayload);
  CHECK(decoder.feed(good.data(), good.size()));
  CHECK(frames.size() == 1);
  CHECK(frames[0].type == 0x02);
  CHECK(frames[0].payload.size() == 3);
}

void test_encode_varint_shortest() {
  struct { uint32_t value; std::vector<uint8_t> want; } cases[] = {
    { 0, { 0x00 } },
    { 127, { 0x7F } },
    { 128, { 0x80, 0x01 } },
    { 300, { 0xAC, 0x02 } },
    { 0x0FFFFFFF, { 0xFF, 0xFF, 0xFF, 0x7F } },
    { 0xFFFFFFFF, { 0xFF, 0xFF, 0xFF, 0xFF, 0x0F } },
  };
  for (const auto& c : cases) {
    std::vector<uint8_t> got;
    framed::encode_varint(c.value, got);
    CHECK(got == c.want);
  }
}

}  // namespace

int main() {
  test_encode_varint_shortest();
  test_valid_samples();
  test_large_payload_chunked();
  test_five_byte_length_header();
  test_errors();
  test_error_is_sticky_until_reset();

  std::printf("%d checks, %d failures\n", g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}
