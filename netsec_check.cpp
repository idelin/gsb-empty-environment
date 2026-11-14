#include "netsec.hpp"

#include <charconv>
#include <cstdint>
#include <iostream>
#include <string>
#include <string_view>

namespace {

void print_help(std::ostream& output) {
    output << "usage: netsec-check [--json] [--page-size BYTES] FILE\n"
              "\n"
              "Inspect an ELF64 little-endian x86-64 ET_DYN image without\n"
              "mapping or executing it. Exit status is 0 for an allow\n"
              "decision, 1 for a reject decision, and 2 for usage errors.\n";
}

std::string json_escape(std::string_view text) {
    std::string escaped;
    escaped.reserve(text.size() + 8);
    for (const char character : text) {
        switch (character) {
        case '"':
            escaped += "\\\"";
            break;
        case '\\':
            escaped += "\\\\";
            break;
        case '\n':
            escaped += "\\n";
            break;
        case '\r':
            escaped += "\\r";
            break;
        case '\t':
            escaped += "\\t";
            break;
        default:
            escaped += character;
            break;
        }
    }
    return escaped;
}

void print_json(const netsec::PolicyResult& report) {
    std::cout << "{\n";
    std::cout << "  \"decision\": \"" << netsec::to_string(report.decision)
              << "\",\n";
    std::cout << "  \"summary\": {\"loads\": " << report.summary.loads.size()
              << ", \"symbols\": " << report.summary.symbol_count
              << ", \"relocations\": " << report.summary.relocation_count
              << ", \"jump_slots\": " << report.summary.jump_slot_count
              << ", \"glob_dat\": " << report.summary.glob_dat_count
              << ", \"copy\": " << report.summary.copy_count
              << ", \"bind_now\": "
              << (report.summary.bind_now ? "true" : "false")
              << ", \"has_relro\": "
              << (report.summary.has_relro ? "true" : "false")
              << ", \"relro_page_start\": "
              << report.summary.relro_page_start
              << ", \"relro_page_end\": " << report.summary.relro_page_end
              << "},\n";
    std::cout << "  \"findings\": [";
    for (std::size_t index = 0; index < report.findings.size(); ++index) {
        const netsec::Finding& finding = report.findings[index];
        if (index != 0) {
            std::cout << ',';
        }
        std::cout << "\n    {\"code\": \"" << netsec::to_string(finding.code)
                  << "\", \"severity\": \"" << netsec::to_string(finding.severity)
                  << "\", \"message\": \""
                  << json_escape(finding.message) << "\", \"file_offset\": "
                  << finding.source.file_offset << ", \"file_size\": "
                  << finding.source.size << ", \"virtual_address\": "
                  << finding.source.virtual_address;
        if (finding.relocation) {
            std::cout << ", \"relocation_file_offset\": "
                      << finding.relocation->file_offset;
        }
        if (finding.target) {
            std::cout << ", \"target_file_offset\": "
                      << finding.target->file_offset
                      << ", \"target_virtual_address\": "
                      << finding.target->virtual_address;
        }
        std::cout << '}';
    }
    if (!report.findings.empty()) {
        std::cout << '\n';
    }
    std::cout << "  ]\n";
    std::cout << "}\n";
}

bool parse_page_size(std::string_view text, std::uint64_t& value) {
    int base = 10;
    if (text.starts_with("0x") || text.starts_with("0X")) {
        text.remove_prefix(2);
        base = 16;
    }
    if (text.empty()) {
        return false;
    }
    const auto result =
        std::from_chars(text.data(), text.data() + text.size(), value, base);
    return result.ec == std::errc{} &&
           result.ptr == text.data() + text.size();
}

} // namespace

int main(int argc, char** argv) {
    bool json = false;
    netsec::PolicyOptions options;
    std::string path;
    for (int index = 1; index < argc; ++index) {
        const std::string_view argument(argv[index]);
        if (argument == "--help" || argument == "-h") {
            print_help(std::cout);
            return 0;
        }
        if (argument == "--json") {
            json = true;
            continue;
        }
        if (argument == "--page-size") {
            if (index + 1 >= argc ||
                !parse_page_size(argv[++index], options.page_size)) {
                std::cerr << "netsec-check: invalid --page-size\n";
                return 2;
            }
            continue;
        }
        if (argument.starts_with("-")) {
            std::cerr << "netsec-check: unknown option " << argument << '\n';
            return 2;
        }
        if (!path.empty()) {
            std::cerr << "netsec-check: more than one input file\n";
            return 2;
        }
        path = std::string(argument);
    }
    if (path.empty()) {
        print_help(std::cerr);
        return 2;
    }
    if (options.page_size == 0 ||
        (options.page_size & (options.page_size - 1)) != 0) {
        std::cerr << "netsec-check: --page-size must be a power of two\n";
        return 2;
    }

    const netsec::PolicyResult report = netsec::analyze_file(path, options);
    if (json) {
        print_json(report);
    } else {
        std::cout << report.format();
    }
    return report.allowed() ? 0 : 1;
}
