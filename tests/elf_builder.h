// Byte-level ELF64 builder producing fully controlled audit fixtures without
// invoking the host linker. Virtual addresses equal file offsets, so every
// PT_LOAD keeps offset/vaddr congruence and the whole image is file-backed.
#ifndef ELFSEC_TESTS_ELF_BUILDER_H
#define ELFSEC_TESTS_ELF_BUILDER_H

#include <cassert>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

namespace elfsec_test {

using u8 = std::uint8_t;
using u16 = std::uint16_t;
using u32 = std::uint32_t;
using u64 = std::uint64_t;
using i64 = std::int64_t;

constexpr u16 ET_DYN = 3;
constexpr u16 EM_X86_64 = 62;

constexpr u32 PT_LOAD = 1;
constexpr u32 PT_DYNAMIC = 2;
constexpr u32 PT_GNU_STACK = 0x6474e551;
constexpr u32 PT_GNU_RELRO = 0x6474e552;

constexpr u32 PF_R = 4;
constexpr u32 PF_W = 2;
constexpr u32 PF_X = 1;

constexpr u64 PAGE = 0x1000;
constexpr u64 V_RO = 0x0;
constexpr u64 V_RW = 0x2000;
constexpr u64 V_EXEC = 0x3000;

constexpr i64 DT_NULL = 0;
constexpr i64 DT_PLTRELSZ = 2;
constexpr i64 DT_STRTAB = 5;
constexpr i64 DT_SYMTAB = 6;
constexpr i64 DT_RELA = 7;
constexpr i64 DT_RELASZ = 8;
constexpr i64 DT_STRSZ = 10;
constexpr i64 DT_SYMENT = 11;
constexpr i64 DT_SONAME = 14;
constexpr i64 DT_RPATH = 15;
constexpr i64 DT_PLTREL = 20;
constexpr i64 DT_JMPREL = 23;
constexpr i64 DT_BIND_NOW = 24;
constexpr i64 DT_INIT_ARRAY = 25;
constexpr i64 DT_FINI_ARRAY = 26;
constexpr i64 DT_INIT_ARRAYSZ = 27;
constexpr i64 DT_FINI_ARRAYSZ = 28;
constexpr i64 DT_RUNPATH = 29;
constexpr i64 DT_FLAGS = 30;
constexpr i64 DT_VERSYM = 0x6ffffff0;
constexpr i64 DT_VERNEED = 0x6ffffffe;
constexpr i64 DT_VERNEEDNUM = 0x6fffffff;

struct BuilderConfig {
  bool emit_relro = true;
  bool relro_page_aligned = true;
  bool bind_now = true;
  bool executable_stack = false;
  bool wx_load = false;
  bool overlapping_load = false;
  bool filesz_gt_memsz = false;
  bool rpath_relative = false;
  bool runpath_relative = false;
  bool unbound_version = false;
  bool hidden_irelative = false;
  bool ifunc_unwhitelisted = false;
  bool relocation_outside_relro = false;
};

struct RelaEntry {
  u64 offset;
  u32 sym;
  u32 type;
  i64 addend;
};

struct SymbolSpec {
  std::string name;
  u8 info;
  u64 value;
  u64 size;
};

class Elf64Builder {
 public:
  explicit Elf64Builder(BuilderConfig config) : m_config(config) {}

  std::vector<u8> build() {
    prepare_strings();
    prepare_symbols();
    prepare_relocations();
    plan_layout();
    emit_image();
    return std::move(m_image);
  }

 private:
  struct StringBlock {
    std::string text;
    u32 offset;
  };

  struct DynRec {
    i64 tag;
    u64 value;
  };

  struct Layout {
    u64 strtab = 0;
    u64 symtab = 0;
    u64 rela = 0;
    u64 jmprel = 0;
    u64 versym = 0;
    u64 verneed = 0;
    u64 dynamic = V_RW;
    u64 init_array = V_RW + 0x100;
    u64 fini_array = V_RW + 0x110;
    u64 got = V_RW + 0x200;
    u64 ro_end = 0;
    u64 dynamic_size = 0;
  };

  BuilderConfig m_config;
  std::vector<u8> m_image;
  std::vector<StringBlock> m_strings;
  u32 m_strtab_size = 0;
  std::vector<SymbolSpec> m_symbols;
  std::vector<RelaEntry> m_rela;
  std::vector<RelaEntry> m_jmprel;
  Layout m_layout;

