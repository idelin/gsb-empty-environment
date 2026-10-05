#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

namespace ringlog {

enum class Status {
  Ok,
  End,         // cursor caught up with the tail; nothing newer yet
  Overwritten, // the record the cursor points at has been overwritten
  Corrupt,     // committed data failed validation
  TooLarge,    // record can never fit in the log capacity
  IOError
};

struct Record {
  uint64_t seq = 0;
  std::string payload;
};

// A read cursor. Only a sequence number: physical positions are never
// exposed, so a new lap reusing an old offset can never be mistaken for
// the record the cursor was pointing at.
struct Cursor {
  uint64_t next_seq = 0;
};

struct Options {
  uint64_t capacity = 0; // data-region bytes; 0 = open existing file
  bool sync = true;      // fsync on every commit
};

class RingLog {
public:
  // On-disk layout: meta slot 0, meta slot 1, then the data region.
  static constexpr uint64_t kMetaSlot0 = 0;
  static constexpr uint64_t kMetaSlot1 = 4096;
  static constexpr uint64_t kDataOffset = 8192;
  static constexpr size_t kRecordHeaderSize = 24;

  // Opens (creating if necessary) the log at `path`.
  // Throws std::runtime_error on unrecoverable errors (e.g. both metadata
  // copies corrupt, capacity mismatch).
  RingLog(const std::string& path, Options opt);
  ~RingLog();

  RingLog(const RingLog&) = delete;
  RingLog& operator=(const RingLog&) = delete;

  // Appends one record. On success *seq_out receives its globally
  // increasing sequence number. The record is committed (durable when
  // Options::sync is set) before this returns Ok.
  Status append(const void* data, size_t len, uint64_t* seq_out = nullptr);
  Status append(const std::string& s, uint64_t* seq_out = nullptr) {
    return append(s.data(), s.size(), seq_out);
  }

  // Reads the record at the cursor and advances it.
  //  - Ok:          rec filled, cursor advanced
  //  - End:         cursor reached the tail; no newer record committed yet
  //  - Overwritten: the record was overwritten by a newer lap
  Status read(Cursor& cur, Record& rec) const;

  Cursor oldestCursor() const { return Cursor{head_seq_}; }
  Cursor tailCursor() const { return Cursor{tail_seq_}; }

  uint64_t headSeq() const { return head_seq_; } // oldest live record
  uint64_t tailSeq() const { return tail_seq_; } // next seq to be assigned
  uint64_t capacity() const { return capacity_; }

  // Test hooks.
  uint64_t debugWriteOffset() const { return write_off_; }
  uint64_t debugHeadOffset() const { return head_off_; }

private:
  bool writeMeta();
  bool writePadding(uint64_t off, uint64_t len);
  void dropOldest();
  // Advances *off past one entry (padding or data record); increments *seq
  // only for data records. Returns false on invalid/corrupt entry.
  bool skipOne(uint64_t* off, uint64_t* seq) const;
  void initNew(uint64_t capacity);
  void recover();

  int fd_ = -1;
  std::string path_;
  uint64_t capacity_ = 0;
  bool sync_ = true;
  uint64_t generation_ = 0;
  uint64_t head_seq_ = 0;
  uint64_t tail_seq_ = 0;
  uint64_t head_off_ = 0;
  uint64_t write_off_ = 0;
};

} // namespace ringlog
