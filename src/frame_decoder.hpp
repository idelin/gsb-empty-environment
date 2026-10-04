#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <vector>

namespace framed {

// Frame layout on the wire:
//   [1 byte type][varint length][length bytes payload]
//
// The length is an unsigned LEB128 varint:
//   - at most kMaxLengthBytes (5) bytes, i.e. a 32-bit value;
//   - must be the shortest encoding (a multi-byte encoding may not
//     end in a 0x00 byte, and the 5th byte may not exceed 0x0F).
//
// A frame whose type is kControlType carries a new maximum payload
// length (itself a shortest varint, value >= 1) that applies to all
// subsequent data frames. Control frames are exempt from the maximum
// payload check so the limit can always be raised again.

constexpr uint8_t kControlType = 0x00;
constexpr uint32_t kDefaultMaxPayload = 1u << 20;  // 1 MiB
constexpr size_t kMaxLengthBytes = 5;

enum class DecodeError {
  kNone = 0,
  kLengthTooLong,      // varint uses more than 5 bytes
  kLengthNotMinimal,   // varint is not the shortest encoding
  kLengthOverflow,     // 5th varint byte exceeds 0x0F (> 32 bits)
  kPayloadTooLarge,    // payload length exceeds current maximum
  kBadControlPayload,  // malformed control frame payload
};

const char* error_name(DecodeError error);

// Appends the shortest LEB128 encoding of value to out.
void encode_varint(uint32_t value, std::vector<uint8_t>& out);

// Appends a complete frame to out.
void encode_frame(uint8_t type, const std::vector<uint8_t>& payload,
                  std::vector<uint8_t>& out);

// Returns a control frame that sets the maximum payload to new_max_payload.
std::vector<uint8_t> make_control_frame(uint32_t new_max_payload);

// Incremental frame decoder.
//
// Bytes are supplied through feed() in arbitrarily sized chunks; complete
// frames are delivered to the FrameHandler callback as soon as they are
// available. Once a protocol error occurs the decoder latches into an
// error state: every subsequent feed() fails until reset() is called
// explicitly, which also restores the default maximum payload length.
class FrameDecoder {
 public:
  using FrameHandler =
      std::function<void(uint8_t type, const uint8_t* payload, size_t length)>;

  explicit FrameDecoder(FrameHandler on_frame);

  // Consumes bytes. Returns false if the decoder is in (or entered) the
  // error state. Feeding zero bytes is always a no-op.
  bool feed(const uint8_t* data, size_t size);
  bool feed(const std::vector<uint8_t>& data) { return feed(data.data(), data.size()); }

  // Clears the error state, drops buffered partial data and restores the
  // default maximum payload length.
  void reset();

  bool ok() const { return error_ == DecodeError::kNone; }
  DecodeError error() const { return error_; }
  uint32_t max_payload() const { return max_payload_; }

 private:
  FrameHandler on_frame_;
  std::vector<uint8_t> buffer_;
  uint32_t max_payload_ = kDefaultMaxPayload;
  DecodeError error_ = DecodeError::kNone;
};

}  // namespace framed
