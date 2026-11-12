#include "netsec.hpp"

#include <cstdio>
#include <string>
#include <vector>

using netsec::ElementSize;
using netsec::GuardConfig;
using netsec::MessagePointerGuard;
using netsec::SegmentView;
using netsec::TraversalError;
using netsec::TraversalResult;
using netsec::makeCompositeTag;
using netsec::makeFarPointer;
using netsec::makeListPointer;
using netsec::makeStructPointer;

namespace {

int g_failed = 0;

void expect(const char* name,
            bool condition,
            const TraversalResult& result,
            TraversalError expected) {
  if (!condition || result.error != expected) {
    std::printf("FAIL %s: got error=%s charged=%llu visited=%llu, want %s\n",
                name, netsec::toString(result.error),
                static_cast<unsigned long long>(result.wordsCharged),
                static_cast<unsigned long long>(result.objectsVisited),
                netsec::toString(expected));
    ++g_failed;
  } else {
    std::printf("PASS %s (charged=%llu, visited=%llu)\n", name,
                static_cast<unsigned long long>(result.wordsCharged),
                static_cast<unsigned long long>(result.objectsVisited));
  }
}

TraversalResult run(const std::vector<std::vector<std::uint64_t>>& segs,
                    GuardConfig config = {}) {
  std::vector<SegmentView> views;
  views.reserve(segs.size());
  for (const auto& seg : segs) {
    views.emplace_back(seg.data(), seg.size());
  }
  MessagePointerGuard guard(views, config);
  return guard.traverseMessage();
}

// 1. Far pointer naming a segment number that does not exist.
void testOutOfBoundsSegmentId() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(4);
  segs[0][0] = makeFarPointer(7, 0, false);
  expect("oob_segment_id", true, run(segs),
         TraversalError::kSegmentIdOutOfBounds);

  // Double-far whose inner far pointer names a missing segment.
  segs[0][0] = makeFarPointer(0, 2, true);
  segs[0][3] = makeStructPointer(0, 0, 0);  // valid struct tag at word 3
  segs[0][2] = makeFarPointer(7, 0, false);
  expect("double_far_inner_oob_segment", true, run(segs),
         TraversalError::kSegmentIdOutOfBounds);
}

// 2. Far landing pad that runs past the end of the target segment.
void testLandingOutOfBounds() {
  std::vector<std::vector<std::uint64_t>> segs(2);
  segs[0].resize(2);
  segs[1].resize(1);
  // Double-far needs two words but segment 1 only has one.
  segs[0][0] = makeFarPointer(1, 0, true);
  expect("landing_oob", true, run(segs),
         TraversalError::kLandingOutOfBounds);

  // Struct whose own body crosses the segment end.
  segs.assign(1, {});
  segs[0].resize(3);
  segs[0][0] = makeStructPointer(0, 4, 1);  // needs 4 words, only 2 remain
  expect("struct_body_oob", true, run(segs),
         TraversalError::kLandingOutOfBounds);
}

// 3. Self-loop: the only struct's pointer slot points back at itself.
void testSelfLoop() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(4);
  // Root struct at word 1: 0 data words, 1 pointer word.
  segs[0][0] = makeStructPointer(0, 1, 0);
  // Pointer at word 1, target word 2 => offset -2 lands back on word 1.
  segs[0][1] = makeStructPointer(0, 1, -2);
  TraversalResult result = run(segs);
  expect("self_loop", true, result, TraversalError::kCycleDetected);

  // Far-pointer self loop: far lands back on the root pointer word.
  segs.assign(1, {});
  segs[0].resize(4);
  segs[0][0] = makeFarPointer(0, 0, false);
  expect("far_self_loop", true, run(segs), TraversalError::kNestingTooDeep);
}

// 4. Two pointers repeatedly referencing the same block: every visit bills.
void testRepeatedReference() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(8);
  // Root struct at word 1 with two pointer slots.
  segs[0][0] = makeStructPointer(0, 2, 0);
  // Both slots reference the same 2-word struct located at word 4.
  segs[0][1] = makeStructPointer(1, 1, 2);  // ptr@1 -> word 4
  segs[0][2] = makeStructPointer(1, 1, 1);  // ptr@2 -> word 4
  // Shared struct with one (null) pointer slot.
  segs[0][4] = 0;
  segs[0][5] = 0;

  // Charges: root 2 words, shared struct twice at 2 words each = 6.
  TraversalResult full = run(segs);
  expect("repeated_reference_ok", full.wordsCharged == 6 &&
                                      full.objectsVisited == 3,
         full, TraversalError::kOk);

  // With a budget below the repeated cost, the second visit is rejected.
  GuardConfig tight;
  tight.traversalBudgetWords = 4;
  TraversalResult blocked = run(segs, tight);
  expect("repeated_reference_budgeted", blocked.wordsCharged == 4, blocked,
         TraversalError::kBudgetExhausted);
}

// 5. Zero-size structs still consume at least one word of budget.
void testZeroSizeStructBilling() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(3);
  segs[0][0] = makeStructPointer(0, 1, 0);
  segs[0][1] = makeStructPointer(0, 0, 0);  // zero data, zero pointers
  GuardConfig config;
  // The zero-size child still bills one, so the root alone must not fit.
  config.traversalBudgetWords = 0;
  expect("zero_size_struct_billed", true, run(segs, config),
         TraversalError::kBudgetExhausted);

  GuardConfig enough;
  enough.traversalBudgetWords = 2;
  expect("zero_size_struct_allowed", true, run(segs, enough),
         TraversalError::kOk);
}

