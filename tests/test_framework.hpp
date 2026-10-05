// Minimal test framework (no external dependencies).
#pragma once

#include <chrono>
#include <cstdio>
#include <functional>
#include <thread>
#include <string>
#include <vector>

namespace tf {

struct Registry {
  struct Case {
    std::string suite;
    std::string name;
    std::function<void()> fn;
  };
  std::vector<Case> cases;
  int checks = 0;
  int failures = 0;
  static Registry& Get() {
    static Registry r;
    return r;
  }
};

struct Registrar {
  Registrar(std::string suite, std::string name, std::function<void()> fn) {
    Registry::Get().cases.push_back({std::move(suite), std::move(name), std::move(fn)});
  }
};

inline void ReportCheck(bool ok, const char* expr, const char* file, int line) {
  ++Registry::Get().checks;
  if (!ok) {
    ++Registry::Get().failures;
    std::fprintf(stderr, "  CHECK FAILED %s:%d: %s\n", file, line, expr);
  }
}

// Polls pred() until it holds or the timeout expires.
template <typename Pred>
bool WaitFor(Pred&& pred, std::chrono::milliseconds timeout =
                            std::chrono::milliseconds(5000)) {
  const auto deadline = std::chrono::steady_clock::now() + timeout;
  while (std::chrono::steady_clock::now() < deadline) {
    if (pred()) return true;
    std::this_thread::sleep_for(std::chrono::microseconds(100));
  }
  return pred();
}

inline int RunAll() {
  int failed_cases = 0;
  for (const auto& c : Registry::Get().cases) {
    int before = Registry::Get().failures;
    std::fprintf(stderr, "[RUN ] %s.%s\n", c.suite.c_str(), c.name.c_str());
    c.fn();
    bool ok = Registry::Get().failures == before;
    if (!ok) ++failed_cases;
    std::fprintf(stderr, "[%s] %s.%s\n", ok ? " OK " : "FAIL",
                 c.suite.c_str(), c.name.c_str());
  }
  std::fprintf(stderr, "----\n%d checks, %d failures, %d failed cases\n",
               Registry::Get().checks, Registry::Get().failures, failed_cases);
  return failed_cases == 0 ? 0 : 1;
}

}  // namespace tf

#define TEST(suite, name)                                          \
  static void suite##_##name##_fn();                               \
  static ::tf::Registrar suite##_##name##_reg(#suite, #name,       \
                                              suite##_##name##_fn);\
  static void suite##_##name##_fn()

#define CHECK(cond) ::tf::ReportCheck(static_cast<bool>(cond), #cond, __FILE__, __LINE__)

#define ASSERT(cond)                                                  \
  do {                                                                \
    if (!(cond)) {                                                    \
      ::tf::ReportCheck(false, #cond, __FILE__, __LINE__);            \
      return;                                                         \
    }                                                                 \
  } while (0)
#define ASSERT_LT(a, b) ASSERT((a) < (b))
