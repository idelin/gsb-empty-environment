#pragma once

#include <expected>
#include <string_view>

namespace swr {

enum class Error {
  kBackpressure,     // all resident-window slots are pinned by live views
  kMapFailed,        // backend failed to establish a mapping
  kCancelled,        // operation cancelled via CancelToken
  kOutOfRange,       // requested range lies outside the file
  kInvalidArgument,  // e.g. zero window size
};

inline std::string_view to_string(Error e) {
  switch (e) {
    case Error::kBackpressure: return "backpressure";
    case Error::kMapFailed: return "map failed";
    case Error::kCancelled: return "cancelled";
    case Error::kOutOfRange: return "out of range";
    case Error::kInvalidArgument: return "invalid argument";
  }
  return "unknown";
}

template <class T>
using Result = std::expected<T, Error>;

}  // namespace swr