// 6. Void lists occupy no storage but still bill one word.
void testVoidListBilling() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(4);
  // Root struct with one pointer slot; its child is a huge void list.
  segs[0][0] = makeStructPointer(0, 1, 0);
  segs[0][1] = makeListPointer(ElementSize::kEmpty, 1000000, 0);

  GuardConfig config;  // root alone fits, the void list then needs one more
  config.traversalBudgetWords = 1;
  expect("void_list_billed", true, run(segs, config),
         TraversalError::kBudgetExhausted);

  GuardConfig enough;
  enough.traversalBudgetWords = 2;  // root 1 + void list minimum 1
  TraversalResult result = run(segs, enough);
  expect("void_list_one_word", result.wordsCharged == 2, result,
         TraversalError::kOk);
}

// 7. Double-far requires the second landing word to be a struct pointer.
void testDoubleFarTagMustBeStruct() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(6);
  segs[0][0] = makeFarPointer(0, 2, true);
  segs[0][2] = makeFarPointer(0, 4, false);
  segs[0][3] = makeListPointer(ElementSize::kByte, 1, 0);  // list, illegal
  segs[0][4] = makeStructPointer(0, 0, 0);
  expect("double_far_tag_not_struct", true, run(segs),
         TraversalError::kDoubleFarNotStruct);
}

// 8. A well-formed double-far reaches the target struct and bills all words.
void testValidDoubleFar() {
  std::vector<std::vector<std::uint64_t>> segs(2);
  segs[0].resize(2);
  segs[1].resize(6);
  // Root: double-far landing at segment 1 word 0.
  segs[0][0] = makeFarPointer(1, 0, true);
  // Landing pad: inner far to segment 1 word 3, then struct tag.
  segs[1][0] = makeFarPointer(1, 3, false);
  segs[1][1] = makeStructPointer(1, 0, 0);
  // Actual struct pointer at word 3: one data word, no pointers, at word 4.
  segs[1][3] = makeStructPointer(1, 0, 0);
  segs[1][4] = 0xDEADBEEF;
  // Charges: landing 2 + inner landing 1 + struct 1 = 4.
  TraversalResult result = run(segs);
  expect("valid_double_far", result.wordsCharged == 4 &&
                                 result.objectsVisited == 1,
         result, TraversalError::kOk);
}

// 9. Capability (interface) pointers are never followed.
void testCapabilityRejected() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(1);
  segs[0][0] = 0x3ULL;  // kind == 3
  expect("capability_rejected", true, run(segs),
         TraversalError::kCapabilityUnsupported);
}

// 10. Composite list of structs whose pointer slots are all traversed.
void testCompositeList() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(7);
  // Root pointer -> list starting at word 2, composite, region size 4 words.
  segs[0][0] = makeListPointer(ElementSize::kComposite, 4, 1);
  segs[0][2] = makeCompositeTag(0, 1, 3);  // 3 structs, each 1 ptr word
  segs[0][3] = 0;
  segs[0][4] = 0;
  segs[0][5] = 0;
  // tag(1) + 3 elements = 4 words charged for the list.
  TraversalResult result = run(segs);
  expect("composite_list", result.wordsCharged == 4 &&
                               result.objectsVisited == 1,
         result, TraversalError::kOk);

  // Lying tag claiming more elements than the region holds is rejected.
  segs[0][2] = makeCompositeTag(0, 1, 99);
  expect("composite_list_oob", true, run(segs),
         TraversalError::kLandingOutOfBounds);
}

// 11. Shared DAG node reachable on two branches is billed on each branch but
// is not mistaken for a cycle once its subtree has been fully left.
void testSharedDagNode() {
  std::vector<std::vector<std::uint64_t>> segs(1);
  segs[0].resize(10);
  // Root struct word 1 with two pointer slots.
  segs[0][0] = makeStructPointer(0, 2, 0);
  // Each slot points to a wrapper struct (words 3 and 6) with one pointer.
  segs[0][1] = makeStructPointer(0, 1, 1);  // -> word 3
  segs[0][2] = makeStructPointer(0, 1, 3);  // -> word 6
  segs[0][3] = makeStructPointer(1, 0, 3);  // wrapper A -> shared word 7
  segs[0][6] = makeStructPointer(1, 0, 0);  // wrapper B -> shared word 7
  segs[0][7] = 0x1111;
  segs[0][8] = 0x2222;
  // root 2 + wrapper 1 + shared 1 + wrapper 1 + shared 1 = 6 words,
  // 5 objects visited, no cycle.
  TraversalResult result = run(segs);
  expect("shared_dag", result.wordsCharged == 6 &&
                           result.objectsVisited == 5,
         result, TraversalError::kOk);
}

}  // namespace

int main() {
  testOutOfBoundsSegmentId();
  testLandingOutOfBounds();
  testSelfLoop();
  testRepeatedReference();
  testZeroSizeStructBilling();
  testVoidListBilling();
  testDoubleFarTagMustBeStruct();
  testValidDoubleFar();
  testCapabilityRejected();
  testCompositeList();
  testSharedDagNode();

  if (g_failed == 0) {
    std::printf("\nAll tests passed.\n");
    return 0;
  }
  std::printf("\n%d test(s) failed.\n", g_failed);
  return 1;
}
