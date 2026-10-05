#include "ringlog.h"

#include <cerrno>
#include <cstring>
#include <stdexcept>

#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

namespace ringlog {
namespace {

constexpr uint32_t kMetaMagic = 0x524C4F47; // "RLOG"
constexpr uint32_t kDataMagic = 0x52454344; // "RECD"
constexpr uint32_t kPadMagic = 0x50414420;  // "PAD "
constexpr uint32_t kVersion = 1;
constexpr uint64_t kMinCapacity = 64;
constexpr size_t kMetaSize = 64;

// crc32 (zlib polynomial), chainable like zlib's crc32().
struct CrcTable {
  uint32_t t[256];
  CrcTable() {
    for (uint32_t i = 0; i < 256; ++i) {
      uint32_t c = i;
      for (int k = 0; k < 8; ++k)
        c = (c & 1) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
      t[i] = c;
    }
  }
};

uint32_t crc32(uint32_t crc, const void* data, size_t len) {
  static const CrcTable tab;
  const uint8_t* p = static_cast<const uint8_t*>(data);
  crc = ~crc;
  for (size_t i = 0; i < len; ++i)
    crc = tab.t[(crc ^ p[i]) & 0xFF] ^ (crc >> 8);
  return ~crc;
}

uint64_t align8(uint64_t v) { return (v + 7) & ~uint64_t(7); }

bool pwriteAll(int fd, uint64_t off, const void* buf, size_t len) {
  const uint8_t* p = static_cast<const uint8_t*>(buf);
  while (len > 0) {
    ssize_t n = ::pwrite(fd, p, len, static_cast<off_t>(off));
    if (n < 0) {
      if (errno == EINTR) continue;
      return false;
    }
    p += n;
    off += n;
    len -= static_cast<size_t>(n);
  }
  return true;
}

bool preadAll(int fd, uint64_t off, void* buf, size_t len) {
  uint8_t* p = static_cast<uint8_t*>(buf);
  while (len > 0) {
    ssize_t n = ::pread(fd, p, len, static_cast<off_t>(off));
    if (n < 0) {
      if (errno == EINTR) continue;
      return false;
    }
    if (n == 0) return false; // unexpected EOF
    p += n;
    off += n;
    len -= static_cast<size_t>(n);
  }
  return true;
}

void put32(uint8_t* p, uint32_t v) { std::memcpy(p, &v, 4); }
void put64(uint8_t* p, uint64_t v) { std::memcpy(p, &v, 8); }
uint32_t get32(const uint8_t* p) {
  uint32_t v;
  std::memcpy(&v, p, 4);
  return v;
}
uint64_t get64(const uint8_t* p) {
  uint64_t v;
  std::memcpy(&v, p, 8);
  return v;
}

// Metadata block, 64 bytes:
//   [0]  u32 magic
//   [4]  u32 version
//   [8]  u64 generation
//   [16] u64 capacity
//   [24] u64 head_seq
//   [32] u64 tail_seq
//   [40] u64 head_off
//   [48] u64 write_off
//   [56] u32 crc32 of bytes [0,56)
//   [60] u32 zero
struct MetaView {
  uint64_t generation, capacity, head_seq, tail_seq, head_off, write_off;
};

void encodeMeta(uint8_t out[kMetaSize], const MetaView& m) {
  std::memset(out, 0, kMetaSize);
  put32(out + 0, kMetaMagic);
  put32(out + 4, kVersion);
  put64(out + 8, m.generation);
  put64(out + 16, m.capacity);
  put64(out + 24, m.head_seq);
  put64(out + 32, m.tail_seq);
  put64(out + 40, m.head_off);
  put64(out + 48, m.write_off);
  put32(out + 56, crc32(0, out, 56));
}

bool decodeMeta(const uint8_t in[kMetaSize], MetaView* m) {
  if (get32(in + 0) != kMetaMagic || get32(in + 4) != kVersion) return false;
  if (crc32(0, in, 56) != get32(in + 56)) return false;
  m->generation = get64(in + 8);
  m->capacity = get64(in + 16);
  m->head_seq = get64(in + 24);
  m->tail_seq = get64(in + 32);
  m->head_off = get64(in + 40);
  m->write_off = get64(in + 48);
  if (m->capacity < kMinCapacity || (m->capacity & 7) != 0) return false;
  if (m->head_seq > m->tail_seq) return false;
  if (m->head_off >= m->capacity || (m->head_off & 7) != 0) return false;
  if (m->write_off >= m->capacity || (m->write_off & 7) != 0) return false;
  return true;
}

} // namespace

RingLog::RingLog(const std::string& path, Options opt)
    : path_(path), sync_(opt.sync) {
  fd_ = ::open(path.c_str(), O_RDWR | O_CREAT, 0644);
  if (fd_ < 0)
    throw std::runtime_error("ringlog: cannot open " + path + ": " +
                             std::strerror(errno));
  struct stat st;
  if (::fstat(fd_, &st) != 0)
    throw std::runtime_error("ringlog: fstat failed");
  if (st.st_size == 0) {
    if (opt.capacity == 0)
      throw std::runtime_error("ringlog: capacity required for new log");
    initNew(opt.capacity);
  } else {
    recover();
    if (opt.capacity != 0 && opt.capacity != capacity_)
      throw std::runtime_error("ringlog: capacity mismatch");
  }
}

RingLog::~RingLog() {
  if (fd_ >= 0) ::close(fd_);
}

void RingLog::initNew(uint64_t capacity) {
  if (capacity < kMinCapacity || (capacity & 7) != 0)
    throw std::runtime_error("ringlog: capacity must be a multiple of 8 >= 64");
  capacity_ = capacity;
  if (::ftruncate(fd_, static_cast<off_t>(kDataOffset + capacity_)) != 0)
    throw std::runtime_error("ringlog: ftruncate failed");
  generation_ = 0;
  head_seq_ = tail_seq_ = 0;
  head_off_ = write_off_ = 0;
  // Write both slots so recovery always has a valid copy.
  MetaView m{generation_, capacity_, head_seq_, tail_seq_, head_off_,
             write_off_};
  uint8_t buf[kMetaSize];
  encodeMeta(buf, m);
  if (!pwriteAll(fd_, kMetaSlot0, buf, kMetaSize) ||
      !pwriteAll(fd_, kMetaSlot1, buf, kMetaSize))
    throw std::runtime_error("ringlog: metadata write failed");
  if (sync_) ::fsync(fd_);
}

void RingLog::recover() {
  MetaView best{};
  bool have = false;
  for (uint64_t slot : {kMetaSlot0, kMetaSlot1}) {
    uint8_t buf[kMetaSize];
    MetaView m;
    if (preadAll(fd_, slot, buf, kMetaSize) && decodeMeta(buf, &m)) {
      if (!have || m.generation > best.generation) {
        best = m;
        have = true;
      }
    }
  }
  if (!have)
    throw std::runtime_error("ringlog: both metadata copies corrupt");

  capacity_ = best.capacity;
  generation_ = best.generation;
  head_seq_ = best.head_seq;
  tail_seq_ = best.tail_seq;
  head_off_ = best.head_off;

  // Scan the committed region from the head. A torn tail (crash between
  // the record write and the metadata commit) fails magic/length/seq/crc
  // validation and is discarded; only the uncommitted tail is affected.
  uint64_t off = head_off_;
  uint64_t seq = head_seq_;
  while (seq < tail_seq_) {
    uint64_t before = off;
    if (!skipOne(&off, &seq)) break; // torn tail: truncate here
    (void)before;
  }
  tail_seq_ = seq;
  write_off_ = off;
  if (head_seq_ == tail_seq_) head_off_ = write_off_;
}

bool RingLog::writeMeta() {
  ++generation_;
  MetaView m{generation_, capacity_, head_seq_, tail_seq_, head_off_,
             write_off_};
  uint8_t buf[kMetaSize];
  encodeMeta(buf, m);
  // Alternate slots: a torn metadata write leaves the other slot intact.
  uint64_t slot = (generation_ % 2 == 0) ? kMetaSlot0 : kMetaSlot1;
  if (!pwriteAll(fd_, slot, buf, kMetaSize)) return false;
  if (sync_) ::fsync(fd_);
  return true;
}

bool RingLog::writePadding(uint64_t off, uint64_t len) {
  uint8_t hdr[8];
  put32(hdr + 0, kPadMagic);
  put32(hdr + 4, static_cast<uint32_t>(len));
  return pwriteAll(fd_, kDataOffset + off, hdr, sizeof(hdr));
}

bool RingLog::skipOne(uint64_t* off, uint64_t* seq) const {
  uint8_t hdr[RingLog::kRecordHeaderSize];
  if (!preadAll(fd_, kDataOffset + *off, hdr, 8)) return false;
  uint32_t magic = get32(hdr + 0);
  if (magic == kPadMagic) {
    uint64_t len = get32(hdr + 4);
    if (len < 8 || (len & 7) != 0 || *off + len > capacity_) return false;
    *off += len;
    if (*off == capacity_) *off = 0;
    return true;
  }
  if (magic != kDataMagic) return false;
  if (!preadAll(fd_, kDataOffset + *off + 8, hdr + 8,
                RingLog::kRecordHeaderSize - 8))
    return false;
  uint64_t payload_len = get32(hdr + 4);
  uint64_t total = RingLog::kRecordHeaderSize + align8(payload_len);
  if (*off + total > capacity_) return false;
  if (get64(hdr + 8) != *seq) return false; // stale entry from an older lap
  std::string payload(static_cast<size_t>(payload_len), '\0');
  if (payload_len > 0 &&
      !preadAll(fd_, kDataOffset + *off + RingLog::kRecordHeaderSize,
                payload.data(), payload_len))
    return false;
  uint32_t crc = crc32(0, hdr, 16);
  crc = crc32(crc, payload.data(), payload.size());
  if (crc != get32(hdr + 16)) return false;
  *off += total;
  if (*off == capacity_) *off = 0;
  ++*seq;
  return true;
}

void RingLog::dropOldest() {
  uint64_t off = head_off_;
  uint64_t seq = head_seq_;
  // The head record is committed, so this must succeed.
  if (!skipOne(&off, &seq))
    throw std::runtime_error("ringlog: corrupt head record");
  head_off_ = off;
  head_seq_ = seq;
  if (head_seq_ == tail_seq_) head_off_ = write_off_; // now empty
}

Status RingLog::append(const void* data, size_t len, uint64_t* seq_out) {
  uint64_t need = kRecordHeaderSize + align8(len);
  if (need > capacity_) return Status::TooLarge;

  uint64_t old_head = head_seq_;
  bool wrapped = false;
  uint64_t pad_off = 0, pad_len = 0;

  // Phase 1: advance the head (in memory) until there is a contiguous
  // free slot, deciding whether to wrap. Overwritten records are only
  // dropped here; nothing is written yet.
  for (;;) {
    bool wrapping = write_off_ + need > capacity_;
    if (head_seq_ == tail_seq_) { // empty: head_off_ tracks write_off_
      if (wrapping) {
        pad_off = write_off_;
        pad_len = capacity_ - write_off_;
        write_off_ = 0;
        wrapped = true;
      }
      head_off_ = write_off_;
      break;
    }
    if (!wrapping) {
      // Live data is [head_off_, write_off_) when head_off_ <= write_off_,
      // else [head_off_, capacity) + [0, write_off_).
      if (head_off_ == write_off_ || // log completely full
          (head_off_ > write_off_ && head_off_ < write_off_ + need)) {
        dropOldest();
        continue;
      }
      break;
    }
    // Wrapping: the padding would occupy [write_off_, capacity).
    if (head_off_ >= write_off_) { // padding would clobber live data
      dropOldest();
      continue;
    }
    pad_off = write_off_;
    pad_len = capacity_ - write_off_;
    write_off_ = 0;
    wrapped = true;
  }

  // Phase 2: commit the head advancement BEFORE clobbering anything, so a
  // crash cannot leave metadata referencing overwritten records.
  if (head_seq_ != old_head && !writeMeta()) return Status::IOError;

  // Phase 3: write padding (if wrapping) and the record itself.
  if (wrapped && !writePadding(pad_off, pad_len)) return Status::IOError;

  uint64_t seq = tail_seq_;
  std::string buf(static_cast<size_t>(need), '\0');
  uint8_t* hdr = reinterpret_cast<uint8_t*>(buf.data());
  put32(hdr + 0, kDataMagic);
  put32(hdr + 4, static_cast<uint32_t>(len));
  put64(hdr + 8, seq);
  if (len > 0) std::memcpy(buf.data() + kRecordHeaderSize, data, len);
  uint32_t crc = crc32(0, hdr, 16);
  crc = crc32(crc, buf.data() + kRecordHeaderSize, len);
  put32(hdr + 16, crc);
  if (!pwriteAll(fd_, kDataOffset + write_off_, buf.data(),
                 static_cast<size_t>(need)))
    return Status::IOError;

  write_off_ += need;
  if (write_off_ == capacity_) write_off_ = 0;
  tail_seq_ = seq + 1;

  // Phase 4: make the record durable, then commit the new tail.
  if (sync_) ::fsync(fd_);
  if (!writeMeta()) return Status::IOError;
  if (seq_out) *seq_out = seq;
  return Status::Ok;
}

Status RingLog::read(Cursor& cur, Record& rec) const {
  if (cur.next_seq < head_seq_) return Status::Overwritten;
  if (cur.next_seq >= tail_seq_) return Status::End;

  // Locate by sequence number, scanning from the head. Physical offsets
  // are derived from live metadata, never from the cursor, so a new lap
  // reusing an offset cannot be mistaken for the old record.
  uint64_t off = head_off_;
  uint64_t seq = head_seq_;
  for (;;) {
    uint8_t peek[8];
    if (!preadAll(fd_, kDataOffset + off, peek, 8)) return Status::Corrupt;
    uint32_t magic = get32(peek + 0);
    if (magic == kPadMagic) { // padding only ever leads up to the wrap point
      uint64_t len = get32(peek + 4);
      if (len < 8 || (len & 7) != 0 || off + len > capacity_)
        return Status::Corrupt;
      off += len;
      if (off == capacity_) off = 0;
      continue;
    }
    if (magic != kDataMagic) return Status::Corrupt;
    if (seq == cur.next_seq) break;
    if (!skipOne(&off, &seq)) return Status::Corrupt;
  }

  uint8_t hdr[kRecordHeaderSize];
  if (!preadAll(fd_, kDataOffset + off, hdr, kRecordHeaderSize))
    return Status::Corrupt;
  if (get32(hdr + 0) != kDataMagic) return Status::Corrupt;
  uint64_t payload_len = get32(hdr + 4);
  if (off + kRecordHeaderSize + align8(payload_len) > capacity_)
    return Status::Corrupt;
  if (get64(hdr + 8) != cur.next_seq) return Status::Corrupt;
  std::string payload(static_cast<size_t>(payload_len), '\0');
  if (payload_len > 0 &&
      !preadAll(fd_, kDataOffset + off + kRecordHeaderSize, payload.data(),
                payload_len))
    return Status::Corrupt;
  uint32_t crc = crc32(0, hdr, 16);
  crc = crc32(crc, payload.data(), payload.size());
  if (crc != get32(hdr + 16)) return Status::Corrupt;

  rec.seq = cur.next_seq;
  rec.payload = std::move(payload);
  ++cur.next_seq;
  return Status::Ok;
}

} // namespace ringlog
