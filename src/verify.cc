#include "elfsec/verify.h"

#include <algorithm>
#include <cstring>
#include <limits>
#include <set>
#include <string>
#include <utility>

namespace elfsec {
namespace {

// ---------------------------------------------------------------------------
// ELF64 on-disk structures. The ELF64 ABI fixes every type width and the
// structures contain no padding on the supported ABIs, so fixed-width
// standard-layout representations plus memcpy() avoid host alignment and
// endianness assumptions.
// ---------------------------------------------------------------------------

using Elf64_Half = std::uint16_t;
using Elf64_Word = std::uint32_t;
using Elf64_Sword = std::int32_t;
using Elf64_Xword = std::uint64_t;
using Elf64_Sxword = std::int64_t;
using Elf64_Addr = std::uint64_t;
using Elf64_Off = std::uint64_t;

constexpr unsigned char ELFCLASS64 = 2;
constexpr unsigned char ELFDATA2LSB = 1;
constexpr unsigned char EV_CURRENT = 1;

constexpr Elf64_Half ET_EXEC = 2;
constexpr Elf64_Half ET_DYN = 3;
constexpr Elf64_Half EM_X86_64_VAL = 62;
constexpr Elf64_Half EM_AARCH64_VAL = 183;

constexpr Elf64_Word PT_NULL = 0;
constexpr Elf64_Word PT_LOAD = 1;
constexpr Elf64_Word PT_DYNAMIC = 2;
constexpr Elf64_Word PT_INTERP = 3;
constexpr Elf64_Word PT_GNU_STACK = 0x6474e551;
constexpr Elf64_Word PT_GNU_RELRO = 0x6474e552;

constexpr Elf64_Word PF_X = 1;
constexpr Elf64_Word PF_W = 2;

constexpr Elf64_Sxword DT_NULL = 0;
constexpr Elf64_Sxword DT_PLTRELSZ = 2;
constexpr Elf64_Sxword DT_RELA = 7;
constexpr Elf64_Sxword DT_RELASZ = 8;
constexpr Elf64_Sxword DT_STRTAB = 5;
constexpr Elf64_Sxword DT_SYMTAB = 6;
constexpr Elf64_Sxword DT_STRSZ = 10;
constexpr Elf64_Sxword DT_SYMENT = 11;
constexpr Elf64_Sxword DT_RPATH = 15;
constexpr Elf64_Sxword DT_PLTREL = 20;
constexpr Elf64_Sxword DT_JMPREL = 23;
constexpr Elf64_Sxword DT_BIND_NOW = 24;
constexpr Elf64_Sxword DT_INIT_ARRAY = 25;
constexpr Elf64_Sxword DT_FINI_ARRAY = 26;
constexpr Elf64_Sxword DT_INIT_ARRAYSZ = 27;
constexpr Elf64_Sxword DT_FINI_ARRAYSZ = 28;
constexpr Elf64_Sxword DT_RUNPATH = 29;
constexpr Elf64_Sxword DT_FLAGS = 30;
constexpr Elf64_Sxword DT_VERSYM = 0x6ffffff0;
constexpr Elf64_Sxword DT_FLAGS_1 = 0x6ffffffb;
constexpr Elf64_Sxword DT_VERNEED = 0x6ffffffe;
constexpr Elf64_Sxword DT_VERNEEDNUM = 0x6fffffff;

constexpr Elf64_Xword DF_BIND_NOW = 0x8;
constexpr Elf64_Xword DF_1_NOW = 0x1;

constexpr unsigned char STT_GNU_IFUNC = 10;
constexpr unsigned char STT_TYPE_MASK = 0x0f;

constexpr Elf64_Word R_X86_64_GLOB_DAT = 6;
constexpr Elf64_Word R_X86_64_JUMP_SLOT = 7;
constexpr Elf64_Word R_X86_64_RELATIVE = 8;
constexpr Elf64_Word R_X86_64_IRELATIVE = 37;

constexpr Elf64_Word R_AARCH64_GLOB_DAT = 0x401;
constexpr Elf64_Word R_AARCH64_JUMP_SLOT = 0x402;
constexpr Elf64_Word R_AARCH64_RELATIVE = 0x403;
constexpr Elf64_Word R_AARCH64_IRELATIVE = 0x408;

constexpr Elf64_Half VER_NDX_GLOBAL = 1;
constexpr Elf64_Half VER_HIDDEN = 0x8000;

struct Elf64_Ehdr {
  unsigned char e_ident[16];
  Elf64_Half e_type;
  Elf64_Half e_machine;
  Elf64_Word e_version;
  Elf64_Addr e_entry;
  Elf64_Off e_phoff;
  Elf64_Off e_shoff;
  Elf64_Word e_flags;
  Elf64_Half e_ehsize;
  Elf64_Half e_phentsize;
  Elf64_Half e_phnum;
  Elf64_Half e_shentsize;
  Elf64_Half e_shnum;
  Elf64_Half e_shstrndx;
};

struct Elf64_Phdr {
  Elf64_Word p_type;
  Elf64_Word p_flags;
  Elf64_Off p_offset;
  Elf64_Addr p_vaddr;
  Elf64_Addr p_paddr;
  Elf64_Xword p_filesz;
  Elf64_Xword p_memsz;
  Elf64_Xword p_align;
};

struct Elf64_Dyn {
  Elf64_Sxword d_tag;
  Elf64_Xword d_val;
};

struct Elf64_Sym {
  Elf64_Word st_name;
  unsigned char st_info;
  unsigned char st_other;
  Elf64_Half st_shndx;
  Elf64_Addr st_value;
  Elf64_Xword st_size;
};

struct Elf64_Rela {
  Elf64_Addr r_offset;
  Elf64_Xword r_info;
  Elf64_Sxword r_addend;
};

struct Elf64_Verneed {
  Elf64_Half vn_version;
  Elf64_Half vn_cnt;
  Elf64_Word vn_file;
  Elf64_Word vn_aux;
  Elf64_Word vn_next;
};

struct Elf64_Vernaux {
  Elf64_Word vna_hash;
  Elf64_Half vna_flags;
  Elf64_Half vna_other;
  Elf64_Word vna_name;
  Elf64_Word vna_next;
};

static_assert(sizeof(Elf64_Ehdr) == 64, "Elf64_Ehdr size");
static_assert(sizeof(Elf64_Phdr) == 56, "Elf64_Phdr size");
static_assert(sizeof(Elf64_Dyn) == 16, "Elf64_Dyn size");
static_assert(sizeof(Elf64_Sym) == 24, "Elf64_Sym size");
static_assert(sizeof(Elf64_Rela) == 24, "Elf64_Rela size");
static_assert(sizeof(Elf64_Verneed) == 16, "Elf64_Verneed size");
static_assert(sizeof(Elf64_Vernaux) == 16, "Elf64_Vernaux size");

struct LoadSegment {
  Elf64_Addr vaddr = 0;
  Elf64_Off offset = 0;
  Elf64_Xword filesz = 0;
  Elf64_Xword memsz = 0;
  Elf64_Word flags = 0;
};

struct AddrRange {
  Elf64_Addr start = 0;
  Elf64_Xword size = 0;
};

bool checked_add(std::uint64_t a, std::uint64_t b, std::uint64_t* out) {
  return !__builtin_add_overflow(a, b, out);
}

bool checked_mul(std::uint64_t a, std::uint64_t b, std::uint64_t* out) {
  return !__builtin_mul_overflow(a, b, out);
}

bool is_power_of_two(std::uint64_t value) {
  return value != 0 && (value & (value - 1)) == 0;
}

template <typename T>
bool read_struct(const std::uint8_t* data, std::size_t size, std::uint64_t offset,
                 T* out) {
  std::uint64_t end = 0;
  if (!checked_add(offset, static_cast<std::uint64_t>(sizeof(T)), &end)) {
    return false;
  }
  if (end > size) {
    return false;
  }
  std::memcpy(out, data + offset, sizeof(T));
  return true;
}

class Verifier {
 public:
  Verifier(const std::uint8_t* data, std::size_t size, VerifyOptions options)
      : m_data(data), m_size(size), m_options(std::move(options)) {
    if (!is_power_of_two(m_options.page_size)) {
      m_options.page_size = 4096;
    }
  }

