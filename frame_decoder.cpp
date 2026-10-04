#include "frame_decoder.hpp"

#include <algorithm>

namespace framed {

const char* ToString(DecodeError error) {
  switch (error) {
    case DecodeError::kNone:
      return "none";
    case DecodeError::kLengthTooLong:
      return "length varint exceeds 5 bytes";
    case DecodeError::kLengthNotShortest:
      return "length varint is not the shortest encoding";
    case DecodeError::kLengthOverflow:
      return "length value does not fit in 32 bits";
    case DecodeError::kFrameTooLarge:
      return "payload length exceeds the current maximum frame length";
    case DecodeError::kInvalidControlPayload:
      return "control payload is not a single shortest-form varint";
  }
  return "unknown";
}

bool operator==(const Frame& a, const Frame& b) {
  return a.type == b.type && a.payload == b.payload;
}

bool operator!=(const Frame& a, const Frame& b) { return !(a == b); }

namespace {

// Parses exactly one shortest-form varint from the front of `data`.
// On success stores the value and the number of consumed bytes.
bool ParseShortestVarint(const std::uint8_t* data, std::size_t size,
                         std::uint32_t* value, std::size_t* used) {
  std::uint32_t result = 0;
  for (std::size_t i = 0; i < kMaxLengthBytes; ++i) {
    if (i >= size) {
      return false;  // truncated
    }
    const std::uint8_t byte = data[i];
    if (i == kMaxLengthBytes - 1) {
      if (byte & 0x80u) {
        return false;  // would need a 6th byte
      }
      if (byte > 0x0Fu) {
        return false;  // does not fit in 32 bits
      }
    }
    const bool last = (byte & 0x80u) == 0;
    if (last && byte == 0 && i > 0) {
      return false;  // trailing zero group: not the shortest encoding
    }
    result |= static_cast<std::uint32_t>(byte & 0x7Fu) << (7 * i);
    if (last) {
      *value = result;
      *used = i + 1;
      return true;
    }
  }
  return false;
}

}  // namespace

FrameDecoder::FrameDecoder(std::uint32_t initial_max_frame_length)
    : initial_max_frame_length_(initial_max_frame_length) {
  reset();
}

void FrameDecoder::reset() {
  max_frame_length_ = initial_max_frame_length_;
  error_ = DecodeError::kNone;
  state_ = State::kType;
  type_ = 0;
  length_value_ = 0;
  length_bytes_ = 0;
  payload_.clear();
}

bool FrameDecoder::Fail(DecodeError error) {
  error_ = error;
  return false;
}

bool FrameDecoder::FinishFrame(std::vector<Frame>* out) {
  if (type_ == kControlFrameType && !ApplyControlFrame(payload_)) {
    return false;
  }
  out->push_back(Frame{type_, std::move(payload_)});
  payload_.clear();
  state_ = State::kType;
  return true;
}

bool FrameDecoder::ApplyControlFrame(const std::vector<std::uint8_t>& payload) {
  std::uint32_t value = 0;
  std::size_t used = 0;
  if (!ParseShortestVarint(payload.data(), payload.size(), &value, &used) ||
      used != payload.size()) {
    return Fail(DecodeError::kInvalidControlPayload);
  }
  max_frame_length_ = value;
  return true;
}

bool FrameDecoder::feed(const std::uint8_t* data, std::size_t size,
                        std::vector<Frame>* out) {
  if (!ok()) {
    return false;  // latched error: explicit reset() is required
  }
  std::size_t pos = 0;
  while (pos < size) {
    switch (state_) {
      case State::kType:
        type_ = data[pos++];
        length_value_ = 0;
        length_bytes_ = 0;
        state_ = State::kLength;
        break;

      case State::kLength: {
        const std::uint8_t byte = data[pos++];
        if (length_bytes_ == kMaxLengthBytes - 1) {
          if (byte & 0x80u) {
            return Fail(DecodeError::kLengthTooLong);
          }
          if (byte > 0x0Fu) {
            return Fail(DecodeError::kLengthOverflow);
          }
        }
        const bool last = (byte & 0x80u) == 0;
        if (last && byte == 0 && length_bytes_ > 0) {
          return Fail(DecodeError::kLengthNotShortest);
        }
        length_value_ |=
            static_cast<std::uint32_t>(byte & 0x7Fu) << (7 * length_bytes_);
        ++length_bytes_;
        if (last) {
          if (length_value_ > max_frame_length_) {
            return Fail(DecodeError::kFrameTooLarge);
          }
          payload_.clear();
          payload_.reserve(length_value_);
          if (length_value_ == 0) {
            // Emit immediately so that completion never depends on where
            // chunk boundaries fall.
            if (!FinishFrame(out)) {
              return false;
            }
          } else {
            state_ = State::kPayload;
          }
        }
        break;
      }

      case State::kPayload: {
        const std::size_t remaining = length_value_ - payload_.size();
        const std::size_t take = std::min(remaining, size - pos);
        payload_.insert(payload_.end(), data + pos, data + pos + take);
        pos += take;
        if (payload_.size() == length_value_ && !FinishFrame(out)) {
          return false;
        }
        break;
      }
    }
  }
  return true;
}

}  // namespace framed
