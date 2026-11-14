#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdio>
#include <fstream>
#include <span>
#include <string>
#include <vector>

namespace {

constexpr std::uint64_t kFileSize = 0x4000;
constexpr std::uint64_t kPhdrOffset = 0x40;
constexpr std::uint64_t kPhdrSize = 56;
constexpr std::uint64_t kDynamicOffset = 0x1400;
constexpr std::uint64_t kRelroPhdr = 3;

void put_u16(std::vector<std::uint8_t>& image, std::uint64_t offset,
             std::uint16_t value) {
    image[offset] = static_cast<std::uint8_t>(value & 0xff);
    image[offset + 1] = static_cast<std::uint8_t>((value >> 8) & 0xff);
}

void put_u32(std::vector<std::uint8_t>& image, std::uint64_t offset,
             std::uint32_t value) {
    for (unsigned index = 0; index < 4; ++index) {
        image[offset + index] =
            static_cast<std::uint8_t>((value >> (index * 8)) & 0xff);
    }
}

void put_u64(std::vector<std::uint8_t>& image, std::uint64_t offset,
             std::uint64_t value) {
    for (unsigned index = 0; index < 8; ++index) {
        image[offset + index] =
            static_cast<std::uint8_t>((value >> (index * 8)) & 0xff);
    }
}

void put_program_header(std::vector<std::uint8_t>& image, std::uint64_t index,
                        std::uint32_t type, std::uint32_t flags,
                        std::uint64_t offset, std::uint64_t vaddr,
                        std::uint64_t filesz, std::uint64_t memsz,
                        std::uint64_t align) {
    const std::uint64_t base = kPhdrOffset + index * kPhdrSize;
    put_u32(image, base, type);
    put_u32(image, base + 4, flags);
    put_u64(image, base + 8, offset);
    put_u64(image, base + 16, vaddr);
    put_u64(image, base + 24, vaddr);
    put_u64(image, base + 32, filesz);
    put_u64(image, base + 40, memsz);
    put_u64(image, base + 48, align);
}

std::vector<std::uint8_t> make_fixture() {
    std::vector<std::uint8_t> image(kFileSize, 0);

    image[0] = 0x7f;
    image[1] = 'E';
    image[2] = 'L';
    image[3] = 'F';
    image[4] = 2; // ELFCLASS64
    image[5] = 1; // ELFDATA2LSB
    image[6] = 1; // EV_CURRENT
    image[7] = 0; // System V

    put_u16(image, 16, 3);  // ET_DYN
    put_u16(image, 18, 62); // EM_X86_64
    put_u32(image, 20, 1);
    put_u64(image, 24, 0x600); // e_entry points at fixture text
    put_u64(image, 32, kPhdrOffset);
    put_u64(image, 40, 0);
    put_u32(image, 48, 0);
    put_u16(image, 52, 64);
    put_u16(image, 54, 56);
    put_u16(image, 56, 4);
    put_u16(image, 58, 64);
    put_u16(image, 60, 0);
    put_u16(image, 62, 0);

    put_program_header(image, 0, 1, 5, 0x0000, 0x0000, 0x1000,
                       0x1000, 0x1000);
    put_program_header(image, 1, 1, 6, 0x1000, 0x2000, 0x3000,
                       0x3000, 0x1000);
    put_program_header(image, 2, 2, 6, 0x1400, 0x2400, 0x0200,
                       0x0200, 8);
    put_program_header(image, kRelroPhdr, 0x6474e552, 4, 0x1800,
                       0x2800, 0x0900, 0x0900, 1);

    // A function entry used by the fixture's JUMP_SLOT and RELATIVE entries.
    image[0x600] = 0xc3;

    const std::string strings = std::string("\0func\0libc.so.6\0plugin.so\0",
                                            27);
    std::copy(strings.begin(), strings.end(),
              image.begin() + static_cast<std::ptrdiff_t>(0x1600));

    // Symbol 1 is a defined STT_FUNC at virtual address 0x600.
    put_u32(image, 0x1700 + 24, 1);
    image[0x1700 + 28] = 0x12;
    image[0x1700 + 29] = 0;
    put_u16(image, 0x1700 + 30, 1);
    put_u64(image, 0x1700 + 32, 0x600);
    put_u64(image, 0x1700 + 40, 0x10);

    // Minimal SysV hash with two symbols in one bucket.
    put_u32(image, 0x1800, 1);
    put_u32(image, 0x1804, 2);
    put_u32(image, 0x1808, 1);
    put_u32(image, 0x180c, 0);
    put_u32(image, 0x1810, 0);

    // .rela.dyn: R_X86_64_RELATIVE writes a code pointer into GOT+8.
    put_u64(image, 0x1900, 0x2c08);
    put_u64(image, 0x1908, 8);
    put_u64(image, 0x1910, 0x600);

    // .rela.plt: one R_X86_64_JUMP_SLOT against symbol 1.
    put_u64(image, 0x1980, 0x2c00);
    put_u64(image, 0x1988, (1ULL << 32) | 7);
    put_u64(image, 0x1990, 0);

    // .init_array within the declared RELRO range.
    put_u64(image, 0x1a00, 0x600);

    const std::array<std::pair<std::uint64_t, std::uint64_t>, 16> dynamic{{
        {1, 6},           // DT_NEEDED
        {5, 0x2600},      // DT_STRTAB
        {10, 0x20},       // DT_STRSZ
        {6, 0x2700},      // DT_SYMTAB
        {11, 24},         // DT_SYMENT
        {4, 0x2800},      // DT_HASH
        {7, 0x2900},      // DT_RELA
        {8, 24},          // DT_RELASZ
        {9, 24},          // DT_RELAENT
        {23, 0x2980},     // DT_JMPREL
        {2, 24},          // DT_PLTRELSZ
        {20, 7},          // DT_PLTREL = DT_RELA
        {25, 0x2a00},     // DT_INIT_ARRAY
        {27, 8},          // DT_INIT_ARRAYSZ
        {0x6ffffffb, 1},  // DT_FLAGS_1 = DF_1_NOW
        {0, 0},           // DT_NULL
    }};
    for (std::size_t index = 0; index < dynamic.size(); ++index) {
        put_u64(image, kDynamicOffset + index * 16, dynamic[index].first);
        put_u64(image, kDynamicOffset + index * 16 + 8, dynamic[index].second);
    }
    return image;
}

std::uint64_t find_dynamic(const std::vector<std::uint8_t>& image,
                           std::uint64_t tag) {
    for (std::uint64_t index = 0; index < 32; ++index) {
        const std::uint64_t offset = kDynamicOffset + index * 16;
        std::uint64_t found_tag = 0;
        for (unsigned byte = 0; byte < 8; ++byte) {
            found_tag |= static_cast<std::uint64_t>(image[offset + byte])
                         << (byte * 8);
        }
        if (found_tag == tag) {
            return offset;
        }
        if (found_tag == 0) {
            break;
        }
    }
    return 0;
}

void set_dynamic(std::vector<std::uint8_t>& image, std::uint64_t tag,
                 std::uint64_t value) {
    const std::uint64_t offset = find_dynamic(image, tag);
    if (offset != 0) {
        put_u64(image, offset + 8, value);
    }
}

void retag_dynamic(std::vector<std::uint8_t>& image, std::uint64_t old_tag,
                   std::uint64_t new_tag, std::uint64_t value) {
    const std::uint64_t offset = find_dynamic(image, old_tag);
    if (offset != 0) {
        put_u64(image, offset, new_tag);
        put_u64(image, offset + 8, value);
    }
}

netsec::PolicyResult inspect(const std::vector<std::uint8_t>& image) {
    return netsec::analyze(
        std::span<const std::uint8_t>(image.data(), image.size()));
}

bool has_code(const netsec::PolicyResult& report, netsec::FindingCode code) {
    return std::any_of(
        report.findings.begin(), report.findings.end(),
        [code](const netsec::Finding& finding) {
            return finding.code == code;
        });
}

struct TestContext {
    int failures = 0;
};

void expect(TestContext& context, bool condition, const std::string& text) {
    if (!condition) {
        ++context.failures;
        std::fprintf(stderr, "FAIL: %s\n", text.c_str());
    }
}

void expect_allow(TestContext& context, const netsec::PolicyResult& report,
                  const std::string& name) {
    if (!report.allowed()) {
        std::fprintf(stderr, "FAIL: %s should be allowed\n%s\n", name.c_str(),
                     report.format().c_str());
        ++context.failures;
    }
}

void expect_reject(TestContext& context, const netsec::PolicyResult& report,
                   netsec::FindingCode code, const std::string& name) {
    if (report.allowed() || !has_code(report, code)) {
        std::fprintf(stderr, "FAIL: %s should reject with %s\n%s\n",
                     name.c_str(),
                     std::string(netsec::to_string(code)).c_str(),
                     report.format().c_str());
        ++context.failures;
    }
}

void run_tests(TestContext& context) {
    {
        auto image = make_fixture();
        const auto report = inspect(image);
        expect_allow(context, report, "full RELRO");
        expect(context, report.summary.bind_now, "full RELRO detects DF_1_NOW");
        expect(context, report.summary.relro_page_start == 0x2000,
               "RELRO page start is rounded down");
        expect(context, report.summary.relro_page_end == 0x4000,
               "RELRO page end is rounded up");
        expect(context, report.summary.jump_slot_count == 1,
               "JUMP_SLOT is counted");
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 0x6ffffffb, 0);
        const auto report = inspect(image);
        expect_reject(context, report, netsec::FindingCode::lazy_binding,
                      "-z relro with lazy binding");
    }
    {
        auto image = make_fixture();
        put_u64(image, 0x1980, 0x3120);
        put_u64(image, 0x1900, 0x3128);
        const auto report = inspect(image);
        expect_allow(context, report,
                     "target beyond declared RELRO but inside final page");
        expect(context, report.summary.relro_end == 0x3100,
               "declared RELRO end remains distinct from final page");
    }
    {
        auto image = make_fixture();
        retag_dynamic(image, 0x6ffffffb, 24, 0);
        const auto report = inspect(image);
        expect_allow(context, report, "DT_BIND_NOW full RELRO");
    }
    {
        auto image = make_fixture();
        retag_dynamic(image, 0x6ffffffb, 30, 8);
        const auto report = inspect(image);
        expect_allow(context, report, "DF_BIND_NOW full RELRO");
        expect(context, report.summary.bind_now,
               "full RELRO detects DF_BIND_NOW");
    }
    {
        auto image = make_fixture();
        retag_dynamic(image, 4, 0x6ffffef5, 0x2800);
        put_u32(image, 0x1800, 1); // nbuckets
        put_u32(image, 0x1804, 1); // symoffset
        put_u32(image, 0x1808, 1); // bloom_size
        put_u32(image, 0x180c, 0); // bloom_shift
        put_u64(image, 0x1810, 0); // bloom word
        put_u32(image, 0x1818, 1); // bucket
        put_u32(image, 0x181c, 1); // chain with terminal low bit
        const auto report = inspect(image);
        expect_allow(context, report, "DT_GNU_HASH plugin");
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 23, 0);
        set_dynamic(image, 2, 0);
        const auto report = inspect(image);
        expect_allow(context, report, "no PLT plugin");
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 8, 0x1800);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::malformed_relocation_table,
                      "oversized RELA table overlaps JMPREL");
    }
    {
        auto image = make_fixture();
        put_program_header(image, 1, 1, 6, 0x1000, 0x2000, 0x2000,
                           0x3000, 0x1000);
        set_dynamic(image, 5, 0x3ff0);
        set_dynamic(image, 10, 0x20);
        const auto report = inspect(image);
        expect_reject(context, report, netsec::FindingCode::truncated_table,
                      "truncated string table");
    }
    {
        auto image = make_fixture();
        put_program_header(image, 1, 1, 6, 0x1000, 0x2000, 0x1000,
                           0x3000, 0x1000);
        put_u32(image, 0x1804, 0x7fd);
        const auto report = inspect(image);
        expect_reject(context, report, netsec::FindingCode::truncated_table,
                      "truncated symbol table");
    }
    {
        auto image = make_fixture();
        put_program_header(image, 1, 1, 6, 0x0800, 0x0800, 0x3000,
                           0x3000, 0x1000);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::overlapping_load_segments,
                      "overlapping PT_LOAD");
    }
    {
        auto image = make_fixture();
        put_program_header(image, kRelroPhdr, 0x6474e552, 4, 0x1800,
                           0x2800, 0x0900, 0x4000, 1);
        const auto report = inspect(image);
        expect_reject(context, report, netsec::FindingCode::invalid_relro,
                      "RELRO outside containing PT_LOAD");
    }
    {
        auto image = make_fixture();
        put_program_header(image, 0, 1, 7, 0, 0, 0x1000, 0x1000,
                           0x1000);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::rwx_load_segment, "RWX PT_LOAD");
    }
    {
        auto image = make_fixture();
        put_u64(image, 0x1980, 0x600);
        const auto report = inspect(image);
        expect_reject(context, report, netsec::FindingCode::text_relocation,
                      "text relocation");
    }
    {
        auto image = make_fixture();
        retag_dynamic(image, 0x6ffffffb, 22, 0);
        const auto report = inspect(image);
        expect_reject(context, report, netsec::FindingCode::text_relocation,
                      "DT_TEXTREL marker");
    }
    {
        auto image = make_fixture();
        put_u64(image, 0x1980, 0x4000);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::writable_got_relocation,
                      "JUMP_SLOT outside RELRO");
        for (const auto& finding : report.findings) {
            if (finding.code ==
                netsec::FindingCode::writable_got_relocation) {
                expect(context, finding.source.file_offset == 0x1980,
                       "JUMP_SLOT finding witnesses the RELA entry");
                expect(context, finding.target.has_value() &&
                                   finding.target->file_offset == 0x3000,
                       "JUMP_SLOT finding witnesses the target bytes");
            }
        }
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 23, 0);
        set_dynamic(image, 2, 0);
        put_u64(image, 0x1908, (1ULL << 32) | 6);
        put_u64(image, 0x1900, 0x4000);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::writable_got_relocation,
                      "GLOB_DAT outside RELRO");
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 23, 0);
        set_dynamic(image, 2, 0);
        put_u64(image, 0x1908, (1ULL << 32) | 6);
        put_u64(image, 0x1900, 0x2c00);
        const auto report = inspect(image);
        expect_allow(context, report, "GLOB_DAT inside RELRO");
        expect(context, report.summary.glob_dat_count == 1,
               "GLOB_DAT is counted");
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 23, 0);
        set_dynamic(image, 2, 0);
        put_u64(image, 0x1908, (1ULL << 32) | 5);
        put_u64(image, 0x1900, 0x4000);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::writable_got_relocation,
                      "COPY destination outside RELRO");
        expect(context, report.summary.copy_count == 1,
               "COPY is counted");
    }
    {
        auto image = make_fixture();
        put_u64(image, 0x1900, 0x4008);
        const auto report = inspect(image);
        expect_reject(
            context, report,
            netsec::FindingCode::writable_function_pointer_relocation,
            "RELATIVE function pointer outside RELRO");
    }
    {
        auto image = make_fixture();
        put_u64(image, 0x1900, 0x4008);
        put_u64(image, 0x1910, 0);
        const auto report = inspect(image);
        expect_allow(context, report,
                     "RELATIVE scalar in writable data outside RELRO");
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 25, 0x4000);
        set_dynamic(image, 27, 8);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::writable_constructor_array,
                      "constructor array outside RELRO");
    }
    {
        auto image = make_fixture();
        put_u64(image, 0x1988, (1ULL << 32) | 999);
        const auto report = inspect(image);
        expect_reject(context, report,
                      netsec::FindingCode::unknown_relocation_type,
                      "unknown relocation type");
    }
    {
        auto image = make_fixture();
        put_u64(image, 0x1900, 0x2400);
        const auto report = inspect(image);
        expect_reject(
            context, report,
            netsec::FindingCode::relocation_overlaps_dynamic_metadata,
            "relocation into dynamic metadata");
    }
    {
        auto image = make_fixture();
        set_dynamic(image, 8, 0xfffffffffffffff0ULL);
        const auto report = inspect(image);
        expect_reject(context, report, netsec::FindingCode::integer_overflow,
                      "overflowing RELA size");
    }
}

