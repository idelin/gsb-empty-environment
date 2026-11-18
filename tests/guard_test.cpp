#include <cstdint>
#include <cstdio>
#include <vector>

#include "netsec.hpp"

namespace {

int g_failures = 0;

#define CHECK_STATUS(actual, expected)                                        \
  do {                                                                        \
    netsec::GuardStatus a_ = (actual);                                        \
    if (a_ != (expected)) {                                                   \
      std::printf("FAIL %s:%d: got %s, want %s\n", __FILE__, __LINE__,        \
                  netsec::toString(a_), netsec::toString(expected));          \
      ++g_failures;                                                           \
    }                                                                         \
  } while (0)

#define CHECK_TRUE(cond)                                                      \
  do {                                                                        \
    if (!(cond)) {                                                            \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);             \
      ++g_failures;                                                           \
    }                                                                         \
  } while (0)

// Wire-format pointer encoders (offsets in words, relative to the word after
// the pointer).
uint64_t structPtr(int32_t offset, uint16_t dataWords, uint16_t pointerCount) {
  return (static_cast<uint64_t>(static_cast<uint32_t>(offset) << 2)) |
         (static_cast<uint64_t>(dataWords) << 32) |
         (static_cast<uint64_t>(pointerCount) << 48);
}

uint64_t listPtr(int32_t offset, uint8_t elementSize, uint32_t count) {
  return (static_cast<uint64_t>(static_cast<uint32_t>(offset) << 2) | 1u) |
         (static_cast<uint64_t>(elementSize) << 32) |
         (static_cast<uint64_t>(count) << 35);
}

uint64_t farPtr(bool doubleFar, uint32_t offset, uint32_t segment) {
  return 2u | (static_cast<uint64_t>(doubleFar) << 2) |
         (static_cast<uint64_t>(offset) << 3) |
         (static_cast<uint64_t>(segment) << 32);
}

netsec::Segment seg(const std::vector<uint64_t>& v) {
  return {v.data(), v.size()};
}

// 越界段号: far pointer names a segment that does not exist.
void testSegmentOutOfRange() {
  std::vector<uint64_t> s0 = {farPtr(false, 0, 7)};
  netsec::Segment segs[] = {seg(s0)};
  netsec::TraversalGuard guard(segs, 1, {});
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::SegmentOutOfRange);
}

// 自环: a struct whose pointer section points back at itself.
void testSelfLoop() {
  std::vector<uint64_t> s0 = {structPtr(0, 0, 1), structPtr(-1, 0, 1)};
  netsec::Segment segs[] = {seg(s0)};
  netsec::TraversalGuard guard(segs, 1, {});
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::DuplicateReference);
}

// 重复引用: two pointers resolve to the same block (traversal amplification).
void testRepeatedReference() {
  std::vector<uint64_t> s0 = {
      structPtr(0, 0, 2),   // root struct: pointer section at words 1..2
      structPtr(1, 1, 0),   // -> word 3
      structPtr(0, 1, 0),   // -> word 3 again
      0xAAAAAAAAAAAAAAAA,   // shared data word
  };
  netsec::Segment segs[] = {seg(s0)};
  netsec::TraversalGuard guard(segs, 1, {});
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::DuplicateReference);
}

// 合法深层嵌套: a 64-deep struct chain in segment 1 reached via a far
// pointer, ending in a double-far pointer back into segment 0 whose content
// finishes with a zero-size struct. Must be accepted.
void testLegalDeepNesting() {
  constexpr uint32_t kDepth = 64;

  std::vector<uint64_t> s0 = {
      farPtr(false, 0, 1),    // word 0: root -> landing pad at seg1 word 0
      0,                      // word 1: padding
      farPtr(false, 5, 0),    // word 2: double-far pad[0] -> content at word 5
      structPtr(0, 1, 1),     // word 3: double-far pad[1] tag: 1 data, 1 ptr
      0,                      // word 4: padding
      0xDEADBEEF,             // word 5: content data word
      structPtr(-1, 0, 0),    // word 6: content pointer -> zero-size struct
  };
  std::vector<uint64_t> s1(kDepth + 1);
  for (uint32_t i = 0; i < kDepth; ++i) s1[i] = structPtr(0, 0, 1);
  s1[kDepth] = farPtr(true, 2, 0);  // chain tail: double-far back to seg0

  netsec::Segment segs[] = {seg(s0), seg(s1)};
  netsec::TraversalGuard guard(segs, 2, {});
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::Ok);
  // Pointer words: root far + landing + kDepth chain + double-far + final
  // struct pointer = kDepth + 3. Struct bodies: kDepth chain words. Landing
  // pad: 1. Double-far pad: 2. Content: 1 data + 1 pointer. Zero-size: 0.
  CHECK_TRUE(guard.wordsRead() == 2 * kDepth + 8);
  CHECK_TRUE(guard.trackedObjects() == kDepth + 2);
}

// 预算耗尽: the same legal message, but the word budget runs out mid-walk.
void testBudgetExhausted() {
  constexpr uint32_t kDepth = 64;
  std::vector<uint64_t> s0 = {
      farPtr(false, 0, 1), 0, farPtr(false, 5, 0), structPtr(0, 1, 1),
      0, 0xDEADBEEF, structPtr(0, 0, 0),
  };
  std::vector<uint64_t> s1(kDepth + 1);
  for (uint32_t i = 0; i < kDepth; ++i) s1[i] = structPtr(0, 0, 1);
  s1[kDepth] = farPtr(true, 2, 0);

  netsec::Segment segs[] = {seg(s0), seg(s1)};
  netsec::GuardOptions options;
  options.traversalWordBudget = 2 * kDepth + 7;  // one word short
  netsec::TraversalGuard guard(segs, 2, options);
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::BudgetExhausted);
}

