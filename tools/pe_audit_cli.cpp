#include "pe_audit/pe_audit.h"

#include <iostream>
#include <string>

namespace {

void print_usage(const char* program) {
    std::cerr << "usage: " << program
              << " [--allow-dll <name.dll>]... <pe32plus-file>\n";
}

}  // namespace

int main(int argc, char** argv) {
    pe_audit::AuditOptions options;
    std::string path;
    for (int index = 1; index < argc; ++index) {
        const std::string argument(argv[index]);
        if (argument == "--allow-dll") {
            if (index + 1 >= argc) {
                print_usage(argv[0]);
                return 2;
            }
            ++index;
            options.additional_allowed_dlls.emplace_back(argv[index]);
            continue;
        }
        if (!path.empty()) {
            print_usage(argv[0]);
            return 2;
        }
        path = argument;
    }
    if (path.empty()) {
        print_usage(argv[0]);
        return 2;
    }

    const pe_audit::Report report = pe_audit::audit_file(path, options);
    std::cout << "file: " << path << '\n';
    std::cout << "delay imports: " << report.delay_imports.size() << '\n';
    std::cout << "relocations: " << report.relocations.size() << '\n';
    std::cout << "guard flags: "
              << (report.load_config.guard_flags_available ? "present"
                                                            : "unavailable")
              << '\n';
    std::cout << "GUARD_CF: "
              << (report.guard_cf_present ? "yes" : "no") << '\n';
    std::cout << "delay IAT protected: "
              << (report.delay_iat_protected ? "yes" : "no") << '\n';

    for (const pe_audit::Finding& finding : report.findings) {
        std::cout << pe_audit::severity_name(finding.severity) << ": "
                  << pe_audit::issue_code_name(finding.code) << " at 0x"
                  << std::hex << finding.rva << std::dec;
        if (!finding.detail.empty()) {
            std::cout << " (" << finding.detail << ')';
        }
        std::cout << '\n';
    }
    return report.ok() ? 0 : 1;
}
