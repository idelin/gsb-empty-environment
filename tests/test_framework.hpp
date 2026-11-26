// SPDX-License-Identifier: MIT
// Minimal deterministic test harness: no external test framework.

#ifndef DTLS13_TEST_FRAMEWORK_HPP
#define DTLS13_TEST_FRAMEWORK_HPP

#include <cstdio>
#include <cstdlib>
#include <functional>
#include <stdexcept>
#include <string>
#include <vector>

namespace dtls13_test {

struct TestCase {
    std::string name;
    std::function<void()> fn;
};

inline std::vector<TestCase>& registry() {
    static std::vector<TestCase> cases;
    return cases;
}

struct Registrar {
    Registrar(const char* name, std::function<void()> fn) {
        registry().push_back(TestCase{name, std::move(fn)});
    }
};

inline int runAll() {
    int failures = 0;
    for (const auto& test : registry()) {
        try {
            test.fn();
            std::printf("[ PASS ] %s\n", test.name.c_str());
        } catch (const std::exception& ex) {
            ++failures;
            std::printf("[ FAIL ] %s: %s\n", test.name.c_str(), ex.what());
        } catch (...) {
            ++failures;
            std::printf("[ FAIL ] %s: unknown exception\n",
                        test.name.c_str());
        }
    }
    std::printf("\n%zu tests, %d failure(s)\n", registry().size(),
                failures);
    return failures == 0 ? 0 : 1;
}

}  // namespace dtls13_test

#define TEST_CASE(name)                                                    \
    static void name();                                                    \
    static ::dtls13_test::Registrar registrar_##name(#name, name);         \
    static void name()

#define CHECK(condition)                                                   \
    do {                                                                   \
        if (!(condition)) {                                                \
            throw std::runtime_error(std::string("CHECK failed: ") +       \
                                     #condition + " (" + __FILE__ + ":" +  \
                                     std::to_string(__LINE__) + ")");      \
        }                                                                  \
    } while (0)

#define CHECK_EQ(actual, expected)                                         \
    do {                                                                   \
        auto actual_value = (actual);                                      \
        auto expected_value = (expected);                                  \
        if (!(actual_value == expected_value)) {                           \
            throw std::runtime_error(                                      \
                std::string("CHECK_EQ failed: ") + #actual + " == " +      \
                #expected + " (" + __FILE__ + ":" +                        \
                std::to_string(__LINE__) + ")");                           \
        }                                                                  \
    } while (0)

#endif  // DTLS13_TEST_FRAMEWORK_HPP
