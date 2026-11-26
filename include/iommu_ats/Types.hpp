#pragma once

#include <cstdint>
#include <functional>
#include <optional>
#include <string>
#include <string_view>

namespace iommu_ats {

using u8 = std::uint8_t;
using u16 = std::uint16_t;
using u32 = std::uint32_t;
using u64 = std::uint64_t;

using PhysicalAddress = u64;
using VirtualAddress = u64;
using Iova = u64;
using DomainId = u32;
using Pasid = u32;
using TenantId = u32;
using TicketId = u64;
using CommandId = u64;

inline constexpr u64 PAGE_SIZE_4K = 4096ULL;
inline constexpr u64 PAGE_SIZE_2M = 2ULL * 1024ULL * 1024ULL;
inline constexpr u64 PAGE_SIZE_1G = 1024ULL * 1024ULL * 1024ULL;

enum class DmaOp {
    Read,
    Write,
};

[[nodiscard]] constexpr std::string_view toString(DmaOp op) noexcept {
    return op == DmaOp::Read ? "R" : "W";
}

struct AccessPermissions {
    bool read = false;
    bool write = false;
    bool execute = false;

    [[nodiscard]] static constexpr AccessPermissions none() noexcept {
        return {false, false, false};
    }

    [[nodiscard]] static constexpr AccessPermissions readOnly() noexcept {
        return {true, false, false};
    }

    [[nodiscard]] static constexpr AccessPermissions readWrite() noexcept {
        return {true, true, false};
    }

    [[nodiscard]] static constexpr AccessPermissions readExecute() noexcept {
        return {true, false, true};
    }

    [[nodiscard]] static constexpr AccessPermissions all() noexcept {
        return {true, true, true};
    }

    [[nodiscard]] constexpr bool allows(DmaOp op) const noexcept {
        return op == DmaOp::Read ? read : write;
    }

    [[nodiscard]] constexpr bool allowsInstructionFetch() const noexcept {
        return execute;
    }

    [[nodiscard]] constexpr AccessPermissions intersection(
        const AccessPermissions& other) const noexcept {
        return {read && other.read, write && other.write,
                execute && other.execute};
    }

    [[nodiscard]] constexpr bool operator==(
        const AccessPermissions& other) const noexcept {
        return read == other.read && write == other.write &&
               execute == other.execute;
    }
};

struct Bdf {
    u8 bus = 0;
    u8 device = 0;
    u8 function = 0;

    [[nodiscard]] constexpr u16 requesterId() const noexcept {
        return static_cast<u16>((static_cast<u16>(bus) << 8U) |
                                (static_cast<u16>(device) << 3U) |
                                static_cast<u16>(function));
    }

    [[nodiscard]] static constexpr Bdf fromRequesterId(u16 id) noexcept {
        return {static_cast<u8>((id >> 8U) & 0xFFU),
                static_cast<u8>((id >> 3U) & 0x1FU),
                static_cast<u8>(id & 0x07U)};
    }

    [[nodiscard]] static std::optional<Bdf> parse(std::string_view text) {
        // Accept BB:DD.F and the shorter B:D.F forms used in tests.
        const auto first_colon = text.find(':');
        const auto dot = text.rfind('.');
        if (first_colon == std::string_view::npos ||
            dot == std::string_view::npos || dot + 1U >= text.size()) {
            return std::nullopt;
        }

        unsigned long bus_value = 0;
        unsigned long dev_value = 0;
        unsigned func_value = 0;
        try {
            const std::string bus_text(text.substr(0, first_colon));
            const std::string dev_text(
                text.substr(first_colon + 1U, dot - first_colon - 1U));
            const std::string func_text(text.substr(dot + 1U));
            std::size_t bus_used = 0;
            std::size_t dev_used = 0;
            std::size_t func_used = 0;
            bus_value = std::stoul(bus_text, &bus_used, 16);
            dev_value = std::stoul(dev_text, &dev_used, 16);
            func_value = static_cast<unsigned>(
                std::stoul(func_text, &func_used, 10));
            if (bus_used != bus_text.size() ||
                dev_used != dev_text.size() ||
                func_used != func_text.size() || bus_value > 0xFFU ||
                dev_value > 0x1FU || func_value > 7U) {
                return std::nullopt;
            }
        } catch (...) {
            return std::nullopt;
        }

        return Bdf{static_cast<u8>(bus_value), static_cast<u8>(dev_value),
                  static_cast<u8>(func_value)};
    }

    [[nodiscard]] std::string toString() const {
        constexpr char hex[] = "0123456789abcdef";
        std::string result("00:00.0");
        result[0] = hex[(bus >> 4U) & 0xFU];
        result[1] = hex[bus & 0xFU];
        result[3] = hex[(device >> 4U) & 0x1U];
        result[4] = hex[device & 0xFU];
        result[6] = static_cast<char>('0' + function);
        return result;
    }

    [[nodiscard]] constexpr bool operator==(const Bdf& other) const noexcept {
        return bus == other.bus && device == other.device &&
               function == other.function;
    }

    [[nodiscard]] constexpr bool operator<(const Bdf& other) const noexcept {
        return requesterId() < other.requesterId();
    }
};

struct BdfHash {
    [[nodiscard]] std::size_t operator()(Bdf bdf) const noexcept {
        return std::hash<u16>{}(bdf.requesterId());
    }
};

struct DmaRequest {
    Bdf bdf{};
    Pasid pasid = 0;
    Iova iova = 0;
    u32 length = 0;
    DmaOp op = DmaOp::Read;

    [[nodiscard]] constexpr Iova end() const noexcept {
        return iova + static_cast<Iova>(length);
    }
};

} // namespace iommu_ats
