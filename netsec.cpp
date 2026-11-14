#include "netsec.hpp"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <map>
#include <mutex>
#include <random>
#include <sstream>
#include <unordered_map>
#include <vector>

namespace {

struct object_entry {
    std::uintptr_t base = 0;
    std::size_t size = 0;
    netsec::tag_t tag = netsec::no_tag;
    std::uint64_t generation = 0;
};

struct registry_state {
    std::mutex mutex;
    std::map<std::uintptr_t, object_entry> by_base;
    std::unordered_map<netsec::tag_t, object_entry> by_tag;
    std::uint64_t next_generation = 0;
    std::uint64_t tag_state = 0;
};

registry_state& registry() {
    static registry_state state;
    return state;
}

std::atomic<netsec_trap_handler> trap_handler{nullptr};
std::atomic<std::uint64_t> syscall_validations{0};

std::uint64_t mix64(std::uint64_t value) noexcept {
    value ^= value >> 30U;
    value *= 0xbf58476d1ce4e5b9ULL;
    value ^= value >> 27U;
    value *= 0x94d049bb133111ebULL;
    value ^= value >> 31U;
    return value;
}

netsec::tag_t make_tag_locked(registry_state& state) {
    for (;;) {
        state.tag_state += 0x9e3779b97f4a7c15ULL;
        auto candidate = mix64(state.tag_state);
        candidate |= 1ULL << 63U;
        if (candidate != netsec::no_tag &&
            state.by_tag.find(candidate) == state.by_tag.end()) {
            return candidate;
        }
    }
}

void seed_tag_state_locked(registry_state& state) {
    if (state.tag_state != 0) {
        return;
    }
    std::random_device random;
    const auto now = static_cast<std::uint64_t>(
        std::chrono::steady_clock::now().time_since_epoch().count());
    const auto address = reinterpret_cast<std::uintptr_t>(&state);
    state.tag_state =
        mix64(static_cast<std::uint64_t>(random()) ^ now ^ address ^
              reinterpret_cast<std::uintptr_t>(&state.next_generation));
}

[[noreturn]] void raise_trap_impl(int reason,
                                  std::uintptr_t address,
                                  std::size_t bytes,
                                  netsec::tag_t expected_tag,
                                  netsec::tag_t actual_tag,
                                  const char* operation) {
    const auto handler = trap_handler.load(std::memory_order_acquire);
    if (handler != nullptr) {
        handler(reason,
                address,
                bytes,
                expected_tag,
                actual_tag,
                operation != nullptr ? operation : "access");
    }

#if defined(NETSEC_TRAP_ABORT)
    std::abort();
#endif

    netsec::trap_info info{
        static_cast<netsec::trap_reason>(reason),
        address,
        bytes,
        expected_tag,
        actual_tag,
        operation != nullptr ? operation : "access"};

    std::ostringstream message;
    message << "netsec tag violation: " << netsec_reason_name(reason)
            << " at 0x" << std::hex << address << std::dec;
    if (bytes != 0) {
        message << " (" << bytes << " bytes)";
    }
    if (expected_tag != netsec::no_tag || actual_tag != netsec::no_tag) {
        message << " [expected tag 0x" << std::hex << expected_tag
                << ", actual tag 0x" << actual_tag << std::dec << ']';
    }
    message << " in " << info.operation;
    throw netsec::tag_violation(info, message.str());
}

const char* access_operation(int access) noexcept {
    switch (access) {
        case NETSEC_ACCESS_READ:
            return "tagged load";
        case NETSEC_ACCESS_WRITE:
            return "tagged store";
        case NETSEC_ACCESS_READ_WRITE:
            return "tagged access";
        case NETSEC_ACCESS_SYSCALL_READ:
            return "syscall copy-in";
        case NETSEC_ACCESS_SYSCALL_WRITE:
            return "syscall copy-out";
        default:
            return "tagged access";
    }
}

struct validation_result {
    bool ok = false;
    int reason = NETSEC_TRAP_NONE;
    std::uintptr_t address = 0;
    std::size_t bytes = 0;
    netsec::tag_t expected_tag = netsec::no_tag;
    netsec::tag_t actual_tag = netsec::no_tag;
};

validation_result validate_descriptor(const netsec_ptr& pointer,
                                      std::size_t bytes) noexcept {
    validation_result result;
    result.address = pointer.address;
    result.bytes = bytes;
    result.expected_tag = pointer.tag;
    result.actual_tag = pointer.tag;

    if (pointer.tagged == 0 || pointer.tag == netsec::no_tag) {
        result.reason = NETSEC_TRAP_UNTAGGED;
        result.actual_tag = netsec::no_tag;
        return result;
    }

    if (bytes != 0 &&
        pointer.address > std::numeric_limits<std::uintptr_t>::max() - bytes) {
        result.reason = NETSEC_TRAP_OUT_OF_BOUNDS;
        return result;
    }

    auto& state = registry();
    std::lock_guard lock(state.mutex);

    const auto tag_it = state.by_tag.find(pointer.tag);
    if (tag_it == state.by_tag.end()) {
        result.reason = NETSEC_TRAP_STALE_OBJECT;
        return result;
    }

    const object_entry& object = tag_it->second;
    result.actual_tag = object.tag;

    if (pointer.object_base != object.base ||
        pointer.object_size != object.size ||
        pointer.generation != object.generation) {
        result.reason = NETSEC_TRAP_PROVENANCE_MISMATCH;
        return result;
    }

    const bool zero_length = bytes == 0;
    const auto object_end = object.base + object.size;

    if (zero_length) {
        if (pointer.address < object.base || pointer.address > object_end) {
            result.reason = NETSEC_TRAP_OUT_OF_BOUNDS;
            return result;
        }
        result.ok = true;
        return result;
    }

    /*
     * Detect an access whose address resolves to another live object.  This
     * is the cross-object attack that load/store-only tag hardware misses
     * when the attacker keeps a stale tag but moves the address.
     */
    auto region_it = state.by_base.upper_bound(pointer.address);
    if (region_it == state.by_base.begin()) {
        result.reason = NETSEC_TRAP_UNREGISTERED;
        return result;
    }
    --region_it;

    const object_entry& region = region_it->second;
    if (pointer.address < region.base ||
        pointer.address - region.base >= region.size) {
        result.reason = NETSEC_TRAP_OUT_OF_BOUNDS;
        return result;
    }

    if (region.tag != pointer.tag) {
        result.reason = NETSEC_TRAP_TAG_MISMATCH;
        result.actual_tag = region.tag;
        return result;
    }

    if (bytes > region.size - (pointer.address - region.base)) {
        result.reason = NETSEC_TRAP_OUT_OF_BOUNDS;
        return result;
    }

    if (pointer.address < object.base || pointer.address >= object_end) {
        result.reason = NETSEC_TRAP_OUT_OF_BOUNDS;
        return result;
    }

    if (bytes > object.size - (pointer.address - object.base)) {
        result.reason = NETSEC_TRAP_OUT_OF_BOUNDS;
        return result;
    }

    result.ok = true;
    return result;
}

[[noreturn]] void raise_validation(const validation_result& result,
                                   const char* operation) {
    raise_trap_impl(result.reason,
                    result.address,
                    result.bytes,
                    result.expected_tag,
                    result.actual_tag,
                    operation);
}

netsec_ptr make_descriptor(const object_entry& object,
                           std::uintptr_t address) noexcept {
    netsec_ptr output{};
    output.address = address;
    output.object_base = object.base;
    output.object_size = object.size;
    output.tag = object.tag;
    output.generation = object.generation;
    output.tagged = object.tag != netsec::no_tag ? 1U : 0U;
    return output;
}

std::uintptr_t offset_address(std::uintptr_t address,
                              std::ptrdiff_t offset) {
    const bool negative = offset < 0;
    const auto cast_offset = static_cast<std::uintptr_t>(offset);
    const auto magnitude = negative ? (~cast_offset + 1U) : cast_offset;

    if (negative) {
        if (address < magnitude) {
            raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                            address,
                            0,
                            netsec::no_tag,
                            netsec::no_tag,
                            "tagged pointer subtraction");
        }
        return address - magnitude;
    }

