#include "ringlog.h"

#include <cstdio>
#include <cstring>
#include <deque>
#include <random>
#include <string>
#include <utility>
#include <vector>

#include <fcntl.h>
#include <unistd.h>

static int g_failures = 0;
static int g_checks = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    ++g_checks;                                                            \
    if (!(cond)) {                                                         \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
      ++g_failures;                                                        \
    }                                                                      \
  } while (0)

using ringlog::Cursor;
using ringlog::Record;
using ringlog::RingLog;
using ringlog::Status;

static std::string tmpPath(const char* name) {
  std::string p = std::string("/tmp/ringlog_test_") + name + "_" +
                  std::to_string(::getpid()) + ".log";
  ::unlink(p.c_str());
  return p;
}

// Reads all live records; returns false on unexpected status/content.
static bool drainAndVerify(RingLog& log,
                           const std::deque<std::pair<uint64_t, std::string>>&
                               model) {
  Cursor cur = log.oldestCursor();
  size_t idx = 0;
  for (;;) {
    Record r;
    Status st = log.read(cur, r);
    if (st == Status::End) break;
    if (st != Status::Ok) return false;
    if (idx >= model.size()) return false;
    if (r.seq != model[idx].first || r.payload != model[idx].second)
      return false;
    ++idx;
  }
  return idx == model.size();
}

static void testBasicAppendReadRecover() {
  std::string path = tmpPath("basic");
  {
    RingLog log(path, {4096, true});
    for (int i = 0; i < 10; ++i) {
      uint64_t seq = 999;
      CHECK(log.append("record_" + std::to_string(i), &seq) == Status::Ok);
      CHECK(seq == static_cast<uint64_t>(i));
    }
    CHECK(log.headSeq() == 0);
    CHECK(log.tailSeq() == 10);
    Cursor cur = log.oldestCursor();
    for (int i = 0; i < 10; ++i) {
      Record r;
      CHECK(log.read(cur, r) == Status::Ok);
      CHECK(r.seq == static_cast<uint64_t>(i));
      CHECK(r.payload == "record_" + std::to_string(i));
    }
    Record r;
    CHECK(log.read(cur, r) == Status::End);
  }
  { // clean reopen: state fully recovered
    RingLog log(path, {0, true});
    CHECK(log.headSeq() == 0);
    CHECK(log.tailSeq() == 10);
    Cursor cur = log.oldestCursor();
    Record r;
    CHECK(log.read(cur, r) == Status::Ok);
    CHECK(r.payload == "record_0");
    uint64_t seq;
    CHECK(log.append("record_10", &seq) == Status::Ok);
    CHECK(seq == 10); // sequence keeps increasing across reopen
  }
  ::unlink(path.c_str());
}

static void testExactFill() {
  std::string path = tmpPath("exactfill");
  const uint64_t cap = 96; // exactly 3 records of 24 header + 8 payload
  RingLog log(path, {cap, true});
  for (int i = 0; i < 3; ++i) {
    std::string p(8, char('a' + i));
    CHECK(log.append(p, nullptr) == Status::Ok);
  }
  CHECK(log.debugWriteOffset() == 0); // wrapped exactly to the start
  CHECK(log.headSeq() == 0 && log.tailSeq() == 3);
  // All three records of the exactly-full log are readable.
  Cursor cur = log.oldestCursor();
  for (int i = 0; i < 3; ++i) {
    Record r;
    CHECK(log.read(cur, r) == Status::Ok);
    CHECK(r.payload == std::string(8, char('a' + i)));
  }
  Record r;
  CHECK(log.read(cur, r) == Status::End);
  // One more append evicts exactly the oldest record.
  CHECK(log.append(std::string(8, 'd'), nullptr) == Status::Ok);
  CHECK(log.headSeq() == 1 && log.tailSeq() == 4);
  Cursor old{0};
  CHECK(log.read(old, r) == Status::Overwritten);
  ::unlink(path.c_str());
}

static void testPaddingOnWrap() {
  std::string path = tmpPath("padding");
  const uint64_t cap = 72; // records of 32 bytes: offsets 0, 32, then pad 8
  RingLog log(path, {cap, true});
  CHECK(log.append(std::string(8, '1'), nullptr) == Status::Ok); // off 0
  CHECK(log.append(std::string(8, '2'), nullptr) == Status::Ok); // off 32
  CHECK(log.debugWriteOffset() == 64);
  // 64 + 32 > 72: 8 bytes of padding at 64, wrap to 0, evicting record 0.
  CHECK(log.append(std::string(8, '3'), nullptr) == Status::Ok);
  CHECK(log.debugWriteOffset() == 32);
  CHECK(log.headSeq() == 1);
  // Padding header must be on disk at offset 64.
  uint8_t hdr[8];
  CHECK(::pread(::open(path.c_str(), O_RDONLY), hdr, 8,
                RingLog::kDataOffset + 64) == 8);
  uint32_t magic, len;
  std::memcpy(&magic, hdr, 4);
  std::memcpy(&len, hdr + 4, 4);
  CHECK(magic == 0x50414420u); // "PAD "
  CHECK(len == 8);
  // Records 1 and 2 survive; record 0 is gone.
  Cursor cur{0};
  Record r;
  CHECK(log.read(cur, r) == Status::Overwritten);
  cur = log.oldestCursor();
  CHECK(log.read(cur, r) == Status::Ok && r.payload == std::string(8, '2'));
  CHECK(log.read(cur, r) == Status::Ok && r.payload == std::string(8, '3'));
  CHECK(log.read(cur, r) == Status::End);
  ::unlink(path.c_str());
}

