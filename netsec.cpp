#include "netsec.hpp"

namespace netsec {

namespace {

constexpr uint64_t kPointerTypeMask = 0x3;
constexpr uint32_t kMaxSegments = 1u << 29;  // far pointers carry a 29-bit segment id

// Signed 30-bit offset stored in bits 2..31 of struct/list pointers.
int32_t decodeOffset(uint64_t word) {
  int32_t raw = static_cast<int32_t>(static_cast<uint32_t>(word));
  raw &= ~static_cast<int32_t>(kPointerTypeMask);
  return raw / 4;  // raw is a multiple of 4, so truncation == arithmetic shift
}

uint64_t locationKey(uint32_t segment, uint32_t wordIndex) {
  return (static_cast<uint64_t>(segment) << 32) | wordIndex;
}

}  // namespace

const char* toString(GuardStatus status) {
  switch (status) {
    case GuardStatus::Ok: return "Ok";
    case GuardStatus::SegmentOutOfRange: return "SegmentOutOfRange";
    case GuardStatus::LandingPadOutOfBounds: return "LandingPadOutOfBounds";
    case GuardStatus::InvalidFarLanding: return "InvalidFarLanding";
    case GuardStatus::InvalidDoubleFarTag: return "InvalidDoubleFarTag";
    case GuardStatus::PointerOutOfBounds: return "PointerOutOfBounds";
    case GuardStatus::ObjectOutOfBounds: return "ObjectOutOfBounds";
    case GuardStatus::BudgetExhausted: return "BudgetExhausted";
    case GuardStatus::DuplicateReference: return "DuplicateReference";
    case GuardStatus::TrackingLimitExceeded: return "TrackingLimitExceeded";
  }
  return "Unknown";
}

TraversalGuard::TraversalGuard(const Segment* segments, size_t segmentCount,
                               GuardOptions options)
    : segments_(segments), segmentCount_(segmentCount), options_(options) {}

GuardStatus TraversalGuard::charge(uint64_t words) {
  if (words > options_.traversalWordBudget - wordsRead_) {
    return GuardStatus::BudgetExhausted;
  }
  wordsRead_ += words;
  return GuardStatus::Ok;
}

GuardStatus TraversalGuard::readWord(uint32_t segment, uint32_t wordIndex,
                                     uint64_t& out) const {
  if (segment >= segmentCount_) return GuardStatus::SegmentOutOfRange;
  if (wordIndex >= segments_[segment].wordCount) {
    return GuardStatus::PointerOutOfBounds;
  }
  out = segments_[segment].words[wordIndex];
  return GuardStatus::Ok;
}

bool TraversalGuard::rangeInSegment(uint32_t segment, int64_t start,
                                    uint64_t words) const {
  if (segment >= segmentCount_ || start < 0) return false;
  uint64_t size = segments_[segment].wordCount;
  return static_cast<uint64_t>(start) <= size && words <= size - static_cast<uint64_t>(start);
}

GuardStatus TraversalGuard::trackObject(uint32_t segment, uint32_t wordIndex) {
  uint64_t key = locationKey(segment, wordIndex);
  if (seen_.find(key) != seen_.end()) return GuardStatus::DuplicateReference;
  if (seen_.size() >= options_.maxTrackedObjects) {
    return GuardStatus::TrackingLimitExceeded;
  }
  seen_.insert(key);
  return GuardStatus::Ok;
}

GuardStatus TraversalGuard::traverse(uint32_t rootSegment, uint32_t rootWordIndex) {
  if (rootSegment >= segmentCount_) return GuardStatus::SegmentOutOfRange;
  if (rootWordIndex >= segments_[rootSegment].wordCount) {
    return GuardStatus::PointerOutOfBounds;
  }
  pending_.push_back(locationKey(rootSegment, rootWordIndex));
  while (!pending_.empty()) {
    uint64_t key = pending_.back();
    pending_.pop_back();
    GuardStatus status =
        processPointer(static_cast<uint32_t>(key >> 32), static_cast<uint32_t>(key));
    if (status != GuardStatus::Ok) return status;
  }
  return GuardStatus::Ok;
}

GuardStatus TraversalGuard::processPointer(uint32_t segment, uint32_t wordIndex) {
  uint64_t word = 0;
  GuardStatus status = readWord(segment, wordIndex, word);
  if (status != GuardStatus::Ok) return status;

  // The pointer word itself is always charged, even for zero-size structs,
  // Void lists and null pointers.
  status = charge(1);
  if (status != GuardStatus::Ok) return status;

  if (word == 0) return GuardStatus::Ok;  // null pointer

  switch (word & kPointerTypeMask) {
    case 0: {  // struct
      int64_t target = static_cast<int64_t>(wordIndex) + 1 + decodeOffset(word);
      uint64_t dataWords = (word >> 32) & 0xFFFF;
      uint64_t pointerCount = (word >> 48) & 0xFFFF;
      if (!rangeInSegment(segment, target, dataWords + pointerCount)) {
        return GuardStatus::ObjectOutOfBounds;
      }
      status = trackObject(segment, static_cast<uint32_t>(target));
      if (status != GuardStatus::Ok) return status;
      status = charge(dataWords + pointerCount);
      if (status != GuardStatus::Ok) return status;
      for (uint64_t i = 0; i < pointerCount; ++i) {
        pending_.push_back(locationKey(
            segment, static_cast<uint32_t>(target + dataWords + i)));
      }
      return GuardStatus::Ok;
    }
    case 1: {  // list
      int64_t target = static_cast<int64_t>(wordIndex) + 1 + decodeOffset(word);
      uint64_t elementSize = (word >> 32) & 0x7;
      uint64_t count = (word >> 35) & 0x1FFFFFFF;
      if (elementSize == 7) {  // composite: tag word + element words
        if (!rangeInSegment(segment, target, 1)) return GuardStatus::ObjectOutOfBounds;
        uint64_t tag = segments_[segment].words[target];
        if ((tag & kPointerTypeMask) != 0) return GuardStatus::ObjectOutOfBounds;
        uint64_t elementCount = static_cast<uint32_t>(tag) >> 2;
        uint64_t dataWords = (tag >> 32) & 0xFFFF;
        uint64_t pointersPerElement = (tag >> 48) & 0xFFFF;
        uint64_t bodyWords = elementCount * (dataWords + pointersPerElement);
        if (!rangeInSegment(segment, target + 1, bodyWords)) {
          return GuardStatus::ObjectOutOfBounds;
        }
        status = trackObject(segment, static_cast<uint32_t>(target));
        if (status != GuardStatus::Ok) return status;
        status = charge(1 + bodyWords);
        if (status != GuardStatus::Ok) return status;
        uint64_t pointerBase = static_cast<uint64_t>(target) + 1 +
                               elementCount * dataWords;
        for (uint64_t i = 0; i < elementCount * pointersPerElement; ++i) {
          pending_.push_back(
              locationKey(segment, static_cast<uint32_t>(pointerBase + i)));
        }
        return GuardStatus::Ok;
      }
      // Word count per element size: Void=0, Bit=ceil(n/64), 1/2/4/8-byte and
      // pointer elements scale directly.
      uint64_t bodyWords = 0;
      switch (elementSize) {
        case 0: bodyWords = 0; break;                          // Void
        case 1: bodyWords = (count + 63) / 64; break;          // Bit
        case 2: bodyWords = (count + 7) / 8; break;            // Byte
        case 3: bodyWords = (count + 3) / 4; break;            // TwoBytes
        case 4: bodyWords = (count + 1) / 2; break;            // FourBytes
        case 5: bodyWords = count; break;                      // EightBytes
        case 6: bodyWords = count; break;                      // Pointer
      }
      if (!rangeInSegment(segment, target, bodyWords)) {
        return GuardStatus::ObjectOutOfBounds;
      }
      status = trackObject(segment, static_cast<uint32_t>(target));
      if (status != GuardStatus::Ok) return status;
      status = charge(bodyWords);
      if (status != GuardStatus::Ok) return status;
      if (elementSize == 6) {
        for (uint64_t i = 0; i < count; ++i) {
          pending_.push_back(
              locationKey(segment, static_cast<uint32_t>(target + i)));
        }
      }
      return GuardStatus::Ok;
    }
    case 2:  // far
      return followFar(word);
    default:  // capability / other: no message words to traverse
      return GuardStatus::Ok;
  }
}

GuardStatus TraversalGuard::followFar(uint64_t word) {
  bool doubleFar = ((word >> 2) & 1) != 0;
  uint32_t padOffset = static_cast<uint32_t>((word >> 3) & 0x1FFFFFFF);
  uint32_t segmentId = static_cast<uint32_t>(word >> 32);

  if (segmentId >= segmentCount_ || segmentId >= kMaxSegments) {
    return GuardStatus::SegmentOutOfRange;
  }
  uint64_t padWords = doubleFar ? 2 : 1;
  if (!rangeInSegment(segmentId, padOffset, padWords)) {
    return GuardStatus::LandingPadOutOfBounds;
  }
  GuardStatus status = charge(padWords);
  if (status != GuardStatus::Ok) return status;

  const uint64_t* pad = segments_[segmentId].words + padOffset;
  if (!doubleFar) {
    // Single far: the landing pad word is a regular pointer interpreted
    // relative to its own location. It must not itself be a far pointer.
    if ((pad[0] & kPointerTypeMask) == 2) return GuardStatus::InvalidFarLanding;
    pending_.push_back(locationKey(segmentId, padOffset));
    return GuardStatus::Ok;
  }

  // Double far: pad[0] is a single-far pointer to the content, pad[1] is a
  // tag word which must be a struct pointer describing that content.
  uint64_t contentFar = pad[0];
  uint64_t tag = pad[1];
  if ((contentFar & kPointerTypeMask) != 2 || ((contentFar >> 2) & 1) != 0) {
    return GuardStatus::InvalidFarLanding;
  }
  if ((tag & kPointerTypeMask) != 0) return GuardStatus::InvalidDoubleFarTag;

  uint32_t contentSegment = static_cast<uint32_t>(contentFar >> 32);
  uint32_t contentOffset = static_cast<uint32_t>((contentFar >> 3) & 0x1FFFFFFF);
  if (contentSegment >= segmentCount_) return GuardStatus::SegmentOutOfRange;

  uint64_t dataWords = (tag >> 32) & 0xFFFF;
  uint64_t pointerCount = (tag >> 48) & 0xFFFF;
  if (!rangeInSegment(contentSegment, contentOffset, dataWords + pointerCount)) {
    return GuardStatus::ObjectOutOfBounds;
  }
  status = trackObject(contentSegment, contentOffset);
  if (status != GuardStatus::Ok) return status;
  status = charge(dataWords + pointerCount);
  if (status != GuardStatus::Ok) return status;
  for (uint64_t i = 0; i < pointerCount; ++i) {
    pending_.push_back(locationKey(
        contentSegment, static_cast<uint32_t>(contentOffset + dataWords + i)));
  }
  return GuardStatus::Ok;
}

}  // namespace netsec
