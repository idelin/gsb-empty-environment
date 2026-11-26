#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace machosec::sample {

inline constexpr size_t kSignatureOffset = 4096;
inline constexpr size_t kTextOffset = 512;
inline constexpr uint32_t kCpuX8664 = 0x01000007u;
inline constexpr uint32_t kCpuArm64 = 0x0100000cu;

struct Entitlement {
    enum class Type { Boolean, String, Integer };
    std::string key;
    Type type = Type::Boolean;
    bool boolean = true;
    std::string string;
    int64_t integer = 0;

    static Entitlement make_bool(std::string key, bool value) {
        Entitlement result;
        result.key = std::move(key);
        result.type = Type::Boolean;
        result.boolean = value;
        return result;
    }

    static Entitlement make_string(std::string key, std::string value) {
        Entitlement result;
        result.key = std::move(key);
        result.type = Type::String;
        result.string = std::move(value);
        return result;
    }

    static Entitlement make_integer(std::string key, int64_t value) {
        Entitlement result;
        result.key = std::move(key);
        result.type = Type::Integer;
        result.integer = value;
        return result;
    }
};

std::vector<Entitlement> default_entitlements(bool jit = true);
std::string make_xml_entitlements(const std::vector<Entitlement>& entitlements);
std::vector<uint8_t> make_der_entitlements(
    const std::vector<Entitlement>& entitlements);

struct MachOConfig {
    uint32_t cpu_type = kCpuArm64;
    uint32_t cpu_subtype = 0;
    std::string rpath = "/Library/machosec/runtime";
    std::string dylib = "/usr/lib/libSystem.B.dylib";
    std::string identifier = "machosec.test";

    bool include_code_signature = true;
    bool hardened_runtime = true;
    bool wx_segment = false;
    bool overlapping_vm_segments = false;
    bool overflowing_file_range = false;

    bool use_custom_entitlements = false;
    std::string xml_entitlements;
    std::vector<uint8_t> der_entitlements;
};

std::vector<uint8_t> build_macho(const MachOConfig& config = {});

std::vector<uint8_t> build_fat(
    const std::vector<std::vector<uint8_t>>& slices,
    bool sixty_four = false);

}  // namespace machosec::sample
