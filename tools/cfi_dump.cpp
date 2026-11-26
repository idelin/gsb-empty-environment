#include "dwarf/cfi.hpp"

#include <cstdlib>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <iterator>
#include <string>
#include <string_view>
#include <vector>

namespace {

std::uint64_t parse_number(const std::string& text) {
    std::size_t consumed = 0;
    const std::uint64_t value = std::stoull(text, &consumed, 0);
    if (consumed != text.size()) {
        throw std::invalid_argument("invalid number: " + text);
    }
    return value;
}

const char* kind_name(dwarf::cfi::SectionKind kind) {
    switch (kind) {
    case dwarf::cfi::SectionKind::auto_detect:
        return "auto";
    case dwarf::cfi::SectionKind::debug_frame:
        return ".debug_frame";
    case dwarf::cfi::SectionKind::eh_frame:
        return ".eh_frame";
    }
    return "?";
}

void print_rule(std::ostream& out, std::uint64_t reg,
                const dwarf::cfi::RegisterRule& rule) {
    out << " r" << reg << '=';
    switch (rule.kind) {
    case dwarf::cfi::RegisterRuleKind::undefined:
        out << "undefined";
        break;
    case dwarf::cfi::RegisterRuleKind::same_value:
        out << "same";
        break;
    case dwarf::cfi::RegisterRuleKind::offset:
        out << "CFA" << std::showpos << rule.offset << std::noshowpos;
        break;
    case dwarf::cfi::RegisterRuleKind::val_offset:
        out << "value(CFA" << std::showpos << rule.offset << std::noshowpos
            << ')';
        break;
    case dwarf::cfi::RegisterRuleKind::reg:
        out << "r" << rule.reg;
        break;
    case dwarf::cfi::RegisterRuleKind::expression:
        out << "expr[" << rule.expression.size() << ']';
        break;
    case dwarf::cfi::RegisterRuleKind::val_expression:
        out << "val_expr[" << rule.expression.size() << ']';
        break;
    }
}

void print_row(std::ostream& out, const dwarf::cfi::FrameRow& row) {
    out << "  row 0x" << std::hex << row.location << std::dec << " CFA=";
    switch (row.cfa.kind) {
    case dwarf::cfi::CfaRuleKind::undefined:
        out << "undefined";
        break;
    case dwarf::cfi::CfaRuleKind::reg_offset:
        out << "r" << row.cfa.reg << std::showpos << row.cfa.offset
            << std::noshowpos;
        break;
    case dwarf::cfi::CfaRuleKind::expression:
        out << "expr[" << row.cfa.expression.size() << ']';
        break;
    }
    out << " RA=r" << row.return_address_register;
    for (const auto& [reg, rule] : row.registers) {
        print_rule(out, reg, rule);
    }
    out << '\n';
}

void usage(const char* program) {
    std::cerr
        << "usage: " << program
        << " [--base ADDRESS] [--addr-size BYTES] [--kind auto|eh|debug]\n"
           "       [--big-endian] [--rows] [--offset BYTES] FILE\n";
}

}  // namespace

int main(int argc, char** argv) {
    dwarf::cfi::ParseOptions options;
    bool print_rows = false;
    std::string path;

    try {
        for (int i = 1; i < argc; ++i) {
            const std::string argument = argv[i];
            if (argument == "--base" && i + 1 < argc) {
                options.section_address = parse_number(argv[++i]);
            } else if (argument == "--offset" && i + 1 < argc) {
                options.section_offset = parse_number(argv[++i]);
            } else if (argument == "--addr-size" && i + 1 < argc) {
                const auto size = parse_number(argv[++i]);
                if (size == 0 || size > 8) {
                    throw std::invalid_argument("address size must be 1..8");
                }
                options.address_size = static_cast<std::uint8_t>(size);
            } else if (argument == "--kind" && i + 1 < argc) {
                const std::string_view kind = argv[++i];
                if (kind == "auto") {
                    options.kind = dwarf::cfi::SectionKind::auto_detect;
                } else if (kind == "eh") {
                    options.kind = dwarf::cfi::SectionKind::eh_frame;
                } else if (kind == "debug") {
                    options.kind = dwarf::cfi::SectionKind::debug_frame;
                } else {
                    throw std::invalid_argument("unknown section kind");
                }
            } else if (argument == "--big-endian") {
                options.little_endian = false;
            } else if (argument == "--rows") {
                print_rows = true;
            } else if (!argument.empty() && argument[0] == '-') {
                throw std::invalid_argument("unknown option: " + argument);
            } else if (path.empty()) {
                path = argument;
            } else {
                throw std::invalid_argument("multiple input files");
            }
        }
        if (path.empty()) {
            usage(argv[0]);
            return 2;
        }

        std::ifstream input(path, std::ios::binary);
        if (!input) {
            std::cerr << "cannot open " << path << '\n';
            return 1;
        }
        const std::vector<std::uint8_t> bytes{
            std::istreambuf_iterator<char>(input),
            std::istreambuf_iterator<char>()};

        const dwarf::cfi::CfiSection section =
            dwarf::cfi::parse_cfi_section(bytes, options);
        std::cout << "section: " << bytes.size() << " bytes, "
                  << section.cies.size() << " CIE(s), "
                  << section.fdes.size() << " FDE(s)\n";

        for (const dwarf::cfi::Cie& cie : section.cies) {
            std::cout << "CIE 0x" << std::hex << cie.offset << std::dec
                      << " v" << static_cast<unsigned>(cie.version)
                      << " kind=" << kind_name(cie.kind)
                      << " code_align=" << cie.code_alignment_factor
                      << " data_align=" << cie.data_alignment_factor
                      << " ra=r" << cie.return_address_register
                      << " aug=\"" << cie.augmentation << "\"\n";
        }
        for (const dwarf::cfi::Fde& fde : section.fdes) {
            const std::uint64_t end =
                fde.initial_location + fde.address_range;
            std::cout << "FDE 0x" << std::hex << fde.offset << std::dec
                      << " [0x" << std::hex << fde.initial_location << ",0x"
                      << end << std::dec << ")"
                      << " cie=0x" << std::hex << fde.cie_offset << std::dec
                      << " instructions=" << fde.instructions.size() << '\n';
            if (print_rows) {
                const dwarf::cfi::Cie* cie = section.cie_for(fde);
                if (cie == nullptr) {
                    std::cout << "  <missing CIE>\n";
                    continue;
                }
                std::vector<dwarf::cfi::FrameRow> rows;
                try {
                    rows = dwarf::cfi::FrameTable(*cie, fde).rows();
                } catch (const dwarf::cfi::CfiError& error) {
                    std::cout << "  error: " << error.what() << '\n';
                    continue;
                }
                for (const dwarf::cfi::FrameRow& row : rows) {
                    print_row(std::cout, row);
                }
            }
        }
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "error: " << error.what() << '\n';
        return 1;
    }
}