  VerifyResult run() {
    VerifyResult result;
    if (!parse_header() || !parse_program_headers() || !parse_dynamic()) {
      return finish(std::move(result));
    }
    evaluate_policy();
    return finish(std::move(result));
  }

 private:
  const std::uint8_t* m_data;
  std::size_t m_size;
  VerifyOptions m_options;
  std::vector<Violation> m_violations;
  bool m_fatal = false;

  Machine m_machine = Machine::kUnknown;
  bool m_is_dyn = false;
  std::vector<LoadSegment> m_loads;
  bool m_have_dynamic_phdr = false;
  Elf64_Phdr m_dynamic_phdr{};
  bool m_have_relro = false;
  Elf64_Phdr m_relro_phdr{};
  bool m_have_stack = false;
  Elf64_Phdr m_stack_phdr{};
  bool m_have_interp = false;

  struct DynTag {
    Elf64_Sxword tag;
    Elf64_Xword value;
  };
  std::vector<DynTag> m_dyn;

  Elf64_Addr m_strtab = 0;
  Elf64_Xword m_strsz = 0;
  Elf64_Addr m_symtab = 0;
  Elf64_Addr m_versym = 0;
  bool m_have_versym = false;
  std::set<Elf64_Half> m_defined_versions;
  std::size_t m_verneed_count = 0;
  std::vector<AddrRange> m_relro_targets;
  std::vector<AddrRange> m_init_ranges;
  std::vector<AddrRange> m_fini_ranges;
  bool m_bind_now = false;
  std::size_t m_rela_count = 0;
  std::size_t m_jmprel_count = 0;

  void fail(ViolationCode code, const std::string& message) {
    m_violations.emplace_back(code, message);
    m_fatal = true;
  }

  void policy(ViolationCode code, const std::string& message) {
    m_violations.emplace_back(code, message);
  }

  bool va_to_offset(Elf64_Addr va, Elf64_Xword size, std::uint64_t* offset) const {
    if (size == 0) {
      *offset = 0;
      return true;
    }
    Elf64_Addr end = 0;
    if (!checked_add(va, size, &end)) {
      return false;
    }
    for (const LoadSegment& seg : m_loads) {
      Elf64_Addr seg_end = 0;
      if (!checked_add(seg.vaddr, seg.filesz, &seg_end)) {
        continue;
      }
      if (va >= seg.vaddr && end <= seg_end) {
        std::uint64_t candidate = 0;
        if (!checked_add(seg.offset,
                         static_cast<std::uint64_t>(va - seg.vaddr),
                         &candidate)) {
          return false;
        }
        std::uint64_t file_end = 0;
        if (!checked_add(candidate, size, &file_end) || file_end > m_size) {
          return false;
        }
        *offset = candidate;
        return true;
      }
    }
    return false;
  }

  template <typename T>
  bool read_va(Elf64_Addr va, T* out) const {
    std::uint64_t offset = 0;
    if (!va_to_offset(va, static_cast<Elf64_Xword>(sizeof(T)), &offset)) {
      return false;
    }
    return read_struct(m_data, m_size, offset, out);
  }

  bool range_mapped(Elf64_Addr va, Elf64_Xword size) const {
    std::uint64_t offset = 0;
    return va_to_offset(va, size, &offset);
  }

  bool read_dynstr(Elf64_Word index, std::string* out) const {
    if (m_strtab == 0 || static_cast<Elf64_Xword>(index) >= m_strsz) {
      return false;
    }
    Elf64_Addr addr = 0;
    if (!checked_add(m_strtab, static_cast<Elf64_Xword>(index), &addr)) {
      return false;
    }
    Elf64_Xword remaining = m_strsz - static_cast<Elf64_Xword>(index);
    std::uint64_t offset = 0;
    if (!va_to_offset(addr, remaining, &offset)) {
      return false;
    }
    const std::uint8_t* begin = m_data + offset;
    const std::uint8_t* end = begin + remaining;
    const std::uint8_t* nul = begin;
    while (nul < end && *nul != 0) {
      ++nul;
    }
    if (nul == end) {
      return false;
    }
    out->assign(reinterpret_cast<const char*>(begin),
                static_cast<std::size_t>(nul - begin));
    return true;
  }

