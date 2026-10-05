#ifndef SPC_SPARSE_COPY_H
#define SPC_SPARSE_COPY_H

#include <cstdint>
#include <stdexcept>
#include <string>

#include "posix_adapter.h"

namespace spc {

// Identity of the source snapshot the copy is bound to. Captured before the
// copy and re-checked afterwards; any drift fails the operation.
struct SourceIdentity {
  uint64_t dev = 0;
  uint64_t ino = 0;
  uint64_t size = 0;
  uint64_t mtime_ns = 0;
  uint64_t ctime_ns = 0;
  bool operator==(const SourceIdentity& o) const {
    return dev == o.dev && ino == o.ino && size == o.size &&
           mtime_ns == o.mtime_ns && ctime_ns == o.ctime_ns;
  }
  bool operator!=(const SourceIdentity& o) const { return !(*this == o); }
};

class CopyError : public std::runtime_error {
public:
  using std::runtime_error::runtime_error;
};

// Source changed while (or before) it was being copied.
class SourceChangedError : public CopyError {
public:
  using CopyError::CopyError;
};

// Recovery cursor does not match the current source identity/length.
class CursorMismatchError : public CopyError {
public:
  using CopyError::CopyError;
};

class IoError : public CopyError {
public:
  IoError(const std::string& what, int err)
      : CopyError(what + ": errno=" + std::to_string(err)), err_(err) {}
  explicit IoError(const std::string& what) : CopyError(what), err_(0) {}
  int err() const { return err_; }

private:
  int err_;
};

struct CopyOptions {
  size_t chunk_size = 1 << 20;  // bytes per read/write cycle
  // Recovery cursor location; defaults to "<dst>.spcopy".
  std::string cursor_path;
};

struct CopyStats {
  uint64_t logical_bytes = 0;  // source length
  uint64_t data_bytes = 0;     // bytes actually read+written
  uint64_t hole_bytes = 0;     // bytes skipped as holes
  uint64_t extents = 0;        // data extents copied
  bool resumed = false;        // started from a recovery cursor
};

class SparseCopier {
public:
  explicit SparseCopier(PosixAdapter& adapter) : ad_(adapter) {}

  // Copies src to dst preserving logical content and hole layout.
  // Throws SourceChangedError if the source mutates mid-copy,
  // CursorMismatchError if a stale cursor names a different source,
  // IoError on I/O failure (cursor is kept so a later call resumes).
  CopyStats copy(const std::string& src, const std::string& dst,
                 const CopyOptions& opt = CopyOptions());

private:
  PosixAdapter& ad_;
};

// Verifies that dst is a faithful sparse copy of src: equal length, equal
// logical bytes, and (when the adapter supports extent scanning) identical
// data/hole extent maps. Compares logical content, never physical block
// occupancy. Returns false and sets *why on mismatch.
bool verify_copy(PosixAdapter& adapter, const std::string& src,
                 const std::string& dst, std::string* why = nullptr);

}  // namespace spc

#endif
