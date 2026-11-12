#include "ghcb_response_validator.hpp"

#include <cstdio>
#include <cstdlib>

namespace {

using netsec::Ghcb;
using netsec::GhcbExitCode;
using netsec::GhcbField;
using netsec::GhcbResponseValidator;
using netsec::PortIoRequest;
using netsec::CpuidRequest;
using netsec::CpuidTrustEntry;
using netsec::CpuidTrustTable;
using netsec::MsrRequest;
using netsec::ValidationError;

using netsec::kGhcbPageSize;
using netsec::kGhcbSharedBufferOffset;
using netsec::kGhcbSharedBufferSize;
using netsec::kGhcbSwExitCodeOffset;
using netsec::kGhcbSwScratchOffset;
using netsec::kGhcbValidBitmapSize;

int g_failures = 0;

#define CHECK(cond)                                                      \
    do {                                                                 \
        if (!(cond)) {                                                   \
            std::printf("FAIL %s:%d  %s\n", __FILE__, __LINE__, #cond); \
            ++g_failures;                                                \
        }                                                                \
    } while (0)

#define CHECK_EQ(a, b)                                                         \
    do {                                                                       \
        auto lhs_ = (a);                                                       \
        auto rhs_ = (b);                                                       \
        if (!(lhs_ == rhs_)) {                                                 \
            std::printf("FAIL %s:%d  %s != %s\n", __FILE__, __LINE__, #a,     \
                        #b);                                                   \
            ++g_failures;                                                      \
        }                                                                      \
    } while (0)

void set_bit(Ghcb& ghcb, GhcbField field) {
    netsec::ghcb_set_bit(ghcb, netsec::ghcb_field_bit(field));
}

void set_sw_exit_code(Ghcb& ghcb, GhcbExitCode code) {
    netsec::ghcb_set_bit(ghcb,
                         kGhcbSwExitCodeOffset / sizeof(std::uint64_t));
    ghcb.save_area.sw_exit_code = static_cast<std::uint64_t>(code);
}

void set_scratch_in_buffer(Ghcb& ghcb, std::uint32_t size) {
    netsec::ghcb_set_bit(ghcb,
                         kGhcbSwScratchOffset / sizeof(std::uint64_t));
    const auto base = reinterpret_cast<std::uintptr_t>(&ghcb);
    ghcb.save_area.sw_scratch =
        base + kGhcbSharedBufferOffset + (kGhcbSharedBufferSize - size);
}

CpuidTrustTable make_trust_table() {
    CpuidTrustTable table;
    table.add(CpuidTrustEntry{1, 0, 0x00020F51u, 0x00100800u, 0x7FFAFBFFu,
                              0xBFEBFBFFu});
    table.add(CpuidTrustEntry{7, 0, 0x00000000u, 0x00000000u, 0x00000000u,
                              0x00000010u});
    return table;
}

void fill_cpuid_ok(Ghcb& ghcb, std::uint64_t rax, std::uint64_t rbx,
                   std::uint64_t rcx, std::uint64_t rdx) {
    set_sw_exit_code(ghcb, GhcbExitCode::Cpuid);
    ghcb.save_area.sw_exit_info_1 = 0;
    ghcb.save_area.rax = rax;
    ghcb.save_area.rbx = rbx;
    ghcb.save_area.rcx = rcx;
    ghcb.save_area.rdx = rdx;
    set_bit(ghcb, GhcbField::Rax);
    set_bit(ghcb, GhcbField::Rbx);
    set_bit(ghcb, GhcbField::Rcx);
    set_bit(ghcb, GhcbField::Rdx);
}

void test_reset_clears_bitmap() {
    alignas(16) Ghcb ghcb{};
    netsec::ghcb_set_bit(ghcb, netsec::ghcb_field_bit(GhcbField::Rax));
    netsec::ghcb_set_bit(ghcb, 1000);
    ghcb.save_area.sw_exit_info_1 = 0x9u;

    GhcbResponseValidator::reset_for_request(ghcb);

    for (unsigned i = 0; i < kGhcbValidBitmapSize * 8; ++i) {
        CHECK(!netsec::ghcb_bit_is_set(ghcb, i));
    }
    CHECK_EQ(ghcb.save_area.sw_exit_info_1, 0u);
    CHECK_EQ(ghcb.save_area.sw_exit_code, 0u);
    CHECK_EQ(ghcb.save_area.rax, 0u);
}

void test_cpuid_happy_path_subset() {
    alignas(16) Ghcb ghcb{};
    const auto table = make_trust_table();
    CpuidRequest request{1, 0};

    fill_cpuid_ok(ghcb, 0x00020F51u & 0x7FFFFFFFu, 0x00100800u,
                  0x7FFAFBFFu & 0x0FFFFFFFu, 0xBFEBFBFFu);

    auto result = GhcbResponseValidator::validate_cpuid(ghcb, request, table);
    CHECK(result.ok());
    CHECK_EQ(result.rax, 0x00020F51u & 0x7FFFFFFFu);
    CHECK_EQ(result.rbx, 0x00100800u);
    CHECK_EQ(result.rdx, 0xBFEBFBFFu);
}

void test_cpuid_rejects_extra_feature_bits() {
    alignas(16) Ghcb ghcb{};
    const auto table = make_trust_table();
    CpuidRequest request{1, 0};

    fill_cpuid_ok(ghcb, 0x00020F51u, 0x00100800u,
                  0x7FFAFBFFu | 0x80000000u, 0xBFEBFBFFu);
    auto result = GhcbResponseValidator::validate_cpuid(ghcb, request, table);
    CHECK_EQ(result.error, ValidationError::UntrustedCpuidResult);

    fill_cpuid_ok(ghcb, 0x00020F51u, 0x00100800u | 0x40000000u,
                  0x7FFAFBFFu, 0xBFEBFBFFu);
    result = GhcbResponseValidator::validate_cpuid(ghcb, request, table);
    CHECK_EQ(result.error, ValidationError::UntrustedCpuidResult);

    fill_cpuid_ok(ghcb, 0x00020F51u, 0x00100800u, 0x7FFAFBFFu,
                  0xBFEBFBFFull | (0xDEADull << 32));
    result = GhcbResponseValidator::validate_cpuid(ghcb, request, table);
    CHECK_EQ(result.error, ValidationError::UntrustedCpuidResult);
}

void test_cpuid_rejects_unknown_leaf() {
    alignas(16) Ghcb ghcb{};
    const auto table = make_trust_table();
    CpuidRequest request{0x80000001u, 0};

    fill_cpuid_ok(ghcb, 1, 2, 3, 4);
    auto result = GhcbResponseValidator::validate_cpuid(ghcb, request, table);
    CHECK_EQ(result.error, ValidationError::UntrustedCpuidResult);
}

void test_cpuid_rejects_missing_field() {
    alignas(16) Ghcb ghcb{};
    const auto table = make_trust_table();
    CpuidRequest request{1, 0};

    fill_cpuid_ok(ghcb, 0x00020F51u, 0x00100800u, 0x7FFAFBFFu, 0xBFEBFBFFu);
    netsec::ghcb_clear_bit(ghcb, netsec::ghcb_field_bit(GhcbField::Rbx));
    auto result = GhcbResponseValidator::validate_cpuid(ghcb, request, table);
    CHECK_EQ(result.error, ValidationError::MissingField);
}

}  // namespace

namespace {

void test_cpuid_rejects_unexpected_field_for_port_read() {
    alignas(16) Ghcb ghcb{};
    PortIoRequest request{0x60, 1, true, 1};

    // 端口读只允许 rax，宿主额外置位 rdx 即多余字段。
    set_sw_exit_code(ghcb, GhcbExitCode::IoIo);
    ghcb.save_area.rax = 0x41;
    ghcb.save_area.rdx = 0xDEADBEEF;
    set_bit(ghcb, GhcbField::Rax);
    set_bit(ghcb, GhcbField::Rdx);
    set_scratch_in_buffer(ghcb, 1);

    auto result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::UnexpectedField);
}

void test_port_write_rejects_any_returned_field() {
    alignas(16) Ghcb ghcb{};
    PortIoRequest request{0x60, 1, false, 1};

    set_sw_exit_code(ghcb, GhcbExitCode::IoIo);
    set_scratch_in_buffer(ghcb, 1);
    // 端口写无返回，rax 置位即违规。
    set_bit(ghcb, GhcbField::Rax);

    auto result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::UnexpectedField);

    netsec::ghcb_clear_bit(ghcb, netsec::ghcb_field_bit(GhcbField::Rax));
    result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK(result.ok());
}

void test_hypervisor_failure_code() {
    alignas(16) Ghcb ghcb{};
    const auto table = make_trust_table();
    CpuidRequest cpuid_request{1, 0};

    fill_cpuid_ok(ghcb, 0x00020F51u, 0x00100800u, 0x7FFAFBFFu, 0xBFEBFBFFu);
    ghcb.save_area.sw_exit_info_1 = 0x1;
    auto result =
        GhcbResponseValidator::validate_cpuid(ghcb, cpuid_request, table);
    CHECK_EQ(result.error, ValidationError::HypervisorError);

    alignas(16) Ghcb io_ghcb{};
    PortIoRequest port_request{0x60, 1, true, 1};
    set_sw_exit_code(io_ghcb, GhcbExitCode::IoIo);
    io_ghcb.save_area.sw_exit_info_1 = 0x5A;
    io_ghcb.save_area.rax = 0x41;
    set_bit(io_ghcb, GhcbField::Rax);
    set_scratch_in_buffer(io_ghcb, 1);
    result = GhcbResponseValidator::validate_port_io(io_ghcb, port_request);
    CHECK_EQ(result.error, ValidationError::HypervisorError);
}

void test_exit_code_mismatch_and_missing_bit() {
    alignas(16) Ghcb ghcb{};
    PortIoRequest request{0x60, 1, true, 1};

    // sw_exit_code 的有效位未置。
    ghcb.save_area.sw_exit_code =
        static_cast<std::uint64_t>(GhcbExitCode::IoIo);
    auto result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::ExitCodeBitClear);

