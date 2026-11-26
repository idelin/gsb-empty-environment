#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <string_view>
#include <vector>

namespace machosec {

inline constexpr uint32_t MH_MAGIC_64 = 0xFEEDFACFu;
inline constexpr uint32_t FAT_MAGIC = 0xCAFEBABEu;
inline constexpr uint32_t FAT_MAGIC_64 = 0xCAFEBABFu;

inline constexpr uint32_t LC_SEGMENT_64 = 0x19u;
inline constexpr uint32_t LC_LOAD_DYLIB = 0x0Cu;
inline constexpr uint32_t LC_CODE_SIGNATURE = 0x1Du;
inline constexpr uint32_t LC_RPATH = 0x8000001Cu;

inline constexpr uint32_t VM_PROT_READ = 0x1u;
inline constexpr uint32_t VM_PROT_WRITE = 0x2u;
inline constexpr uint32_t VM_PROT_EXECUTE = 0x4u;

inline constexpr uint32_t CSMAGIC_REQUIREMENTS = 0xFADE0C01u;
inline constexpr uint32_t CSMAGIC_CODEDIRECTORY = 0xFADE0C02u;
inline constexpr uint32_t CSMAGIC_EMBEDDED_SIGNATURE = 0xFADE0CC0u;
inline constexpr uint32_t CSMAGIC_ENTITLEMENTS_XML = 0xFADE7171u;
inline constexpr uint32_t CSMAGIC_ENTITLEMENTS_DER = 0xFADE7172u;
inline constexpr uint32_t CSMAGIC_BLOBWRAPPER = 0xFADE0B01u;

inline constexpr uint32_t CSSLOT_REQUIREMENTS = 2u;
inline constexpr uint32_t CSSLOT_ENTITLEMENTS_XML = 5u;
inline constexpr uint32_t CSSLOT_ENTITLEMENTS_DER = 7u;
inline constexpr uint32_t CSSLOT_SIGNATURE = 0x10000u;

inline constexpr uint32_t CS_RUNTIME = 0x00010000u;
inline constexpr uint8_t CS_HASHTYPE_SHA256 = 2u;
inline constexpr uint8_t CS_SHA256_SIZE = 32u;

using Sha256Digest = std::array<uint8_t, CS_SHA256_SIZE>;

struct ByteView {
    const uint8_t* data = nullptr;
    size_t size = 0;

    ByteView() = default;
    ByteView(const uint8_t* input_data, size_t input_size)
        : data(input_data), size(input_size) {}
    ByteView(const std::vector<uint8_t>& bytes)  // NOLINT(google-explicit-constructor)
        : data(bytes.data()), size(bytes.size()) {}
    ByteView(const std::string& bytes)  // NOLINT(google-explicit-constructor)
        : data(reinterpret_cast<const uint8_t*>(bytes.data())), size(bytes.size()) {}
};

class Sha256 {
public:
    Sha256();
    void update(ByteView data);
    void finish(Sha256Digest& digest);
    static Sha256Digest hash(ByteView data);

private:
    std::array<uint32_t, 8> state_{};
    std::array<uint8_t, 64> buffer_{};
    uint64_t bit_length_ = 0;
    size_t buffer_size_ = 0;
};

std::string to_hex(const Sha256Digest& digest);

struct Pkcs7Context {
    uint32_t super_blob_slot = CSSLOT_SIGNATURE;
    ByteView cms_bytes;
    ByteView code_directory;
    Sha256Digest code_directory_hash{};
};

class IPkcs7Verifier {
public:
    IPkcs7Verifier() = default;
    IPkcs7Verifier(const IPkcs7Verifier&) = delete;
    IPkcs7Verifier& operator=(const IPkcs7Verifier&) = delete;
    virtual ~IPkcs7Verifier() = default;

    virtual bool verify_pkcs7(const Pkcs7Context& context, std::string& error) = 0;
};

using Pkcs7Callback =
    std::function<bool(const Pkcs7Context& context, std::string& error)>;

struct VerifyOptions {
    IPkcs7Verifier* pkcs7_verifier = nullptr;
    Pkcs7Callback pkcs7_callback;

    bool require_hardened_runtime = true;
    bool require_requirements_slot = true;
    bool require_xml_entitlements = true;
    bool require_der_entitlements = true;
    bool require_entitlement_semantic_equality = true;
    bool compare_only_sensitive_entitlements = true;

    bool require_pkcs7_verifier = false;
    bool require_pkcs7_signature_slot = false;

    bool reject_relative_rpaths = true;
    bool allow_zero_signature_padding = true;
    bool reject_file_segment_overlap = true;
};

struct LoadCommandInfo {
    uint32_t cmd = 0;
    uint32_t cmd_size = 0;
    size_t file_offset = 0;
    std::string description;
};

struct SegmentInfo {
    std::string name;
    uint64_t vmaddr = 0;
    uint64_t vmsize = 0;
    uint64_t fileoff = 0;
    uint64_t filesize = 0;
    uint32_t maxprot = 0;
    uint32_t initprot = 0;
    uint32_t section_count = 0;
};

struct SliceReport {
    std::string architecture;
    uint32_t cpu_type = 0;
    uint32_t cpu_subtype = 0;
    uint64_t fat_offset = 0;
    uint64_t fat_size = 0;

    std::vector<LoadCommandInfo> load_commands;
    std::vector<SegmentInfo> segments;
    std::vector<std::string> dylibs;
    std::vector<std::string> rpaths;

    bool has_code_signature = false;
    uint64_t code_signature_offset = 0;
    uint64_t code_signature_size = 0;
    uint32_t code_directory_version = 0;
    uint32_t code_directory_flags = 0;
    uint32_t code_slots = 0;
    uint32_t special_slots = 0;
    uint8_t page_size_shift = 0;
    Sha256Digest code_directory_hash{};

    bool pkcs7_slot_present = false;
    bool pkcs7_verifier_called = false;
    bool pkcs7_verified = false;
};

struct Report {
    bool is_fat = false;
    bool is_fat64 = false;
    std::vector<SliceReport> slices;
};

bool is_dangerous_rpath(std::string_view rpath);

bool verify_bytes(ByteView bytes,
                  const VerifyOptions& options = {},
                  Report* report = nullptr,
                  std::string* error = nullptr);

bool verify_file(const std::string& path,
                 const VerifyOptions& options = {},
                 Report* report = nullptr,
                 std::string* error = nullptr);

}  // namespace machosec
