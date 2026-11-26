#include "machosec/machosec.h"
#include "sample_builder.h"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& expression, const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "CHECK failed: " << expression << " (" << file << ':' << line
                  << ")\n";
    }
}

#define CHECK(expression) check((expression), #expression, __FILE__, __LINE__)

class MockPkcs7Verifier final : public machosec::IPkcs7Verifier {
public:
    bool should_reject = false;
    int calls = 0;

    bool verify_pkcs7(const machosec::Pkcs7Context& context,
                      std::string& error) override {
        ++calls;
        if (context.cms_bytes.size < 8) {
            error = "mock PKCS#7 token is too short";
            return false;
        }
        if (should_reject) {
            error = "mock PKCS#7 trust anchor rejected";
            return false;
        }
        return true;
    }
};

struct VerificationExpectation {
    bool ok = false;
    machosec::Report report;
    std::string error;
};

VerificationExpectation run_verify(const std::vector<uint8_t>& bytes,
                                   machosec::VerifyOptions options = {}) {
    VerificationExpectation result;
    result.ok = machosec::verify_bytes(machosec::ByteView(bytes), options,
                                       &result.report, &result.error);
    return result;
}

machosec::VerifyOptions strict_options(machosec::IPkcs7Verifier* pkcs7 = nullptr) {
    machosec::VerifyOptions options;
    options.pkcs7_verifier = pkcs7;
    options.require_pkcs7_signature_slot = true;
    if (pkcs7 != nullptr) options.require_pkcs7_verifier = true;
    return options;
}

std::vector<uint8_t> single_macho(uint32_t cpu = machosec::sample::kCpuArm64) {
    machosec::sample::MachOConfig config;
    config.cpu_type = cpu;
    return machosec::sample::build_macho(config);
}

void flip_byte(std::vector<uint8_t>& bytes, size_t offset) {
    bytes.at(offset) = static_cast<uint8_t>(bytes.at(offset) ^ 0xffu);
}

size_t find_bytes(const std::vector<uint8_t>& haystack, const std::string& needle) {
    const std::vector<uint8_t> pattern(needle.begin(), needle.end());
    for (size_t i = 0; i + pattern.size() <= haystack.size(); ++i) {
        bool matches = true;
        for (size_t j = 0; j < pattern.size(); ++j) {
            if (haystack[i + j] != pattern[j]) {
                matches = false;
                break;
            }
        }
        if (matches) return i;
    }
    throw std::runtime_error("test pattern not found: " + needle);
}

void write_be32(std::vector<uint8_t>& bytes, size_t offset, uint32_t value) {
    bytes.at(offset) = static_cast<uint8_t>(value >> 24u);
    bytes.at(offset + 1) = static_cast<uint8_t>(value >> 16u);
    bytes.at(offset + 2) = static_cast<uint8_t>(value >> 8u);
    bytes.at(offset + 3) = static_cast<uint8_t>(value);
}