    if (address > std::numeric_limits<std::uintptr_t>::max() - magnitude) {
        raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                        address,
                        0,
                        netsec::no_tag,
                        netsec::no_tag,
                        "tagged pointer addition");
    }
    return address + magnitude;
}

void invalidate_overlapping_locked(registry_state& state,
                                   std::uintptr_t base,
                                   std::uintptr_t end) {
    std::vector<netsec::tag_t> dead_tags;

    auto it = state.by_base.lower_bound(base);
    if (it != state.by_base.begin()) {
        const auto previous = std::prev(it);
        if (previous->first + previous->second.size > base) {
            dead_tags.push_back(previous->second.tag);
        }
    }

    it = state.by_base.lower_bound(base);
    while (it != state.by_base.end() &&
           (base == end ? it->first == base : it->first < end)) {
        dead_tags.push_back(it->second.tag);
        ++it;
    }

    for (const auto tag : dead_tags) {
        const auto tag_it = state.by_tag.find(tag);
        if (tag_it == state.by_tag.end()) {
            continue;
        }
        state.by_base.erase(tag_it->second.base);
        state.by_tag.erase(tag_it);
    }
}

}  // namespace

extern "C" {

const char* netsec_reason_name(int reason) {
    switch (reason) {
        case NETSEC_TRAP_NONE:
            return "none";
        case NETSEC_TRAP_UNTAGGED:
            return "untagged pointer";
        case NETSEC_TRAP_STALE_OBJECT:
            return "stale object tag";
        case NETSEC_TRAP_OUT_OF_BOUNDS:
            return "object bounds violation";
        case NETSEC_TRAP_TAG_MISMATCH:
            return "cross-object tag mismatch";
        case NETSEC_TRAP_UNREGISTERED:
            return "address is not backed by a tagged object";
        case NETSEC_TRAP_ARITHMETIC_OVERFLOW:
            return "pointer arithmetic overflow";
        case NETSEC_TRAP_PROVENANCE_MISMATCH:
            return "forged pointer provenance";
        case NETSEC_TRAP_INVALID_ARGUMENT:
            return "invalid argument";
        default:
            return "unknown tag violation";
    }
}

void netsec_set_trap_handler(netsec_trap_handler handler) {
    trap_handler.store(handler, std::memory_order_release);
}

netsec_trap_handler netsec_get_trap_handler(void) {
    return trap_handler.load(std::memory_order_acquire);
}

void netsec_raise_trap(int reason,
                       uintptr_t address,
                       size_t bytes,
                       uint64_t expected_tag,
                       uint64_t actual_tag,
                       const char* operation) {
    raise_trap_impl(reason,
                    address,
                    bytes,
                    expected_tag,
                    actual_tag,
                    operation);
}

netsec_object netsec_register_object(void* base, size_t size) {
    if (base == nullptr) {
        raise_trap_impl(NETSEC_TRAP_INVALID_ARGUMENT,
                        0,
                        size,
                        netsec::no_tag,
                        netsec::no_tag,
                        "object registration");
    }

    const auto object_base = reinterpret_cast<std::uintptr_t>(base);
    if (size != 0 &&
        object_base > std::numeric_limits<std::uintptr_t>::max() - size) {
        raise_trap_impl(NETSEC_TRAP_INVALID_ARGUMENT,
                        object_base,
                        size,
                        netsec::no_tag,
                        netsec::no_tag,
                        "object registration");
    }

    auto& state = registry();
    std::lock_guard lock(state.mutex);
    seed_tag_state_locked(state);

    /*
     * Overlapping registrations invalidate the older tag.  This keeps the
     * address-to-tag lookup unambiguous and makes stale descriptors fail
     * closed instead of accidentally inheriting a newer object's tag.
     */
    const auto object_end = object_base + size;
    invalidate_overlapping_locked(state, object_base, object_end);

    object_entry object;
    object.base = object_base;
    object.size = size;
    object.generation = ++state.next_generation;
    object.tag = make_tag_locked(state);

    state.by_base.emplace(object.base, object);
    state.by_tag.emplace(object.tag, object);

    netsec_object result{};
    result.tag = object.tag;
    result.base = base;
    result.size = size;
    result.generation = object.generation;
    return result;
}

int netsec_unregister_object(uint64_t tag) {
    if (tag == netsec::no_tag) {
        return 0;
    }
    auto& state = registry();
    std::lock_guard lock(state.mutex);
    const auto tag_it = state.by_tag.find(tag);
    if (tag_it == state.by_tag.end()) {
        return 0;
    }
    const auto base_it = state.by_base.find(tag_it->second.base);
    if (base_it != state.by_base.end() &&
        base_it->second.tag == tag) {
        state.by_base.erase(base_it);
    }
    state.by_tag.erase(tag_it);
    return 1;
}

int netsec_object_alive(uint64_t tag) {
    if (tag == netsec::no_tag) {
        return 0;
    }
    auto& state = registry();
    std::lock_guard lock(state.mutex);
    return state.by_tag.find(tag) != state.by_tag.end() ? 1 : 0;
}

netsec_object netsec_object_for_tag(uint64_t tag) {
    netsec_object output{};
    if (tag == netsec::no_tag) {
        return output;
    }
    auto& state = registry();
    std::lock_guard lock(state.mutex);
    const auto it = state.by_tag.find(tag);
    if (it == state.by_tag.end()) {
        return output;
    }
    output.tag = it->second.tag;
    output.base = reinterpret_cast<void*>(it->second.base);
    output.size = it->second.size;
    output.generation = it->second.generation;
    return output;
}

netsec_ptr netsec_ptr_untagged(void) {
    netsec_ptr output{};
    return output;
}

netsec_ptr netsec_ptr_from_object(netsec_object object) {
    netsec_ptr output{};
    output.address = reinterpret_cast<std::uintptr_t>(object.base);
    output.object_base = output.address;
    output.object_size = object.size;
    output.tag = object.tag;
    output.generation = object.generation;
    output.tagged = object.tag != netsec::no_tag ? 1U : 0U;
    return output;
}

netsec_ptr netsec_ptr_from_object_at(netsec_object object,
                                     size_t byte_offset) {
    if (byte_offset >
        static_cast<size_t>(std::numeric_limits<std::ptrdiff_t>::max())) {
        raise_trap_impl(NETSEC_TRAP_OUT_OF_BOUNDS,
                        reinterpret_cast<std::uintptr_t>(object.base),
                        byte_offset,
                        object.tag,
                        object.tag,
                        "object-relative pointer construction");
    }

    netsec_ptr output = netsec_ptr_from_object(object);
    output.address = offset_address(output.address,
                                    static_cast<std::ptrdiff_t>(byte_offset));

    const validation_result check = validate_descriptor(output, 0);
    if (!check.ok) {
        raise_validation(check, "object-relative pointer construction");
    }
    return output;
}

netsec_ptr netsec_ptr_from_address(const void* address, size_t bytes) {
    netsec_ptr output{};
    output.address = reinterpret_cast<std::uintptr_t>(address);

    const auto tag = netsec_tag_at(address, bytes);
    if (tag == netsec::no_tag) {
        return output;
    }

    output.tag = tag;
    output.tagged = 1;
    return netsec_ptr_reassociate(output, tag, bytes);
}

netsec_ptr netsec_ptr_add(netsec_ptr pointer, ptrdiff_t byte_offset) {
    pointer.address = offset_address(pointer.address, byte_offset);
    return pointer;
}

netsec_ptr netsec_ptr_sub(netsec_ptr pointer, ptrdiff_t byte_offset) {
    if (byte_offset == std::numeric_limits<std::ptrdiff_t>::min()) {
        raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                        pointer.address,
                        0,
                        pointer.tag,
                        pointer.tag,
                        "tagged pointer subtraction");
    }
    return netsec_ptr_add(pointer, -byte_offset);
}