  u32 intern(const std::string& text) {
    for (const StringBlock& block : m_strings) {
      if (block.text == text) {
        return block.offset;
      }
    }
    u32 offset = m_strtab_size;
    m_strings.push_back(StringBlock{text, offset});
    m_strtab_size += static_cast<u32>(text.size()) + 1;
    return offset;
  }

  u32 index_of(const std::string& text) const {
    for (const StringBlock& block : m_strings) {
      if (block.text == text) {
        return block.offset;
      }
    }
    assert(false && "string missing");
    return 0;
  }

  void prepare_strings() {
    intern("");
    intern("libc.so.6");
    intern("memcpy");
    intern("data_symbol");
    intern("trusted_ifunc");
    intern("evil_ifunc");
    intern("GLIBC_2.2.5");
    intern("audit.so");
    intern(m_config.rpath_relative ? "../evil/lib" : "/usr/lib/secure");
    intern(m_config.runpath_relative ? "relative/runpath" : "/opt/secure/lib");
  }

  void prepare_symbols() {
    m_symbols.push_back(SymbolSpec{"", 0, 0, 0});
    m_symbols.push_back(SymbolSpec{"memcpy", 0x12, V_EXEC + 0x10, 0x20});
    m_symbols.push_back(SymbolSpec{"data_symbol", 0x11, V_RW + 0x300, 8});
    const std::string ifunc_name =
        m_config.ifunc_unwhitelisted ? "evil_ifunc" : "trusted_ifunc";
    constexpr u8 STT_GNU_IFUNC = 10;
    m_symbols.push_back(
        SymbolSpec{ifunc_name, static_cast<u8>(0x10 | STT_GNU_IFUNC),
                   V_EXEC + 0x40, 0x10});
  }

  void prepare_relocations() {
    constexpr u32 R_X86_64_GLOB_DAT = 6;
    constexpr u32 R_X86_64_JUMP_SLOT = 7;
    constexpr u32 R_X86_64_RELATIVE = 8;
    constexpr u32 R_X86_64_IRELATIVE = 37;
    m_rela.push_back(RelaEntry{m_layout.got, 2, R_X86_64_GLOB_DAT, 0});
    m_jmprel.push_back(RelaEntry{m_layout.got + 8, 1, R_X86_64_JUMP_SLOT, 0});
    m_rela.push_back(RelaEntry{m_layout.got + 16, 0, R_X86_64_RELATIVE,
                               static_cast<i64>(V_RW + 0x300)});
    const i64 resolver = m_config.hidden_irelative
                             ? static_cast<i64>(0xdead0000ULL)
                             : static_cast<i64>(V_EXEC + 0x40);
    m_rela.push_back(RelaEntry{m_layout.got + 24, 3, R_X86_64_IRELATIVE,
                               resolver});
    if (m_config.relocation_outside_relro) {
      m_rela.push_back(
          RelaEntry{V_EXEC + 0x200, 2, R_X86_64_GLOB_DAT, 0});
    }
  }

  void plan_layout() {
    u64 cursor = 0x400;
    m_layout.strtab = cursor;
    cursor += m_strtab_size;
    m_layout.symtab = cursor;
    cursor += m_symbols.size() * 24;
    m_layout.rela = cursor;
    cursor += m_rela.size() * 24;
    m_layout.jmprel = cursor;
    cursor += m_jmprel.size() * 24;
    m_layout.versym = cursor;
    cursor += m_symbols.size() * 2;
    m_layout.verneed = cursor;
    cursor += 32;
    m_layout.ro_end = cursor;
    assert(m_layout.ro_end < V_RW);
    m_layout.dynamic = V_RW;
    m_layout.dynamic_size = dynamic_entries().size() * 16;
  }