// 零大小结构与 Void 列表也要计费: each costs its pointer word.
void testZeroSizeAccounting() {
  // Note: structPtr(0,0,0) encodes as all-zero, i.e. a null pointer, so a
  // real zero-size struct pointer uses offset -1 (object at its own word).
  std::vector<uint64_t> s0 = {structPtr(-1, 0, 0)};  // zero-size struct
  std::vector<uint64_t> s1 = {listPtr(0, 0, 1000)};  // Void list, 1000 elems
  netsec::Segment segs[] = {seg(s0), seg(s1)};

  netsec::GuardOptions exact;
  exact.traversalWordBudget = 1;
  netsec::TraversalGuard g1(segs, 2, exact);
  CHECK_STATUS(g1.traverse(0, 0), netsec::GuardStatus::Ok);
  CHECK_TRUE(g1.wordsRead() == 1);
  netsec::TraversalGuard g2(segs, 2, exact);
  CHECK_STATUS(g2.traverse(1, 0), netsec::GuardStatus::Ok);
  CHECK_TRUE(g2.wordsRead() == 1);

  netsec::GuardOptions zero;
  zero.traversalWordBudget = 0;
  netsec::TraversalGuard g3(segs, 2, zero);
  CHECK_STATUS(g3.traverse(0, 0), netsec::GuardStatus::BudgetExhausted);
  netsec::TraversalGuard g4(segs, 2, zero);
  CHECK_STATUS(g4.traverse(1, 0), netsec::GuardStatus::BudgetExhausted);
}

// 落脚区越界: landing pad outside the target segment is rejected.
void testLandingPadOutOfBounds() {
  std::vector<uint64_t> s0 = {farPtr(false, 100, 0)};
  netsec::Segment segs[] = {seg(s0)};
  netsec::TraversalGuard g1(segs, 1, {});
  CHECK_STATUS(g1.traverse(0, 0), netsec::GuardStatus::LandingPadOutOfBounds);

  std::vector<uint64_t> s1 = {farPtr(true, 0, 0)};  // needs 2 pad words, has 1
  netsec::Segment segs1[] = {seg(s1)};
  netsec::TraversalGuard g2(segs1, 1, {});
  CHECK_STATUS(g2.traverse(0, 0), netsec::GuardStatus::LandingPadOutOfBounds);
}

// 双重远指针的第二个字必须是结构指针.
void testInvalidDoubleFarTag() {
  std::vector<uint64_t> s0 = {
      farPtr(true, 1, 0),    // root: double-far, pad at words 1..2
      farPtr(false, 3, 0),   // pad[0]: content at word 3
      listPtr(0, 5, 1),      // pad[1]: tag is a LIST pointer -> reject
      0,
  };
  netsec::Segment segs[] = {seg(s0)};
  netsec::TraversalGuard guard(segs, 1, {});
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::InvalidDoubleFarTag);
}

// 去重检测内存有界: too many distinct objects -> reject, not unbounded growth.
void testTrackingLimitExceeded() {
  constexpr uint32_t kChain = 10;
  std::vector<uint64_t> s0(kChain + 1);
  for (uint32_t i = 0; i < kChain; ++i) s0[i] = structPtr(0, 0, 1);
  s0[kChain] = structPtr(0, 0, 0);
  netsec::Segment segs[] = {seg(s0)};
  netsec::GuardOptions options;
  options.maxTrackedObjects = 3;
  netsec::TraversalGuard guard(segs, 1, options);
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::TrackingLimitExceeded);
  CHECK_TRUE(guard.trackedObjects() == 3);
}

// Composite list with a pointer section is walked and charged correctly.
void testCompositeList() {
  std::vector<uint64_t> s0 = {
      listPtr(0, 7, 2),       // composite list, 2 body words
      structPtr(2, 0, 1),     // tag: 2 elements, 0 data, 1 pointer each
      structPtr(-1, 0, 0),    // element 0 pointer -> zero-size struct @ word 2
      0,                      // element 1 pointer: null
  };
  netsec::Segment segs[] = {seg(s0)};
  netsec::TraversalGuard guard(segs, 1, {});
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::Ok);
  // list ptr + tag + 2 body + struct ptr + null pointer word (still charged)
  CHECK_TRUE(guard.wordsRead() == 6);
  CHECK_TRUE(guard.trackedObjects() == 2);
}

// Object body outside its segment is rejected.
void testObjectOutOfBounds() {
  std::vector<uint64_t> s0 = {structPtr(5, 1, 0)};
  netsec::Segment segs[] = {seg(s0)};
  netsec::TraversalGuard guard(segs, 1, {});
  CHECK_STATUS(guard.traverse(0, 0), netsec::GuardStatus::ObjectOutOfBounds);
}

}  // namespace

int main() {
  testSegmentOutOfRange();
  testSelfLoop();
  testRepeatedReference();
  testLegalDeepNesting();
  testBudgetExhausted();
  testZeroSizeAccounting();
  testLandingPadOutOfBounds();
  testInvalidDoubleFarTag();
  testTrackingLimitExceeded();
  testCompositeList();
  testObjectOutOfBounds();

  if (g_failures == 0) {
    std::printf("all guard tests passed\n");
    return 0;
  }
  std::printf("%d check(s) failed\n", g_failures);
  return 1;
}
