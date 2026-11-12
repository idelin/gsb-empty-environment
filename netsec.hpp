#pragma once

#include <cstdint>
#include <string>
#include <vector>

// Cap'n Proto far-pointer traversal guard.
//
// One MessagePointerGuard is used while reading a single message. The segment
// tables themselves are shared/read-only; every reading thread must own its own
// guard so that the traversal budget and the ancestor set stay thread-private.
// A guard is therefore not thread-safe across threads, but independent guards
// over the same segments can run concurrently without locking.
namespace netsec {

// Why a traversal was rejected. kOk means the whole message was visited.
enum class TraversalError : std::uint8_t {
  kOk = 0,
  kSegmentIdOutOfBounds,  // A pointer named a segment that does not exist.
  kLandingOutOfBounds,    // A landing pad / target object leaves the segment.
  kDoubleFarNotStruct,    // Second word of a double-far is not a struct tag.
  kMalformedPointer,      // Reserved pointer kind or malformed list element size.
  kCapabilityUnsupported, // Capability (interface) pointers cannot be followed.
  kBudgetExhausted,       // Cumulative words read exceed the traversal budget.
  kCycleDetected,         // Pointer reaches an object overlapping an ancestor.
  kNestingTooDeep,        // Pointer / far-hop chain exceeds the nesting limit.
};

const char* toString(TraversalError error) noexcept;

// One message segment: a contiguous run of 64-bit words. The view does not own
// the backing storage; the caller must keep it alive for the guard's lifetime.
struct SegmentView {
  const std::uint64_t* words = nullptr;
  std::uint64_t wordCount = 0;

  SegmentView() = default;
  SegmentView(const std::uint64_t* data, std::uint64_t sizeInWords)
      : words(data), wordCount(sizeInWords) {}
};

struct GuardConfig {
  // Total words that all followed objects are allowed to charge together.
  std::uint64_t traversalBudgetWords = 8 * 1024 * 1024;
  // Maximum depth of nested pointers, also bounding far-pointer hop chains.
  std::uint32_t maxNestingDepth = 64;
};

struct TraversalResult {
  TraversalError error = TraversalError::kOk;
  // Words charged so far; on rejection this holds the budget state at failure.
  std::uint64_t wordsCharged = 0;
  // Number of distinct objects visited (the traversal multiplier).
  std::uint64_t objectsVisited = 0;

  bool ok() const noexcept { return error == TraversalError::kOk; }
  explicit operator bool() const noexcept { return ok(); }
};

class MessagePointerGuard {
 public:
  // `segments` is referenced, not copied, and must outlive the guard.
  MessagePointerGuard(const std::vector<SegmentView>& segments,
                      GuardConfig config = {});

  // Traverse the whole message starting at its root pointer (word 0 of
  // segment 0) and charge every reached struct/list against the budget.
  // A null root is an empty, valid message.
  TraversalResult traverseMessage();

  // Follow the pointer stored at words[ptrWordIndex] in segment `segmentId`.
  // Exposed so a reader can validate one pointer at a time; the budget and
  // ancestor set are shared across calls on this guard.
  TraversalResult followPointer(std::uint32_t segmentId,
                                std::uint64_t ptrWordIndex);

  const TraversalResult& state() const noexcept { return state_; }

 private:
  struct Region {
    std::uint32_t segmentId;
    std::uint64_t startWord;
    std::uint64_t wordCount;
  };

  struct FarTarget {
    std::uint32_t segmentId;
    std::uint64_t wordIndex;
  };

  bool fail(TraversalError error) noexcept;
  bool charge(std::uint64_t words);
  bool entersObject(const Region& region);
  void leaveObject() noexcept;
  bool regionOnPath(const Region& region) const;

  bool segmentInBounds(std::uint32_t segmentId,
                       std::uint64_t wordIndex,
                       std::uint64_t wordCount) const;

  // Resolve a far pointer (single or double) to the word holding the actual
  // struct/list pointer. Validates every landing pad and the double-far struct
  // tag rule. `depth` counts far hops so far loops cannot run away.
  bool resolveFar(std::uint32_t segmentId,
                  std::uint64_t ptrWordIndex,
                  std::uint64_t pointerWord,
                  std::uint32_t depth,
                  FarTarget& out);

  bool walkAt(std::uint32_t segmentId,
              std::uint64_t wordIndex,
              std::uint32_t depth,
              std::uint32_t farHops);
  bool walkStruct(std::uint32_t segmentId,
                  std::uint64_t wordIndex,
                  std::uint64_t pointerWord,
                  std::uint32_t depth);
  bool walkList(std::uint32_t segmentId,
                std::uint64_t wordIndex,
                std::uint64_t pointerWord,
                std::uint32_t depth);

  const std::vector<SegmentView>& segments_;
  GuardConfig config_;
  TraversalResult state_;
  std::vector<Region> ancestorStack_;
};

// --- Wire-format encoders (little-endian) for building test messages. -------

// Null pointer.
constexpr std::uint64_t makeNullPointer() { return 0; }

// Struct pointer: data section `dataWords`, pointer section `ptrWords`, and a
// signed word offset relative to the pointer itself.
std::uint64_t makeStructPointer(std::uint16_t dataWords,
                                std::uint16_t ptrWords,
                                std::int32_t offsetWords);

// List element size codes, matching the Cap'n Proto wire encoding.
enum class ElementSize : std::uint8_t {
  kEmpty = 0,  // Zero-bit (void) elements.
  kBit = 1,
  kByte = 2,
  kTwoBytes = 3,
  kFourBytes = 4,
  kEightBytes = 5,
  kPointer = 6,
  kComposite = 7,
};

std::uint64_t makeListPointer(ElementSize elementSize,
                              std::uint32_t elementCount,
                              std::int32_t offsetWords);

// Far pointer landing in segment `segmentId` at word `wordIndex`;
// `doubleFar` selects the two-word double-far form.
std::uint64_t makeFarPointer(std::uint32_t segmentId,
                             std::uint32_t wordIndex,
                             bool doubleFar);

// Struct tag stored as the first word of a composite list's data.
std::uint64_t makeCompositeTag(std::uint16_t dataWords,
                               std::uint16_t ptrWords,
                               std::uint32_t elementCount);

}  // namespace netsec
