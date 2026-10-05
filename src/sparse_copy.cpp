#include "sparse_copy.h"

#include <cstdio>
#include <cstring>
#include <fstream>
#include <vector>

namespace spc {

namespace {

SourceIdentity identity_of(const FileStat& st) {
  return SourceIdentity{st.dev, st.ino, st.size, st.mtime_ns, st.ctime_ns};
}

struct Cursor {
  uint64_t dev = 0;
  uint64_t ino = 0;
  uint64_t size = 0;
  uint64_t offset = 0;
};

bool cursor_exists(const std::string& path) {
  std::ifstream is(path);
  return is.good();
}

Cursor load_cursor(const std::string& path) {
  std::ifstream is(path);
  std::string magic, version;
  Cursor c;
  if (!(is >> magic >> version >> c.dev >> c.ino >> c.size >> c.offset) ||
      magic != "spcopy-cursor" || version != "v1") {
    throw CursorMismatchError("corrupt recovery cursor: " + path);
  }
  return c;
}

void save_cursor(const std::string& path, const SourceIdentity& id,
                 uint64_t offset) {
  std::ofstream os(path, std::ios::trunc);
  if (!os) throw IoError("cannot write recovery cursor: " + path);
  os << "spcopy-cursor v1 " << id.dev << ' ' << id.ino << ' ' << id.size << ' '
     << offset << '\n';
  os.flush();
  if (!os) throw IoError("cannot write recovery cursor: " + path);
}

bool all_zero(const char* buf, size_t n) {
  for (size_t i = 0; i < n; ++i)
    if (buf[i] != 0) return false;
  return true;
}

}  // namespace

class SparseCopierImpl {
public:
  SparseCopierImpl(PosixAdapter& ad, const CopyOptions& opt)
      : ad_(ad), opt_(opt), buf_(opt.chunk_size) {}

  CopyStats run(const std::string& src, const std::string& dst) {
    const std::string cursor_path = opt_.cursor_path.empty()
                                        ? dst + ".spcopy"
                                        : opt_.cursor_path;

    int sfd = ad_.open_read(src);
    if (sfd < 0) throw IoError("open source " + src, ad_.last_errno());

    FileStat st0;
    if (!ad_.fstat(sfd, st0)) {
      int e = ad_.last_errno();
      ad_.close(sfd);
      throw IoError("fstat source", e);
    }
    const SourceIdentity id = identity_of(st0);

    uint64_t offset = 0;
    if (cursor_exists(cursor_path)) {
      Cursor c = load_cursor(cursor_path);
      if (c.dev != id.dev || c.ino != id.ino || c.size != id.size) {
        ad_.close(sfd);
        throw CursorMismatchError(
            "recovery cursor binds to a different source (identity/length "
            "mismatch); refusing to resume");
      }
      offset = c.offset;
      stats_.resumed = offset > 0;
    }

    int dfd = ad_.open_write(dst);
    if (dfd < 0) {
      int e = ad_.last_errno();
      ad_.close(sfd);
      throw IoError("open destination " + dst, e);
    }

    try {
      if (offset == 0) {
        // Fresh copy: drop any stale destination content (including a dirty
        // tail) so skipped holes read back as zeros.
        if (!ad_.ftruncate(dfd, 0)) throw IoError("truncate dst", ad_errno());
      }
      copy_extents(sfd, dfd, id, offset, cursor_path);
      // Fix the final length; also removes any dirty tail beyond the source
      // length when resuming.
      if (!ad_.ftruncate(dfd, id.size)) throw IoError("size dst", ad_errno());
      if (!ad_.fsync(dfd)) throw IoError("fsync dst", ad_errno());
    } catch (...) {
      ad_.close(sfd);
      ad_.close(dfd);
      throw;
    }

    // Snapshot check: the source must not have moved during the copy.
    FileStat st1;
    if (!ad_.fstat(sfd, st1)) {
      int e = ad_.last_errno();
      ad_.close(sfd);
      ad_.close(dfd);
      throw IoError("re-fstat source", e);
    }
    ad_.close(sfd);
    ad_.close(dfd);
    if (identity_of(st1) != id) {
      throw SourceChangedError(
          "source mutated during copy; destination is not a snapshot");
    }

    std::remove(cursor_path.c_str());
    stats_.logical_bytes = id.size;
    stats_.hole_bytes = id.size - stats_.data_bytes;
    return stats_;
  }

private:
  int ad_errno() const { return ad_.last_errno(); }