  std::vector<DynRec> dynamic_entries() const {
    std::vector<DynRec> entries;
    entries.push_back(DynRec{DT_STRTAB, m_layout.strtab});
    entries.push_back(DynRec{DT_STRSZ, m_strtab_size});
    entries.push_back(DynRec{DT_SYMTAB, m_layout.symtab});
    entries.push_back(DynRec{DT_SYMENT, 24});
    entries.push_back(DynRec{DT_RELA, m_layout.rela});
    entries.push_back(DynRec{DT_RELASZ, m_rela.size() * 24});
    entries.push_back(DynRec{DT_JMPREL, m_layout.jmprel});
    entries.push_back(DynRec{DT_PLTRELSZ, m_jmprel.size() * 24});
    entries.push_back(DynRec{DT_PLTREL, 7});
    entries.push_back(DynRec{DT_VERSYM, m_layout.versym});
    entries.push_back(DynRec{DT_VERNEED, m_layout.verneed});
    entries.push_back(DynRec{DT_VERNEEDNUM, 1});
    entries.push_back(DynRec{DT_SONAME, index_of("audit.so")});
    entries.push_back(DynRec{
        DT_RPATH, index_of(m_config.rpath_relative ? "../evil/lib"
                                                    : "/usr/lib/secure")});
    entries.push_back(DynRec{
        DT_RUNPATH,
        index_of(m_config.runpath_relative ? "relative/runpath"
                                           : "/opt/secure/lib")});
    entries.push_back(DynRec{DT_INIT_ARRAY, m_layout.init_array});
    entries.push_back(DynRec{DT_INIT_ARRAYSZ, 8});
    entries.push_back(DynRec{DT_FINI_ARRAY, m_layout.fini_array});
    entries.push_back(DynRec{DT_FINI_ARRAYSZ, 8});
    if (m_config.bind_now) {
      entries.push_back(DynRec{DT_BIND_NOW, 0});
      entries.push_back(DynRec{DT_FLAGS, 0x8});
    }
    entries.push_back(DynRec{DT_NULL, 0});
    return entries;
  }

  void write_u16(u64 offset, u16 value) {
    for (int i = 0; i < 2; ++i) {
      m_image[offset + static_cast<u64>(i)] =
          static_cast<u8>((value >> (8 * i)) & 0xff);
    }
  }

  void write_u32(u64 offset, u32 value) {
    for (int i = 0; i < 4; ++i) {
      m_image[offset + static_cast<u64>(i)] =
          static_cast<u8>((value >> (8 * i)) & 0xff);
    }
  }

  void write_u64(u64 offset, u64 value) {
    for (int i = 0; i < 8; ++i) {
      m_image[offset + static_cast<u64>(i)] =
          static_cast<u8>((value >> (8 * i)) & 0xff);
    }
  }

  void write_phdr(u64 index, u32 type, u32 flags, u64 offset, u64 vaddr,
                  u64 filesz, u64 memsz, u64 align) {
    u64 base = 64 + index * 56;
    write_u32(base + 0, type);
    write_u32(base + 4, flags);
    write_u64(base + 8, offset);
    write_u64(base + 16, vaddr);
    write_u64(base + 24, vaddr);
    write_u64(base + 32, filesz);
    write_u64(base + 40, memsz);
    write_u64(base + 48, align);
  }

  void emit_image() {
    m_image.assign(0x4000, 0);
    emit_elf_header();
    emit_program_headers();
    emit_strtab();
    emit_symtab();
    emit_rela_tables();
    emit_versym();
    emit_verneed();
    emit_dynamic();
    emit_arrays_and_got();
  }

  void emit_elf_header() {
    static const u8 ident[16] = {0x7f, 'E', 'L', 'F', 2, 1, 1, 0,
                                 0,    0,   0,   0,   0, 0, 0, 0};
    std::memcpy(m_image.data(), ident, sizeof(ident));
    write_u16(16, ET_DYN);
    write_u16(18, EM_X86_64);
    write_u32(20, 1);
    write_u64(24, 0);
    write_u64(32, 64);
    write_u64(40, 0);
    write_u32(48, 0);
    write_u16(52, 64);
    write_u16(54, 56);
    write_u16(56, program_header_count());
    write_u16(58, 0);
    write_u16(60, 0);
    write_u16(62, 0);
  }

  u16 program_header_count() const {
    return static_cast<u16>(m_config.overlapping_load ? 7 : 6);
  }