netsec_ptr netsec_ptr_add_checked(netsec_ptr pointer, ptrdiff_t byte_offset) {
    netsec_ptr output = netsec_ptr_add(pointer, byte_offset);
    const validation_result check = validate_descriptor(output, 0);
    if (!check.ok) {
        raise_validation(check, "checked tagged pointer addition");
    }
    return output;
}

netsec_ptr netsec_ptr_sub_checked(netsec_ptr pointer, ptrdiff_t byte_offset) {
    if (byte_offset == std::numeric_limits<std::ptrdiff_t>::min()) {
        raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                        pointer.address,
                        0,
                        pointer.tag,
                        pointer.tag,
                        "checked tagged pointer subtraction");
    }
    return netsec_ptr_add_checked(pointer, -byte_offset);
}

ptrdiff_t netsec_ptr_distance(netsec_ptr left, netsec_ptr right) {
    const bool left_tagged = left.tagged != 0 && left.tag != netsec::no_tag;
    const bool right_tagged =
        right.tagged != 0 && right.tag != netsec::no_tag;

    if (left_tagged != right_tagged) {
        raise_trap_impl(NETSEC_TRAP_UNTAGGED,
                        left_tagged ? right.address : left.address,
                        0,
                        left_tagged ? left.tag : right.tag,
                        netsec::no_tag,
                        "tagged pointer difference");
    }
    if (!left_tagged) {
        if (left.address >= right.address) {
            const auto difference = left.address - right.address;
            if (difference >
                static_cast<std::uintptr_t>(
                    std::numeric_limits<std::ptrdiff_t>::max())) {
                raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                                left.address,
                                0,
                                netsec::no_tag,
                                netsec::no_tag,
                                "pointer difference");
            }
            return static_cast<std::ptrdiff_t>(difference);
        }
        const auto difference = right.address - left.address;
        if (difference >
            static_cast<std::uintptr_t>(
                std::numeric_limits<std::ptrdiff_t>::max())) {
            raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                            left.address,
                            0,
                            netsec::no_tag,
                            netsec::no_tag,
                            "pointer difference");
        }
        return -static_cast<std::ptrdiff_t>(difference);
    }

    if (left.tag != right.tag || left.generation != right.generation) {
        raise_trap_impl(NETSEC_TRAP_TAG_MISMATCH,
                        left.address,
                        0,
                        left.tag,
                        right.tag,
                        "tagged pointer difference");
    }

    if (left.address >= right.address) {
        const auto difference = left.address - right.address;
        if (difference >
            static_cast<std::uintptr_t>(
                std::numeric_limits<std::ptrdiff_t>::max())) {
            raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                            left.address,
                            0,
                            left.tag,
                            right.tag,
                            "tagged pointer difference");
        }
        return static_cast<std::ptrdiff_t>(difference);
    }

    const auto difference = right.address - left.address;
    if (difference >
        static_cast<std::uintptr_t>(
            std::numeric_limits<std::ptrdiff_t>::max())) {
        raise_trap_impl(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                        left.address,
                        0,
                        left.tag,
                        right.tag,
                        "tagged pointer difference");
    }
    return -static_cast<std::ptrdiff_t>(difference);
}