  // Reads exactly n bytes at offset. A short EOF means the source shrank
  // under us -> snapshot violation.
  void read_full(int fd, char* out, size_t n, uint64_t offset) {
    size_t got = 0;
    while (got < n) {
      ssize_t r = ad_.pread(fd, out + got, n - got, offset + got);
      if (r < 0) throw IoError("read source", ad_errno());
      if (r == 0)
        throw SourceChangedError("source truncated during copy");
      got += static_cast<size_t>(r);
    }
  }

  void write_full(int fd, const char* data, size_t n, uint64_t offset) {
    size_t put = 0;
    while (put < n) {
      ssize_t w = ad_.pwrite(fd, data + put, n - put, offset + put);
      if (w < 0) throw IoError("write destination", ad_errno());
      if (w == 0) throw IoError("write destination returned 0");
      put += static_cast<size_t>(w);
    }
  }

  void copy_range(int sfd, int dfd, uint64_t begin, uint64_t end,
                  const SourceIdentity& id, const std::string& cursor_path) {
    uint64_t pos = begin;
    while (pos < end) {
      size_t want = static_cast<size_t>(
          std::min<uint64_t>(buf_.size(), end - pos));
      // Persist the resume point before mutating the destination.
      save_cursor(cursor_path, id, pos);
      read_full(sfd, buf_.data(), want, pos);
      write_full(dfd, buf_.data(), want, pos);
      stats_.data_bytes += want;
      pos += want;
    }
  }

  // Fallback extent scan for backends without SEEK_DATA/SEEK_HOLE: a block
  // of zeros is treated as a hole. Logical content is preserved exactly;
  // hole layout is preserved for holes aligned to the scan block size.
  bool scan_next_extent(int sfd, uint64_t size, uint64_t& pos,
                        uint64_t& data, uint64_t& hole) {
    uint64_t p = pos;
    while (p < size) {
      size_t n = static_cast<size_t>(std::min<uint64_t>(buf_.size(), size - p));
      read_full(sfd, buf_.data(), n, p);
      if (!all_zero(buf_.data(), n)) break;
      p += n;
    }
    if (p >= size) {
      pos = p;
      return false;
    }
    data = p;
    while (p < size) {
      size_t n = static_cast<size_t>(std::min<uint64_t>(buf_.size(), size - p));
      read_full(sfd, buf_.data(), n, p);
      if (all_zero(buf_.data(), n)) break;
      p += n;
    }
    hole = p;
    pos = p;
    return true;
  }

  void copy_extents(int sfd, int dfd, const SourceIdentity& id,
                    uint64_t offset, const std::string& cursor_path) {
    bool supported = true;
    uint64_t pos = offset;
    while (pos < id.size) {
      uint64_t data = 0, hole = 0;
      if (supported) {
        int64_t d = ad_.seek_data(sfd, pos, supported);
        if (!supported) continue;  // rescan from pos in fallback mode
        if (d < 0) break;          // no data left; rest is a trailing hole
        int64_t h = ad_.seek_hole(sfd, static_cast<uint64_t>(d), supported);
        if (!supported) continue;
        data = static_cast<uint64_t>(d);
        hole = (h < 0 || static_cast<uint64_t>(h) > id.size)
                   ? id.size
                   : static_cast<uint64_t>(h);
        pos = hole;
      } else {
        if (!scan_next_extent(sfd, id.size, pos, data, hole)) break;
      }
      ++stats_.extents;
      copy_range(sfd, dfd, data, hole, id, cursor_path);
    }
  }

