#pragma once

#include <cstdint>
#include <stdexcept>
#include <string>

#include "sparsecopy/posix_adapter.hpp"

namespace sparsecopy {

// Identity a resume cursor is bound to: (dev, ino) pins the inode,
// size pins the logical length. Any mismatch invalidates the cursor.
struct SourceIdentity {
  uint64_t dev = 0;
  uint64_t ino = 0;
  int64_t size = 0;

  bool operator==(const SourceIdentity& other) const {
    return dev == other.dev && ino == other.ino && size == other.size;
  }
  bool operator!=(const SourceIdentity& other) const { return !(*this == other); }
};

// Durable resume state. next_offset is always a hole boundary returned by
// SEEK_HOLE, i.e. every extent before it has been fully copied and fsync'd.
struct ResumeCursor {
  SourceIdentity source;
  int64_t next_offset = 0;

  std::string serialize() const;
  // Returns false on malformed input.
  static bool parse(const std::string& text, ResumeCursor& out);
  bool matches(const SourceIdentity& id) const { return source == id; }
};

class CopyError : public std::runtime_error {
public:
  using std::runtime_error::runtime_error;
};

// The source changed (size/mtime/ctime/identity) while being copied, or a
// read hit EOF before the snapshotted length. The destination is not
// trustworthy; the copy must be restarted from a fresh snapshot.
class SourceChangedError : public CopyError {
public:
  using CopyError::CopyError;
};

struct CopyOptions {
  size_t buffer_size = 1 << 20;
};

class SparseCopier {
public:
  explicit SparseCopier(PosixAdapter& os, CopyOptions opts = {});

  // Copies sfd -> dfd preserving logical content and hole layout.
  //
  // The source is pinned to the fstat snapshot taken at entry: if size,
  // mtime, ctime or identity differ when the copy finishes (or a read ends
  // early), SourceChangedError is thrown.
  //
  // If cursor is non-null and matches the source identity, copying resumes
  // at cursor->next_offset; otherwise the destination is truncated to zero
  // (discarding any dirty tail) and the cursor is re-initialized. The
  // cursor is advanced after every completed extent.
  void copy(int sfd, int dfd, ResumeCursor* cursor = nullptr);

private:
  void copy_extent(int sfd, int dfd, int64_t begin, int64_t end);

  PosixAdapter& os_;
  CopyOptions opts_;
};

}  // namespace sparsecopy
