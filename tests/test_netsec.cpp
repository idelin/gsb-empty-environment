#include "netsec.hpp"

#include <array>
#include <cstddef>
#include <cstdio>
#include <cstring>
#include <limits>
#include <string>
#include <utility>

namespace {

int failures = 0;
int handler_calls = 0;
int handler_last_reason = NETSEC_TRAP_NONE;

void fail(const char* expression, const char* file, int line,
          const std::string& detail = {}) {
    ++failures;
    std::fprintf(stderr, "FAIL %s:%d: %s", file, line, expression);
    if (!detail.empty()) {
        std::fprintf(stderr, " (%s)", detail.c_str());
    }
    std::fputc('\n', stderr);
}

#define CHECK(expression)                                                   \
    do {                                                                    \
        if (!(expression)) {                                                \
            fail(#expression, __FILE__, __LINE__);                          \
        }                                                                   \
    } while (false)

template <class Function>
void expect_trap(Function&& function,
                 const char* expression,
                 const char* file,
                 int line,
                 netsec::trap_reason expected = netsec::trap_reason::none) {
    try {
        std::forward<Function>(function)();
    } catch (const netsec::tag_violation& violation) {
        if (expected != netsec::trap_reason::none &&
            violation.reason() != expected) {
            fail(expression, file, line,
                 std::string("wrong trap reason: ") +
                     netsec::reason_name(violation.reason()));
        }
        return;
    } catch (...) {
        fail(expression, file, line, "wrong exception type");
        return;
    }
    fail(expression, file, line, "no trap was raised");
}

#define EXPECT_TRAP(expression)                                             \
    expect_trap([&] { expression; }, #expression, __FILE__, __LINE__)

#define EXPECT_TRAP_REASON(expression, reason)                              \
    expect_trap([&] { expression; }, #expression, __FILE__, __LINE__,       \
                netsec::trap_reason::reason)

void test_legal_access_and_arithmetic() {
    int values[8] = {0, 1, 2, 3, 4, 5, 6, 7};
    auto pointer = netsec::tag_object(values);

    CHECK(pointer.has_tag());
    CHECK(pointer.tag() != netsec::no_tag);
    CHECK(pointer.object_size() == sizeof(values));

    *pointer = 11;
    CHECK(values[0] == 11);

    auto at_three = pointer + 3;
    CHECK(at_three.tag() == pointer.tag());
    CHECK(at_three.object_size() == sizeof(values));
    CHECK(*at_three == 3);
    *at_three = 33;
    CHECK(values[3] == 33);

    auto back = at_three - 2;
    CHECK(back.tag() == pointer.tag());
    CHECK(*back == 1);
    CHECK(at_three - back == 2);

    CHECK(pointer[7] == 7);
    pointer[7] = 77;
    CHECK(values[7] == 77);

    auto last = pointer + 7;
    CHECK(last.check(sizeof(int)));
    last.require(sizeof(int));

    auto one_past = pointer + 8;
    CHECK(one_past.check(0));
    CHECK(!one_past.check(1));
}

void test_implicit_conversion_and_instrumentation() {
    std::array<unsigned long long, 4> values{1, 2, 3, 4};
    auto pointer = netsec::tag_object(values);

    // Compiler-generated implicit conversion between tagged pointee types.
    netsec::tagged_ptr<const unsigned char> bytes = pointer;
    CHECK(bytes.tag() == pointer.tag());
    CHECK(bytes.object_size() == sizeof(values));
    CHECK(bytes.check(sizeof(unsigned char)));
    CHECK(*bytes ==
          reinterpret_cast<const unsigned char*>(values.data())[0]);

    auto explicit_bytes =
        netsec::implicit_cast<unsigned char>(pointer);
    CHECK(explicit_bytes.tag() == pointer.tag());

    // C ABI lowering hook used by instrumented code.
    auto descriptor = netsec_ptr_convert(pointer.descriptor(),
                                         sizeof(unsigned long long),
                                         sizeof(unsigned short));
    CHECK(descriptor.tag == pointer.tag());
    CHECK(descriptor.object_size == sizeof(values));
    CHECK(netsec_ptr_check(descriptor, sizeof(unsigned short)) == 1);

    auto converted = netsec::tagged_ptr<unsigned short>::from_descriptor(
        descriptor);
    CHECK(converted.has_tag());
    CHECK(converted.tag() == pointer.tag());
    CHECK(converted.check(sizeof(unsigned short)));

    // Raw-to-tagged lowering recovers provenance from the registry instead
    // of silently stripping the tag.
    auto recovered =
        netsec::tag_address(values.data() + 2, sizeof(values[2]));
    CHECK(recovered.tag() == pointer.tag());
    CHECK(*recovered == static_cast<unsigned char>(values[2]));

    auto descriptor_recovered = netsec_ptr_from_address(values.data() + 3,
                                                        sizeof(values[3]));
    CHECK(descriptor_recovered.tagged == 1);
    CHECK(descriptor_recovered.tag == pointer.tag());
    CHECK(netsec_ptr_check(descriptor_recovered, sizeof(values[3])) == 1);
}

void test_tag_stripping() {
    int value = 7;
    auto pointer = netsec::tag_object(&value, 1);

    auto stripped = pointer.strip_tag();
    CHECK(!stripped.has_tag());
    CHECK(stripped.tag() == netsec::no_tag);
    CHECK(!stripped.check(sizeof(int)));
    EXPECT_TRAP_REASON(*stripped, untagged);

    auto abi_stripped = netsec_ptr_strip(pointer.descriptor());
    CHECK(netsec_ptr_check(abi_stripped, sizeof(int)) == 0);
    EXPECT_TRAP_REASON(netsec_ptr_require(abi_stripped,
                                          sizeof(int),
                                          NETSEC_ACCESS_READ),
                       untagged);
}

void test_cross_object_access() {
    alignas(64) std::byte storage[64]{};
    auto first = netsec::tag_bytes(storage, 32);
    auto second = netsec::tag_bytes(storage + 32, 32);

    CHECK(first.tag() != second.tag());
    CHECK(first.check(32));
    CHECK(second.check(32));

    // Pointer arithmetic carries first's tag into the second object.
    auto moved = first.add(32);
    CHECK(moved.tag() == first.tag());
    CHECK(netsec_tag_at(storage + 32, 1) == second.tag());
    EXPECT_TRAP_REASON(*moved, tag_mismatch);

    // Re-associating first's tag with an address inside the second object
    // must fail rather than bless the cross-object pointer.
    EXPECT_TRAP_REASON(
        netsec::retag(reinterpret_cast<std::byte*>(storage + 32),
                      first.tag(),
                      1),
        tag_mismatch);

    // A raw address plus a stale tag is not a capability either.
    EXPECT_TRAP_REASON(
        netsec::tagged_ptr<std::byte>::from_raw(
            storage + 32, first.tag(), 1),
        tag_mismatch);
}

void test_bounds_validation() {
    int values[4] = {10, 20, 30, 40};
    auto pointer = netsec::tag_object(values);
    const auto bytes = sizeof(values);

    CHECK(pointer.check(bytes));
    CHECK(pointer.bounds_check(bytes));
    CHECK(!pointer.check(bytes + 1));
    CHECK(!pointer.bounds_check(bytes + 1));

    EXPECT_TRAP_REASON(netsec::bounds_require(pointer.descriptor(),
                                              bytes + 1),
                       out_of_bounds);

    auto tail = pointer.add(bytes - sizeof(int));
    CHECK(tail.check(sizeof(int)));
    CHECK(!tail.check(sizeof(int) + 1));

    auto one_past = pointer.add(bytes);
    CHECK(one_past.check(0));
    CHECK(!one_past.check(1));
    EXPECT_TRAP_REASON(*one_past, out_of_bounds);

    EXPECT_TRAP(pointer[-1]);
    EXPECT_TRAP(pointer + static_cast<std::ptrdiff_t>(
                             std::numeric_limits<std::ptrdiff_t>::max()));
}

void test_checked_arithmetic_and_span() {
    int values[4] = {1, 2, 3, 4};
    auto pointer = netsec::tag_object(std::span<int>(values));

    auto last = pointer.checked_add(3 * static_cast<std::ptrdiff_t>(
                                            sizeof(int)));
    CHECK(*last == 4);
    EXPECT_TRAP_REASON(
        pointer.checked_add(4 * static_cast<std::ptrdiff_t>(sizeof(int)) +
                            1),
        out_of_bounds);

    auto view = netsec::make_span(pointer, 4);
    CHECK(view.size() == 4);
    view[2] = 30;
    CHECK(values[2] == 30);
    EXPECT_TRAP(view[4]);

    auto as_std_span = netsec::as_span(pointer, 4);
    CHECK(as_std_span.size() == 4);
    CHECK(as_std_span[2] == 30);
}

void test_stale_object_and_registry_reuse() {
    int value = 1;
    auto old_pointer = netsec::tag_object(&value, 1);
    const auto old_tag = old_pointer.tag();

    CHECK(netsec::object_alive(old_tag));
    CHECK(netsec::unregister_object(old_tag));
    CHECK(!netsec::object_alive(old_tag));
    EXPECT_TRAP_REASON(*old_pointer, stale_object);
    CHECK(!old_pointer.check(sizeof(int)));
    EXPECT_TRAP_REASON(old_pointer.require(sizeof(int)), stale_object);

    auto fresh_pointer = netsec::tag_object(&value, 1);
    CHECK(fresh_pointer.tag() != old_tag);
    CHECK(*fresh_pointer == 1);
    EXPECT_TRAP_REASON(*old_pointer, stale_object);
}

void test_syscall_boundary() {
    char source[32] = "tagged user buffer";
    char destination[32]{};
    auto user_source = netsec::tag_object(source);

    const auto before = netsec::syscall_validation_count();
    netsec::copy_from_user(destination, user_source, sizeof(source));
    CHECK(netsec::syscall_validation_count() > before);
    CHECK(std::memcmp(destination, source, sizeof(source)) == 0);

    auto user_destination = netsec::tag_object(destination);
    const char kernel_source[] = "kernel data";
    netsec::copy_to_user(user_destination, kernel_source,
                         sizeof(kernel_source));
    CHECK(std::memcmp(destination, kernel_source,
                      sizeof(kernel_source)) == 0);

    auto stripped = user_source.strip_tag();
    EXPECT_TRAP_REASON(netsec::copy_from_user(destination,
                                              stripped,
                                              1),
                       untagged);

    // The syscall entry point revalidates even if the descriptor was
    // constructed before the object disappeared.
    netsec::syscall_revalidate(user_source, sizeof(source), false);
    const auto tag = user_source.tag();
    CHECK(netsec::unregister_object(tag));
    EXPECT_TRAP_REASON(netsec::syscall_revalidate(user_source,
                                                  sizeof(source),
                                                  false),
                       stale_object);
}

void test_syscall_length_crosses_object() {
    alignas(64) std::byte storage[64]{};
    auto first = netsec::tag_bytes(storage, 16);
    auto second = netsec::tag_bytes(storage + 16, 16);
    (void)second;

    char destination[32]{};
    EXPECT_TRAP_REASON(netsec::copy_from_user(destination, first, 20),
                       out_of_bounds);
}

void test_trap_handler_observes_violation() {
    handler_calls = 0;
    handler_last_reason = NETSEC_TRAP_NONE;

    netsec::set_trap_handler(
        [](int reason, uintptr_t, size_t, uint64_t, uint64_t, const char*) {
            ++handler_calls;
            handler_last_reason = reason;
        });

    int value = 3;
    auto pointer = netsec::tag_object(&value, 1);
    auto stripped = pointer.strip_tag();

    EXPECT_TRAP_REASON(*stripped, untagged);
    CHECK(handler_calls == 1);
    CHECK(handler_last_reason == NETSEC_TRAP_UNTAGGED);

    netsec::set_trap_handler(nullptr);
}

}  // namespace

int main() {
    test_legal_access_and_arithmetic();
    test_implicit_conversion_and_instrumentation();
    test_tag_stripping();
    test_cross_object_access();
    test_bounds_validation();
    test_checked_arithmetic_and_span();
    test_stale_object_and_registry_reuse();
    test_syscall_boundary();
    test_syscall_length_crosses_object();
    test_trap_handler_observes_violation();

    if (failures != 0) {
        std::fprintf(stderr, "%d test failure(s)\n", failures);
        return 1;
    }
    std::fputs("all netsec tests passed\n", stdout);
    return 0;
}
