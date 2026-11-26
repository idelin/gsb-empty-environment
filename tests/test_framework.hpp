// Tiny dependency-free test harness.  No third-party framework is pulled in;
// assertions record failures and main() returns a non-zero exit code on error.
#ifndef CCM_TEST_FRAMEWORK_HPP
#define CCM_TEST_FRAMEWORK_HPP

#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

namespace ccm_test {

struct TestCase {
    std::string name;
    void (*fn)();
};

inline std::vector<TestCase>& registry() {
    static std::vector<TestCase> cases;
    return cases;
}

inline int& failure_count() {
    static int failures = 0;
    return failures;
}

struct Registrar {
    Registrar(const char* name, void (*fn)()) {
        registry().push_back(TestCase{name, fn});
    }
};

inline void check(bool condition, const char* expression, const char* file,
                  int line) {
    if (!condition) {
        ++failure_count();
        std::fprintf(stderr, "  FAIL %s:%d: %s\n", file, line, expression);
    }
}

inline int run_all(const char* suite_name) {
    std::printf("== %s: %zu cases ==\n", suite_name, registry().size());
    int ran = 0;
    for (const TestCase& test : registry()) {
        int before = failure_count();
        std::printf("[ RUN ] %s\n", test.name.c_str());
        test.fn();
        ++ran;
        if (failure_count() == before) {
            std::printf("[  OK ] %s\n", test.name.c_str());
        }
    }
    std::printf("== %s: %d run, %d failure(s) ==\n", suite_name, ran,
                failure_count());
    return failure_count() == 0 ? 0 : 1;
}

}  // namespace ccm_test

#define CCM_TEST(name)                                              \
    static void name();                                             \
    static ccm_test::Registrar registrar_##name(#name, name);       \
    static void name()

#define CCM_CHECK(expression) \
    ccm_test::check((expression), #expression, __FILE__, __LINE__)

#define CCM_CHECK_EQ(a, b) \
    ccm_test::check((a) == (b), #a " == " #b, __FILE__, __LINE__)

#define CCM_CHECK_STATUS_OK(status) \
    ccm_test::check((status).ok(), #status " must be ok", __FILE__, __LINE__)

#define CCM_CHECK_STATUS(expected, status)                              \
    ccm_test::check((status).code == (expected),                        \
                    #status " code must be " #expected, __FILE__,       \
                    __LINE__)

#endif  // CCM_TEST_FRAMEWORK_HPP
