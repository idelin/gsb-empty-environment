#ifndef PACBTI_TEST_UTIL_HPP
#define PACBTI_TEST_UTIL_HPP

#include <cstdio>
#include <cstdlib>
#include <functional>
#include <string>
#include <vector>

namespace pacbtitest {

struct TestCase {
    std::string name;
    std::function<void()> body;
};

inline std::vector<TestCase>& registry() {
    static std::vector<TestCase> cases;
    return cases;
}

struct Registrar {
    Registrar(const std::string& name, std::function<void()> body) {
        registry().push_back(TestCase{name, std::move(body)});
    }
};

struct AssertionFailure {
    std::string message;
};

inline void fail(const std::string& message) {
    throw AssertionFailure{message};
}

inline int runAll() {
    int passed = 0;
    int failed = 0;
    for (const TestCase& test : registry()) {
        try {
            test.body();
            std::printf("[ PASS ] %s\n", test.name.c_str());
            ++passed;
        } catch (const AssertionFailure& failure) {
            std::printf("[ FAIL ] %s: %s\n", test.name.c_str(),
                        failure.message.c_str());
            ++failed;
        } catch (const std::exception& ex) {
            std::printf("[ FAIL ] %s: unexpected exception: %s\n",
                        test.name.c_str(), ex.what());
            ++failed;
        }
    }
    std::printf("\n%d passed, %d failed, %zu total\n", passed, failed,
                registry().size());
    return failed == 0 ? 0 : 1;
}

}  // namespace pacbtitest

#define TEST_CONCAT_INNER(a, b) a##b
#define TEST_CONCAT(a, b) TEST_CONCAT_INNER(a, b)

#define TEST_CASE(name)                                                     \
    static void TEST_CONCAT(pacbti_test_fn_, __LINE__)();                   \
    static pacbtitest::Registrar TEST_CONCAT(pacbti_test_reg_, __LINE__)(   \
            name, &TEST_CONCAT(pacbti_test_fn_, __LINE__));                 \
    static void TEST_CONCAT(pacbti_test_fn_, __LINE__)()

#define CHECK(condition)                                                    \
    do {                                                                    \
        if (!(condition)) {                                                 \
            pacbtitest::fail(std::string("CHECK failed: ") + #condition +   \
                             " at " + __FILE__ + ":" +                      \
                             std::to_string(__LINE__));                     \
        }                                                                   \
    } while (0)

#define CHECK_EQ(actual, expected)                                          \
    do {                                                                    \
        auto checkActual = (actual);                                        \
        auto checkExpected = (expected);                                    \
        if (!(checkActual == checkExpected)) {                              \
            pacbtitest::fail(std::string("CHECK_EQ failed: ") + #actual +   \
                             " == " + #expected + " at " + __FILE__ + ":" + \
                             std::to_string(__LINE__));                     \
        }                                                                   \
    } while (0)

#endif
