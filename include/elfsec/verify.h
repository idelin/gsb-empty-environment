// -*- C++ -*-
// elfsec: zero-dependency ELF64 dynamic-loader security verification.
//
// The verifier consumes a raw byte buffer of an in-memory ELF64 object
// (ET_EXEC or ET_DYN) and derives every security decision exclusively from
// the ELF header and the program header table. Section headers are never
// consulted and therefore cannot be used to spoof hardening metadata.
#ifndef ELFSEC_VERIFY_H
#define ELFSEC_VERIFY_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace elfsec {

// Machine architectures that this verifier knows how to audit.
enum class Machine : std::uint16_t {
  kUnknown = 0,
  kX86_64 = 62,   // EM_X86_64
  kAArch64 = 183, // EM_AARCH64
};

// Every distinct finding produced by the verifier. Structural violations are
// fatal: verification stops at the first one because subsequent parsing would
// be untrustworthy. Policy violations are accumulated until parsing completes.
enum class ViolationCode : int {
  kNone = 0,

  // ---- Structural / parsing violations (fatal) ----
  kBufferTooSmall,
  kBadMagic,
  kNotElf64,
  kNotLittleEndian,
  kBadElfVersion,
  kBadElfType,
  kUnsupportedMachine,
  kProgramHeaderTableOutOfBounds,
  kProgramHeaderEntrySize,
  kProgramHeaderCountOverflow,
  kSegmentFileRangeOutOfBounds,
  kLoadFileszGreaterMemsz,
  kLoadSizeOverflow,
  kLoadVirtualPageOverlap,
  kLoadAlignNotPowerOfTwo,
  kLoadOffsetVaddrMisaligned,
  kDuplicateProgramHeader,
  kDynamicSegmentMissing,
  kDynamicSegmentNotInLoad,
  kDynamicEntryOutOfBounds,
  kDynamicEntryCountOverflow,
  kDynamicPointerUnmapped,
  kStringTableUnmapped,
  kSymbolTableUnmapped,
  kSymbolEntryOutOfBounds,
  kRelaTableUnmapped,
  kRelaEntryOutOfBounds,
  kVersionSymbolTableUnmapped,
  kVersionSymbolEntryOutOfBounds,
  kVersionNeedUnmapped,
  kVersionNeedMalformed,
  kInitArrayUnmapped,
  kInitArraySizeOverflow,
  kFiniArrayUnmapped,
  kFiniArraySizeOverflow,
  kInterpMalformed,

  // ---- Hardening policy violations ----
  kExecutableStack,
  kWritableExecutableSegment,
  kRelroSegmentMissing,
  kRelroNotPageAligned,
  kNoImmediateBinding,
  kRelroGapDynamic,
  kRelroGapInitArray,
  kRelroGapFiniArray,
  kRelroGapRelocationTarget,
  kUnwhitelistedIfuncResolver,
  kRpathRelativeOrEmpty,
  kRunpathRelativeOrEmpty,
  kSymbolVersionUnbound,
};

struct Violation {
  ViolationCode code = ViolationCode::kNone;
  std::string message;

  Violation() = default;
  Violation(ViolationCode c, std::string msg)
      : code(c), message(std::move(msg)) {}
};

struct VerifyOptions {
  // Target page size used for loader-style page rounding. Must be a power of
  // two; 4096 matches the common x86-64/aarch64 loader granularity.
  std::uint64_t page_size = 4096;

  // STT_GNU_IFUNC resolver symbol names that the host explicitly trusts.
  // Any other IFUNC resolver reachable through R_X86_64_IRELATIVE or present
  // in the symbol table is reported.
  std::vector<std::string> ifunc_whitelist;

  // When true, presence of a PT_INTERP segment is permitted (for ET_EXEC /
  // PIE executables). Shared libraries never carry one.
  bool allow_interpreter = true;
};

struct VerifyResult {
  bool ok = false;
  Machine machine = Machine::kUnknown;
  bool is_shared_object = false;
  std::vector<Violation> violations;

  // Diagnostic counters describing the parsed object.
  std::size_t load_segment_count = 0;
  std::size_t dynamic_entry_count = 0;
  std::size_t rela_entry_count = 0;
  std::size_t jmprel_entry_count = 0;
  bool bind_now = false;
  bool relro_present = false;

  const Violation* first_fatal() const;
  bool has(ViolationCode code) const;
};

// Verifies an ELF64 image held in a contiguous byte buffer. Never throws:
// malformed input always produces a structured VerifyResult instead.
VerifyResult verify_elf64(const std::uint8_t* data, std::size_t size,
                          const VerifyOptions& options = VerifyOptions{});

// Convenience overload owning no extra state.
VerifyResult verify_elf64(const std::vector<std::uint8_t>& bytes,
                          const VerifyOptions& options = VerifyOptions{});

const char* violation_name(ViolationCode code);

}  // namespace elfsec

#endif  // ELFSEC_VERIFY_H