netsec_ptr netsec_ptr_convert(netsec_ptr pointer,
                              size_t old_element_size,
                              size_t new_element_size) {
    /*
     * Conversion changes the pointee type, not the object provenance.  The
     * element sizes are supplied by compiler instrumentation for auditing and
     * to catch malformed lowering, but the tag, bounds and generation are
     * carried verbatim.
     */
    if (old_element_size == 0 || new_element_size == 0) {
        raise_trap_impl(NETSEC_TRAP_INVALID_ARGUMENT,
                        pointer.address,
                        0,
                        pointer.tag,
                        pointer.tag,
                        "pointer conversion");
    }
    return pointer;
}

netsec_ptr netsec_ptr_strip(netsec_ptr pointer) {
    pointer.tag = netsec::no_tag;
    pointer.tagged = 0;
    return pointer;
}

netsec_ptr netsec_ptr_reassociate(netsec_ptr raw,
                                  uint64_t tag,
                                  size_t bytes) {
    if (tag == netsec::no_tag) {
        raise_trap_impl(NETSEC_TRAP_UNTAGGED,
                        raw.address,
                        bytes,
                        tag,
                        netsec::no_tag,
                        "tag re-association");
    }
    if (raw.tagged != 0 && raw.tag != netsec::no_tag && raw.tag != tag) {
        raise_trap_impl(NETSEC_TRAP_TAG_MISMATCH,
                        raw.address,
                        bytes,
                        tag,
                        raw.tag,
                        "tag re-association");
    }

    netsec_ptr output{};
    {
        auto& state = registry();
        std::lock_guard lock(state.mutex);
        const auto it = state.by_tag.find(tag);
        if (it == state.by_tag.end()) {
            raise_trap_impl(NETSEC_TRAP_STALE_OBJECT,
                            raw.address,
                            bytes,
                            tag,
                            netsec::no_tag,
                            "tag re-association");
        }
        output = make_descriptor(it->second, raw.address);
    }

    const validation_result check = validate_descriptor(output, bytes);
    if (!check.ok) {
        raise_validation(check, "tag re-association");
    }
    return output;
}

