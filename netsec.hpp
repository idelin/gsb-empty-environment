#pragma once

#include <cstdint>
#include <optional>
#include <set>
#include <string>
#include <unordered_map>
#include <utility>

namespace netsec {

// Capability rights bits (Capsicum-style). A descriptor's right set may only
// ever shrink over its lifetime.
using Rights = std::uint64_t;

inline constexpr Rights CAP_READ   = 1ull << 0;
inline constexpr Rights CAP_WRITE  = 1ull << 1;
inline constexpr Rights CAP_SEEK   = 1ull << 2;
inline constexpr Rights CAP_LOOKUP = 1ull << 3; // required on the base dirfd of openat
inline constexpr Rights CAP_FCNTL  = 1ull << 4; // sub-right gating fcntl
inline constexpr Rights CAP_IOCTL  = 1ull << 5; // sub-right gating ioctl
inline constexpr Rights CAP_FSTAT  = 1ull << 6;

inline constexpr Rights CAP_ALL = CAP_READ | CAP_WRITE | CAP_SEEK | CAP_LOOKUP |
                                  CAP_FCNTL | CAP_IOCTL | CAP_FSTAT;

enum class CapErr {
  NoSuchFd,       // fd not present in the table
  NotCapable,     // missing right, or an attempt to widen rights/allow-lists
  IsAbsolute,     // openat path is absolute
  EscapesBase,    // openat path escapes the base dirfd via ".."
  BadPath,        // empty or malformed path
  CmdNotAllowed,  // fcntl/ioctl command outside the descriptor's allow-list
};

const char* to_string(CapErr err);

// Minimal expected-like result type (C++17 has no std::expected).
template <typename T>
class Result {
public:
  static Result ok(T value) { return Result(std::move(value)); }
  static Result fail(CapErr err) { return Result(err); }

  bool has_value() const { return ok_; }
  explicit operator bool() const { return ok_; }
  const T& value() const { return value_; }
  T& value() { return value_; }
  const T& operator*() const { return value_; }
  T& operator*() { return value_; }
  CapErr error() const { return err_; }

private:
  explicit Result(T value) : ok_(true), value_(std::move(value)), err_(CapErr::NoSuchFd) {}
  explicit Result(CapErr err) : ok_(false), value_(), err_(err) {}

  bool ok_;
  T value_;
  CapErr err_;
};

struct FdEntry {
  Rights rights = 0;
  // std::nullopt = unrestricted; a set = only these commands are allowed.
  // May only transition unrestricted -> restricted, or to a subset.
  std::optional<std::set<unsigned long>> ioctl_allow;
  std::optional<std::set<int>> fcntl_allow;
  std::string path;  // normalized path relative to the sandbox root (model only)
  bool is_dir = false;
};

// In-memory descriptor table modelling a sandboxed process.
class FdTable {
public:
  // Install a descriptor (e.g. a pre-opened sandbox root directory).
  int install(Rights rights, std::string path, bool is_dir);

  // Narrow a descriptor's rights. Any bit set in `next` but not currently
  // held is a widening attempt and fails with NotCapable.
  Result<bool> cap_rights_limit(int fd, Rights next);

  // Narrow the ioctl/fcntl command allow-lists. Widening fails.
  Result<bool> cap_ioctls_limit(int fd, std::set<unsigned long> cmds);
  Result<bool> cap_fcntls_limit(int fd, std::set<int> cmds);

  // Open `path` relative to `dirfd`.
  //  - dirfd must hold CAP_LOOKUP;
  //  - `path` must be relative and must not escape dirfd via "..";
  //  - the new descriptor's rights are clamped to requested & dirfd rights,
  //    so a child can never exceed its base directory's rights.
  Result<int> openat(int dirfd, const std::string& path, Rights requested,
                     bool is_dir = false);

  // Gated by the CAP_FCNTL sub-right and the fcntl allow-list.
  Result<bool> fcntl(int fd, int cmd);
  // Gated by the CAP_IOCTL sub-right and the ioctl allow-list.
  Result<bool> ioctl(int fd, unsigned long cmd);

  const FdEntry* get(int fd) const;

private:
  std::unordered_map<int, FdEntry> table_;
  int next_fd_ = 3;  // 0..2 reserved for stdio
};

// Resolve `path` against `base_path`. Pure function; exposed for testing.
// Rejects absolute paths and any ".." that would climb above `base_path`.
Result<std::string> resolve_relative(const std::string& base_path,
                                     const std::string& path);

}  // namespace netsec