  bool parse_header() {
    Elf64_Ehdr eh{};
    if (!read_struct(m_data, m_size, 0, &eh)) {
      fail(ViolationCode::kBufferTooSmall, "buffer smaller than ELF64 header");
      return false;
    }
    static const unsigned char magic[4] = {0x7f, 'E', 'L', 'F'};
    if (std::memcmp(eh.e_ident, magic, 4) != 0) {
      fail(ViolationCode::kBadMagic, "missing 0x7fELF magic");
      return false;
    }
    if (eh.e_ident[4] != ELFCLASS64) {
      fail(ViolationCode::kNotElf64, "only ELFCLASS64 objects are supported");
      return false;
    }
    if (eh.e_ident[5] != ELFDATA2LSB) {
      fail(ViolationCode::kNotLittleEndian,
           "only little-endian objects are supported");
      return false;
    }
    if (eh.e_ident[6] != EV_CURRENT || eh.e_version != EV_CURRENT) {
      fail(ViolationCode::kBadElfVersion, "unexpected ELF version");
      return false;
    }
    if (eh.e_type != ET_EXEC && eh.e_type != ET_DYN) {
      fail(ViolationCode::kBadElfType, "only ET_EXEC and ET_DYN are auditable");
      return false;
    }
    m_is_dyn = (eh.e_type == ET_DYN);
    if (eh.e_machine == EM_X86_64_VAL) {
      m_machine = Machine::kX86_64;
    } else if (eh.e_machine == EM_AARCH64_VAL) {
      m_machine = Machine::kAArch64;
    } else {
      fail(ViolationCode::kUnsupportedMachine, "unsupported e_machine");
      return false;
    }
    if (eh.e_phentsize != sizeof(Elf64_Phdr) || eh.e_phnum == 0) {
      fail(ViolationCode::kProgramHeaderEntrySize,
           "invalid program header entry size or zero count");
      return false;
    }
    std::uint64_t table_size = 0;
    if (!checked_mul(static_cast<std::uint64_t>(eh.e_phentsize),
                     static_cast<std::uint64_t>(eh.e_phnum), &table_size)) {
      fail(ViolationCode::kProgramHeaderCountOverflow,
           "program header table size wraps");
      return false;
    }
    std::uint64_t table_end = 0;
    if (!checked_add(eh.e_phoff, table_size, &table_end) || table_end > m_size) {
      fail(ViolationCode::kProgramHeaderTableOutOfBounds,
           "program header table exceeds buffer");
      return false;
    }
    return true;
  }

  bool file_range_valid(Elf64_Off offset, Elf64_Xword size) const {
    std::uint64_t end = 0;
    if (!checked_add(offset, size, &end)) {
      return false;
    }
    return end <= m_size;
  }

  bool validate_interp(const Elf64_Phdr& ph) {
    if (ph.p_filesz == 0 || !file_range_valid(ph.p_offset, ph.p_filesz)) {
      fail(ViolationCode::kInterpMalformed, "PT_INTERP range invalid");
      return false;
    }
    std::uint64_t end = 0;
    if (!checked_add(ph.p_offset, ph.p_filesz, &end)) {
      fail(ViolationCode::kInterpMalformed, "PT_INTERP size wraps");
      return false;
    }
    if (m_data[end - 1] != 0) {
      fail(ViolationCode::kInterpMalformed,
           "PT_INTERP string not NUL terminated");
      return false;
    }
    return true;
  }

  bool handle_load(const Elf64_Phdr& ph) {
    if (ph.p_filesz > ph.p_memsz) {
      fail(ViolationCode::kLoadFileszGreaterMemsz, "p_filesz exceeds p_memsz");
      return false;
    }
    std::uint64_t mem_end = 0;
    std::uint64_t file_end = 0;
    if (!checked_add(ph.p_vaddr, ph.p_memsz, &mem_end) ||
        !checked_add(ph.p_offset, ph.p_filesz, &file_end) || file_end > m_size) {
      fail(ViolationCode::kLoadSizeOverflow,
           "PT_LOAD range overflow or file range out of bounds");
      return false;
    }
    if (ph.p_align != 0 && ph.p_align != 1 && !is_power_of_two(ph.p_align)) {
      fail(ViolationCode::kLoadAlignNotPowerOfTwo,
           "p_align not 0, 1 or a power of two");
      return false;
    }
    if (ph.p_align > 1 &&
        (ph.p_vaddr % ph.p_align) != (ph.p_offset % ph.p_align)) {
      fail(ViolationCode::kLoadOffsetVaddrMisaligned,
           "p_vaddr and p_offset disagree modulo p_align");
      return false;
    }
    if ((ph.p_flags & (PF_W | PF_X)) == (PF_W | PF_X)) {
      policy(ViolationCode::kWritableExecutableSegment,
             "PT_LOAD segment is simultaneously writable and executable");
    }
    m_loads.push_back(LoadSegment{ph.p_vaddr, ph.p_offset, ph.p_filesz,
                                  ph.p_memsz, ph.p_flags});
    return true;
  }

  bool check_load_overlap() {
    std::vector<LoadSegment> ordered = m_loads;
    std::sort(ordered.begin(), ordered.end(),
              [](const LoadSegment& a, const LoadSegment& b) {
                return a.vaddr < b.vaddr;
              });
    for (std::size_t i = 1; i < ordered.size(); ++i) {
      const LoadSegment& prev = ordered[i - 1];
      const LoadSegment& cur = ordered[i];
      std::uint64_t prev_end = 0;
      if (!checked_add(prev.vaddr, prev.memsz, &prev_end)) {
        fail(ViolationCode::kLoadSizeOverflow, "PT_LOAD memory range wraps");
        return false;
      }
      if (cur.vaddr < prev_end) {
        fail(ViolationCode::kLoadVirtualPageOverlap,
             "PT_LOAD virtual address ranges overlap");
        return false;
      }
    }
    return true;
  }

