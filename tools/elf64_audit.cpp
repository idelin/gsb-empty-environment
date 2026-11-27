#include "elf64_audit/elf64_audit.hpp"

#include <cstddef>
#include <iostream>
#include <string>

int main(int argc, char** argv) {
    if (argc != 2) {
        std::cerr << "usage: elf64-audit <elf64-file>\n";
        return 2;
    }

    const std::string path(argv[1]);
    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64_file(path);

    std::cout << "file: " << path << '\n';
    std::cout << "parsed: " << (report.parsed ? "yes" : "no") << '\n';
    std::cout << "relro: "
              << elf64_audit::to_string(report.relro) << '\n';
    std::cout << "immediate binding: "
              << (report.immediate_binding ? "yes" : "no") << '\n';
    std::cout << "executable stack: "
              << (report.executable_stack ? "yes" : "no") << '\n';
    std::cout << "unsafe IFUNC/IRELATIVE: "
              << (report.unsafe_ifunc ? "yes" : "no") << '\n';

    for (const elf64_audit::Finding& finding : report.findings) {
        std::cout << elf64_audit::to_string(finding.severity) << ": "
                  << elf64_audit::to_string(finding.code) << ": "
                  << finding.message;
        if (finding.file_offset != 0U) {
            std::cout << " (file offset " << finding.file_offset << ')';
        }
        std::cout << '\n';
    }

    if (!report.parsed) {
        return 2;
    }
    return report.admission_passed ? 0 : 1;
}
