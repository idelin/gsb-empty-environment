cmake_minimum_required(VERSION 3.20)
project(netsec LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(netsec
    src/crypto.cpp
    src/encoding.cpp
    src/manifest.cpp
    src/policy.cpp
    src/result.cpp
    src/updater.cpp
    src/verification.cpp
    src/version.cpp)
target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>")
target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
add_library(netsec::netsec ALIAS netsec)

enable_testing()

option(NETSEC_BUILD_TESTS "Build netsec updater manifest tests" ON)
if (NETSEC_BUILD_TESTS)
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif ()