  bool parse_program_headers() {
    Elf64_Ehdr eh{};
    if (!read_struct(m_data, m_size, 0, &eh)) {
      fail(ViolationCode::kBufferTooSmall, "ELF header vanished");
      return false;
    }
    bool seen_dynamic = false;
    bool seen_relro = false;
    bool seen_stack = false;
    bool seen_interp = false;

    for (Elf64_Half index = 0; index < eh.e_phnum; ++index) {
      std::uint64_t ph_offset = 0;
      if (!checked_mul(static_cast<std::uint64_t>(index),
                       static_cast<std::uint64_t>(eh.e_phentsize),
                       &ph_offset) ||
          !checked_add(eh.e_phoff, ph_offset, &ph_offset)) {
        fail(ViolationCode::kProgramHeaderCountOverflow, "phdr index overflow");
        return false;
      }
      Elf64_Phdr ph{};
      if (!read_struct(m_data, m_size, ph_offset, &ph)) {
        fail(ViolationCode::kProgramHeaderTableOutOfBounds, "phdr read failed");
        return false;
      }
      if (ph.p_type == PT_NULL) {
        continue;
      }
      if (ph.p_type != PT_LOAD && ph.p_filesz != 0 &&
          !file_range_valid(ph.p_offset, ph.p_filesz)) {
        fail(ViolationCode::kSegmentFileRangeOutOfBounds,
             "segment file range exceeds buffer");
        return false;
      }
      switch (ph.p_type) {
        case PT_LOAD:
          if (!handle_load(ph)) {
            return false;
          }
          break;
        case PT_DYNAMIC:
          if (seen_dynamic) {
            fail(ViolationCode::kDuplicateProgramHeader, "duplicate PT_DYNAMIC");
            return false;
          }
          seen_dynamic = true;
          m_have_dynamic_phdr = true;
          m_dynamic_phdr = ph;
          break;
        case PT_GNU_RELRO:
          if (seen_relro) {
            fail(ViolationCode::kDuplicateProgramHeader,
                 "duplicate PT_GNU_RELRO");
            return false;
          }
          seen_relro = true;
          m_have_relro = true;
          m_relro_phdr = ph;
          break;
        case PT_GNU_STACK:
          if (seen_stack) {
            fail(ViolationCode::kDuplicateProgramHeader,
                 "duplicate PT_GNU_STACK");
            return false;
          }
          seen_stack = true;
          m_have_stack = true;
          m_stack_phdr = ph;
          break;
        case PT_INTERP:
          if (seen_interp) {
            fail(ViolationCode::kDuplicateProgramHeader, "duplicate PT_INTERP");
            return false;
          }
          seen_interp = true;
          m_have_interp = true;
          if (!validate_interp(ph)) {
            return false;
          }
          break;
        default:
          break;
      }
    }

    if (!check_load_overlap()) {
      return false;
    }
    if (!m_have_dynamic_phdr) {
      fail(ViolationCode::kDynamicSegmentMissing, "no PT_DYNAMIC segment");
      return false;
    }
    if (!range_mapped(m_dynamic_phdr.p_vaddr, m_dynamic_phdr.p_memsz)) {
      fail(ViolationCode::kDynamicSegmentNotInLoad,
           "PT_DYNAMIC virtual range is not backed by PT_LOAD");
      return false;
    }
    if (m_have_relro &&
        (!range_mapped(m_relro_phdr.p_vaddr, m_relro_phdr.p_memsz) ||
         !file_range_valid(m_relro_phdr.p_offset,
                           m_relro_phdr.p_filesz))) {
      fail(ViolationCode::kSegmentFileRangeOutOfBounds,
           "PT_GNU_RELRO range exceeds buffer or is not backed by PT_LOAD");
      return false;
    }
    return true;
  }

  const DynTag* find_tag(Elf64_Sxword tag) const {
    for (const DynTag& entry : m_dyn) {
      if (entry.tag == tag) {
        return &entry;
      }
    }
    return nullptr;
  }

  bool require_mapped(Elf64_Addr va, Elf64_Xword size, ViolationCode code,
                      const std::string& message) {
    if (!range_mapped(va, size)) {
      fail(code, message);
      return false;
    }
    return true;
  }

  bool parse_dynamic() {
    const Elf64_Xword dyn_bytes = m_dynamic_phdr.p_memsz;
    if (dyn_bytes == 0 || (dyn_bytes % sizeof(Elf64_Dyn)) != 0) {
      fail(ViolationCode::kDynamicEntryOutOfBounds,
           "PT_DYNAMIC size is zero or not a multiple of Elf64_Dyn");
      return false;
    }
    std::uint64_t count64 = dyn_bytes / sizeof(Elf64_Dyn);
    if (count64 > (std::numeric_limits<std::size_t>::max)() / sizeof(DynTag)) {
      fail(ViolationCode::kDynamicEntryCountOverflow, "dynamic count overflow");
      return false;
    }
    const std::size_t count = static_cast<std::size_t>(count64);
    m_dyn.reserve(count);
    bool terminated = false;
    for (std::size_t i = 0; i < count; ++i) {
      Elf64_Addr entry_va = 0;
      if (!checked_add(m_dynamic_phdr.p_vaddr,
                       static_cast<Elf64_Xword>(i * sizeof(Elf64_Dyn)),
                       &entry_va)) {
        fail(ViolationCode::kDynamicEntryCountOverflow,
             "dynamic entry address wraps");
        return false;
      }
      Elf64_Dyn dyn{};
      if (!read_va(entry_va, &dyn)) {
        fail(ViolationCode::kDynamicEntryOutOfBounds,
             "dynamic entry not readable through PT_LOAD mapping");
        return false;
      }
      if (dyn.d_tag == DT_NULL) {
        terminated = true;
        break;
      }
      m_dyn.push_back(DynTag{dyn.d_tag, dyn.d_val});
    }
    if (!terminated) {
      fail(ViolationCode::kDynamicEntryOutOfBounds,
           "PT_DYNAMIC missing DT_NULL terminator");
      return false;
    }
    if (!collect_string_table()) {
      return false;
    }
    if (!collect_symbol_table()) {
      return false;
    }
    if (!collect_version_metadata()) {
      return false;
    }
    if (!collect_init_fini_arrays()) {
      return false;
    }
    if (!collect_relocations()) {
      return false;
    }
    evaluate_binding();
    evaluate_path(DT_RPATH);
    evaluate_path(DT_RUNPATH);
    return !m_fatal;
  }

  bool collect_string_table() {
    const DynTag* strtab = find_tag(DT_STRTAB);
    const DynTag* strsz = find_tag(DT_STRSZ);
    if (strtab == nullptr || strsz == nullptr) {
      fail(ViolationCode::kStringTableUnmapped,
           "DT_STRTAB/DT_STRSZ missing from dynamic section");
      return false;
    }
    m_strtab = strtab->value;
    m_strsz = strsz->value;
    if (!require_mapped(m_strtab, m_strsz,
                        ViolationCode::kStringTableUnmapped,
                        "DT_STRTAB range is not file-backed")) {
      return false;
    }
    return true;
  }

  bool collect_symbol_table() {
    const DynTag* symtab = find_tag(DT_SYMTAB);
    const DynTag* syment = find_tag(DT_SYMENT);
    if (symtab == nullptr) {
      fail(ViolationCode::kSymbolTableUnmapped, "DT_SYMTAB missing");
      return false;
    }
    if (syment != nullptr && syment->value != sizeof(Elf64_Sym)) {
      fail(ViolationCode::kSymbolTableUnmapped,
           "DT_SYMENT does not match Elf64_Sym width");
      return false;
    }
    m_symtab = symtab->value;
    return true;
  }

  bool symbol_at(Elf64_Word index, Elf64_Sym* sym) const {
    std::uint64_t byte_offset = 0;
    if (!checked_mul(static_cast<std::uint64_t>(index),
                     static_cast<std::uint64_t>(sizeof(Elf64_Sym)),
                     &byte_offset)) {
      return false;
    }
    Elf64_Addr addr = 0;
    if (!checked_add(m_symtab, byte_offset, &addr)) {
      return false;
    }
    return read_va(addr, sym);
  }