    // 有效位置位但退出码被宿主篡改。
    set_sw_exit_code(ghcb, GhcbExitCode::Cpuid);
    result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::ExitCodeMismatch);
}

void test_scratch_out_of_range() {
    alignas(16) Ghcb ghcb{};
    PortIoRequest request{0x60, 1, true, 4};
    const auto base = reinterpret_cast<std::uintptr_t>(&ghcb);

    set_sw_exit_code(ghcb, GhcbExitCode::IoIo);
    set_bit(ghcb, GhcbField::Rax);
    ghcb.save_area.rax = 0x41;
    netsec::ghcb_set_bit(ghcb,
                         kGhcbSwScratchOffset / sizeof(std::uint64_t));

    // 指向 save area（共享区之前）。
    ghcb.save_area.sw_scratch = base + 0x100;
    auto result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::ScratchOutOfRange);

    // 起点在共享区内，但末尾越过 GHCB 页。
    ghcb.save_area.sw_scratch = base + kGhcbPageSize - 2;
    result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::ScratchOutOfRange);

    // 完全落在页外。
    ghcb.save_area.sw_scratch = base + kGhcbPageSize;
    result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::ScratchOutOfRange);

    // 接近地址空间顶部的回绕指针。
    ghcb.save_area.sw_scratch = ~std::uint64_t{0} - 1;
    result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::ScratchOutOfRange);

    // scratch 有效位未置。
    set_scratch_in_buffer(ghcb, 4);
    netsec::ghcb_clear_bit(ghcb,
                           kGhcbSwScratchOffset / sizeof(std::uint64_t));
    result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::ScratchBitClear);

    // 合法：完整落在共享区内。
    set_scratch_in_buffer(ghcb, 4);
    result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK(result.ok());
    CHECK_EQ(result.rax, 0x41u);
}