void run_parser_robustness_tests(TestContext& context) {
    const auto fixture = make_fixture();
    for (std::size_t length = 0; length < 0x120; ++length) {
        auto image = fixture;
        image.resize(length);
        const auto report = inspect(image);
        expect(context, report.rejected,
               "truncated image is rejected at length " +
                   std::to_string(length));
    }

    std::uint32_t state = 0x9e3779b9U;
    const auto next = [&state]() {
        state ^= state << 13;
        state ^= state >> 17;
        state ^= state << 5;
        return state;
    };
    for (unsigned iteration = 0; iteration < 512; ++iteration) {
        auto image = fixture;
        const unsigned changes = 1 + (next() % 4);
        for (unsigned change = 0; change < changes; ++change) {
            const std::uint64_t offset = next() % image.size();
            image[static_cast<std::size_t>(offset)] ^=
                static_cast<std::uint8_t>(1U << (next() % 8));
        }
        const auto report = inspect(image);
        expect(context, report.allowed != report.rejected,
               "decision flags remain complementary after mutation " +
                   std::to_string(iteration));
    }
}

} // namespace

int main(int argc, char** argv) {
    if (argc == 3 && std::string(argv[1]) == "--write-fixture") {
        const auto image = make_fixture();
        std::ofstream output(argv[2], std::ios::binary);
        if (!output ||
            !output.write(reinterpret_cast<const char*>(image.data()),
                          static_cast<std::streamsize>(image.size()))) {
            std::fprintf(stderr, "cannot write fixture\n");
            return 1;
        }
        return 0;
    }
    TestContext context;
    run_tests(context);
    run_parser_robustness_tests(context);
    if (context.failures == 0) {
        std::printf("all netsec tests passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d netsec test(s) failed\n", context.failures);
    return 1;
}