  bool collect_version_metadata() {
    const DynTag* versym = find_tag(DT_VERSYM);
    if (versym != nullptr) {
      m_have_versym = true;
      m_versym = versym->value;
    }
    const DynTag* verneed = find_tag(DT_VERNEED);
    if (verneed != nullptr) {
      if (!parse_verneed(verneed->value)) {
        return false;
      }
      if (const DynTag* need_num = find_tag(DT_VERNEEDNUM);
          need_num != nullptr &&
          need_num->value != static_cast<Elf64_Xword>(m_verneed_count)) {
        fail(ViolationCode::kVersionNeedMalformed,
             "DT_VERNEEDNUM disagrees with verneed chain length");
        return false;
      }
    }
    return true;
  }

  bool parse_verneed(Elf64_Addr start) {
    Elf64_Addr current = start;
    std::size_t hops = 0;
    constexpr std::size_t kMaxHops = 1 << 20;
    do {
      if (hops++ > kMaxHops) {
        fail(ViolationCode::kVersionNeedMalformed,
             "DT_VERNEED chain does not terminate");
        return false;
      }
      Elf64_Verneed need{};
      if (!read_va(current, &need)) {
        fail(ViolationCode::kVersionNeedUnmapped,
             "verneed record is not file-backed");
        return false;
      }
      std::string file;
      if (!read_dynstr(need.vn_file, &file)) {
        fail(ViolationCode::kVersionNeedMalformed,
             "verneed vn_file string unreadable");
        return false;
      }
      ++m_verneed_count;
      if (need.vn_aux == 0) {
        fail(ViolationCode::kVersionNeedMalformed, "verneed has no aux records");
        return false;
      }
      Elf64_Addr aux_addr = 0;
      if (!checked_add(current, static_cast<Elf64_Addr>(need.vn_aux),
                       &aux_addr)) {
        fail(ViolationCode::kVersionNeedMalformed, "verneed aux offset wraps");
        return false;
      }
      std::size_t aux_hops = 0;
      for (Elf64_Half aux_index = 0; aux_index < need.vn_cnt; ++aux_index) {
        if (aux_hops++ > kMaxHops) {
          fail(ViolationCode::kVersionNeedMalformed,
               "vernaux chain does not terminate");
          return false;
        }
        Elf64_Vernaux aux{};
        if (!read_va(aux_addr, &aux)) {
          fail(ViolationCode::kVersionNeedUnmapped,
               "vernaux record is not file-backed");
          return false;
        }
        std::string name;
        if (!read_dynstr(aux.vna_name, &name)) {
          fail(ViolationCode::kVersionNeedMalformed,
               "vernaux vna_name string unreadable");
          return false;
        }
        if (aux.vna_other == 0 || (aux.vna_other & VER_HIDDEN) != 0) {
          fail(ViolationCode::kVersionNeedMalformed,
               "vernaux carries invalid version index");
          return false;
        }
        m_defined_versions.insert(static_cast<Elf64_Half>(aux.vna_other));
        if (aux.vna_next == 0) {
          break;
        }
        Elf64_Addr next = 0;
        if (!checked_add(aux_addr, static_cast<Elf64_Addr>(aux.vna_next),
                         &next)) {
          fail(ViolationCode::kVersionNeedMalformed, "vernaux next wraps");
          return false;
        }
        aux_addr = next;
      }
      if (need.vn_next == 0) {
        return true;
      }
      Elf64_Addr next_need = 0;
      if (!checked_add(current, static_cast<Elf64_Addr>(need.vn_next),
                       &next_need)) {
        fail(ViolationCode::kVersionNeedMalformed, "verneed next wraps");
        return false;
      }
      current = next_need;
    } while (true);
  }

  bool collect_init_fini_arrays() {
    const DynTag* init = find_tag(DT_INIT_ARRAY);
    const DynTag* init_sz = find_tag(DT_INIT_ARRAYSZ);
    const DynTag* fini = find_tag(DT_FINI_ARRAY);
    const DynTag* fini_sz = find_tag(DT_FINI_ARRAYSZ);
    if (init != nullptr) {
      Elf64_Xword size = init_sz != nullptr ? init_sz->value : 0;
      if (!require_mapped(init->value, size, ViolationCode::kInitArrayUnmapped,
                          "DT_INIT_ARRAY range is not file-backed")) {
        return false;
      }
      if ((size % sizeof(Elf64_Addr)) != 0) {
        fail(ViolationCode::kInitArraySizeOverflow,
             "DT_INIT_ARRAYSZ not a multiple of pointer width");
        return false;
      }
      m_init_ranges.push_back(AddrRange{init->value, size});
    }
    if (fini != nullptr) {
      Elf64_Xword size = fini_sz != nullptr ? fini_sz->value : 0;
      if (!require_mapped(fini->value, size, ViolationCode::kFiniArrayUnmapped,
                          "DT_FINI_ARRAY range is not file-backed")) {
        return false;
      }
      if ((size % sizeof(Elf64_Addr)) != 0) {
        fail(ViolationCode::kFiniArraySizeOverflow,
             "DT_FINI_ARRAYSZ not a multiple of pointer width");
        return false;
      }
      m_fini_ranges.push_back(AddrRange{fini->value, size});
    }
    return true;
  }

  bool is_covered_type(Elf64_Word type) const {
    if (m_machine == Machine::kX86_64) {
      return type == R_X86_64_GLOB_DAT || type == R_X86_64_JUMP_SLOT ||
             type == R_X86_64_RELATIVE || type == R_X86_64_IRELATIVE;
    }
    return type == R_AARCH64_GLOB_DAT || type == R_AARCH64_JUMP_SLOT ||
           type == R_AARCH64_RELATIVE || type == R_AARCH64_IRELATIVE;
  }

  bool is_irelative(Elf64_Word type) const {
    return m_machine == Machine::kX86_64 ? type == R_X86_64_IRELATIVE
                                         : type == R_AARCH64_IRELATIVE;
  }

  bool is_symbol_relocation(Elf64_Word type) const {
    if (m_machine == Machine::kX86_64) {
      return type == R_X86_64_GLOB_DAT || type == R_X86_64_JUMP_SLOT;
    }
    return type == R_AARCH64_GLOB_DAT || type == R_AARCH64_JUMP_SLOT;
  }

  bool versym_at(Elf64_Word symbol_index, Elf64_Half* version) const {
    std::uint64_t byte_offset = 0;
    if (!checked_mul(static_cast<std::uint64_t>(symbol_index),
                     static_cast<std::uint64_t>(sizeof(Elf64_Half)),
                     &byte_offset)) {
      return false;
    }
    Elf64_Addr addr = 0;
    if (!checked_add(m_versym, byte_offset, &addr)) {
      return false;
    }
    return read_va(addr, version);
  }

