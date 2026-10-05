cmake_minimum_required(VERSION 3.16)
project(tlpool CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(tlpool INTERFACE)
target_include_directories(tlpool INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(pool_tests tests/pool_tests.cpp)
target_link_libraries(pool_tests PRIVATE tlpool Threads::Threads)
find_package(Threads REQUIRED)
target_compile_options(pool_tests PRIVATE -Wall -Wextra -Wpedantic)

enable_testing()
add_test(NAME pool_tests COMMAND pool_tests)

# Sanitizer build (optional): cmake -B build-asan -DTLPOOL_SANITIZERS=ON
option(TLPOOL_SANITIZERS "Build tests with ASan+UBSan" OFF)
if(TLPOOL_SANITIZERS)
  target_compile_options(pool_tests PRIVATE -fsanitize=address,undefined -fno-omit-frame-pointer)
  target_link_options(pool_tests PRIVATE -fsanitize=address,undefined)
endif()
