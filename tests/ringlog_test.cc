#include "ringlog/ringlog.h"

#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>

#include <deque>
#include <random>
#include <string>
#include <utility>

#define CHECK(cond)                                                        \
  do {                                                                     \
    if (!(cond)) {                                                         \
      fprintf(stderr, "CHECK failed %s:%d: %s\n", __FILE__, __LINE__, #cond); \
      exit(1);                                                             \
    }                                                                      \
  } while (0)

using ringlog::Record;
using ringlog::RingLog;
using ringlog::Status;

namespace {

constexpr off_t kDataBase = 8192;
int g_tmp_counter = 0;

std::string TmpPath() {
  return std::string("/tmp/ringlog_test_") + std::to_string(getpid()) + "_" +
         std::to_string(g_tmp_counter++) + ".log";
}

// Deterministic payload derived from seq, exactly len bytes.
std::string MakePayload(uint64_t seq, size_t len) {
  std::string s(len, '\0');
  for (size_t i = 0; i < len; ++i) s[i] = static_cast<char>('A' + (seq + i) % 26);
  return s;
}

void VerifyModel(RingLog* log, const std::deque<std::pair<uint64_t, std::string>>& model) {
  CHECK(log->head_seq() + model.size() == log->tail_seq());
  auto cur = log->NewCursor();
  Record r;
  size_t idx = 0;
  for (;;) {
    Status s = cur.Next(&r);
    if (s == Status::kEnd) break;
    CHECK(s == Status::kOk);
    CHECK(idx < model.size());
    CHECK(r.seq == model[idx].first);
    CHECK(r.data == model[idx].second);
    ++idx;
  }
  CHECK(idx == model.size());
}

void test_basic() {
  std::string path = TmpPath();
  {
    auto log = RingLog::Create(path, 256);
    CHECK(log != nullptr);
    for (uint64_t i = 0; i < 3; ++i) {
      uint64_t seq = 999;
      CHECK(log->Append(MakePayload(i, 10), &seq) == Status::kOk);
      CHECK(seq == i);  // globally increasing from 0
    }
    CHECK(log->head_seq() == 0);
    CHECK(log->tail_seq() == 3);
    auto cur = log->NewCursor();
    Record r;
    for (uint64_t i = 0; i < 3; ++i) {
      CHECK(cur.Next(&r) == Status::kOk);
      CHECK(r.seq == i);
      CHECK(r.data == MakePayload(i, 10));
    }
    CHECK(cur.Next(&r) == Status::kEnd);
  }
  unlink(path.c_str());
}

void test_exact_fill() {
  std::string path = TmpPath();
  const uint64_t cap = 96;  // header 24 + payload 8 = 32 per record -> 3 slots
  auto log = RingLog::Create(path, cap);
  for (uint64_t i = 0; i < 3; ++i) CHECK(log->Append(MakePayload(i, 8)) == Status::kOk);
  CHECK(log->used_bytes() == cap);  // exactly full, no padding
  CHECK(log->head_seq() == 0 && log->tail_seq() == 3);
  std::deque<std::pair<uint64_t, std::string>> model;
  for (uint64_t i = 0; i < 3; ++i) model.emplace_back(i, MakePayload(i, 8));
  VerifyModel(log.get(), model);

  // One more append evicts exactly the oldest record.
  CHECK(log->Append(MakePayload(3, 8)) == Status::kOk);
  CHECK(log->used_bytes() == cap);
  CHECK(log->head_seq() == 1 && log->tail_seq() == 4);
  model.pop_front();
  model.emplace_back(3, MakePayload(3, 8));
  VerifyModel(log.get(), model);
  unlink(path.c_str());
}

void test_padding_on_wrap() {
  std::string path = TmpPath();
  const uint64_t cap = 96;
  auto log = RingLog::Create(path, cap);
  CHECK(log->Append(MakePayload(0, 8)) == Status::kOk);   // [0,32)
  CHECK(log->Append(MakePayload(1, 8)) == Status::kOk);   // [32,64)
  // span 64 > remaining 32: padding fills [64,96), record wraps to offset 0,
  // evicting records 0 and 1.
  CHECK(log->Append(MakePayload(2, 40)) == Status::kOk);
  CHECK(log->used_bytes() == cap);
  CHECK(log->head_seq() == 2 && log->tail_seq() == 3);
  auto cur = log->NewCursor();
  Record r;
  CHECK(cur.Next(&r) == Status::kOk);  // cursor transparently skips padding
  CHECK(r.seq == 2 && r.data == MakePayload(2, 40));
  CHECK(cur.Next(&r) == Status::kEnd);
  unlink(path.c_str());
}

void test_overwrite_detection() {
  std::string path = TmpPath();
  const uint64_t cap = 96;  // 3 slots of 32
  auto log = RingLog::Create(path, cap);
  for (uint64_t i = 0; i < 3; ++i) CHECK(log->Append(MakePayload(i, 8)) == Status::kOk);

  auto stale = log->NewCursor();  // expects seq 0 at offset 0
  // Overwrite the whole first lap; offset 0 now holds seq 3, not seq 0.
  for (uint64_t i = 3; i < 6; ++i) CHECK(log->Append(MakePayload(i, 8)) == Status::kOk);
  Record r;
  CHECK(stale.Next(&r) == Status::kOverwritten);
  // After resync the cursor continues from the oldest live record.
  for (uint64_t i = 3; i < 6; ++i) {
    CHECK(stale.Next(&r) == Status::kOk);
    CHECK(r.seq == i && r.data == MakePayload(i, 8));
  }
  CHECK(stale.Next(&r) == Status::kEnd);

  // A cursor that read some records, then falls behind mid-stream.
  auto cur2 = log->NewCursor();
  CHECK(cur2.Next(&r) == Status::kOk && r.seq == 3);
  for (uint64_t i = 6; i < 12; ++i) CHECK(log->Append(MakePayload(i, 8)) == Status::kOk);
  CHECK(cur2.Next(&r) == Status::kOverwritten);
  CHECK(cur2.Next(&r) == Status::kOk && r.seq == log->head_seq());
  unlink(path.c_str());
}

void test_recovery() {
  std::string path = TmpPath();
  {
    auto log = RingLog::Create(path, 256);
    for (uint64_t i = 0; i < 5; ++i) CHECK(log->Append(MakePayload(i, 1)) == Status::kOk);
  }
  auto log = RingLog::Open(path);
  CHECK(log != nullptr);
  CHECK(log->head_seq() == 0 && log->tail_seq() == 5);
  std::deque<std::pair<uint64_t, std::string>> model;
  for (uint64_t i = 0; i < 5; ++i) model.emplace_back(i, MakePayload(i, 1));
  VerifyModel(log.get(), model);
  // Sequence numbers keep increasing across reopen.
  uint64_t seq = 0;
  CHECK(log->Append(MakePayload(5, 1), &seq) == Status::kOk);
  CHECK(seq == 5);
  model.emplace_back(5, MakePayload(5, 1));
  VerifyModel(log.get(), model);
  unlink(path.c_str());
}

void CorruptByte(const std::string& path, off_t off) {
  int fd = ::open(path.c_str(), O_RDWR);
  CHECK(fd >= 0);
  char b;
  CHECK(::pread(fd, &b, 1, off) == 1);
  b ^= 0x5A;
  CHECK(::pwrite(fd, &b, 1, off) == 1);
  ::close(fd);
}

void test_meta_dual_copy() {
  std::string path = TmpPath();
  {
    auto log = RingLog::Create(path, 128);
    for (uint64_t i = 0; i < 4; ++i) CHECK(log->Append(MakePayload(i, 4)) == Status::kOk);
  }
  // Corrupt copy A: recovery falls back to copy B.
  CorruptByte(path, 20);
  {
    auto log = RingLog::Open(path);
    CHECK(log != nullptr);
    CHECK(log->tail_seq() == 4);
  }
  // Corrupt copy B: copy A was healed by the previous open, still fine.
  CorruptByte(path, 4096 + 20);
  {
    auto log = RingLog::Open(path);
    CHECK(log != nullptr);
    CHECK(log->tail_seq() == 4);
    std::deque<std::pair<uint64_t, std::string>> model;
    for (uint64_t i = 0; i < 4; ++i) model.emplace_back(i, MakePayload(i, 4));
    VerifyModel(log.get(), model);
  }
  // Corrupt both copies: open must fail.
  CorruptByte(path, 20);
  CorruptByte(path, 4096 + 30);
  CHECK(RingLog::Open(path) == nullptr);
  unlink(path.c_str());
}

void test_torn_tail() {
  std::string path = TmpPath();
  const uint64_t cap = 96;
  {
    auto log = RingLog::Create(path, cap);
    CHECK(log->Append(MakePayload(0, 8)) == Status::kOk);
    CHECK(log->Append(MakePayload(1, 8)) == Status::kOk);
    // tail now at offset 64; simulate a torn future write there.
  }
  int fd = ::open(path.c_str(), O_RDWR);
  CHECK(fd >= 0);
  std::string garbage(32, '\xFF');
  CHECK(::pwrite(fd, garbage.data(), garbage.size(), kDataBase + 64) == 32);
  ::close(fd);

  auto log = RingLog::Open(path);
  CHECK(log != nullptr);
  // Committed records are unaffected by garbage in the uncommitted tail.
  std::deque<std::pair<uint64_t, std::string>> model;
  model.emplace_back(0, MakePayload(0, 8));
  model.emplace_back(1, MakePayload(1, 8));
  VerifyModel(log.get(), model);
  // The next append lands on the garbage and fully overwrites it.
  uint64_t seq = 0;
  CHECK(log->Append(MakePayload(2, 8), &seq) == Status::kOk);
  CHECK(seq == 2);
  model.emplace_back(2, MakePayload(2, 8));
  VerifyModel(log.get(), model);
  unlink(path.c_str());
}

void test_invalid_args() {
  std::string path = TmpPath();
  CHECK(RingLog::Create(path, 16) == nullptr);  // below minimum capacity
  auto log = RingLog::Create(path, 96);
  CHECK(log != nullptr);
  CHECK(log->Append(std::string(100, 'x')) == Status::kInvalidArgument);  // too big
  CHECK(log->Append(std::string(72, 'x')) == Status::kOk);                // exactly fits
  unlink(path.c_str());
}

// Exhaustive small-capacity wrap-around: for many capacities and payload
// sizes, append well past several laps and verify every confirmed
// (not-yet-overwritten) record against a model, before and after reopen.
void test_exhaustive_wrap() {
  for (uint64_t cap : {96UL, 104UL, 120UL, 128UL, 144UL, 192UL, 256UL}) {
    for (size_t len = 1; len <= 32; ++len) {
      std::string path = TmpPath();
      std::deque<std::pair<uint64_t, std::string>> model;
      uint64_t next = 0;
      {
        auto log = RingLog::Create(path, cap);
        CHECK(log != nullptr);
        for (int i = 0; i < 30; ++i) {
          std::string data = MakePayload(next, len);
          uint64_t seq = 0;
          CHECK(log->Append(data, &seq) == Status::kOk);
          CHECK(seq == next);
          model.emplace_back(next++, std::move(data));
          while (!model.empty() && model.front().first < log->head_seq()) model.pop_front();
          VerifyModel(log.get(), model);
        }
      }
      // Recovery preserves exactly the confirmed live records.
      auto log = RingLog::Open(path);
      CHECK(log != nullptr);
      CHECK(log->tail_seq() == next);
      VerifyModel(log.get(), model);
      unlink(path.c_str());
    }
  }
}

// Randomized model test with periodic reopen.
void test_randomized() {
  std::string path = TmpPath();
  const uint64_t cap = 200;
  std::mt19937_64 rng(42);
  std::deque<std::pair<uint64_t, std::string>> model;
  uint64_t next = 0;
  auto log = RingLog::Create(path, cap);
  for (int i = 0; i < 400; ++i) {
    size_t len = 1 + rng() % 60;
    std::string data = MakePayload(next, len);
    CHECK(log->Append(data) == Status::kOk);
    model.emplace_back(next++, std::move(data));
    while (!model.empty() && model.front().first < log->head_seq()) model.pop_front();
    VerifyModel(log.get(), model);
    if (i % 50 == 49) {
      log = RingLog::Open(path);
      CHECK(log != nullptr);
      CHECK(log->tail_seq() == next);
      VerifyModel(log.get(), model);
    }
  }
  unlink(path.c_str());
}

}  // namespace

int main() {
  struct { const char* name; void (*fn)(); } tests[] = {
    {"basic", test_basic},
    {"exact_fill", test_exact_fill},
    {"padding_on_wrap", test_padding_on_wrap},
    {"overwrite_detection", test_overwrite_detection},
    {"recovery", test_recovery},
    {"meta_dual_copy", test_meta_dual_copy},
    {"torn_tail", test_torn_tail},
    {"invalid_args", test_invalid_args},
    {"exhaustive_wrap", test_exhaustive_wrap},
    {"randomized", test_randomized},
  };
  for (auto& t : tests) {
    fprintf(stderr, "[ RUN  ] %s\n", t.name);
    t.fn();
    fprintf(stderr, "[  OK  ] %s\n", t.name);
  }
  printf("all tests passed\n");
  return 0;
}