  bool symbol_whitelisted(const std::string& name) const {
    for (const std::string& allowed : m_options.ifunc_whitelist) {
      if (allowed == name) {
        return true;
      }
    }
    return false;
  }

  bool collect_relocations() {
    const DynTag* rela = find_tag(DT_RELA);
    const DynTag* relasz = find_tag(DT_RELASZ);
    if (rela != nullptr) {
      Elf64_Xword bytes = relasz != nullptr ? relasz->value : 0;
      if (!process_rela_table(rela->value, bytes,
                              ViolationCode::kRelaTableUnmapped,
                              ViolationCode::kRelaEntryOutOfBounds, false)) {
        return false;
      }
    }
    const DynTag* jmprel = find_tag(DT_JMPREL);
    const DynTag* pltrelsz = find_tag(DT_PLTRELSZ);
    if (jmprel != nullptr) {
      if (const DynTag* pltrel = find_tag(DT_PLTREL);
          pltrel != nullptr && pltrel->value != 7 /* DT_RELA */) {
        fail(ViolationCode::kRelaTableUnmapped,
             "DT_PLTREL does not select the RELA format");
        return false;
      }
      Elf64_Xword bytes = pltrelsz != nullptr ? pltrelsz->value : 0;
      if (!process_rela_table(jmprel->value, bytes,
                              ViolationCode::kRelaTableUnmapped,
                              ViolationCode::kRelaEntryOutOfBounds, true)) {
        return false;
      }
    }
    return true;
  }

  bool process_rela_table(Elf64_Addr table, Elf64_Xword bytes,
                          ViolationCode unmapped_code,
                          ViolationCode entry_code, bool is_jmprel) {
    if (!require_mapped(table, bytes, unmapped_code,
                        "RELA relocation table is not file-backed")) {
      return false;
    }
    if ((bytes % sizeof(Elf64_Rela)) != 0) {
      fail(entry_code, "RELA table size not a multiple of Elf64_Rela");
      return false;
    }
    const std::uint64_t entry_count = bytes / sizeof(Elf64_Rela);
    for (std::uint64_t i = 0; i < entry_count; ++i) {
      Elf64_Addr entry_va = 0;
      if (!checked_add(table, i * sizeof(Elf64_Rela), &entry_va)) {
        fail(entry_code, "RELA entry address wraps");
        return false;
      }
      Elf64_Rela rela{};
      if (!read_va(entry_va, &rela)) {
        fail(entry_code, "RELA entry not readable through mapping");
        return false;
      }
      const Elf64_Word type =
          static_cast<Elf64_Word>(rela.r_info & 0xffffffffULL);
      const Elf64_Word sym_index =
          static_cast<Elf64_Word>(rela.r_info >> 32);
      if (!is_covered_type(type)) {
        continue;
      }
      m_relro_targets.push_back(AddrRange{
          rela.r_offset, static_cast<Elf64_Xword>(sizeof(Elf64_Addr))});
      if (is_jmprel) {
        ++m_jmprel_count;
      } else {
        ++m_rela_count;
      }
      if (is_irelative(type)) {
        check_irelative(rela);
      } else if (is_symbol_relocation(type) &&
                 !check_symbol_reference(sym_index)) {
        return false;
      }
    }
    return true;
  }

  static constexpr std::uint64_t kSymbolScanLimit = 1u << 20;

  bool find_symbol_at(Elf64_Addr address, Elf64_Sym* found,
                      std::string* name) const {
    if (m_symtab == 0) {
      return false;
    }
    for (std::uint64_t index = 0; index < kSymbolScanLimit; ++index) {
      Elf64_Sym sym{};
      if (!symbol_at(static_cast<Elf64_Word>(index), &sym)) {
        return false;
      }
      if (sym.st_value == address && sym.st_size != 0) {
        if (!read_dynstr(sym.st_name, name)) {
          return false;
        }
        *found = sym;
        return true;
      }
    }
    return false;
  }

  void check_irelative(const Elf64_Rela& rela) {
    // IRELATIVE resolvers are selected by r_addend; the in-table symbol index
    // is conventionally zero and must never be allowed to mask a bad addend.
    if (rela.r_addend == 0 ||
        !range_mapped(static_cast<Elf64_Addr>(rela.r_addend), 1)) {
      policy(ViolationCode::kUnwhitelistedIfuncResolver,
             "IRELATIVE resolver target is not a mapped non-zero address");
      return;
    }
    Elf64_Sym resolver_sym{};
    std::string resolver_name;
    if (!find_symbol_at(static_cast<Elf64_Addr>(rela.r_addend),
                        &resolver_sym, &resolver_name) ||
        (resolver_sym.st_info & STT_TYPE_MASK) != STT_GNU_IFUNC ||
        !symbol_whitelisted(resolver_name)) {
      policy(ViolationCode::kUnwhitelistedIfuncResolver,
             "IRELATIVE resolver symbol is not on the IFUNC whitelist");
    }
  }

  bool check_symbol_reference(Elf64_Word sym_index) {
    Elf64_Sym sym{};
    if (!symbol_at(sym_index, &sym)) {
      fail(ViolationCode::kSymbolEntryOutOfBounds,
           "relocation references symbol outside DT_SYMTAB");
      return false;
    }
    std::string name;
    if (!read_dynstr(sym.st_name, &name)) {
      fail(ViolationCode::kSymbolEntryOutOfBounds,
           "relocation symbol name outside DT_STRTAB");
      return false;
    }
    if ((sym.st_info & STT_TYPE_MASK) == STT_GNU_IFUNC &&
        !symbol_whitelisted(name)) {
      policy(ViolationCode::kUnwhitelistedIfuncResolver,
             "dynamic IFUNC symbol is not on the whitelist: " + name);
    }
    if (m_have_versym) {
      Elf64_Half version = 0;
      if (!versym_at(sym_index, &version)) {
        fail(ViolationCode::kVersionSymbolEntryOutOfBounds,
             "DT_VERSYM entry is not file-backed for symbol: " + name);
        return false;
      }
      const Elf64_Half masked =
          static_cast<Elf64_Half>(version & ~VER_HIDDEN);
      if (masked > VER_NDX_GLOBAL &&
          m_defined_versions.find(masked) == m_defined_versions.end()) {
        policy(ViolationCode::kSymbolVersionUnbound,
               "symbol references version absent from DT_VERNEED: " + name);
      }
    }
    return true;
  }