void test_port_read_width_masking() {
    alignas(16) Ghcb ghcb{};

    set_sw_exit_code(ghcb, GhcbExitCode::IoIo);
    set_bit(ghcb, GhcbField::Rax);
    set_scratch_in_buffer(ghcb, 4);
    ghcb.save_area.rax = 0x12345678u;

    PortIoRequest byte_read{0x60, 1, true, 1};
    auto result = GhcbResponseValidator::validate_port_io(ghcb, byte_read);
    CHECK(result.ok());
    CHECK_EQ(result.rax, 0x78u);

    PortIoRequest word_read{0x60, 2, true, 2};
    result = GhcbResponseValidator::validate_port_io(ghcb, word_read);
    CHECK(result.ok());
    CHECK_EQ(result.rax, 0x5678u);

    PortIoRequest dword_read{0x60, 4, true, 4};
    result = GhcbResponseValidator::validate_port_io(ghcb, dword_read);
    CHECK(result.ok());
    CHECK_EQ(result.rax, 0x12345678u);

    // 非法宽度直接拒绝。
    PortIoRequest bad_width{0x60, 3, true, 3};
    result = GhcbResponseValidator::validate_port_io(ghcb, bad_width);
    CHECK_EQ(result.error, ValidationError::BadPortWidth);
}