void test_sha256_known_vector() {
    const std::string input = "abc";
    const machosec::Sha256Digest digest =
        machosec::Sha256::hash(machosec::ByteView(input));
    CHECK(machosec::to_hex(digest) ==
          "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
}

void test_legal_macho_and_pkcs7_callback() {
    MockPkcs7Verifier pkcs7;
    const auto bytes = single_macho();
    const auto result = run_verify(bytes, strict_options(&pkcs7));
    CHECK(result.ok);
    CHECK(result.error.empty());
    CHECK(pkcs7.calls == 1);
    CHECK(result.report.slices.size() == 1);
    CHECK(result.report.slices[0].has_code_signature);
    CHECK(result.report.slices[0].pkcs7_verified);
    CHECK(result.report.slices[0].segments.size() == 2);
    CHECK(!result.report.slices[0].dylibs.empty());
    CHECK(!result.report.slices[0].rpaths.empty());
}

void test_legal_fat32_and_fat64_every_slice() {
    const auto arm64 = single_macho(machosec::sample::kCpuArm64);
    const auto x8664 = single_macho(machosec::sample::kCpuX8664);

    for (bool sixty_four : {false, true}) {
        const auto fat = machosec::sample::build_fat({arm64, x8664}, sixty_four);
        MockPkcs7Verifier pkcs7;
        const auto result = run_verify(fat, strict_options(&pkcs7));
        CHECK(result.ok);
        CHECK(result.report.is_fat);
        CHECK(result.report.is_fat64 == sixty_four);
        CHECK(result.report.slices.size() == 2);
        CHECK(pkcs7.calls == 2);
    }
}

void test_tampered_code_page_is_blocked() {
    auto bytes = single_macho();
    flip_byte(bytes, machosec::sample::kTextOffset + 3);
    const auto result = run_verify(bytes, strict_options());
    CHECK(!result.ok);
    CHECK(result.error.find("page SHA-256 mismatch") != std::string::npos);
}

void test_unsigned_tail_is_blocked() {
    auto bytes = single_macho();
    bytes.push_back(0x90);
    const auto result = run_verify(bytes, strict_options());
    CHECK(!result.ok);
    CHECK(result.error.find("trailing bytes") != std::string::npos);
}

void test_xml_slot_hash_tamper_is_blocked() {
    auto bytes = single_macho();
    const size_t key_offset =
        find_bytes(bytes, "com.apple.security.cs.allow-jit");
    flip_byte(bytes, key_offset + 4);
    const auto result = run_verify(bytes, strict_options());
    CHECK(!result.ok);
    CHECK(result.error.find("special slot hash mismatch") != std::string::npos);
}

void test_xml_der_semantic_divergence_is_blocked() {
    const std::string sensitive_key = "com.apple.security.cs.allow-jit";
    machosec::sample::MachOConfig config;
    config.use_custom_entitlements = true;
    config.xml_entitlements = machosec::sample::make_xml_entitlements(
        {machosec::sample::Entitlement::make_bool(sensitive_key, true)});
    config.der_entitlements = machosec::sample::make_der_entitlements(
        {machosec::sample::Entitlement::make_bool(sensitive_key, false)});

    const auto bytes = machosec::sample::build_macho(config);
    const auto result = run_verify(bytes, strict_options());
    CHECK(!result.ok);
    CHECK(result.error.find("sensitive entitlement mismatch") != std::string::npos);
}

void test_nonsensitive_divergence_can_be_ignored() {
    const std::string sensitive_key = "com.apple.security.cs.allow-jit";
    machosec::sample::MachOConfig config;
    config.use_custom_entitlements = true;
    config.xml_entitlements = machosec::sample::make_xml_entitlements({
        machosec::sample::Entitlement::make_bool(sensitive_key, true),
        machosec::sample::Entitlement::make_string("com.example.label", "xml"),
    });
    config.der_entitlements = machosec::sample::make_der_entitlements({
        machosec::sample::Entitlement::make_bool(sensitive_key, true),
        machosec::sample::Entitlement::make_string("com.example.label", "der"),
    });

    machosec::VerifyOptions sensitive_only = strict_options();
    sensitive_only.compare_only_sensitive_entitlements = true;
    CHECK(run_verify(machosec::sample::build_macho(config), sensitive_only).ok);

    machosec::VerifyOptions all_values = strict_options();
    all_values.compare_only_sensitive_entitlements = false;
    const auto result = run_verify(machosec::sample::build_macho(config), all_values);
    CHECK(!result.ok);
}

void test_unsigned_fat_slice_is_blocked() {
    machosec::sample::MachOConfig unsigned_config;
    unsigned_config.include_code_signature = false;
    const auto unsigned_slice = machosec::sample::build_macho(unsigned_config);
    const auto signed_slice = single_macho(machosec::sample::kCpuX8664);
    const auto fat = machosec::sample::build_fat({signed_slice, unsigned_slice});
    const auto result = run_verify(fat, strict_options());
    CHECK(!result.ok);
    CHECK(result.error.find("no LC_CODE_SIGNATURE") != std::string::npos);
}

void test_overlapping_fat_slices_are_blocked() {
    const auto first = single_macho(machosec::sample::kCpuArm64);
    const auto second = single_macho(machosec::sample::kCpuX8664);
    auto fat = machosec::sample::build_fat({first, second}, false);
    // The second fat_arch begins at byte 28; its offset field is at +8.
    // Point just inside the first slice while remaining past the Fat table.
    write_be32(fat, 28 + 8, 52);
    const auto result = run_verify(fat, strict_options());
    CHECK(!result.ok);
    CHECK(result.error.find("Fat slices overlap") != std::string::npos);
}

void test_out_of_bounds_fat_slice_is_blocked() {
    const auto first = single_macho(machosec::sample::kCpuArm64);
    const auto second = single_macho(machosec::sample::kCpuX8664);
    auto fat = machosec::sample::build_fat({first, second}, false);
    write_be32(fat, 28 + 8, static_cast<uint32_t>(fat.size() + 4096));
    const auto result = run_verify(fat, strict_options());
    CHECK(!result.ok);
}

void test_wx_overlap_and_wrapping_segments_blocked() {
    machosec::sample::MachOConfig wx_config;
    wx_config.wx_segment = true;
    CHECK(!run_verify(machosec::sample::build_macho(wx_config)).ok);

    machosec::sample::MachOConfig overlap_config;
    overlap_config.overlapping_vm_segments = true;
    const auto overlap = run_verify(machosec::sample::build_macho(overlap_config));
    CHECK(!overlap.ok);
    CHECK(overlap.error.find("virtual addresses overlap") != std::string::npos);

    machosec::sample::MachOConfig wrap_config;
    wrap_config.overflowing_file_range = true;
    const auto wrap = run_verify(machosec::sample::build_macho(wrap_config));
    CHECK(!wrap.ok);
    CHECK(wrap.error.find("range") != std::string::npos);
}

void test_relative_rpaths_are_blocked() {
    for (const std::string& rpath :
         {"../Frameworks", "./lib", "relative/path", "@executable_path/Frameworks",
          "/safe/../escape", ""}) {
        CHECK(machosec::is_dangerous_rpath(rpath));
        if (!rpath.empty()) {
            machosec::sample::MachOConfig config;
            config.rpath = rpath;
            const auto result = run_verify(machosec::sample::build_macho(config));
            CHECK(!result.ok);
            CHECK(result.error.find("LC_RPATH") != std::string::npos);
        }
    }
    CHECK(!machosec::is_dangerous_rpath("/Library/Frameworks"));
}

void test_runtime_flag_is_required() {
    machosec::sample::MachOConfig config;
    config.hardened_runtime = false;
    const auto bytes = machosec::sample::build_macho(config);
    CHECK(!run_verify(bytes, strict_options()).ok);

    machosec::VerifyOptions lenient = strict_options();
    lenient.require_hardened_runtime = false;
    CHECK(run_verify(bytes, lenient).ok);
}

void test_pkcs7_rejection_propagates() {
    MockPkcs7Verifier pkcs7;
    pkcs7.should_reject = true;
    const auto result = run_verify(single_macho(), strict_options(&pkcs7));
    CHECK(!result.ok);
    CHECK(result.error.find("PKCS#7") != std::string::npos);
}

void test_every_truncation_fails_safely() {
    const auto macho = single_macho();
    for (size_t length = 0; length < macho.size(); ++length) {
        std::vector<uint8_t> prefix(macho.begin(),
                                    macho.begin() + static_cast<std::ptrdiff_t>(length));
        std::string error;
        const bool valid = machosec::verify_bytes(prefix, strict_options(),
                                                  nullptr, &error);
        if (valid) {
            check(false, "truncated sample must not verify", __FILE__, __LINE__);
            return;
        }
    }

    const auto fat = machosec::sample::build_fat(
        {single_macho(machosec::sample::kCpuArm64),
         single_macho(machosec::sample::kCpuX8664)},
        true);
    for (size_t length = 0; length < fat.size(); ++length) {
        std::vector<uint8_t> prefix(fat.begin(),
                                    fat.begin() + static_cast<std::ptrdiff_t>(length));
        std::string error;
        const bool valid = machosec::verify_bytes(prefix, strict_options(),
                                                  nullptr, &error);
        if (valid) {
            check(false, "truncated Fat sample must not verify", __FILE__, __LINE__);
            return;
        }
    }
    CHECK(true);
}

void test_random_mutations_are_memory_safe() {
    const std::vector<std::vector<uint8_t>> seeds = {
        single_macho(),
        machosec::sample::build_fat(
            {single_macho(machosec::sample::kCpuArm64),
             single_macho(machosec::sample::kCpuX8664)},
            true),
    };

    uint32_t random_state = 0x12345678u;
    auto next_random = [&random_state]() {
        random_state ^= random_state << 13u;
        random_state ^= random_state >> 17u;
        random_state ^= random_state << 5u;
        return random_state;
    };

    for (int iteration = 0; iteration < 3000; ++iteration) {
        std::vector<uint8_t> bytes = seeds[size_t(iteration) % seeds.size()];
        const int mutation_count = 1 + int(next_random() % 4u);
        for (int mutation = 0; mutation < mutation_count; ++mutation) {
            const size_t offset = size_t(next_random()) % bytes.size();
            bytes[offset] = static_cast<uint8_t>(next_random() & 0xffu);
        }
        std::string error;
        machosec::Report report;
        (void)machosec::verify_bytes(bytes, strict_options(), &report, &error);
    }
    CHECK(true);
}

}  // namespace

int main() {
    test_sha256_known_vector();
    test_legal_macho_and_pkcs7_callback();
    test_legal_fat32_and_fat64_every_slice();
    test_tampered_code_page_is_blocked();
    test_unsigned_tail_is_blocked();
    test_xml_slot_hash_tamper_is_blocked();
    test_xml_der_semantic_divergence_is_blocked();
    test_nonsensitive_divergence_can_be_ignored();
    test_unsigned_fat_slice_is_blocked();
    test_overlapping_fat_slices_are_blocked();
    test_out_of_bounds_fat_slice_is_blocked();
    test_wx_overlap_and_wrapping_segments_blocked();
    test_relative_rpaths_are_blocked();
    test_runtime_flag_is_required();
    test_pkcs7_rejection_propagates();
    test_every_truncation_fails_safely();
    test_random_mutations_are_memory_safe();

    if (g_failures != 0) {
        std::cerr << g_failures << " failure(s) in " << g_checks << " checks\n";
        return 1;
    }
    std::cout << "All " << g_checks << " Mach-O/signature checks passed\n";
    return 0;
}
