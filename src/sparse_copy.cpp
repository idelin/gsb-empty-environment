#include "sparsecopy/sparse_copy.hpp"

#include <algorithm>
#include <cerrno>
#include <cstdio>
#include <cstring>
#include <vector>

namespace sparsecopy {

std::string ResumeCursor::serialize() const {
  char buf[128];
  std::snprintf(buf, sizeof(buf), "sparsecopy/1 %llx %llx %lld %lld",
                static_cast<unsigned long long>(source.dev),
                static_cast<unsigned long long>(source.ino),
                static_cast<long long>(source.size),
                static_cast<long long>(next_offset));
  return buf;
}

bool ResumeCursor::parse(const std::string& text, ResumeCursor& out) {
  char tag[32];
  unsigned long long dev = 0, ino = 0;
  long long size = 0, off = 0;
  if (std::sscanf(text.c_str(), "%31s %llx %llx %lld %lld", tag, &dev, &ino,
                  &size, &off) != 5) {
    return false;
  }
  if (std::strcmp(tag, "sparsecopy/1") != 0 || size < 0 || off < 0 || off > size) {
    return false;
  }
  out.source.dev = dev;
  out.source.ino = ino;
  out.source.size = size;
  out.next_offset = off;
  return true;
}

SparseCopier::SparseCopier(PosixAdapter& os, CopyOptions opts)
    : os_(os), opts_(opts) {
  if (opts_.buffer_size == 0) opts_.buffer_size = 1 << 20;
}

static SourceIdentity identity_of(const FileStat& st) {
  return SourceIdentity{st.dev, st.ino, st.size};
}

void SparseCopier::copy(int sfd, int dfd, ResumeCursor* cursor) {
  FileStat before;
  if (os_.fstat(sfd, before) != 0) {
    throw CopyError(std::string("fstat(source) failed: ") + std::strerror(errno));
  }
  const SourceIdentity id = identity_of(before);

  int64_t pos = 0;
  if (cursor && cursor->matches(id)) {
    pos = cursor->next_offset;
  } else {
    // Fresh copy: discard any pre-existing destination content (including a
    // dirty tail beyond the source length) so holes read back as zeros.
    if (os_.ftruncate(dfd, 0) != 0) {
      throw CopyError(std::string("ftruncate(dest, 0) failed: ") + std::strerror(errno));
    }
    if (cursor) {
      cursor->source = id;
      cursor->next_offset = 0;
    }
  }

  while (pos < before.size) {
    errno = 0;
    const int64_t data = os_.seek_data(sfd, pos);
    if (data < 0) {
      if (errno == ENXIO) break;  // no more data extents before EOF
      throw CopyError(std::string("SEEK_DATA failed: ") + std::strerror(errno));
    }
    if (data >= before.size) break;

    errno = 0;
    int64_t hole = os_.seek_hole(sfd, data);
    if (hole < 0 || hole > before.size) hole = before.size;

    copy_extent(sfd, dfd, data, hole);

    pos = hole;
    if (cursor) {
      cursor->source = id;
      cursor->next_offset = pos;
    }
  }

  // Drops a dirty tail when the destination was longer than the source and
  // materializes the trailing hole of the source at exactly the right size.
  if (os_.ftruncate(dfd, before.size) != 0) {
    throw CopyError(std::string("ftruncate(dest) failed: ") + std::strerror(errno));
  }
  if (os_.fsync(dfd) != 0) {
    throw CopyError(std::string("fsync(dest) failed: ") + std::strerror(errno));
  }

  FileStat after;
  if (os_.fstat(sfd, after) != 0) {
    throw CopyError(std::string("fstat(source) failed: ") + std::strerror(errno));
  }
  if (identity_of(after) != id || after.mtime_ns != before.mtime_ns ||
      after.ctime_ns != before.ctime_ns) {
    throw SourceChangedError("source changed during copy");
  }
}

void SparseCopier::copy_extent(int sfd, int dfd, int64_t begin, int64_t end) {
  std::vector<char> buf(opts_.buffer_size);
  int64_t off = begin;
  while (off < end) {
    const size_t want =
        std::min(static_cast<int64_t>(buf.size()), end - off);
    const ssize_t got = os_.pread(sfd, buf.data(), want, off);
    if (got < 0) {
      if (errno == EINTR) continue;
      throw CopyError(std::string("pread failed: ") + std::strerror(errno));
    }
    if (got == 0) {
      // EOF before the snapshotted length: the source was truncated
      // underneath us.
      throw SourceChangedError("source truncated during copy");
    }
    size_t written = 0;
    while (written < static_cast<size_t>(got)) {
      const ssize_t put =
          os_.pwrite(dfd, buf.data() + written,
                     static_cast<size_t>(got) - written, off + written);
      if (put < 0) {
        if (errno == EINTR) continue;
        throw CopyError(std::string("pwrite failed: ") + std::strerror(errno));
      }
      if (put == 0) {
        throw CopyError("pwrite made no progress");
      }
      written += static_cast<size_t>(put);
    }
    off += got;
  }
}

}  // namespace sparsecopy
