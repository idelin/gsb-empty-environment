#include "sample_builder.h"

#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>

namespace fs = std::filesystem;
using machosec::sample::Entitlement;
using machosec::sample::MachOConfig;

namespace {

void write_file(const fs::path& path, const std::vector<uint8_t>& bytes) {
    std::ofstream output(path, std::ios::binary | std::ios::trunc);
    if (!output) throw std::runtime_error("cannot write " + path.string());
    output.write(reinterpret_cast<const char*>(bytes.data()),
                 static_cast<std::streamsize>(bytes.size()));
    if (!output) throw std::runtime_error("failed writing " + path.string());
}

std::vector<uint8_t> entitlement_divergence() {
    const std::string key = "com.apple.security.cs.allow-unsigned-executable-memory";
    MachOConfig config;
    config.use_custom_entitlements = true;
    config.xml_entitlements = machosec::sample::make_xml_entitlements(
        {Entitlement::make_bool(key, true)});
    config.der_entitlements = machosec::sample::make_der_entitlements(
        {Entitlement::make_bool(key, false)});
    return machosec::sample::build_macho(config);
}

std::vector<uint8_t> unsigned_fat() {
    MachOConfig unsigned_config;
    unsigned_config.include_code_signature = false;
    const auto unsigned_slice = machosec::sample::build_macho(unsigned_config);
    MachOConfig signed_config;
    signed_config.cpu_type = machosec::sample::kCpuX8664;
    const auto signed_slice = machosec::sample::build_macho(signed_config);
    return machosec::sample::build_fat({signed_slice, unsigned_slice}, false);
}

}  // namespace

int main(int argc, char** argv) {
    if (argc != 2) {
        std::cerr << "usage: machosec_samples <output-directory>\n";
        return 2;
    }

    try {
        const fs::path directory(argv[1]);
        fs::create_directories(directory);

        MachOConfig arm_config;
        arm_config.cpu_type = machosec::sample::kCpuArm64;
        const auto arm64 = machosec::sample::build_macho(arm_config);

        MachOConfig x86_config;
        x86_config.cpu_type = machosec::sample::kCpuX8664;
        const auto x8664 = machosec::sample::build_macho(x86_config);

        auto tampered_code = arm64;
        tampered_code[machosec::sample::kTextOffset + 7] ^= 0x01;

        auto tail_injection = arm64;
        tail_injection.push_back(0xcc);

        MachOConfig rpath_config;
        rpath_config.rpath = "@executable_path/../PlugIns";

        write_file(directory / "legal-arm64-macho.bin", arm64);
        write_file(directory / "legal-fat32.bin",
                   machosec::sample::build_fat({arm64, x8664}, false));
        write_file(directory / "legal-fat64.bin",
                   machosec::sample::build_fat({arm64, x8664}, true));
        write_file(directory / "tampered-code-page.bin", tampered_code);
        write_file(directory / "tail-injection.bin", tail_injection);
        write_file(directory / "xml-der-divergence.bin", entitlement_divergence());
        write_file(directory / "unsigned-slice-fat.bin", unsigned_fat());
        write_file(directory / "relative-rpath.bin",
                   machosec::sample::build_macho(rpath_config));

        std::cout << "wrote byte-level Mach-O/Fat samples to " << directory << '\n';
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "sample generation failed: " << error.what() << '\n';
        return 1;
    }
}
