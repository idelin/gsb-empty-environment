#pragma once

#include <cstdint>
#include <memory>
#include <string>

namespace ringlog {

enum class Status {
  kOk,
  kEnd,              // cursor caught up with the writer
  kOverwritten,      // the record the cursor wanted was overwritten by a newer lap
  kInvalidArgument,  // record larger than capacity, etc.
  kIOError,
  kCorruption,
};

const char* StatusName(Status s);

struct Record {
  uint64_t seq = 0;
  std::string data;
};

// Fixed-capacity append-only ring log stored in a single file.
//
// File layout:
//   [0, 4096)          metadata copy A (magic, offsets, seqs, generation, crc32)
//   [4096, 8192)       metadata copy B (same format)
//   [8192, 8192+cap)   circular data region of fixed-size records + padding
//
// Guarantees:
//  - Sequence numbers are globally increasing and survive reopen.
//  - A record that does not fit before the physical end is preceded by a
//    padding record and written at offset 0 (records never wrap).
//  - A torn write only affects the uncommitted tail: data is written and
//    fsynced before metadata, and metadata is kept in two crc-protected
//    copies, so a crash can never expose a partial record.
//  - Readers detect overwrite via sequence numbers + crc; a stale cursor
//    never mistakes a newer-lap record at the same offset for its own.
//
// Single writer; cursors may be used between appends. Not thread-safe.
class RingLog {
 public:
  RingLog(const RingLog&) = delete;
  RingLog& operator=(const RingLog&) = delete;
  ~RingLog();

  // Creates a new log (truncates any existing file). `capacity` is the size
  // of the circular data region in bytes. Returns nullptr on failure.
  static std::unique_ptr<RingLog> Create(const std::string& path, uint64_t capacity);

  // Opens an existing log, recovering state from the newest valid metadata
  // copy. Returns nullptr if no valid copy exists.
  static std::unique_ptr<RingLog> Open(const std::string& path);

  // Appends one record, evicting oldest records as needed. On success
  // *seq_out receives the assigned sequence number.
  Status Append(const std::string& data, uint64_t* seq_out = nullptr);

  uint64_t capacity() const { return capacity_; }
  uint64_t used_bytes() const { return used_; }
  uint64_t head_seq() const { return head_seq_; }  // oldest live record
  uint64_t tail_seq() const { return tail_seq_; }  // next seq to assign

  class Cursor {
   public:
    // kOk: *out filled. kEnd: caught up with writer. kOverwritten: the
    // expected record was overwritten; the cursor has been resynced to the
    // oldest live record, call Next again to continue.
    Status Next(Record* out);
    uint64_t next_seq() const { return seq_; }

   private:
    friend class RingLog;
    Cursor(RingLog* log, uint64_t seq, uint64_t off) : log_(log), seq_(seq), off_(off) {}
    void Resync();
    RingLog* log_;
    uint64_t seq_;
    uint64_t off_;
  };

  // Cursor positioned at the oldest live record.
  Cursor NewCursor() { return Cursor(this, head_seq_, head_off_); }

 private:
  RingLog() = default;

  struct Parsed {
    bool is_data = false;
    uint64_t span = 0;  // total bytes occupied (header + payload/padding)
    uint64_t seq = 0;
    std::string payload;
  };

  Status EnsureFree(uint64_t n);
  Status WritePadding(uint64_t off, uint64_t span);
  Status WriteRecord(uint64_t off, uint64_t seq, const std::string& data);
  Status ReadAt(uint64_t off, Parsed* out) const;
  Status PersistMeta();

  int fd_ = -1;
  uint64_t capacity_ = 0;
  uint64_t used_ = 0;
  uint64_t head_off_ = 0;
  uint64_t head_seq_ = 0;
  uint64_t tail_off_ = 0;
  uint64_t tail_seq_ = 0;
  uint64_t generation_ = 0;
};

}  // namespace ringlog
