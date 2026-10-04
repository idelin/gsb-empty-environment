#include "frame_decoder.hpp"

namespace framed {

const char* error_name(DecodeError error) {
  switch (error) {
    case DecodeError::kNone: return "none";
    case DecodeError::kLengthTooLong: return "length-too-long";
    case DecodeError::kLengthNotMinimal: return "length-not-minimal";
    case DecodeError::kLengthOverflow: return "length-overflow";
    case DecodeError::kPayloadTooLarge: return "payload-too-large";
    case DecodeError::kBadControlPayload: return "bad-control-payload";
  }
  return "unknown";
}

void encode_varint(uint32_t value, std::vector<uint8_t>& out) {
  do {
    uint8_t byte = value & 0x7F;
    value >>= 7;
    if (value != 0) byte |= 0x80;
    out.push_back(byte);
  } while (value != 0);
}

void encode_frame(uint8_t type, const std::vector<uint8_t>& payload,
                  std::vector<uint8_t>& out) {
  out.push_back(type);
  encode_varint(static_cast<uint32_t>(payload.size()), out);
  out.insert(out.end(), payload.begin(), payload.end());
}

std::vector<uint8_t> make_control_frame(uint32_t new_max_payload) {
  std::vector<uint8_t> payload;
  encode_varint(new_max_payload, payload);
  std::vector<uint8_t> frame;
  encode_frame(kControlType, payload, frame);
  return frame;
}

namespace {

// Parses a control payload: a shortest-form varint, exactly n bytes,
// encoding a value >= 1.
bool parse_control_payload(const uint8_t* data, size_t size, uint32_t& out) {
  if (size < 1 || size > kMaxLengthBytes) return false;
  uint32_t value = 0;
  for (size_t i = 0; i < size; ++i) {
    uint8_t byte = data[i];
    // All but the last byte must carry the continuation bit; the last
    // byte must not, so the varint terminates exactly at the payload end.
    if (i + 1 < size ? !(byte & 0x80) : (byte & 0x80)) return false;
    if (i == kMaxLengthBytes - 1 && byte > 0x0F) return false;
    value |= static_cast<uint32_t>(byte & 0x7F) << (7 * i);
  }
  if (size > 1 && data[size - 1] == 0) return false;  // shortest form
  if (value == 0) return false;
  out = value;
  return true;
}

}  // namespace

FrameDecoder::FrameDecoder(FrameHandler on_frame) : on_frame_(std::move(on_frame)) {}

bool FrameDecoder::feed(const uint8_t* data, size_t size) {
  if (error_ != DecodeError::kNone) return false;
  if (size == 0) return true;
  buffer_.insert(buffer_.end(), data, data + size);

  size_t pos = 0;
  while (pos < buffer_.size()) {
    const size_t avail = buffer_.size() - pos;
    const uint8_t type = buffer_[pos];
    if (avail < 2) break;  // waiting for the first length byte

    uint32_t length = 0;
    size_t length_bytes = 0;
    bool length_done = false;
    for (size_t i = 0; i < kMaxLengthBytes; ++i) {
      if (1 + i >= avail) break;  // incomplete varint, wait for more bytes
      const uint8_t byte = buffer_[pos + 1 + i];
      length_bytes = i + 1;
      if (i == kMaxLengthBytes - 1) {
        if (byte & 0x80) { error_ = DecodeError::kLengthTooLong; return false; }
        if (byte > 0x0F) { error_ = DecodeError::kLengthOverflow; return false; }
      }
      length |= static_cast<uint32_t>(byte & 0x7F) << (7 * i);
      if (!(byte & 0x80)) { length_done = true; break; }
    }
    if (!length_done) break;  // incomplete varint, wait for more bytes

    if (length_bytes > 1 && buffer_[pos + length_bytes] == 0) {
      error_ = DecodeError::kLengthNotMinimal;
      return false;
    }

    const size_t header = 1 + length_bytes;
    if (type == kControlType) {
      if (length < 1 || length > kMaxLengthBytes) {
        error_ = DecodeError::kBadControlPayload;
        return false;
      }
    } else if (length > max_payload_) {
      error_ = DecodeError::kPayloadTooLarge;
      return false;
    }

    if (avail < header + length) break;  // waiting for the payload

    const uint8_t* payload = buffer_.data() + pos + header;
    if (type == kControlType) {
      uint32_t new_max = 0;
      if (!parse_control_payload(payload, length, new_max)) {
        error_ = DecodeError::kBadControlPayload;
        return false;
      }
      max_payload_ = new_max;
    }
    if (on_frame_) on_frame_(type, payload, length);
    pos += header + length;
  }

  buffer_.erase(buffer_.begin(), buffer_.begin() + static_cast<ptrdiff_t>(pos));
  return true;
}

void FrameDecoder::reset() {
  buffer_.clear();
  max_payload_ = kDefaultMaxPayload;
  error_ = DecodeError::kNone;
}

}  // namespace framed
