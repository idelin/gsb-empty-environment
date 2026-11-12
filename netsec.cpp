#include "netsec.hpp"

#include <limits>

namespace netsec {

namespace {

namespace wire {

constexpr std::uint64_t kKindMask = 0x3ULL;
constexpr std::uint64_t kOffsetMask = 0x3FFFFFFFULL;    // 30-bit signed
constexpr std::uint64_t kListCountMask = 0x1FFFFFFFULL;  // 29-bit
constexpr std::uint64_t kFarIndexMask = 0x1FFFFFFFULL;  // 30-bit unsigned

enum PointerKind : std::uint64_t {
  kStruct = 0,
  kList = 1,
  kFar = 2,
  kOther = 3,
};

std::int32_t signExtendOffset(std::uint64_t pointerWord) {
  std::uint64_t raw = (pointerWord >> 2) & kOffsetMask;
  if (raw & (1ULL << 29)) {
    raw -= (1ULL << 30);
  }
  return static_cast<std::int32_t>(raw);
}

// An offset is relative to the word following the pointer itself. Returns
// max() when the signed target would be negative.
std::uint64_t targetFromOffset(std::uint64_t pointerWordIndex,
                               std::int32_t offset) {
  std::int64_t target =
      static_cast<std::int64_t>(pointerWordIndex) + 1 + offset;
  if (target < 0) {
    return std::numeric_limits<std::uint64_t>::max();
  }
  return static_cast<std::uint64_t>(target);
}

}  // namespace wire

}  // namespace

const char* toString(TraversalError error) noexcept {
  switch (error) {
    case TraversalError::kOk:
      return "ok";
    case TraversalError::kSegmentIdOutOfBounds:
      return "segment id out of bounds";
    case TraversalError::kLandingOutOfBounds:
      return "landing region out of segment bounds";
    case TraversalError::kDoubleFarNotStruct:
      return "second word of double-far pointer is not a struct pointer";
    case TraversalError::kMalformedPointer:
      return "malformed pointer encoding";
    case TraversalError::kCapabilityUnsupported:
      return "capability pointers cannot be traversed";
    case TraversalError::kBudgetExhausted:
      return "traversal budget exhausted";
    case TraversalError::kCycleDetected:
      return "pointer cycle detected";
    case TraversalError::kNestingTooDeep:
      return "pointer nesting too deep";
  }
  return "unknown traversal error";
}

MessagePointerGuard::MessagePointerGuard(
    const std::vector<SegmentView>& segments, GuardConfig config)
    : segments_(segments), config_(config) {}

TraversalResult MessagePointerGuard::traverseMessage() {
  if (segments_.empty() || segments_[0].wordCount == 0 ||
      segments_[0].words == nullptr) {
    fail(TraversalError::kSegmentIdOutOfBounds);
    return state_;
  }
  walkAt(0, 0, 0, 0);
  return state_;
}

TraversalResult MessagePointerGuard::followPointer(
    std::uint32_t segmentId, std::uint64_t ptrWordIndex) {
  walkAt(segmentId, ptrWordIndex, 0, 0);
  return state_;
}

bool MessagePointerGuard::fail(TraversalError error) noexcept {
  if (state_.error == TraversalError::kOk) {
    state_.error = error;
  }
  return false;
}

bool MessagePointerGuard::charge(std::uint64_t words) {
  // Every object/landing bills at least one word, so zero-size structs and
  // void lists cannot amplify traversal free of charge.
  std::uint64_t billed = words == 0 ? 1 : words;
  if (billed > config_.traversalBudgetWords ||
      state_.wordsCharged > config_.traversalBudgetWords - billed) {
    return fail(TraversalError::kBudgetExhausted);
  }
  state_.wordsCharged += billed;
  return true;
}

bool MessagePointerGuard::segmentInBounds(std::uint32_t segmentId,
                                          std::uint64_t wordIndex,
                                          std::uint64_t wordCount) const {
  if (segmentId >= segments_.size()) {
    return false;
  }
  const SegmentView& segment = segments_[segmentId];
  if (segment.words == nullptr) {
    return false;
  }
  return wordIndex <= segment.wordCount &&
         wordCount <= segment.wordCount - wordIndex;
}

bool MessagePointerGuard::regionOnPath(const Region& region) const {
  for (const Region& ancestor : ancestorStack_) {
    if (ancestor.segmentId != region.segmentId) {
      continue;
    }
    bool startsBeforeAncestorEnds =
        region.startWord < ancestor.startWord + ancestor.wordCount;
    bool ancestorStartsBeforeRegionEnds =
        ancestor.startWord < region.startWord + region.wordCount;
    if (startsBeforeAncestorEnds && ancestorStartsBeforeRegionEnds) {
      return true;
    }
  }
  return false;
}

bool MessagePointerGuard::entersObject(const Region& region) {
  // Landing back inside an object still being walked is a self/mutual loop.
  // Shared DAG references, whose ancestor was fully left, are allowed but get
  // billed again every time they are reached.
  if (regionOnPath(region)) {
    return fail(TraversalError::kCycleDetected);
  }
  ancestorStack_.push_back(region);
  ++state_.objectsVisited;
  return true;
}

void MessagePointerGuard::leaveObject() noexcept {
  if (!ancestorStack_.empty()) {
    ancestorStack_.pop_back();
  }
}

bool MessagePointerGuard::resolveFar(std::uint32_t segmentId,
                                     std::uint64_t ptrWordIndex,
                                     std::uint64_t pointerWord,
                                     std::uint32_t farHops,
                                     FarTarget& out) {
  (void)segmentId;
  (void)ptrWordIndex;

  bool doubleFar = ((pointerWord >> 2) & 1ULL) != 0;
  std::uint32_t targetSegment =
      static_cast<std::uint32_t>(pointerWord >> 32);
  std::uint64_t landingWord =
      (pointerWord >> 3) & wire::kFarIndexMask;

  // Reject segment numbers outside the message's segment table.
  if (targetSegment >= segments_.size()) {
    return fail(TraversalError::kSegmentIdOutOfBounds);
  }

  std::uint64_t landingSize = doubleFar ? 2 : 1;
  // The landing pad must lie fully inside the target segment.
  if (!segmentInBounds(targetSegment, landingWord, landingSize)) {
    return fail(TraversalError::kLandingOutOfBounds);
  }
  if (!charge(landingSize)) {
    return false;
  }

  if (!doubleFar) {
    out.segmentId = targetSegment;
    out.wordIndex = landingWord;
    return true;
  }

  const SegmentView& segment = segments_[targetSegment];
  std::uint64_t innerWord = segment.words[landingWord];
  std::uint64_t tagWord = segment.words[landingWord + 1];

  // Double-far rule: landing word 1 is another far pointer, word 2 is a
  // struct pointer (never a list, far, or other pointer).
  if ((innerWord & wire::kKindMask) != wire::kFar) {
    return fail(TraversalError::kMalformedPointer);
  }

  if (farHops + 1 > config_.maxNestingDepth) {
    return fail(TraversalError::kNestingTooDeep);
  }
  // Resolve the inner far first so an out-of-range segment it names is
  // reported before any tag-shape check on this landing pad.
  FarTarget innerTarget;
  if (!resolveFar(targetSegment, landingWord, innerWord, farHops + 1,
                  innerTarget)) {
    return false;
  }
  if ((tagWord & wire::kKindMask) != wire::kStruct || tagWord == 0) {
    return fail(TraversalError::kDoubleFarNotStruct);
  }
  out = innerTarget;
  return true;
}

bool MessagePointerGuard::walkStruct(std::uint32_t segmentId,
                                     std::uint64_t ptrWordIndex,
                                     std::uint64_t pointerWord,
                                     std::uint32_t depth) {
  std::uint16_t dataWords =
      static_cast<std::uint16_t>((pointerWord >> 32) & 0xFFFFULL);
  std::uint16_t ptrWords =
      static_cast<std::uint16_t>((pointerWord >> 48) & 0xFFFFULL);
  std::int32_t offset = wire::signExtendOffset(pointerWord);

  std::uint64_t target = wire::targetFromOffset(ptrWordIndex, offset);
  if (target == std::numeric_limits<std::uint64_t>::max()) {
    return fail(TraversalError::kLandingOutOfBounds);
  }

  std::uint64_t sizeWords =
      static_cast<std::uint64_t>(dataWords) + ptrWords;
  Region region{segmentId, target, sizeWords};

  // The entire struct body must land inside its target segment.
  if (!segmentInBounds(segmentId, target, sizeWords)) {
    return fail(TraversalError::kLandingOutOfBounds);
  }
  if (!entersObject(region)) {
    return false;
  }
  // A zero-size struct still bills one word through charge().
  if (!charge(sizeWords)) {
    leaveObject();
    return false;
  }

  bool ok = true;
  for (std::uint16_t slot = 0; ok && slot < ptrWords; ++slot) {
    std::uint64_t slotIndex =
        target + dataWords + static_cast<std::uint64_t>(slot);
    std::uint64_t childWord = segments_[segmentId].words[slotIndex];
    if (childWord != 0) {
      ok = walkAt(segmentId, slotIndex, depth + 1, 0);
    }
  }

  leaveObject();
  return ok;
}

bool MessagePointerGuard::walkAt(std::uint32_t segmentId,
                                 std::uint64_t wordIndex,
                                 std::uint32_t depth,
                                 std::uint32_t farHops) {
  if (segmentId >= segments_.size()) {
    return fail(TraversalError::kSegmentIdOutOfBounds);
  }
  if (wordIndex >= segments_[segmentId].wordCount ||
      segments_[segmentId].words == nullptr) {
    return fail(TraversalError::kLandingOutOfBounds);
  }

  std::uint64_t pointerWord = segments_[segmentId].words[wordIndex];
  if (pointerWord == 0) {
    return true;  // Null pointer: nothing to traverse.
  }

  std::uint64_t kind = pointerWord & wire::kKindMask;
  if (kind == wire::kFar) {
    // Far hops and nested pointers share one depth bound, so a chain of far
    // pointers can never loop without eventually being stopped.
    if (depth + farHops >= config_.maxNestingDepth) {
      return fail(TraversalError::kNestingTooDeep);
    }
    FarTarget target;
    if (!resolveFar(segmentId, wordIndex, pointerWord, farHops, target)) {
      return false;
    }
    return walkAt(target.segmentId, target.wordIndex, depth, farHops + 1);
  }

  if (depth >= config_.maxNestingDepth) {
    return fail(TraversalError::kNestingTooDeep);
  }

  switch (kind) {
    case wire::kStruct:
      return walkStruct(segmentId, wordIndex, pointerWord, depth);
    case wire::kList:
      return walkList(segmentId, wordIndex, pointerWord, depth);
    case wire::kOther:
      return fail(TraversalError::kCapabilityUnsupported);
    default:
      return fail(TraversalError::kMalformedPointer);
  }
}

// --- Wire-format encoders (little-endian; values are built in host order
// because the test segments are held in native uint64 words). ----------------

namespace {

std::uint64_t encodeSignedOffset(std::int32_t offsetWords) {
  std::uint32_t raw = static_cast<std::uint32_t>(offsetWords);
  return static_cast<std::uint64_t>(raw & 0x3FFFFFFFU) << 2;
}

}  // namespace

std::uint64_t makeStructPointer(std::uint16_t dataWords,
                                std::uint16_t ptrWords,
                                std::int32_t offsetWords) {
  return (static_cast<std::uint64_t>(ptrWords) << 48) |
         (static_cast<std::uint64_t>(dataWords) << 32) |
         encodeSignedOffset(offsetWords) | 0x0ULL;
}

std::uint64_t makeListPointer(ElementSize elementSize,
                              std::uint32_t elementCount,
                              std::int32_t offsetWords) {
  return (static_cast<std::uint64_t>(elementCount & 0x1FFFFFFFU) << 35) |
         (static_cast<std::uint64_t>(elementSize) << 32) |
         encodeSignedOffset(offsetWords) | 0x1ULL;
}

std::uint64_t makeFarPointer(std::uint32_t segmentId,
                             std::uint32_t wordIndex,
                             bool doubleFar) {
  return (static_cast<std::uint64_t>(segmentId) << 32) |
         (static_cast<std::uint64_t>(wordIndex & 0x1FFFFFFFU) << 3) |
         (doubleFar ? (1ULL << 2) : 0ULL) | 0x2ULL;
}

std::uint64_t makeCompositeTag(std::uint16_t dataWords,
                               std::uint16_t ptrWords,
                               std::uint32_t elementCount) {
  return (static_cast<std::uint64_t>(ptrWords) << 48) |
         (static_cast<std::uint64_t>(dataWords) << 32) |
         (static_cast<std::uint64_t>(elementCount & 0x1FFFFFFFU) << 2);
}


bool MessagePointerGuard::walkList(std::uint32_t segmentId,
                                   std::uint64_t ptrWordIndex,
                                   std::uint64_t pointerWord,
                                   std::uint32_t depth) {
  ElementSize elementSize =
      static_cast<ElementSize>((pointerWord >> 32) & 0x7ULL);
  std::uint32_t elementCount =
      static_cast<std::uint32_t>((pointerWord >> 35) &
                                 wire::kListCountMask);
  std::int32_t offset = wire::signExtendOffset(pointerWord);

  std::uint64_t target = wire::targetFromOffset(ptrWordIndex, offset);
  if (target == std::numeric_limits<std::uint64_t>::max()) {
    return fail(TraversalError::kLandingOutOfBounds);
  }

  std::uint64_t regionWords = 0;
  switch (elementSize) {
    case ElementSize::kEmpty:
      regionWords = 0;  // Void list has no storage; billed later anyway.
      break;
    case ElementSize::kBit:
      regionWords = (static_cast<std::uint64_t>(elementCount) + 63) / 64;
      break;
    case ElementSize::kByte:
      regionWords = elementCount;
      break;
    case ElementSize::kTwoBytes:
      regionWords = (static_cast<std::uint64_t>(elementCount) + 3) / 4;
      break;
    case ElementSize::kFourBytes:
      regionWords = (static_cast<std::uint64_t>(elementCount) + 1) / 2;
      break;
    case ElementSize::kEightBytes:
    case ElementSize::kPointer:
      regionWords = elementCount;
      break;
    case ElementSize::kComposite:
      // The count field is the data region size in words (tag included).
      regionWords = elementCount;
      break;
    default:
      return fail(TraversalError::kMalformedPointer);
  }

  if (!segmentInBounds(segmentId, target, regionWords)) {
    return fail(TraversalError::kLandingOutOfBounds);
  }

  const SegmentView& segment = segments_[segmentId];
  std::uint64_t billedWords = regionWords;
  std::uint64_t pointerRunStart = target;
  std::uint64_t pointerRunCount = 0;
  std::uint64_t compositeElementWords = 0;
  std::uint16_t compositeDataWords = 0;
  std::uint32_t compositeCount = 0;

  if (elementSize == ElementSize::kComposite) {
    if (regionWords == 0) {
      return fail(TraversalError::kMalformedPointer);
    }
    std::uint64_t tagWord = segment.words[target];
    if ((tagWord & wire::kKindMask) != wire::kStruct) {
      return fail(TraversalError::kMalformedPointer);
    }
    compositeDataWords =
        static_cast<std::uint16_t>((tagWord >> 32) & 0xFFFFULL);
    std::uint16_t tagPtrWords =
        static_cast<std::uint16_t>((tagWord >> 48) & 0xFFFFULL);
    compositeCount =
        static_cast<std::uint32_t>((tagWord >> 2) & wire::kListCountMask);
    compositeElementWords =
        static_cast<std::uint64_t>(compositeDataWords) + tagPtrWords;

    std::uint64_t neededWords = 1;  // tag word
    if (compositeElementWords != 0) {
      if (compositeCount >
          (regionWords - 1) / compositeElementWords) {
        return fail(TraversalError::kLandingOutOfBounds);
      }
      neededWords +=
          static_cast<std::uint64_t>(compositeCount) * compositeElementWords;
    }
    if (neededWords > regionWords) {
      return fail(TraversalError::kLandingOutOfBounds);
    }
    billedWords = neededWords;
    pointerRunStart = target + 1 + compositeDataWords;
    pointerRunCount = tagPtrWords;
  } else if (elementSize == ElementSize::kPointer) {
    pointerRunStart = target;
    pointerRunCount = elementCount;
  }

  Region region{segmentId, target, billedWords};
  if (!entersObject(region)) {
    return false;
  }
  // Void lists (and any zero-word region) still bill one word here.
  if (!charge(billedWords)) {
    leaveObject();
    return false;
  }

  bool ok = true;
  if (elementSize == ElementSize::kComposite) {
    for (std::uint32_t element = 0;
         ok && element < compositeCount; ++element) {
      std::uint64_t elementBase =
          target + 1 +
          static_cast<std::uint64_t>(element) * compositeElementWords;
      for (std::uint64_t slot = 0;
           ok && slot < pointerRunCount; ++slot) {
        std::uint64_t slotIndex = elementBase + compositeDataWords + slot;
        std::uint64_t childWord = segment.words[slotIndex];
        if (childWord != 0) {
          ok = walkAt(segmentId, slotIndex, depth + 1, 0);
        }
      }
    }
  } else if (elementSize == ElementSize::kPointer) {
    for (std::uint64_t slot = 0;
         ok && slot < pointerRunCount; ++slot) {
      std::uint64_t slotIndex = pointerRunStart + slot;
      std::uint64_t childWord = segment.words[slotIndex];
      if (childWord != 0) {
        ok = walkAt(segmentId, slotIndex, depth + 1, 0);
      }
    }
  }

  leaveObject();
  return ok;
}

}  // namespace netsec
