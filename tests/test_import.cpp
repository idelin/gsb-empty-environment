#include <cstdint>
#include <cstdio>
#include <cstring>

#if defined(__unix__) || defined(__APPLE__)
#include <dlfcn.h>
#endif

namespace {

using DecodeElementFn = int (*)(const std::uint8_t*, std::size_t,
                                std::uint32_t, void*, char*, std::size_t);
using PredicateFn = int (*)(std::uint32_t, std::uint32_t);
using StatusNameFn = const char* (*)(int);

}  // namespace

int main(int argc, char** argv) {
  if (argc != 2) {
    std::fprintf(stderr, "usage: %s <shared-library>\n", argv[0]);
    return 2;
  }

#if defined(__unix__) || defined(__APPLE__)
  void* library = dlopen(argv[1], RTLD_NOW);
  if (library == nullptr) {
    std::fprintf(stderr, "dlopen failed: %s\n", dlerror());
    return 1;
  }

  auto decode_element = reinterpret_cast<DecodeElementFn>(
      dlsym(library, "netsec_decode_element_segment"));
  auto predicate = reinterpret_cast<PredicateFn>(
      dlsym(library, "netsec_element_index_is_valid"));
  auto status_name =
      reinterpret_cast<StatusNameFn>(dlsym(library, "netsec_error_code_name"));
  if (decode_element == nullptr || predicate == nullptr ||
      status_name == nullptr) {
    std::fprintf(stderr, "required C ABI symbol is missing\n");
    dlclose(library);
    return 1;
  }

  // Active element segment: flags 0, i32.const 0, end, one entry: index 1.
  // With one function in the index space, index 1 is exactly the boundary and
  // must be rejected.
  const std::uint8_t segment[] = {0x00, 0x41, 0x00, 0x0b, 0x01, 0x01};
  char error_buffer[256];
  const int status = decode_element(segment, sizeof(segment), 1, nullptr,
                                    error_buffer, sizeof(error_buffer));
  if (status != 9) {
    std::fprintf(stderr, "expected status 9, got %d (%s)\n", status,
                 status_name(status));
    dlclose(library);
    return 1;
  }
  if (predicate(1, 1) != 0 || predicate(0, 1) != 1) {
    std::fprintf(stderr, "function-index predicate is wrong\n");
    dlclose(library);
    return 1;
  }

  dlclose(library);
  std::printf("runtime import test passed\n");
  return 0;
#else
  std::fprintf(stderr, "dlopen test is unsupported on this platform\n");
  return 0;
#endif
}
