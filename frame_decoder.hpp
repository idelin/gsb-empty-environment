#pragma once

// Incremental frame decoder.
//
// Wire format of a frame:
//
//   +-----------+-------------------+------------------+
//   | type (1B) | length (varint)   | payload (length) |
//   +-----------+-------------------+------------------+
//
// * `type` is a single byte. Type kControlFrameType is reserved for control
//   frames; all other types are opaque data frames.
// * `length` is an unsigned LEB128 varint encoding the payload length in
//   bytes. It must use at most 5 bytes, must be the shortest possible
//   encoding (no trailing zero group), and the decoded value must fit in
//   32 bits (the 5th byte may carry at most 4 value bits).
// * A frame is rejected when its payload length exceeds the current maximum
//   frame length.
//
// Control frames carry a new maximum frame length as their payload, encoded
// as a single shortest-form varint that fills the payload exactly. The new
// limit applies to all subsequent frames.
//
// The decoder is incremental: bytes may be fed in arbitrarily sized chunks.
// Once an error occurs the decoder stays in the error state and rejects all
// further input until reset() is called explicitly.

#include <cstddef>
#include <cstdint>
#include <vector>

namespace framed {

inline constexpr std::uint8_t kControlFrameType = 0x00;
inline constexpr std::size_t kMaxLengthBytes = 5;
inline constexpr std::uint32_t kDefaultMaxFrameLength = 1u << 20;

enum class DecodeError {
  kNone = 0,
  kLengthTooLong,        // varint uses more than 5 bytes
  kLengthNotShortest,    // varint is not the shortest encoding
  kLengthOverflow,       // value does not fit in 32 bits
  kFrameTooLarge,        // payload length exceeds the current maximum
  kInvalidControlPayload // control payload is not one shortest-form varint
};

const char* ToString(DecodeError error);

struct Frame {
  std::uint8_t type = 0;
  std::vector<std::uint8_t> payload;
};

bool operator==(const Frame& a, const Frame& b);
bool operator!=(const Frame& a, const Frame& b);

class FrameDecoder {
 public:
  explicit FrameDecoder(
      std::uint32_t initial_max_frame_length = kDefaultMaxFrameLength);

  // Feeds `size` bytes. Frames completed by these bytes are appended to
  // `out`. Returns true while the decoder is healthy. On the first malformed
  // byte the decoder records the error, stops consuming input, and returns
  // false; every subsequent call returns false until reset() is called.
  bool feed(const std::uint8_t* data, std::size_t size, std::vector<Frame>* out);
  bool feed(const std::vector<std::uint8_t>& data, std::vector<Frame>* out) {
    return feed(data.data(), data.size(), out);
  }

  // Clears the error state and all partially consumed input, and restores
  // the initial maximum frame length.
  void reset();

  bool ok() const { return error_ == DecodeError::kNone; }
  DecodeError error() const { return error_; }
  std::uint32_t max_frame_length() const { return max_frame_length_; }

 private:
  enum class State { kType, kLength, kPayload };

  bool Fail(DecodeError error);
  bool FinishFrame(std::vector<Frame>* out);
  bool ApplyControlFrame(const std::vector<std::uint8_t>& payload);

  std::uint32_t initial_max_frame_length_;
  std::uint32_t max_frame_length_ = 0;
  DecodeError error_ = DecodeError::kNone;
  State state_ = State::kType;
  std::uint8_t type_ = 0;
  std::uint32_t length_value_ = 0;
  std::size_t length_bytes_ = 0;
  std::vector<std::uint8_t> payload_;
};

}  // namespace framed
