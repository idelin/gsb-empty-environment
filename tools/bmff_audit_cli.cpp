#include "bmff/audit.hpp"

#include <cstdint>
#include <fstream>
#include <iostream>
#include <iterator>
#include <vector>

namespace {

void print_usage(char* program)
{
    std::cerr << "Usage: " << program << " <file.mp4>\n";
}

} // namespace

int main(int argc, char** argv)
{
    if (argc != 2) {
        print_usage(argc > 0 ? argv[0] : nullptr);
        return 2;
    }

    std::ifstream input(argv[1], std::ios::binary);
    if (!input) {
        std::cerr << "error: cannot open input file\n";
        return 2;
    }

    std::vector<std::uint8_t> bytes(
        (std::istreambuf_iterator<char>(input)),
        std::istreambuf_iterator<char>());
    if (!input.eof() && input.fail()) {
        std::cerr << "error: failed while reading input file\n";
        return 2;
    }

    const bmff::AuditResult result = bmff::audit(bytes);
    if (!result.ok()) {
        const bmff::AuditError* error = result.first_error();
        if (error != nullptr) {
            std::cout << "REJECT " << bmff::to_string(error->code)
                      << " at offset " << error->offset << ": "
                      << error->message << '\n';
        }
        return 1;
    }

    std::cout << "OK boxes=" << result.stats.boxes
              << " mdats=" << result.stats.mdats
              << " chunks=" << result.stats.chunks
              << " samples=" << result.stats.samples
              << " sample_bytes=" << result.stats.sample_bytes << '\n';
    return 0;
}
