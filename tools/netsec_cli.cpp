#include "netsec.hpp"

#include <fstream>
#include <iostream>
#include <optional>
#include <sstream>
#include <string>
#include <vector>

namespace {

std::optional<netsec::ByteRange> parse_range(std::string_view text) {
    const std::size_t colon = text.find(':');
    if (colon == std::string_view::npos) {
        return std::nullopt;
    }
    try {
        const std::uint64_t offset = std::stoull(std::string(text.substr(0, colon)));
        const std::uint64_t length = std::stoull(std::string(text.substr(colon + 1)));
        return netsec::ByteRange{offset, length};
    } catch (...) {
        return std::nullopt;
    }
}

}  // namespace

int main(int argc, char** argv) {
    if (argc < 2) {
        std::cerr << "usage: netsec_cli <document.pdf> [--range offset:length]... "
                     "[--original-only] [--auto] [--annotations subtype[,subtype]] "
                     "[--countersign] [--json]\n";
        return 2;
    }
    std::ifstream input(argv[1], std::ios::binary);
    if (!input) {
        std::cerr << "cannot open " << argv[1] << "\n";
        return 2;
    }
    std::ostringstream buffer;
    buffer << input.rdbuf();
    const std::string pdf = buffer.str();

    std::vector<netsec::ByteRange> ranges;
    bool original_only = false;
    bool automatic = false;
    bool json = false;
    netsec::Policy policy;
    for (int i = 2; i < argc; ++i) {
        const std::string argument = argv[i];
        if (argument == "--range" && i + 1 < argc) {
            auto range = parse_range(argv[++i]);
            if (!range.has_value()) {
                std::cerr << "invalid range\n";
                return 2;
            }
            ranges.push_back(*range);
        } else if (argument == "--original-only") {
            original_only = true;
        } else if (argument == "--auto") {
            automatic = true;
        } else if (argument == "--json") {
            json = true;
        } else if (argument == "--countersign") {
            policy = netsec::Policy::countersigning();
        } else if (argument == "--annotations" && i + 1 < argc) {
            policy.allow_annotation_increments = true;
            std::string list = argv[++i];
            std::size_t begin = 0;
            while (begin <= list.size()) {
                const std::size_t comma = list.find(',', begin);
                policy.allowed_annotation_subtypes.insert(
                    list.substr(begin, comma == std::string::npos
                                           ? std::string::npos
                                           : comma - begin));
                if (comma == std::string::npos) {
                    break;
                }
                begin = comma + 1;
            }
        } else {
            std::cerr << "unknown argument: " << argument << "\n";
            return 2;
        }
    }

    netsec::Assessment assessment;
    if (!ranges.empty()) {
        assessment = netsec::assess(pdf, ranges, policy);
    } else if (original_only) {
        assessment = netsec::assess_original_revision_only(pdf, policy);
    } else if (automatic) {
        assessment = netsec::assess(pdf, policy);
    } else {
        assessment = netsec::assess_original_revision_only(pdf, policy);
    }

    if (json) {
        std::cout << assessment.to_json() << "\n";
    } else {
        std::cout << "decision: " << netsec::to_string(assessment.decision) << "\n";
        std::cout << "valid: " << (assessment.valid() ? "true" : "false") << "\n";
        std::cout << "reason: " << assessment.reason << "\n";
        std::cout << "revisions: " << assessment.revisions.size() << "\n";
        std::cout << "differences: " << assessment.visible_difference_summary << "\n";
        for (const std::string& violation : assessment.violations) {
            std::cout << "violation: " << violation << "\n";
        }
    }
    return assessment.valid() ? 0 : 1;
}