  void evaluate_binding() {
    if (find_tag(DT_BIND_NOW) != nullptr) {
      m_bind_now = true;
    }
    if (const DynTag* flags = find_tag(DT_FLAGS);
        flags != nullptr && (flags->value & DF_BIND_NOW) != 0) {
      m_bind_now = true;
    }
    if (const DynTag* flags1 = find_tag(DT_FLAGS_1);
        flags1 != nullptr && (flags1->value & DF_1_NOW) != 0) {
      m_bind_now = true;
    }
  }

  void evaluate_path(Elf64_Sxword tag) {
    const DynTag* entry = find_tag(tag);
    if (entry == nullptr) {
      return;
    }
    std::string raw;
    if (!read_dynstr(static_cast<Elf64_Word>(entry->value), &raw)) {
      policy(tag == DT_RPATH ? ViolationCode::kRpathRelativeOrEmpty
                             : ViolationCode::kRunpathRelativeOrEmpty,
             "search path string is outside DT_STRTAB or unterminated");
      return;
    }
    const ViolationCode code = tag == DT_RPATH
                                   ? ViolationCode::kRpathRelativeOrEmpty
                                   : ViolationCode::kRunpathRelativeOrEmpty;
    std::size_t start = 0;
    bool has_item = false;
    while (start <= raw.size()) {
      std::size_t colon = raw.find(':', start);
      std::string item =
          raw.substr(start, colon == std::string::npos ? std::string::npos
                                                       : colon - start);
      has_item = true;
      if (item.empty() || item.front() != '/' ||
          item.find("$ORIGIN") != std::string::npos ||
          item.find("..") != std::string::npos) {
        policy(code,
               "search path contains empty, relative or expansion-based entry");
        return;
      }
      if (colon == std::string::npos) {
        break;
      }
      start = colon + 1;
    }
    if (!has_item) {
      policy(code, "search path is empty");
    }
  }

  bool rounded_relro(Elf64_Addr* lo, Elf64_Addr* hi) const {
    const std::uint64_t page = m_options.page_size;
    const Elf64_Addr vaddr = m_relro_phdr.p_vaddr;
    std::uint64_t end = 0;
    if (!checked_add(vaddr, m_relro_phdr.p_memsz, &end)) {
      return false;
    }
    *lo = static_cast<Elf64_Addr>(vaddr & ~(page - 1));
    *hi = static_cast<Elf64_Addr>((end + page - 1) & ~(page - 1));
    return true;
  }

  bool relro_covers(Elf64_Addr lo, Elf64_Addr hi, Elf64_Addr start,
                    Elf64_Xword size) const {
    std::uint64_t end = 0;
    if (!checked_add(start, size, &end)) {
      return false;
    }
    return start >= lo && end <= hi;
  }

  void evaluate_policy() {
    if (m_have_stack && (m_stack_phdr.p_flags & PF_X) != 0) {
      policy(ViolationCode::kExecutableStack,
             "PT_GNU_STACK requests executable permissions");
    }
    if (!m_have_relro) {
      policy(ViolationCode::kRelroSegmentMissing,
             "no PT_GNU_RELRO segment present");
      check_relro_coverage(1, 0);
    } else {
      const std::uint64_t page = m_options.page_size;
      if ((m_relro_phdr.p_vaddr & (page - 1)) != 0 ||
          m_relro_phdr.p_memsz == 0) {
        policy(ViolationCode::kRelroNotPageAligned,
               "PT_GNU_RELRO virtual start is not page aligned or empty");
      }
      Elf64_Addr lo = 0;
      Elf64_Addr hi = 0;
      if (rounded_relro(&lo, &hi)) {
        check_relro_coverage(lo, hi);
      }
    }
    if (!m_bind_now) {
      policy(ViolationCode::kNoImmediateBinding,
             "neither DT_BIND_NOW nor DF_BIND_NOW/DF_1_NOW is enabled");
    }
  }

  void check_relro_coverage(Elf64_Addr lo, Elf64_Addr hi) {
    if (!relro_covers(lo, hi, m_dynamic_phdr.p_vaddr,
                      m_dynamic_phdr.p_memsz)) {
      policy(ViolationCode::kRelroGapDynamic,
             "rounded RELRO region does not cover .dynamic");
    }
    for (const AddrRange& range : m_init_ranges) {
      if (!relro_covers(lo, hi, range.start, range.size)) {
        policy(ViolationCode::kRelroGapInitArray,
               "rounded RELRO region does not cover DT_INIT_ARRAY");
        break;
      }
    }
    for (const AddrRange& range : m_fini_ranges) {
      if (!relro_covers(lo, hi, range.start, range.size)) {
        policy(ViolationCode::kRelroGapFiniArray,
               "rounded RELRO region does not cover DT_FINI_ARRAY");
        break;
      }
    }
    for (const AddrRange& target : m_relro_targets) {
      if (!range_mapped(target.start, target.size)) {
        policy(ViolationCode::kRelroGapRelocationTarget,
               "relocation write target is not backed by PT_LOAD");
        break;
      }
      if (!relro_covers(lo, hi, target.start, target.size)) {
        policy(ViolationCode::kRelroGapRelocationTarget,
               "rounded RELRO region misses a relocation write target");
        break;
      }
    }
  }

  VerifyResult finish(VerifyResult result) {
    result.machine = m_machine;
    result.is_shared_object = m_is_dyn;
    result.violations = m_violations;
    result.ok = m_violations.empty();
    result.load_segment_count = m_loads.size();
    result.dynamic_entry_count = m_dyn.size();
    result.rela_entry_count = m_rela_count;
    result.jmprel_entry_count = m_jmprel_count;
    result.bind_now = m_bind_now;
    result.relro_present = m_have_relro;
    return result;
  }
};

bool is_fatal_code(ViolationCode code) {
  switch (code) {
    case ViolationCode::kExecutableStack:
    case ViolationCode::kWritableExecutableSegment:
    case ViolationCode::kRelroSegmentMissing:
    case ViolationCode::kRelroNotPageAligned:
    case ViolationCode::kNoImmediateBinding:
    case ViolationCode::kRelroGapDynamic:
    case ViolationCode::kRelroGapInitArray:
    case ViolationCode::kRelroGapFiniArray:
    case ViolationCode::kRelroGapRelocationTarget:
    case ViolationCode::kUnwhitelistedIfuncResolver:
    case ViolationCode::kRpathRelativeOrEmpty:
    case ViolationCode::kRunpathRelativeOrEmpty:
    case ViolationCode::kSymbolVersionUnbound:
    case ViolationCode::kNone:
      return false;
    default:
      return true;
  }
}

}  // namespace
}  // namespace elfsec

