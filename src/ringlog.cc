#include "ringlog/ringlog.h"

#include <fcntl.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include <array>
#include <vector>

namespace ringlog {

namespace {

constexpr uint64_t kMetaMagic = 0x31474f4c474e4952ULL;  // "RINGLOG1"
constexpr uint32_t kRecordMagic = 0x31434552;           // "REC1"
constexpr uint32_t kPaddingMagic = 0x31444150;          // "PAD1"
constexpr uint32_t kVersion = 1;
constexpr off_t kMetaPageSize = 4096;
constexpr off_t kMetaBOffset = kMetaPageSize;
constexpr off_t kDataBase = 2 * kMetaPageSize;
constexpr uint64_t kHeaderSize = 24;  // magic(4) len(4) seq(8) crc(4) reserved(4)
constexpr uint64_t kMinCapacity = 4 * kHeaderSize;

uint32_t Crc32(const void* data, size_t len, uint32_t crc = 0) {
  static const std::array<uint32_t, 256> table = [] {
    std::array<uint32_t, 256> t{};
    for (uint32_t i = 0; i < 256; ++i) {
      uint32_t c = i;
      for (int k = 0; k < 8; ++k) c = (c & 1) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
      t[i] = c;
    }
    return t;
  }();
  crc = ~crc;
  const auto* p = static_cast<const unsigned char*>(data);
  for (size_t i = 0; i < len; ++i) crc = table[(crc ^ p[i]) & 0xFF] ^ (crc >> 8);
  return ~crc;
}

bool PwriteAll(int fd, const void* buf, size_t len, off_t off) {
  const auto* p = static_cast<const char*>(buf);
  while (len > 0) {
    ssize_t n = ::pwrite(fd, p, len, off);
    if (n <= 0) return false;
    p += n;
    off += n;
    len -= static_cast<size_t>(n);
  }
  return true;
}

bool PreadAll(int fd, void* buf, size_t len, off_t off) {
  auto* p = static_cast<char*>(buf);
  while (len > 0) {
    ssize_t n = ::pread(fd, p, len, off);
    if (n <= 0) return false;
    p += n;
    off += n;
    len -= static_cast<size_t>(n);
  }
  return true;
}

bool FlushData(int fd) {
#if defined(__APPLE__)
  return ::fsync(fd) == 0;
#else
  return ::fdatasync(fd) == 0;
#endif
}

template <typename T>
void Put(std::string* out, T v) {
  out->append(reinterpret_cast<const char*>(&v), sizeof(v));
}

template <typename T>
bool Get(const char** p, const char* end, T* v) {
  if (static_cast<size_t>(end - *p) < sizeof(T)) return false;
  memcpy(v, *p, sizeof(T));
  *p += sizeof(T);
  return true;
}

struct Meta {
  uint64_t capacity = 0;
  uint64_t used = 0;
  uint64_t head_off = 0;
  uint64_t head_seq = 0;
  uint64_t tail_off = 0;
  uint64_t tail_seq = 0;
  uint64_t generation = 0;
};

std::string EncodeMeta(const Meta& m) {
  std::string body;
  Put(&body, kMetaMagic);
  Put(&body, kVersion);
  Put(&body, m.capacity);
  Put(&body, m.used);
  Put(&body, m.head_off);
  Put(&body, m.head_seq);
  Put(&body, m.tail_off);
  Put(&body, m.tail_seq);
  Put(&body, m.generation);
  std::string page = body;
  Put(&page, Crc32(body.data(), body.size()));
  return page;
}

bool DecodeMeta(const std::string& page, uint64_t file_capacity, Meta* out) {
  const char* p = page.data();
  const char* end = p + page.size();
  uint64_t magic;
  uint32_t version, crc;
  Meta m;
  if (!Get(&p, end, &magic) || magic != kMetaMagic) return false;
  if (!Get(&p, end, &version) || version != kVersion) return false;
  if (!Get(&p, end, &m.capacity) || !Get(&p, end, &m.used) ||
      !Get(&p, end, &m.head_off) || !Get(&p, end, &m.head_seq) ||
      !Get(&p, end, &m.tail_off) || !Get(&p, end, &m.tail_seq) ||
      !Get(&p, end, &m.generation) || !Get(&p, end, &crc)) {
    return false;
  }
  if (Crc32(page.data(), p - page.data() - sizeof(uint32_t)) != crc) return false;
  if (m.capacity != file_capacity) return false;
  if (m.used > m.capacity || m.head_off >= m.capacity || m.tail_off >= m.capacity) return false;
  if (m.head_seq > m.tail_seq) return false;
  *out = m;
  return true;
}

}  // namespace

const char* StatusName(Status s) {
  switch (s) {
    case Status::kOk: return "Ok";
    case Status::kEnd: return "End";
    case Status::kOverwritten: return "Overwritten";
    case Status::kInvalidArgument: return "InvalidArgument";
    case Status::kIOError: return "IOError";
    case Status::kCorruption: return "Corruption";
  }
  return "?";
}

RingLog::~RingLog() {
  if (fd_ >= 0) ::close(fd_);
}

std::unique_ptr<RingLog> RingLog::Create(const std::string& path, uint64_t capacity) {
  if (capacity < kMinCapacity) return nullptr;
  int fd = ::open(path.c_str(), O_RDWR | O_CREAT | O_TRUNC, 0644);
  if (fd < 0) return nullptr;
  if (::ftruncate(fd, kDataBase + static_cast<off_t>(capacity)) != 0) {
    ::close(fd);
    return nullptr;
  }
  auto log = std::unique_ptr<RingLog>(new RingLog());
  log->fd_ = fd;
  log->capacity_ = capacity;
  if (log->PersistMeta() != Status::kOk) return nullptr;
  return log;
}

std::unique_ptr<RingLog> RingLog::Open(const std::string& path) {
  int fd = ::open(path.c_str(), O_RDWR);
  if (fd < 0) return nullptr;
  struct stat st;
  if (::fstat(fd, &st) != 0 || st.st_size < kDataBase) {
    ::close(fd);
    return nullptr;
  }
  uint64_t file_capacity = static_cast<uint64_t>(st.st_size - kDataBase);
  if (file_capacity < kMinCapacity) {
    ::close(fd);
    return nullptr;
  }

  Meta meta_a, meta_b;
  bool ok_a = false, ok_b = false;
  std::vector<char> page(EncodeMeta(Meta{}).size());
  if (PreadAll(fd, page.data(), page.size(), 0)) {
    ok_a = DecodeMeta(std::string(page.data(), page.size()), file_capacity, &meta_a);
  }
  if (PreadAll(fd, page.data(), page.size(), kMetaBOffset)) {
    ok_b = DecodeMeta(std::string(page.data(), page.size()), file_capacity, &meta_b);
  }
  if (!ok_a && !ok_b) {
    ::close(fd);
    return nullptr;
  }
  const Meta& m = (ok_a && (!ok_b || meta_a.generation >= meta_b.generation)) ? meta_a : meta_b;

  auto log = std::unique_ptr<RingLog>(new RingLog());
  log->fd_ = fd;
  log->capacity_ = m.capacity;
  log->used_ = m.used;
  log->head_off_ = m.head_off;
  log->head_seq_ = m.head_seq;
  log->tail_off_ = m.tail_off;
  log->tail_seq_ = m.tail_seq;
  log->generation_ = m.generation;

  // Sanity-check that the oldest live span parses and matches head_seq_.
  if (log->used_ > 0) {
    Parsed p;
    if (log->ReadAt(log->head_off_, &p) != Status::kOk) return nullptr;
    if (p.is_data && p.seq != log->head_seq_) return nullptr;
  }
  // Heal a missing/invalid copy so both are valid going forward.
  if (!ok_a || !ok_b) log->PersistMeta();
  return log;
}

Status RingLog::ReadAt(uint64_t off, Parsed* out) const {
  // Implicit gap: fewer than kHeaderSize bytes left before the physical end.
  if (capacity_ - off < kHeaderSize) {
    out->is_data = false;
    out->span = capacity_ - off;
    out->seq = 0;
    out->payload.clear();
    return Status::kOk;
  }
  std::array<char, kHeaderSize> hdr{};
  if (!PreadAll(fd_, hdr.data(), hdr.size(), kDataBase + static_cast<off_t>(off))) {
    return Status::kIOError;
  }
  const char* p = hdr.data();
  const char* end = p + hdr.size();
  uint32_t magic, len, crc, reserved;
  uint64_t seq;
  Get(&p, end, &magic);
  Get(&p, end, &len);
  Get(&p, end, &seq);
  Get(&p, end, &crc);
  Get(&p, end, &reserved);

  uint32_t expect = Crc32(hdr.data() + 4, 12);  // crc over len+seq
  uint64_t span;
  if (magic == kPaddingMagic) {
    span = len;  // total padding span including header
    if (span < kHeaderSize || off + span > capacity_) return Status::kCorruption;
    out->is_data = false;
    out->seq = 0;
    out->payload.clear();
  } else if (magic == kRecordMagic) {
    span = kHeaderSize + len;
    if (off + span > capacity_) return Status::kCorruption;
    out->payload.resize(len);
    if (len > 0 && !PreadAll(fd_, out->payload.data(), len,
                             kDataBase + static_cast<off_t>(off) + kHeaderSize)) {
      return Status::kIOError;
    }
    expect = Crc32(out->payload.data(), out->payload.size(), expect);
    out->is_data = true;
    out->seq = seq;
  } else {
    return Status::kCorruption;
  }
  if (crc != expect) return Status::kCorruption;
  out->span = span;
  return Status::kOk;
}

Status RingLog::EnsureFree(uint64_t n) {
  while (capacity_ - used_ < n) {
    Parsed p;
    Status s = ReadAt(head_off_, &p);
    if (s != Status::kOk) return s;
    head_off_ = (head_off_ + p.span) % capacity_;
    used_ -= p.span;
    if (p.is_data) ++head_seq_;
  }
  return Status::kOk;
}

Status RingLog::WritePadding(uint64_t off, uint64_t span) {
  std::string hdr;
  Put(&hdr, kPaddingMagic);
  Put(&hdr, static_cast<uint32_t>(span));
  Put(&hdr, static_cast<uint64_t>(0));
  Put(&hdr, Crc32(hdr.data() + 4, 12));
  Put(&hdr, static_cast<uint32_t>(0));
  if (!PwriteAll(fd_, hdr.data(), hdr.size(), kDataBase + static_cast<off_t>(off))) {
    return Status::kIOError;
  }
  return Status::kOk;
}

Status RingLog::WriteRecord(uint64_t off, uint64_t seq, const std::string& data) {
  std::string hdr;
  Put(&hdr, kRecordMagic);
  Put(&hdr, static_cast<uint32_t>(data.size()));
  Put(&hdr, seq);
  uint32_t crc = Crc32(hdr.data() + 4, 12);
  crc = Crc32(data.data(), data.size(), crc);
  Put(&hdr, crc);
  Put(&hdr, static_cast<uint32_t>(0));
  bool ok = PwriteAll(fd_, hdr.data(), hdr.size(), kDataBase + static_cast<off_t>(off));
  if (ok && !data.empty()) {
    ok = PwriteAll(fd_, data.data(), data.size(),
                   kDataBase + static_cast<off_t>(off) + kHeaderSize);
  }
  return ok ? Status::kOk : Status::kIOError;
}

Status RingLog::PersistMeta() {
  ++generation_;
  Meta m{capacity_, used_, head_off_, head_seq_, tail_off_, tail_seq_, generation_};
  std::string page = EncodeMeta(m);
  // Two copies, each flushed independently: a crash can tear at most one.
  if (!PwriteAll(fd_, page.data(), page.size(), 0)) return Status::kIOError;
  if (!FlushData(fd_)) return Status::kIOError;
  if (!PwriteAll(fd_, page.data(), page.size(), kMetaBOffset)) return Status::kIOError;
  if (!FlushData(fd_)) return Status::kIOError;
  return Status::kOk;
}

Status RingLog::Append(const std::string& data, uint64_t* seq_out) {
  uint64_t span = kHeaderSize + data.size();
  if (span > capacity_) return Status::kInvalidArgument;

  uint64_t remaining = capacity_ - tail_off_;
  if (remaining < kHeaderSize || remaining < span) {
    // Record does not fit before the physical end: pad to the end and wrap.
    uint64_t pad = capacity_ - tail_off_;
    Status s = EnsureFree(pad);  // advance head bookkeeping before overwriting
    if (s != Status::kOk) return s;
    if (pad >= kHeaderSize) {
      s = WritePadding(tail_off_, pad);
      if (s != Status::kOk) return s;
    }
    used_ += pad;
    tail_off_ = 0;
  }

  Status s = EnsureFree(span);
  if (s != Status::kOk) return s;
  s = WriteRecord(tail_off_, tail_seq_, data);
  if (s != Status::kOk) return s;
  // Data is durable before metadata commits it: a torn write here only
  // leaves garbage in the uncommitted tail, which no reader can reach.
  if (!FlushData(fd_)) return Status::kIOError;

  tail_off_ += span;
  if (tail_off_ == capacity_) tail_off_ = 0;  // keep tail_off in [0, capacity)
  used_ += span;
  uint64_t seq = tail_seq_++;
  s = PersistMeta();
  if (s != Status::kOk) return s;
  if (seq_out) *seq_out = seq;
  return Status::kOk;
}

void RingLog::Cursor::Resync() {
  seq_ = log_->head_seq_;
  off_ = log_->head_off_;
}

Status RingLog::Cursor::Next(Record* out) {
  if (seq_ == log_->tail_seq_) return Status::kEnd;
  if (seq_ < log_->head_seq_) {  // definitely overwritten
    Resync();
    return Status::kOverwritten;
  }
  uint64_t off = off_;
  // Bounded walk: padding/gaps only appear right before the physical end.
  uint64_t hops_left = log_->capacity_ / kHeaderSize + 2;
  for (;;) {
    Parsed p;
    Status s = log_->ReadAt(off, &p);
    if (s != Status::kOk) {  // stale offset: torn or overwritten bytes
      Resync();
      return Status::kOverwritten;
    }
    if (!p.is_data) {
      off = (off + p.span) % log_->capacity_;
      if (hops_left-- == 0) return Status::kCorruption;
      continue;
    }
    // Sequence numbers are globally increasing, so a seq mismatch means our
    // record was overwritten by a newer lap at this same offset.
    if (p.seq != seq_) {
      Resync();
      return Status::kOverwritten;
    }
    out->seq = p.seq;
    out->data = std::move(p.payload);
    ++seq_;
    off_ = (off + p.span) % log_->capacity_;
    return Status::kOk;
  }
}

}  // namespace ringlog
