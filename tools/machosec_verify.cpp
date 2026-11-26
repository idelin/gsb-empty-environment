#include "machosec/machosec.h"

#include <iostream>
#include <string>

namespace {

void print_report(const machosec::Report& report) {
    std::cout << "format: " << (report.is_fat ? (report.is_fat64 ? "fat64" : "fat")
                                              : "mach-o64")
              << '\n';
    for (const machosec::SliceReport& slice : report.slices) {
        std::cout << "slice: " << slice.architecture;
        if (report.is_fat) {
            std::cout << " offset=" << slice.fat_offset
                      << " size=" << slice.fat_size;
        }
        std::cout << "\n  commands=" << slice.load_commands.size()
                  << " segments=" << slice.segments.size()
                  << " codeSlots=" << slice.code_slots
                  << " specialSlots=" << slice.special_slots
                  << " pageShift=" << int(slice.page_size_shift)
                  << " cdHash=" << machosec::to_hex(slice.code_directory_hash)
                  << "\n  pkcs7Slot=" << slice.pkcs7_slot_present
                  << " pkcs7Verified=" << slice.pkcs7_verified << '\n';
        for (const std::string& rpath : slice.rpaths) {
            std::cout << "  rpath: " << rpath << '\n';
        }
    }
}

}  // namespace

int main(int argc, char** argv) {
    if (argc < 2) {
        std::cerr << "usage: machosec_verify [--no-runtime] "
                     "[--allow-relative-rpath] [--all-entitlements] "
                     "[--require-pkcs7-callback] <mach-o-or-fat>\n";
        return 2;
    }

    machosec::VerifyOptions options;
    std::string path;
    for (int i = 1; i < argc; ++i) {
        const std::string argument = argv[i];
        if (argument == "--no-runtime") {
            options.require_hardened_runtime = false;
        } else if (argument == "--allow-relative-rpath") {
            options.reject_relative_rpaths = false;
        } else if (argument == "--sensitive-only") {
            options.compare_only_sensitive_entitlements = true;
        } else if (argument == "--all-entitlements") {
            options.compare_only_sensitive_entitlements = false;
        } else if (argument == "--require-pkcs7-callback") {
            options.require_pkcs7_verifier = true;
            options.require_pkcs7_signature_slot = true;
        } else if (!argument.empty() && argument[0] == '-') {
            std::cerr << "unknown option: " << argument << '\n';
            return 2;
        } else {
            path = argument;
        }
    }

    if (path.empty()) {
        std::cerr << "missing binary path\n";
        return 2;
    }

    machosec::Report report;
    std::string error;
    if (!machosec::verify_file(path, options, &report, &error)) {
        std::cerr << "REJECT " << path << ": " << error << '\n';
        return 1;
    }

    print_report(report);
    std::cout << "ACCEPT " << path << '\n';
    if (options.pkcs7_verifier == nullptr && report.slices.size() == 1 &&
        report.slices[0].pkcs7_slot_present) {
        std::cout << "note: structural checks passed; no PKCS#7 callback was supplied\n";
    }
    return 0;
}
