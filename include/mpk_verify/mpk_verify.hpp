// mpk_verify.hpp
//
// Embeddable, zero-dependency Intel MPK/PKU bytecode scanner and sandbox
// isolation state-machine verifier (C++17, header-only).
//
// The verifier is purely static: it consumes raw untrusted x86-64 machine
// code bytes, a virtual page-table PKEY allocation table, the set of host
// protected PKEYs and a trusted-trampoline whitelist, then reports every
// isolation violation with an exact virtual address, code-segment offset and
// byte witness.
//
// Threat model covered:
//   1. Hidden WRPKRU (0F 01 EF), XRSTOR (0F AE /5), XRSTORS (0F C7 /3),
//      SYSCALL (0F 05) and INT 0x80 (CD 80) bytes embedded *inside* other
//      variable-length x86-64 instructions, reachable through unaligned
//      control-flow transfers. Every byte offset is scanned, not only
//      instruction heads.
//   2. Page-table mappings that leak host protected PKEYs or default PKEY 0
//      into untrusted mappings, plus writable+executable code pages that
//      allow runtime self-injection of WRPKRU.
//   3. PKRU mask transitions at every sandbox entry/exit, modelled as an
//      explicit HOST <-> GUEST state machine with per-key AD/WD checks.

#ifndef MPK_VERIFY_MPK_VERIFY_HPP
#define MPK_VERIFY_MPK_VERIFY_HPP

#include <array>
#include <cstdio>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <string>
#include <vector>