static void testExhaustiveWrap() {
  const uint64_t caps[] = {128, 256, 512, 2048};
  for (uint64_t cap : caps) {
    for (int seed = 0; seed < 4; ++seed) {
      std::string path = tmpPath("exhaustive");
      std::mt19937_64 rng(static_cast<uint64_t>(seed) * 1000 + cap);
      std::deque<std::pair<uint64_t, std::string>> model;
      uint64_t next_seq = 0;
      {
        RingLog log(path, {cap, false});
        for (int i = 0; i < 1500; ++i) {
          size_t len;
          if (rng() % 20 == 0) {
            len = static_cast<size_t>(cap - RingLog::kRecordHeaderSize);
          } else {
            len = static_cast<size_t>(rng() % 90);
          }
          std::string payload(len, '\0');
          for (auto& c : payload) c = static_cast<char>(rng());
          uint64_t seq = 0;
          CHECK(log.append(payload, &seq) == Status::Ok);
          CHECK(seq == next_seq);
          model.emplace_back(seq, std::move(payload));
          ++next_seq;
          while (!model.empty() && model.front().first < log.headSeq())
            model.pop_front();
          CHECK(model.empty() || model.front().first == log.headSeq());
          CHECK((log.headSeq() == log.tailSeq()) == model.empty());
          CHECK(drainAndVerify(log, model));
          // A cursor one lap behind must report Overwritten, never stale
          // data from a previous lap at the same offset.
          if (log.headSeq() > 0) {
            Cursor stale{log.headSeq() - 1};
            Record r;
            CHECK(log.read(stale, r) == Status::Overwritten);
          }
          if (i == 750) { // mid-run crash/recovery drill
            RingLog reopened(path, {0, false});
            CHECK(reopened.headSeq() == log.headSeq());
            CHECK(reopened.tailSeq() == log.tailSeq());
            CHECK(drainAndVerify(reopened, model));
          }
        }
      }
      { // final recovery check after close
        RingLog log(path, {0, false});
        CHECK(log.tailSeq() == next_seq);
        CHECK(drainAndVerify(log, model));
      }
      ::unlink(path.c_str());
    }
  }
}

static void testCursorNotFooledByNewLap() {
  std::string path = tmpPath("newlap");
  RingLog log(path, {128, true}); // 4 records of 32 bytes per lap
  for (int i = 0; i < 4; ++i)
    CHECK(log.append(std::string(8, char('A' + i)), nullptr) == Status::Ok);
  // Next lap reuses offset 0 with seq 4.
  CHECK(log.append(std::string(8, 'E'), nullptr) == Status::Ok);
  CHECK(log.headSeq() == 1);
  Cursor cur{0};
  Record r;
  // Must NOT return the new-lap record "EEEEEEEE" sitting at offset 0.
  CHECK(log.read(cur, r) == Status::Overwritten);
  // And the live records are still correct.
  cur = log.oldestCursor();
  CHECK(log.read(cur, r) == Status::Ok && r.seq == 1 &&
        r.payload == std::string(8, 'B'));
  ::unlink(path.c_str());
}

static void testTornWrite() {
  for (int variant = 0; variant < 2; ++variant) {
    std::string path = tmpPath("torn");
    uint64_t tail_off;
    {
      RingLog log(path, {4096, true});
      for (int i = 0; i < 5; ++i)
        CHECK(log.append("committed_" + std::to_string(i), nullptr) ==
              Status::Ok);
      tail_off = log.debugWriteOffset();
    }
    // Simulate a torn append at the tail: variant 0 = truncated record,
    // variant 1 = full-length garbage with bad crc.
    int fd = ::open(path.c_str(), O_RDWR);
    CHECK(fd >= 0);
    std::string junk = (variant == 0)
                           ? std::string(10, '\x5a')
                           : std::string(24 + 64, '\x5a');
    // Give the junk a plausible data magic so only crc/len/seq can catch it.
    uint32_t magic = 0x52454344u;
    std::memcpy(junk.data(), &magic, 4);
    CHECK(::pwrite(fd, junk.data(), junk.size(),
                   static_cast<off_t>(RingLog::kDataOffset + tail_off)) ==
          static_cast<ssize_t>(junk.size()));
    ::fsync(fd);
    ::close(fd);

    RingLog log(path, {0, true});
    // Torn tail discarded; committed records intact.
    CHECK(log.tailSeq() == 5);
    CHECK(log.headSeq() == 0);
    Cursor cur = log.oldestCursor();
    for (int i = 0; i < 5; ++i) {
      Record r;
      CHECK(log.read(cur, r) == Status::Ok);
      CHECK(r.payload == "committed_" + std::to_string(i));
    }
    Record r;
    CHECK(log.read(cur, r) == Status::End);
    // Log keeps working; next record reuses the torn space and gets seq 5.
    uint64_t seq = 0;
    CHECK(log.append("after_crash", &seq) == Status::Ok);
    CHECK(seq == 5);
    cur = Cursor{5};
    CHECK(log.read(cur, r) == Status::Ok);
    CHECK(r.payload == "after_crash");
    ::unlink(path.c_str());
  }
}