void test_port_read_missing_rax_bit() {
    alignas(16) Ghcb ghcb{};
    PortIoRequest request{0x60, 1, true, 1};

    set_sw_exit_code(ghcb, GhcbExitCode::IoIo);
    set_scratch_in_buffer(ghcb, 1);
    // rax 值已回填但有效位未置。
    ghcb.save_area.rax = 0x41;

    auto result = GhcbResponseValidator::validate_port_io(ghcb, request);
    CHECK_EQ(result.error, ValidationError::MissingField);
}

void test_msr_read_and_write() {
    alignas(16) Ghcb ghcb{};

    // 读 MSR：rbx=索引（请求字段），rcx=值（返回字段）。
    set_sw_exit_code(ghcb, GhcbExitCode::MsrRead);
    ghcb.save_area.rbx = 0xC0000080ull;
    ghcb.save_area.rcx = 0xDEADBEEFCAFEull;
    set_bit(ghcb, GhcbField::Rbx);
    set_bit(ghcb, GhcbField::Rcx);
    MsrRequest read_request{0xC0000080u, true};
    auto result = GhcbResponseValidator::validate_msr(ghcb, read_request);
    CHECK(result.ok());
    CHECK_EQ(result.rcx, 0xDEADBEEFCAFEull);

    // 读 MSR 缺 rcx 位。
    netsec::ghcb_clear_bit(ghcb, netsec::ghcb_field_bit(GhcbField::Rcx));
    result = GhcbResponseValidator::validate_msr(ghcb, read_request);
    CHECK_EQ(result.error, ValidationError::MissingField);
    set_bit(ghcb, GhcbField::Rcx);

    // 读 MSR 出现多余字段 rax。
    set_bit(ghcb, GhcbField::Rax);
    result = GhcbResponseValidator::validate_msr(ghcb, read_request);
    CHECK_EQ(result.error, ValidationError::UnexpectedField);
    netsec::ghcb_clear_bit(ghcb, netsec::ghcb_field_bit(GhcbField::Rax));

    // 写 MSR：rbx/rcx 均为请求字段，无返回字段。
    set_sw_exit_code(ghcb, GhcbExitCode::MsrWrite);
    MsrRequest write_request{0xC0000080u, false};
    result = GhcbResponseValidator::validate_msr(ghcb, write_request);
    CHECK(result.ok());

    // 写 MSR 若宿主回填 rax 则为多余字段。
    set_bit(ghcb, GhcbField::Rax);
    result = GhcbResponseValidator::validate_msr(ghcb, write_request);
    CHECK_EQ(result.error, ValidationError::UnexpectedField);
}

}  // namespace

int main() {
    test_reset_clears_bitmap();
    test_cpuid_happy_path_subset();
    test_cpuid_rejects_extra_feature_bits();
    test_cpuid_rejects_unknown_leaf();
    test_cpuid_rejects_missing_field();
    test_cpuid_rejects_unexpected_field_for_port_read();
    test_port_write_rejects_any_returned_field();
    test_hypervisor_failure_code();
    test_exit_code_mismatch_and_missing_bit();
    test_scratch_out_of_range();
    test_port_read_width_masking();
    test_port_read_missing_rax_bit();
    test_msr_read_and_write();

    if (g_failures != 0) {
        std::printf("%d check(s) failed\n", g_failures);
        return EXIT_FAILURE;
    }
    std::printf("all ghcb response validation tests passed\n");
    return EXIT_SUCCESS;
}