namespace elfsec {

VerifyResult verify_elf64(const std::uint8_t* data, std::size_t size,
                          const VerifyOptions& options) {
  if (data == nullptr && size != 0) {
    VerifyResult result;
    result.violations.emplace_back(ViolationCode::kBufferTooSmall,
                                   "null buffer with non-zero size");
    return result;
  }
  Verifier verifier(data, size, options);
  return verifier.run();
}

VerifyResult verify_elf64(const std::vector<std::uint8_t>& bytes,
                          const VerifyOptions& options) {
  return verify_elf64(bytes.empty() ? nullptr : bytes.data(), bytes.size(),
                      options);
}

const Violation* VerifyResult::first_fatal() const {
  for (const Violation& violation : violations) {
    if (is_fatal_code(violation.code)) {
      return &violation;
    }
  }
  return nullptr;
}

bool VerifyResult::has(ViolationCode code) const {
  for (const Violation& violation : violations) {
    if (violation.code == code) {
      return true;
    }
  }
  return false;
}

const char* violation_name(ViolationCode code) {
  switch (code) {
    case ViolationCode::kNone:
      return "NONE";
    case ViolationCode::kBufferTooSmall:
      return "BUFFER_TOO_SMALL";
    case ViolationCode::kBadMagic:
      return "BAD_MAGIC";
    case ViolationCode::kNotElf64:
      return "NOT_ELF64";
    case ViolationCode::kNotLittleEndian:
      return "NOT_LITTLE_ENDIAN";
    case ViolationCode::kBadElfVersion:
      return "BAD_ELF_VERSION";
    case ViolationCode::kBadElfType:
      return "BAD_ELF_TYPE";
    case ViolationCode::kUnsupportedMachine:
      return "UNSUPPORTED_MACHINE";
    case ViolationCode::kProgramHeaderTableOutOfBounds:
      return "PROGRAM_HEADER_TABLE_OOB";
    case ViolationCode::kProgramHeaderEntrySize:
      return "PROGRAM_HEADER_ENTRY_SIZE";
    case ViolationCode::kProgramHeaderCountOverflow:
      return "PROGRAM_HEADER_COUNT_OVERFLOW";
    case ViolationCode::kSegmentFileRangeOutOfBounds:
      return "SEGMENT_FILE_RANGE_OOB";
    case ViolationCode::kLoadFileszGreaterMemsz:
      return "LOAD_FILESZ_GREATER_MEMSZ";
    case ViolationCode::kLoadSizeOverflow:
      return "LOAD_SIZE_OVERFLOW";
    case ViolationCode::kLoadVirtualPageOverlap:
      return "LOAD_VIRTUAL_PAGE_OVERLAP";
    case ViolationCode::kLoadAlignNotPowerOfTwo:
      return "LOAD_ALIGN_NOT_POWER_OF_TWO";
    case ViolationCode::kLoadOffsetVaddrMisaligned:
      return "LOAD_OFFSET_VADDR_MISALIGNED";
    case ViolationCode::kDuplicateProgramHeader:
      return "DUPLICATE_PROGRAM_HEADER";
    case ViolationCode::kDynamicSegmentMissing:
      return "DYNAMIC_SEGMENT_MISSING";
    case ViolationCode::kDynamicSegmentNotInLoad:
      return "DYNAMIC_SEGMENT_NOT_IN_LOAD";
    case ViolationCode::kDynamicEntryOutOfBounds:
      return "DYNAMIC_ENTRY_OOB";
    case ViolationCode::kDynamicEntryCountOverflow:
      return "DYNAMIC_ENTRY_COUNT_OVERFLOW";
    case ViolationCode::kDynamicPointerUnmapped:
      return "DYNAMIC_POINTER_UNMAPPED";
    case ViolationCode::kStringTableUnmapped:
      return "STRING_TABLE_UNMAPPED";
    case ViolationCode::kSymbolTableUnmapped:
      return "SYMBOL_TABLE_UNMAPPED";
    case ViolationCode::kSymbolEntryOutOfBounds:
      return "SYMBOL_ENTRY_OOB";
    case ViolationCode::kRelaTableUnmapped:
      return "RELA_TABLE_UNMAPPED";
    case ViolationCode::kRelaEntryOutOfBounds:
      return "RELA_ENTRY_OOB";
    case ViolationCode::kVersionSymbolTableUnmapped:
      return "VERSION_SYMBOL_TABLE_UNMAPPED";
    case ViolationCode::kVersionSymbolEntryOutOfBounds:
      return "VERSION_SYMBOL_ENTRY_OOB";
    case ViolationCode::kVersionNeedUnmapped:
      return "VERSION_NEED_UNMAPPED";
    case ViolationCode::kVersionNeedMalformed:
      return "VERSION_NEED_MALFORMED";
    case ViolationCode::kInitArrayUnmapped:
      return "INIT_ARRAY_UNMAPPED";
    case ViolationCode::kInitArraySizeOverflow:
      return "INIT_ARRAY_SIZE_OVERFLOW";
    case ViolationCode::kFiniArrayUnmapped:
      return "FINI_ARRAY_UNMAPPED";
    case ViolationCode::kFiniArraySizeOverflow:
      return "FINI_ARRAY_SIZE_OVERFLOW";
    case ViolationCode::kInterpMalformed:
      return "INTERP_MALFORMED";
    case ViolationCode::kExecutableStack:
      return "EXECUTABLE_STACK";
    case ViolationCode::kWritableExecutableSegment:
      return "WRITABLE_EXECUTABLE_SEGMENT";
    case ViolationCode::kRelroSegmentMissing:
      return "RELRO_SEGMENT_MISSING";
    case ViolationCode::kRelroNotPageAligned:
      return "RELRO_NOT_PAGE_ALIGNED";
    case ViolationCode::kNoImmediateBinding:
      return "NO_IMMEDIATE_BINDING";
    case ViolationCode::kRelroGapDynamic:
      return "RELRO_GAP_DYNAMIC";
    case ViolationCode::kRelroGapInitArray:
      return "RELRO_GAP_INIT_ARRAY";
    case ViolationCode::kRelroGapFiniArray:
      return "RELRO_GAP_FINI_ARRAY";
    case ViolationCode::kRelroGapRelocationTarget:
      return "RELRO_GAP_RELOCATION_TARGET";
    case ViolationCode::kUnwhitelistedIfuncResolver:
      return "UNWHITELISTED_IFUNC_RESOLVER";
    case ViolationCode::kRpathRelativeOrEmpty:
      return "RPATH_RELATIVE_OR_EMPTY";
    case ViolationCode::kRunpathRelativeOrEmpty:
      return "RUNPATH_RELATIVE_OR_EMPTY";
    case ViolationCode::kSymbolVersionUnbound:
      return "SYMBOL_VERSION_UNBOUND";
  }
  return "UNKNOWN";
}

}  // namespace elfsec