  PosixAdapter& ad_;
  CopyOptions opt_;
  std::vector<char> buf_;
  CopyStats stats_;
};

CopyStats SparseCopier::copy(const std::string& src, const std::string& dst,
                             const CopyOptions& opt) {
  if (opt.chunk_size == 0) throw CopyError("chunk_size must be > 0");
  SparseCopierImpl impl(ad_, opt);
  return impl.run(src, dst);
}

namespace {

struct Extent {
  uint64_t data;
  uint64_t hole;
};

// Collects the data/hole extent map. Returns false (unsupported) when the
// adapter cannot scan; caller then falls back to whole-file byte compare.
bool collect_extents(PosixAdapter& ad, int fd, uint64_t size,
                     std::vector<Extent>& out, bool& supported) {
  uint64_t pos = 0;
  while (pos < size) {
    int64_t d = ad.seek_data(fd, pos, supported);
    if (!supported) return false;
    if (d < 0) return true;
    int64_t h = ad.seek_hole(fd, static_cast<uint64_t>(d), supported);
    if (!supported) return false;
    uint64_t hole = (h < 0 || static_cast<uint64_t>(h) > size)
                        ? size
                        : static_cast<uint64_t>(h);
    out.push_back(Extent{static_cast<uint64_t>(d), hole});
    pos = hole;
  }
  return true;
}

bool compare_range(PosixAdapter& ad, int fa, int fb, uint64_t begin,
                   uint64_t end, std::vector<char>& bufa,
                   std::vector<char>& bufb) {
  uint64_t pos = begin;
  while (pos < end) {
    size_t want = static_cast<size_t>(
        std::min<uint64_t>(bufa.size(), end - pos));
    size_t ga = 0, gb = 0;
    while (ga < want) {
      ssize_t r = ad.pread(fa, bufa.data() + ga, want - ga, pos + ga);
      if (r <= 0) return false;
      ga += static_cast<size_t>(r);
    }
    while (gb < want) {
      ssize_t r = ad.pread(fb, bufb.data() + gb, want - gb, pos + gb);
      if (r <= 0) return false;
      gb += static_cast<size_t>(r);
    }
    if (std::memcmp(bufa.data(), bufb.data(), want) != 0) return false;
    pos += want;
  }
  return true;
}

}  // namespace

bool verify_copy(PosixAdapter& ad, const std::string& src,
                 const std::string& dst, std::string* why) {
  auto fail = [&](const std::string& msg) {
    if (why) *why = msg;
    return false;
  };

  int fa = ad.open_read(src);
  if (fa < 0) return fail("cannot open source");
  int fb = ad.open_read(dst);
  if (fb < 0) {
    ad.close(fa);
    return fail("cannot open destination");
  }

  FileStat sa, sb;
  if (!ad.fstat(fa, sa) || !ad.fstat(fb, sb)) {
    ad.close(fa);
    ad.close(fb);
    return fail("cannot stat files");
  }
  if (sa.size != sb.size) {
    ad.close(fa);
    ad.close(fb);
    return fail("length mismatch: src=" + std::to_string(sa.size) +
                " dst=" + std::to_string(sb.size));
  }

  const uint64_t size = sa.size;
  std::vector<char> bufa(1 << 16), bufb(1 << 16);
  bool ok = true;

  bool sup_a = true, sup_b = true;
  std::vector<Extent> ea, eb;
  bool have_a = collect_extents(ad, fa, size, ea, sup_a);
  bool have_b = collect_extents(ad, fb, size, eb, sup_b);

  if (have_a && have_b) {
    // Strict check: identical hole layout plus identical data bytes.
    if (ea.size() != eb.size()) {
      ok = fail("extent count differs");
    } else {
      for (size_t i = 0; i < ea.size() && ok; ++i) {
        if (ea[i].data != eb[i].data || ea[i].hole != eb[i].hole) {
          ok = fail("hole layout differs at extent " + std::to_string(i));
          break;
        }
        if (!compare_range(ad, fa, fb, ea[i].data, ea[i].hole, bufa, bufb))
          ok = fail("data mismatch in extent " + std::to_string(i));
      }
    }
  } else {
    // No extent scanning: compare the full logical byte stream.
    if (!compare_range(ad, fa, fb, 0, size, bufa, bufb))
      ok = fail("logical content differs");
  }

  ad.close(fa);
  ad.close(fb);
  return ok;
}

}  // namespace spc