namespace mpkverify {

using byte_t = unsigned char;
using addr_t = std::uint64_t;
using pkru_t = std::uint32_t;

inline constexpr int kPkeyCount = 16;       // PKEY 0..15
inline constexpr std::size_t kMaxWitness = 16;
inline constexpr addr_t kDefaultPageSize = 4096;

// Exactly one ViolationKind per isolation rule enforced by the verifier.
enum class ViolationKind : unsigned {
    HiddenWrPkru,            // 0F 01 EF outside a trusted trampoline
    Xrstor,                  // 0F AE /5  (indirect PKRU overwrite via XSTATE)
    Xrstors,                 // 0F C7 /3  (XSAVES-family state restore)
    Syscall,                 // 0F 05     (pkey_mprotect / rt_sigreturn vector)
    Int80,                   // CD 80     (legacy int 0x80 syscall vector)
    PageUsesPkey0,           // untrusted mapping backed by default PKEY 0
    PageUsesHostPkey,        // untrusted mapping backed by a host PKEY
    InvalidPkey,             // pkey outside [0, 15]
    CodePageWritable,        // executable untrusted page has W=1
    JumpTargetUnmapped,      // target lands on no mapped page / outside segment
    JumpTargetNonExecutable, // target page lacks X
    JumpTargetPkey0,         // target lands on a PKEY 0 page
    JumpTargetHostPage,      // target lands on a host protected page
    BadEntryPkru,            // entry PKRU fails to clear host-key access
    BadExitPkru,             // exit PKRU does not restore host baseline
    TransitionOrdering,      // HOST->HOST or GUEST->GUEST boundary
};

struct Violation {
    ViolationKind kind = ViolationKind::HiddenWrPkru;
    addr_t address = 0;                 // absolute virtual address
    std::int64_t segment_offset = -1;   // offset inside code segment, -1 if N/A
    std::array<byte_t, kMaxWitness> witness{};
    std::uint8_t witness_len = 0;
    std::string detail;
};

struct CodeSegment {
    addr_t base_address = 0;
    std::vector<byte_t> bytes;
    std::vector<addr_t> entry_points;  // exported entry table (absolute VAs)
    std::vector<addr_t> jump_targets;  // control-flow targets (absolute VAs)
};

// Half-open trusted trampoline interval [start, end).
struct TrampolineRange {
    addr_t start = 0;
    addr_t end = 0;
};

struct PageMapping {
    addr_t page_base = 0;
    bool readable = false;
    bool writable = false;
    bool executable = false;
    int pkey = -1;  // explicit PKEY 0..15; -1 means "no key assigned"
};

struct PkruTransition {
    enum class Type : unsigned { Entry, Exit };
    Type type = Type::Entry;
    pkru_t pkru = 0;
    addr_t site_address = 0;  // boundary instruction address
};

struct VerifyConfig {
    std::vector<CodeSegment> segments;
    std::vector<TrampolineRange> trampolines;
    std::vector<PageMapping> page_table;       // untrusted-visible mappings
    std::vector<int> host_protected_pkeys;     // keys owned by the host
    std::vector<PkruTransition> transitions;   // ordered entry/exit trace
    pkru_t host_baseline_pkru = 0;             // PKRU restored at every exit
    addr_t page_size = kDefaultPageSize;
};

struct VerifyResult {
    bool ok = true;
    std::vector<Violation> violations;
};

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

inline bool checked_add(addr_t base, std::uint64_t offset, addr_t& out) {
    if (offset > (std::numeric_limits<addr_t>::max)() - base) {
        return false;
    }
    out = base + offset;
    return true;
}

inline bool contains_key(const std::vector<int>& keys, int key) {
    for (int candidate : keys) {
        if (candidate == key) {
            return true;
        }
    }
    return false;
}

inline bool in_trampoline(addr_t address,
                          const std::vector<TrampolineRange>& ranges) {
    for (const TrampolineRange& range : ranges) {
        if (address >= range.start && address < range.end) {
            return true;
        }
    }
    return false;
}

inline const PageMapping* find_page(addr_t address, const VerifyConfig& cfg) {
    for (const PageMapping& mapping : cfg.page_table) {
        if (address >= mapping.page_base &&
            address < mapping.page_base + cfg.page_size) {
            return &mapping;
        }
    }
    return nullptr;
}

inline bool inside_any_segment(addr_t address, const VerifyConfig& cfg) {
    for (const CodeSegment& segment : cfg.segments) {
        if (segment.bytes.empty()) {
            continue;
        }
        addr_t last = 0;
        if (!checked_add(segment.base_address, segment.bytes.size() - 1,
                         last)) {
            continue;
        }
        if (address >= segment.base_address && address <= last) {
            return true;
        }
    }
    return false;
}

// PKRU bit layout: key k uses bits 2k (AD, disable read) and 2k+1 (WD,
// disable write). A set bit means the corresponding access is denied.
inline bool pkru_ad(pkru_t pkru, int key) {
    return ((pkru >> static_cast<unsigned>(key * 2)) & 1u) != 0u;
}

inline bool pkru_wd(pkru_t pkru, int key) {
    return ((pkru >> static_cast<unsigned>(key * 2 + 1)) & 1u) != 0u;
}

inline std::array<byte_t, kMaxWitness> le_bytes(addr_t value,
                                                std::uint8_t& len_out) {
    std::array<byte_t, kMaxWitness> out{};
    constexpr std::uint8_t kLen = 8;
    for (std::uint8_t i = 0; i < kLen; ++i) {
        out[i] = static_cast<byte_t>((value >> (i * 8)) & 0xFFu);
    }
    len_out = kLen;
    return out;
}

inline std::array<byte_t, kMaxWitness> le_bytes_pkru(
    pkru_t value, std::uint8_t& len_out) {
    std::array<byte_t, kMaxWitness> out{};
    constexpr std::uint8_t kLen = 4;
    for (std::uint8_t i = 0; i < kLen; ++i) {
        out[i] = static_cast<byte_t>((value >> (i * 8)) & 0xFFu);
    }
    len_out = kLen;
    return out;
}

// ModR/M reg field (bits 5..3); the /digit opcode groups are selected by it.
inline unsigned modrm_reg(byte_t modrm) {
    return static_cast<unsigned>((modrm >> 3) & 0x07u);
}

// ---------------------------------------------------------------------------
// (1) Full byte-offset sliding opcode scan
// ---------------------------------------------------------------------------

class Emitter {
public:
    explicit Emitter(std::vector<Violation>& sink) : sink_(sink) {}

    void emit(ViolationKind kind, addr_t address, std::int64_t offset,
              const std::vector<byte_t>& bytes, std::size_t at,
              const std::string& detail) {
        Violation violation;
        violation.kind = kind;
        violation.address = address;
        violation.segment_offset = offset;
        violation.detail = detail;
        std::size_t available = bytes.size() - at;
        std::size_t take = available < kMaxWitness ? available : kMaxWitness;
        for (std::size_t i = 0; i < take; ++i) {
            violation.witness[i] = bytes[at + i];
        }
        violation.witness_len = static_cast<std::uint8_t>(take);
        sink_.push_back(violation);
    }