  void emit_program_headers() {
    const u32 rw_flags = m_config.wx_load ? (PF_R | PF_W | PF_X)
                                          : (PF_R | PF_W);
    write_phdr(0, PT_LOAD, PF_R, V_RO, V_RO, V_RW, V_RW, PAGE);
    write_phdr(1, PT_LOAD, rw_flags, V_RW, V_RW, PAGE,
               m_config.filesz_gt_memsz ? PAGE / 2 : PAGE, PAGE);
    write_phdr(2, PT_LOAD, PF_R | PF_X, V_EXEC, V_EXEC, PAGE, PAGE, PAGE);
    write_phdr(3, PT_DYNAMIC, PF_R | PF_W, m_layout.dynamic,
               m_layout.dynamic, m_layout.dynamic_size,
               m_layout.dynamic_size, 8);
    if (m_config.emit_relro) {
      const u64 relro_start =
          m_config.relro_page_aligned ? V_RW : V_RW + 0x10;
      write_phdr(4, PT_GNU_RELRO, PF_R, relro_start, relro_start, 0x2f0,
                 0x2f0, 1);
    }
    const u32 stack_flags =
        m_config.executable_stack ? (PF_R | PF_W | PF_X) : (PF_R | PF_W);
    write_phdr(5, PT_GNU_STACK, stack_flags, 0, 0, 0, 0, 0x10);
    if (m_config.overlapping_load) {
      // Intentionally overlaps the first PT_LOAD virtual page range.
      write_phdr(6, PT_LOAD, PF_R | PF_W, 0x800, 0x800, 0x100, 0x100, PAGE);
    }
  }

  void emit_strtab() {
    for (const StringBlock& block : m_strings) {
      std::memcpy(m_image.data() + m_layout.strtab + block.offset,
                  block.text.data(), block.text.size());
    }
  }

  void emit_symtab() {
    for (std::size_t i = 0; i < m_symbols.size(); ++i) {
      const SymbolSpec& sym = m_symbols[i];
      u64 base = m_layout.symtab + i * 24;
      write_u32(base + 0, index_of(sym.name));
      m_image[base + 4] = sym.info;
      m_image[base + 5] = 0;
      write_u16(base + 6, 0);
      write_u64(base + 8, sym.value);
      write_u64(base + 16, sym.size);
    }
  }

  void write_rela(u64 base, const RelaEntry& entry) {
    write_u64(base + 0, entry.offset);
    write_u64(base + 8,
              (static_cast<u64>(entry.sym) << 32) |
                  static_cast<u64>(entry.type));
    write_u64(base + 16, static_cast<u64>(entry.addend));
  }

  void emit_rela_tables() {
    for (std::size_t i = 0; i < m_rela.size(); ++i) {
      write_rela(m_layout.rela + i * 24, m_rela[i]);
    }
    for (std::size_t i = 0; i < m_jmprel.size(); ++i) {
      write_rela(m_layout.jmprel + i * 24, m_jmprel[i]);
    }
  }

  void emit_versym() {
    for (std::size_t i = 0; i < m_symbols.size(); ++i) {
      u16 version = 0;
      if (i == 1) {
        // memcpy@GLIBC_2.2.5 binds to the single Vernaux index 2.
        version = m_config.unbound_version ? 7 : 2;
      } else if (i >= 2) {
        version = 1;
      }
      write_u16(m_layout.versym + i * 2, version);
    }
  }

  void emit_verneed() {
    const u64 base = m_layout.verneed;
    write_u16(base + 0, 1);
    write_u16(base + 2, 1);
    write_u32(base + 4, index_of("libc.so.6"));
    write_u32(base + 8, 16);
    write_u32(base + 12, 0);
    const u64 aux = base + 16;
    write_u32(aux + 0, 0x061d172f);
    write_u16(aux + 4, 0);
    write_u16(aux + 6, 2);
    write_u32(aux + 8, index_of("GLIBC_2.2.5"));
    write_u32(aux + 12, 0);
  }

  void emit_dynamic() {
    const std::vector<DynRec> entries = dynamic_entries();
    for (std::size_t i = 0; i < entries.size(); ++i) {
      u64 base = m_layout.dynamic + i * 16;
      write_u64(base + 0, static_cast<u64>(entries[i].tag));
      write_u64(base + 8, entries[i].value);
    }
  }

  void emit_arrays_and_got() {
    write_u64(m_layout.init_array, V_EXEC + 0x80);
    write_u64(m_layout.fini_array, V_EXEC + 0x90);
    for (u64 slot = 0; slot < 5; ++slot) {
      write_u64(m_layout.got + slot * 8, 0x1122334455667788ULL);
    }
    write_u64(V_RW + 0x300, 0xaabbccddeeff0011ULL);
  }

};

}  // namespace elfsec_test

#endif  // ELFSEC_TESTS_ELF_BUILDER_H
