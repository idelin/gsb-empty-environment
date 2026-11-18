#pragma once

#include <cstddef>
#include <cstdint>
#include <unordered_set>
#include <vector>

namespace netsec {

// One message segment: a contiguous run of 64-bit words.
struct Segment {
  const uint64_t* words;
  size_t wordCount;
};

struct GuardOptions {
  // Traversal budget, counted in words read. Every word the guard touches is
  // charged: pointer words themselves, struct data/pointer sections, list
  // contents, composite-list tag words and far-pointer landing pads. Zero-size
  // structs and Void lists still cost their pointer word.
  uint64_t traversalWordBudget = 8ull * 1024ull * 1024ull;
  // Bound on the dedup set used to detect repeated references to the same
  // object. Exceeding it rejects the message instead of growing memory.
  size_t maxTrackedObjects = 4096;
};

enum class GuardStatus {
  Ok,
  SegmentOutOfRange,      // far pointer names a segment that does not exist
  LandingPadOutOfBounds,  // far/double-far landing pad outside its segment
  InvalidFarLanding,      // single-far landing pad does not hold a regular pointer
  InvalidDoubleFarTag,    // second word of double-far pad is not a struct pointer
  PointerOutOfBounds,     // pointer word location outside its segment
  ObjectOutOfBounds,      // struct/list body outside its segment
  BudgetExhausted,        // traversal word budget spent
  DuplicateReference,     // two pointers resolve to the same object (cycle/amplification)
  TrackingLimitExceeded,  // dedup set full; refuse rather than keep walking
};

const char* toString(GuardStatus status);

// Pointer traversal guard for message reader threads. Walks a Cap'n Proto
// message starting from a root pointer, enforcing structural validity of far
// pointers, a word-count traversal budget, and bounded duplicate detection.
class TraversalGuard {
public:
  TraversalGuard(const Segment* segments, size_t segmentCount, GuardOptions options);

  // Traverses the pointer word at (rootSegment, rootWordIndex) and everything
  // reachable from it. Returns GuardStatus::Ok iff the whole subgraph is sane.
  GuardStatus traverse(uint32_t rootSegment, uint32_t rootWordIndex);

  uint64_t wordsRead() const { return wordsRead_; }
  size_t trackedObjects() const { return seen_.size(); }

private:
  GuardStatus processPointer(uint32_t segment, uint32_t wordIndex);
  GuardStatus followFar(uint64_t word);
  GuardStatus trackObject(uint32_t segment, uint32_t wordIndex);
  GuardStatus charge(uint64_t words);

  GuardStatus readWord(uint32_t segment, uint32_t wordIndex, uint64_t& out) const;
  bool rangeInSegment(uint32_t segment, int64_t start, uint64_t words) const;

  const Segment* segments_;
  size_t segmentCount_;
  GuardOptions options_;
  uint64_t wordsRead_ = 0;
  std::unordered_set<uint64_t> seen_;
  // Explicit work list of pointer-word locations (segment, wordIndex).
  std::vector<uint64_t> pending_;
};

}  // namespace netsec