static void testTornMetadata() {
  // Corrupt the newest slot: recovery falls back to the older copy.
  {
    std::string path = tmpPath("tornmeta");
    {
      RingLog log(path, {4096, true});
      for (int i = 0; i < 3; ++i)
        CHECK(log.append("m" + std::to_string(i), nullptr) == Status::Ok);
    }
    // Generations: create=0 (both slots), appends -> gen1 slot1, gen2 slot0,
    // gen3 slot1. Newest is slot1; corrupt it.
    int fd = ::open(path.c_str(), O_RDWR);
    CHECK(fd >= 0);
    uint8_t b = 0xFF;
    CHECK(::pwrite(fd, &b, 1, RingLog::kMetaSlot1 + 20) == 1);
    ::fsync(fd);
    ::close(fd);
    RingLog log(path, {0, true});
    CHECK(log.tailSeq() == 2); // gen2 state: last append not yet committed
    Cursor cur = log.oldestCursor();
    Record r;
    CHECK(log.read(cur, r) == Status::Ok && r.payload == "m0");
    CHECK(log.read(cur, r) == Status::Ok && r.payload == "m1");
    CHECK(log.read(cur, r) == Status::End);
    uint64_t seq = 0;
    CHECK(log.append("m2_retry", &seq) == Status::Ok);
    CHECK(seq == 2);
    ::unlink(path.c_str());
  }
  // Corrupt the older slot only: no effect.
  {
    std::string path = tmpPath("tornmeta_old");
    {
      RingLog log(path, {4096, true});
      for (int i = 0; i < 3; ++i)
        CHECK(log.append("x", nullptr) == Status::Ok);
    }
    int fd = ::open(path.c_str(), O_RDWR);
    uint8_t b = 0xFF;
    CHECK(::pwrite(fd, &b, 1, RingLog::kMetaSlot0 + 20) == 1);
    ::fsync(fd);
    ::close(fd);
    RingLog log(path, {0, true});
    CHECK(log.tailSeq() == 3);
    ::unlink(path.c_str());
  }
  // Corrupt both slots: open must fail.
  {
    std::string path = tmpPath("tornmeta_both");
    {
      RingLog log(path, {4096, true});
      CHECK(log.append("x", nullptr) == Status::Ok);
    }
    int fd = ::open(path.c_str(), O_RDWR);
    uint8_t b = 0xFF;
    CHECK(::pwrite(fd, &b, 1, RingLog::kMetaSlot0 + 20) == 1);
    CHECK(::pwrite(fd, &b, 1, RingLog::kMetaSlot1 + 20) == 1);
    ::fsync(fd);
    ::close(fd);
    bool threw = false;
    try {
      RingLog log(path, {0, true});
    } catch (const std::runtime_error&) {
      threw = true;
    }
    CHECK(threw);
    ::unlink(path.c_str());
  }
}

static void testLimitsAndEmpty() {
  std::string path = tmpPath("limits");
  RingLog log(path, {128, true});
  // Empty payload is fine.
  uint64_t seq = 0;
  CHECK(log.append("", &seq) == Status::Ok);
  CHECK(seq == 0);
  Cursor cur{0};
  Record r;
  CHECK(log.read(cur, r) == Status::Ok && r.payload.empty());
  // Largest possible record: capacity - header.
  CHECK(log.append(std::string(128 - RingLog::kRecordHeaderSize, 'z'),
                   nullptr) == Status::Ok);
  // Too large to ever fit.
  CHECK(log.append(std::string(200, 'y'), nullptr) == Status::TooLarge);
  // Reading from a fresh tail cursor yields End.
  cur = log.tailCursor();
  CHECK(log.read(cur, r) == Status::End);
  ::unlink(path.c_str());
}

int main() {
  testBasicAppendReadRecover();
  testExactFill();
  testPaddingOnWrap();
  testExhaustiveWrap();
  testCursorNotFooledByNewLap();
  testTornWrite();
  testTornMetadata();
  testLimitsAndEmpty();
  std::fprintf(stderr, "%d checks, %d failures\n", g_checks, g_failures);
  if (g_failures == 0) std::fprintf(stderr, "ALL TESTS PASSED\n");
  return g_failures == 0 ? 0 : 1;
}