    void emit_raw(ViolationKind kind, addr_t address,
                  const std::array<byte_t, kMaxWitness>& witness,
                  std::uint8_t witness_len, const std::string& detail) {
        Violation violation;
        violation.kind = kind;
        violation.address = address;
        violation.segment_offset = -1;
        violation.detail = detail;
        violation.witness = witness;
        violation.witness_len = witness_len;
        sink_.push_back(violation);
    }

private:
    std::vector<Violation>& sink_;
};

inline void scan_segment(const CodeSegment& segment,
                         const std::vector<TrampolineRange>& trampolines,
                         Emitter& emitter) {
    const std::vector<byte_t>& code = segment.bytes;
    const std::size_t size = code.size();

    // Every offset, not just decoded instruction heads: a jump may land in
    // the middle of a benign instruction and execute hidden opcode bytes.
    for (std::size_t i = 0; i < size; ++i) {
        addr_t address = 0;
        if (!checked_add(segment.base_address, i, address)) {
            continue;  // unreachable VA: cannot be a real jump target
        }
        if (in_trampoline(address, trampolines)) {
            continue;  // trusted trampoline code is explicitly whitelisted
        }

        const byte_t b0 = code[i];
        const std::int64_t offset = static_cast<std::int64_t>(i);

        if (b0 == 0x0Fu && i + 1 < size) {
            const byte_t b1 = code[i + 1];

            // WRPKRU: 0F 01 EF (three-byte fixed opcode).
            if (b1 == 0x01u && i + 2 < size && code[i + 2] == 0xEFu) {
                emitter.emit(ViolationKind::HiddenWrPkru, address, offset,
                             code, i,
                             "WRPKRU (0F 01 EF) outside trusted trampoline");
                continue;
            }
            // SYSCALL: 0F 05.
            if (b1 == 0x05u) {
                emitter.emit(ViolationKind::Syscall, address, offset, code, i,
                             "SYSCALL (0F 05) outside trusted trampoline");
                continue;
            }
            // XRSTOR mem: 0F AE /5. The mod=11 register encoding is
            // architecturally undefined; rejecting it is conservative.
            if (b1 == 0xAEu && i + 2 < size &&
                modrm_reg(code[i + 2]) == 5u) {
                emitter.emit(ViolationKind::Xrstor, address, offset, code, i,
                             "XRSTOR (0F AE /5) can reload PKRU from XSTATE");
                continue;
            }
            // XRSTORS mem: 0F C7 /3.
            if (b1 == 0xC7u && i + 2 < size &&
                modrm_reg(code[i + 2]) == 3u) {
                emitter.emit(ViolationKind::Xrstors, address, offset, code, i,
                             "XRSTORS (0F C7 /3) can reload PKRU from XSTATE");
                continue;
            }
        }

        // INT imm8 with imm8 == 0x80: CD 80.
        if (b0 == 0xCDu && i + 1 < size && code[i + 1] == 0x80u) {
            emitter.emit(ViolationKind::Int80, address, offset, code, i,
                         "INT 0x80 (CD 80) legacy syscall vector");
        }
    }
}

// ---------------------------------------------------------------------------
// (2) Page-table PKEY / permission validation
// ---------------------------------------------------------------------------

inline void validate_pages(const VerifyConfig& cfg, Emitter& emitter) {
    for (const PageMapping& mapping : cfg.page_table) {
        if (mapping.pkey < 0 || mapping.pkey >= kPkeyCount) {
            std::uint8_t wlen = 0;
            auto witness = le_bytes(mapping.page_base, wlen);
            emitter.emit_raw(ViolationKind::InvalidPkey, mapping.page_base,
                             witness, wlen,
                             "untrusted mapping has no valid PKEY assignment");
            continue;
        }

        if (mapping.pkey == 0) {
            std::uint8_t wlen = 0;
            auto witness = le_bytes(mapping.page_base, wlen);
            emitter.emit_raw(ViolationKind::PageUsesPkey0, mapping.page_base,
                             witness, wlen,
                             "untrusted mapping leaks default PKEY 0");
        } else if (contains_key(cfg.host_protected_pkeys, mapping.pkey)) {
            std::uint8_t wlen = 0;
            auto witness = le_bytes(mapping.page_base, wlen);
            emitter.emit_raw(ViolationKind::PageUsesHostPkey, mapping.page_base,
                             witness, wlen,
                             "untrusted mapping backed by host protected PKEY");
        }

        // Code pages backing untrusted code must be strictly read-only and
        // executable (W=0, X=1). A writable code page permits runtime
        // self-modification to inject WRPKRU after load-time scanning.
        if (mapping.executable && mapping.writable) {
            std::uint8_t wlen = 0;
            auto witness = le_bytes(mapping.page_base, wlen);
            emitter.emit_raw(ViolationKind::CodePageWritable, mapping.page_base,
                             witness, wlen,
                             "executable untrusted page is also writable");
        }
    }
}

// ---------------------------------------------------------------------------
// Control-flow target / exported entry validation
// ---------------------------------------------------------------------------

inline void validate_target(addr_t target, const VerifyConfig& cfg,
                            bool is_entry, Emitter& emitter) {
    const std::string label = is_entry ? "entry point" : "jump target";

    if (in_trampoline(target, cfg.trampolines)) {
        return;  // control transfer into trusted code is allowed
    }

    const PageMapping* mapping = find_page(target, cfg);
    if (mapping == nullptr) {
        std::uint8_t wlen = 0;
        auto witness = le_bytes(target, wlen);
        emitter.emit_raw(ViolationKind::JumpTargetUnmapped, target, witness,
                        wlen, label + " lands on an unmapped page");
        return;
    }

    if (mapping->pkey == 0) {
        std::uint8_t wlen = 0;
        auto witness = le_bytes(target, wlen);
        emitter.emit_raw(ViolationKind::JumpTargetPkey0, target, witness, wlen,
                         label + " lands on a default PKEY 0 page");
        return;
    }
    if (contains_key(cfg.host_protected_pkeys, mapping->pkey)) {
        std::uint8_t wlen = 0;
        auto witness = le_bytes(target, wlen);
        emitter.emit_raw(ViolationKind::JumpTargetHostPage, target, witness,
                         wlen, label + " lands on a host protected page");
        return;
    }
    if (!mapping->executable) {
        std::uint8_t wlen = 0;
        auto witness = le_bytes(target, wlen);
        emitter.emit_raw(ViolationKind::JumpTargetNonExecutable, target,
                         witness, wlen,
                         label + " lands on a non-executable page");
        return;
    }
    // An executable plugin page that backs no declared code segment is not a
    // legal untrusted landing site either.
    if (!inside_any_segment(target, cfg)) {
        std::uint8_t wlen = 0;
        auto witness = le_bytes(target, wlen);
        emitter.emit_raw(ViolationKind::JumpTargetUnmapped, target, witness,
                         wlen,
                         label + " is outside every untrusted code segment");
        return;
    }
    if (mapping->writable) {
        std::uint8_t wlen = 0;
        auto witness = le_bytes(target, wlen);
        emitter.emit_raw(ViolationKind::CodePageWritable, target, witness,
                         wlen, label + " page is writable and executable");
    }
}

// ---------------------------------------------------------------------------
// (3) PKRU entry/exit state machine
//
//   HOST --Entry(AD=WD=1 for every host key)--> GUEST
//   GUEST --Exit(pkru == host_baseline_pkru)--> HOST
//
// Repeated entries without an exit (or exits while already in HOST) are
// ordering violations.
// ---------------------------------------------------------------------------

enum class SandboxState : unsigned { Host, Guest };

inline void validate_transitions(const VerifyConfig& cfg, Emitter& emitter) {
    SandboxState state = SandboxState::Host;

    for (const PkruTransition& transition : cfg.transitions) {
        const bool is_entry =
            transition.type == PkruTransition::Type::Entry;

        if (is_entry && state == SandboxState::Host) {
            state = SandboxState::Guest;
        } else if (!is_entry && state == SandboxState::Guest) {
            state = SandboxState::Host;
        } else {
            std::uint8_t wlen = 0;
            auto witness = le_bytes_pkru(transition.pkru, wlen);
            emitter.emit_raw(ViolationKind::TransitionOrdering,
                             transition.site_address, witness, wlen,
                             is_entry
                                 ? "Entry transition while already in GUEST"
                                 : "Exit transition while already in HOST");
            continue;
        }

        if (is_entry) {
            for (int key = 0; key < kPkeyCount; ++key) {
                bool must_deny =
                    key == 0 || contains_key(cfg.host_protected_pkeys, key);
                if (must_deny &&
                    (!pkru_ad(transition.pkru, key) ||
                     !pkru_wd(transition.pkru, key))) {
                    std::uint8_t wlen = 0;
                    auto witness = le_bytes_pkru(transition.pkru, wlen);
                    emitter.emit_raw(ViolationKind::BadEntryPkru,
                                     transition.site_address, witness, wlen,
                                     "entry PKRU leaves host key accessible");
                    break;
                }
            }
        } else {
            if (transition.pkru != cfg.host_baseline_pkru) {
                std::uint8_t wlen = 0;
                auto witness = le_bytes_pkru(transition.pkru, wlen);
                emitter.emit_raw(ViolationKind::BadExitPkru,
                                 transition.site_address, witness, wlen,
                                 "exit PKRU does not restore host baseline");
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Top-level verifier entry point
// ---------------------------------------------------------------------------

inline VerifyResult verify_sandbox(const VerifyConfig& cfg) {
    VerifyResult result;
    Emitter emitter(result.violations);

    for (const CodeSegment& segment : cfg.segments) {
        scan_segment(segment, cfg.trampolines, emitter);

        for (addr_t entry : segment.entry_points) {
            validate_target(entry, cfg, /*is_entry=*/true, emitter);
        }
        for (addr_t target : segment.jump_targets) {
            validate_target(target, cfg, /*is_entry=*/false, emitter);
        }
    }

    validate_pages(cfg, emitter);
    validate_transitions(cfg, emitter);

    result.ok = result.violations.empty();
    return result;
}

// ---------------------------------------------------------------------------
// Human-readable reporting
// ---------------------------------------------------------------------------

inline const char* violation_kind_name(ViolationKind kind) {
    switch (kind) {
        case ViolationKind::HiddenWrPkru:
            return "HiddenWrPkru";
        case ViolationKind::Xrstor:
            return "Xrstor";
        case ViolationKind::Xrstors:
            return "Xrstors";
        case ViolationKind::Syscall:
            return "Syscall";
        case ViolationKind::Int80:
            return "Int80";
        case ViolationKind::PageUsesPkey0:
            return "PageUsesPkey0";
        case ViolationKind::PageUsesHostPkey:
            return "PageUsesHostPkey";
        case ViolationKind::InvalidPkey:
            return "InvalidPkey";
        case ViolationKind::CodePageWritable:
            return "CodePageWritable";
        case ViolationKind::JumpTargetUnmapped:
            return "JumpTargetUnmapped";
        case ViolationKind::JumpTargetNonExecutable:
            return "JumpTargetNonExecutable";
        case ViolationKind::JumpTargetPkey0:
            return "JumpTargetPkey0";
        case ViolationKind::JumpTargetHostPage:
            return "JumpTargetHostPage";
        case ViolationKind::BadEntryPkru:
            return "BadEntryPkru";
        case ViolationKind::BadExitPkru:
            return "BadExitPkru";
        case ViolationKind::TransitionOrdering:
            return "TransitionOrdering";
    }
    return "Unknown";
}

inline std::string to_hex(const Violation& violation) {
    static const char kHex[] = "0123456789abcdef";
    std::string out;
    for (std::uint8_t i = 0; i < violation.witness_len; ++i) {
        if (i != 0) {
            out.push_back(' ');
        }
        byte_t value = violation.witness[i];
        out.push_back(kHex[(value >> 4) & 0x0Fu]);
        out.push_back(kHex[value & 0x0Fu]);
    }
    return out;
}

inline std::string to_string(const Violation& violation) {
    std::string out = violation_kind_name(violation.kind);
    out += " @ va=0x";
    char buffer[32];
    std::snprintf(buffer, sizeof(buffer), "%016llx",
                  static_cast<unsigned long long>(violation.address));
    out += buffer;
    if (violation.segment_offset >= 0) {
        std::snprintf(buffer, sizeof(buffer), ", offset=%lld",
                      static_cast<long long>(violation.segment_offset));
        out += buffer;
    }
    out += ", bytes=[";
    out += to_hex(violation);
    out += "], ";
    out += violation.detail;
    return out;
}

}  // namespace mpkverify

#endif  // MPK_VERIFY_MPK_VERIFY_HPP
