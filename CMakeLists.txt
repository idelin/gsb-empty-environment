cmake_minimum_required(VERSION 3.16)
project(ps_4c9edea22b CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(secret_lifecycle INTERFACE)
target_include_directories(secret_lifecycle INTERFACE
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
)

enable_testing()

add_executable(secret_lifecycle_tests
    tests/test_secret_lifecycle.cpp
)
target_link_libraries(secret_lifecycle_tests PRIVATE secret_lifecycle)
target_compile_options(secret_lifecycle_tests PRIVATE -Wall -Wextra -pedantic)
add_test(NAME secret_lifecycle_tests COMMAND secret_lifecycle_tests)