int netsec_ptr_check(netsec_ptr pointer, size_t bytes) {
    return validate_descriptor(pointer, bytes).ok ? 1 : 0;
}

void netsec_ptr_require(netsec_ptr pointer, size_t bytes, int access) {
    const validation_result check = validate_descriptor(pointer, bytes);
    if (!check.ok) {
        raise_validation(check, access_operation(access));
    }
}

int netsec_bounds_check(netsec_ptr pointer, size_t bytes) {
    return netsec_ptr_check(pointer, bytes);
}

void netsec_bounds_require(netsec_ptr pointer, size_t bytes) {
    const validation_result check = validate_descriptor(pointer, bytes);
    if (!check.ok) {
        raise_validation(check, "bounds validation");
    }
}

uint64_t netsec_tag_at(const void* address, size_t bytes) {
    if (address == nullptr) {
        return netsec::no_tag;
    }
    const auto raw = reinterpret_cast<std::uintptr_t>(address);
    if (bytes != 0 &&
        raw > std::numeric_limits<std::uintptr_t>::max() - bytes) {
        return netsec::no_tag;
    }

    auto& state = registry();
    std::lock_guard lock(state.mutex);
    auto it = state.by_base.upper_bound(raw);
    if (it == state.by_base.begin()) {
        return netsec::no_tag;
    }
    --it;
    const auto& object = it->second;
    if (raw < object.base || raw - object.base >= object.size) {
        return netsec::no_tag;
    }
    if (bytes > object.size - (raw - object.base)) {
        return netsec::no_tag;
    }
    return object.tag;
}

