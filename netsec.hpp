#pragma once

#include <array>
#include <cstddef>
#include <cstdint>

namespace netsec {

inline constexpr std::size_t kPmpEntryCount = 16;

enum class PmpMode : std::uint8_t {
    Off,
    Tor,
    Napot,
    Na4,
};

enum class PrivilegeMode : std::uint8_t {
    M,
    S,
    U,
};

inline constexpr PrivilegeMode MachineMode = PrivilegeMode::M;
inline constexpr PrivilegeMode SupervisorMode = PrivilegeMode::S;
inline constexpr PrivilegeMode UserMode = PrivilegeMode::U;

enum class AccessType : std::uint8_t {
    Read,
    Write,
    Execute,
};

enum class PmpDecision : std::uint8_t {
    Allow,
    Deny,
};

using AccessDecision = PmpDecision;

// pmpaddr is the raw PMP address-register value (PA[63:2] on RV64).
// The evaluator performs the RISC-V byte-address conversion internally.
struct PmpEntry {
    std::uint64_t pmpaddr = 0;
    PmpMode mode = PmpMode::Off;
    bool read = false;
    bool write = false;
    bool execute = false;
    bool locked = false;
};

struct PmpAccess {
    std::uint64_t address = 0;
    std::uint64_t size = 0;
    PrivilegeMode privilege = PrivilegeMode::M;
    AccessType type = AccessType::Read;
};

class PmpEvaluator {
public:
    using EntryArray = std::array<PmpEntry, kPmpEntryCount>;

    PmpEvaluator() = default;
    explicit PmpEvaluator(const EntryArray& entries);

    void setEntry(std::size_t index, const PmpEntry& entry);
    const PmpEntry& entry(std::size_t index) const;
    const EntryArray& entries() const noexcept;

    PmpDecision evaluate(const PmpAccess& access) const noexcept;
    PmpDecision evaluate(std::uint64_t address, std::uint64_t size,
                         PrivilegeMode privilege,
                         AccessType type) const noexcept;
    bool allows(const PmpAccess& access) const noexcept;

private:
    EntryArray entries_{};
};

PmpDecision evaluatePmpAccess(const PmpEvaluator::EntryArray& entries,
                              const PmpAccess& access) noexcept;

}  // namespace netsec
