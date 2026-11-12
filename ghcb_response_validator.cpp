#include "ghcb_response_validator.hpp"

#include <cstring>

namespace netsec {

namespace {

constexpr unsigned kSwExitCodeBit = kGhcbSwExitCodeOffset / sizeof(std::uint64_t);
constexpr unsigned kSwScratchBit = kGhcbSwScratchOffset / sizeof(std::uint64_t);

struct FieldMask {
    bool rax = false;
    bool rbx = false;
    bool rcx = false;
    bool rdx = false;
};

bool bit_for_field(const FieldMask& mask, GhcbField field) {
    switch (field) {
        case GhcbField::Rax:
            return mask.rax;
        case GhcbField::Rbx:
            return mask.rbx;
        case GhcbField::Rcx:
            return mask.rcx;
        case GhcbField::Rdx:
            return mask.rdx;
    }
    return false;
}

// 校验四个 GPR 数据字段：置位字段必须在白名单内（防多余字段），
// 必需字段必须置位（防缺位）。
ValidationError check_data_fields(const Ghcb& ghcb, const FieldMask& allowed,
                                  const FieldMask& required) {
    const GhcbField fields[4] = {GhcbField::Rax, GhcbField::Rbx,
                                 GhcbField::Rcx, GhcbField::Rdx};
    for (GhcbField field : fields) {
        const bool is_set = ghcb_bit_is_set(ghcb, ghcb_field_bit(field));
        if (is_set && !bit_for_field(allowed, field)) {
            return ValidationError::UnexpectedField;
        }
        if (!is_set && bit_for_field(required, field)) {
            return ValidationError::MissingField;
        }
    }
    return ValidationError::None;
}

// 协议公共头校验：退出码有效位、退出码取值、宿主失败码。
ValidationError check_header(const Ghcb& ghcb, GhcbExitCode expected) {
    if (!ghcb_bit_is_set(ghcb, kSwExitCodeBit)) {
        return ValidationError::ExitCodeBitClear;
    }
    if (ghcb.save_area.sw_exit_code !=
        static_cast<std::uint64_t>(expected)) {
        return ValidationError::ExitCodeMismatch;
    }
    // sw_exit_info_1 非 0 表示宿主模拟失败，必须按错误处理，
    // 绝不能继续信任同页内的任何回填值。
    if (ghcb.save_area.sw_exit_info_1 != 0) {
        return ValidationError::HypervisorError;
    }
    return ValidationError::None;
}

// 校验 sw_scratch 指向的缓冲完全落在 GHCB 共享区内。
ValidationError check_scratch(const Ghcb& ghcb, std::uint32_t scratch_size) {
    if (scratch_size == 0) {
        return ValidationError::None;
    }
    if (!ghcb_bit_is_set(ghcb, kSwScratchBit)) {
        return ValidationError::ScratchBitClear;
    }

    const std::uint64_t address = ghcb.save_area.sw_scratch;
    const std::uint64_t size = scratch_size;
    const std::uint64_t page_base =
        reinterpret_cast<std::uintptr_t>(&ghcb);
    const std::uint64_t shared_begin = page_base + kGhcbSharedBufferOffset;
    const std::uint64_t shared_end = page_base + kGhcbPageSize;

    // 显式防加法回绕，避免宿主用超大指针绕过区间判断。
    if (address < shared_begin || address + size < address ||
        address + size > shared_end) {
        return ValidationError::ScratchOutOfRange;
    }
    return ValidationError::None;
}

}  // namespace

void GhcbResponseValidator::reset_for_request(Ghcb& ghcb) {
    // 请求前清空 valid_bitmap：宿主回填的任何字段都必须重新置位，
    // 防止上一次交互的残留位让陈旧数据通过校验。
    std::memset(ghcb.save_area.valid_bitmap, 0, kGhcbValidBitmapSize);
    std::memset(&ghcb.save_area.rax, 0,
                (kGhcbRdxOffset + sizeof(std::uint64_t)) - kGhcbRaxOffset);
    ghcb.save_area.sw_exit_code = 0;
    ghcb.save_area.sw_exit_info_1 = 0;
    ghcb.save_area.sw_exit_info_2 = 0;
    ghcb.save_area.sw_scratch = 0;
}

ValidationResult GhcbResponseValidator::validate_cpuid(
    const Ghcb& ghcb, const CpuidRequest& request,
    const CpuidTrustTable& trust_table) {
    ValidationResult result;

    ValidationError error = check_header(ghcb, GhcbExitCode::Cpuid);
    if (error != ValidationError::None) {
        result.error = error;
        return result;
    }

    // CPUID 请求与响应共用四个 GPR：请求写 rax(leaf)/rcx(subleaf)，
    // 响应须回填 rax/rbx/rcx/rdx，四个字段全部允许且全部必需。
    const FieldMask all_fields{true, true, true, true};
    error = check_data_fields(ghcb, all_fields, all_fields);
    if (error != ValidationError::None) {
        result.error = error;
        return result;
    }

    const CpuidTrustEntry* trusted =
        trust_table.find(request.leaf, request.subleaf);
    if (trusted == nullptr) {
        result.error = ValidationError::UntrustedCpuidResult;
        return result;
    }

    // CPUID 只定义低 32 位，高 32 位必须为 0；低位的每个特性位都必须
    // 被可信表条目允许，即宿主值是可信值的子集。
    const std::uint64_t host_rax = ghcb.save_area.rax;
    const std::uint64_t host_rbx = ghcb.save_area.rbx;
    const std::uint64_t host_rcx = ghcb.save_area.rcx;
    const std::uint64_t host_rdx = ghcb.save_area.rdx;

    if ((host_rax & ~std::uint64_t{trusted->rax}) != 0 ||
        (host_rbx & ~std::uint64_t{trusted->rbx}) != 0 ||
        (host_rcx & ~std::uint64_t{trusted->rcx}) != 0 ||
        (host_rdx & ~std::uint64_t{trusted->rdx}) != 0) {
        result.error = ValidationError::UntrustedCpuidResult;
        return result;
    }

    // 输出经可信掩码收敛后的值，顺带保证高 32 位为 0。
    result.rax = host_rax & trusted->rax;
    result.rbx = host_rbx & trusted->rbx;
    result.rcx = host_rcx & trusted->rcx;
    result.rdx = host_rdx & trusted->rdx;
    return result;
}

ValidationResult GhcbResponseValidator::validate_port_io(
    const Ghcb& ghcb, const PortIoRequest& request) {
    ValidationResult result;

    if (request.width_bytes != 1 && request.width_bytes != 2 &&
        request.width_bytes != 4) {
        result.error = ValidationError::BadPortWidth;
        return result;
    }

    ValidationError error = check_header(ghcb, GhcbExitCode::IoIo);
    if (error != ValidationError::None) {
        result.error = error;
        return result;
    }

    FieldMask allowed{};
    FieldMask required{};
    if (request.is_read) {
        // 端口读仅允许 rax 回传读入值，且该位必须置位。
        allowed.rax = true;
        required.rax = true;
    }
    // 端口写无返回字段：任何 GPR 数据位置位都属于多余字段。
    error = check_data_fields(ghcb, allowed, required);
    if (error != ValidationError::None) {
        result.error = error;
        return result;
    }

    error = check_scratch(ghcb, request.scratch_size);
    if (error != ValidationError::None) {
        result.error = error;
        return result;
    }

    if (request.is_read) {
        // 读响应不得超出请求宽度：按宽度生成掩码，超出位清零。
        const std::uint64_t width_mask =
            (request.width_bytes == sizeof(std::uint64_t))
                ? ~std::uint64_t{0}
                : ((std::uint64_t{1} << (request.width_bytes * 8)) - 1);
        result.rax = ghcb.save_area.rax & width_mask;
    }
    return result;
}

ValidationResult GhcbResponseValidator::validate_msr(
    const Ghcb& ghcb, const MsrRequest& request) {
    ValidationResult result;

    ValidationError error =
        check_header(ghcb, request.is_read ? GhcbExitCode::MsrRead
                                           : GhcbExitCode::MsrWrite);
    if (error != ValidationError::None) {
        result.error = error;
        return result;
    }

    // rbx 承载 MSR 索引，是请求侧字段，读/写都允许保持置位。
    FieldMask allowed{};
    allowed.rbx = true;
    FieldMask required{};
    if (request.is_read) {
        // 读：宿主经 rcx 回传 64 位 MSR 值，rcx 必需。
        allowed.rcx = true;
        required.rcx = true;
    } else {
        // 写：rcx 承载待写值，是请求侧字段，无任何返回字段。
        allowed.rcx = true;
    }

    error = check_data_fields(ghcb, allowed, required);
    if (error != ValidationError::None) {
        result.error = error;
        return result;
    }

    if (request.is_read) {
        result.rcx = ghcb.save_area.rcx;
    }
    return result;
}

}  // namespace netsec