void netsec_syscall_revalidate(netsec_ptr pointer,
                               size_t bytes,
                               int kernel_writes) {
    /*
     * Revalidate at the kernel boundary even if the pointer was checked in
     * userspace earlier.  The registry may have been changed while the task
     * was descheduled, so no cached validation result is trusted here.
     */
    syscall_validations.fetch_add(1, std::memory_order_relaxed);
    if (bytes == 0) {
        return;
    }

    const validation_result check = validate_descriptor(pointer, bytes);
    if (!check.ok) {
        raise_validation(check,
                         kernel_writes != 0 ? "syscall copy-out"
                                            : "syscall copy-in");
    }
}

void* netsec_syscall_map(netsec_ptr pointer,
                         size_t bytes,
                         int kernel_writes) {
    netsec_syscall_revalidate(pointer, bytes, kernel_writes);
    return reinterpret_cast<void*>(pointer.address);
}

void netsec_copy_from_user(void* kernel_destination,
                           netsec_ptr user_source,
                           size_t bytes) {
    if (bytes == 0) {
        return;
    }
    if (kernel_destination == nullptr) {
        raise_trap_impl(NETSEC_TRAP_INVALID_ARGUMENT,
                        0,
                        bytes,
                        user_source.tag,
                        netsec::no_tag,
                        "syscall copy-in");
    }
    netsec_syscall_revalidate(user_source, bytes, 0);
    std::memmove(kernel_destination,
                 reinterpret_cast<const void*>(user_source.address),
                 bytes);
}

void netsec_copy_to_user(netsec_ptr user_destination,
                         const void* kernel_source,
                         size_t bytes) {
    if (bytes == 0) {
        return;
    }
    if (kernel_source == nullptr) {
        raise_trap_impl(NETSEC_TRAP_INVALID_ARGUMENT,
                        0,
                        bytes,
                        user_destination.tag,
                        netsec::no_tag,
                        "syscall copy-out");
    }
    netsec_syscall_revalidate(user_destination, bytes, 1);
    std::memmove(reinterpret_cast<void*>(user_destination.address),
                 kernel_source,
                 bytes);
}

uint64_t netsec_syscall_validation_count(void) {
    return syscall_validations.load(std::memory_order_relaxed);
}

}  // extern "C"
