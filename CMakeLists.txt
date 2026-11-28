cmake_minimum_required(VERSION 3.16)
project(w28_f3c2b21892 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(mpk_verify INTERFACE)
target_include_directories(mpk_verify INTERFACE
    ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(mpk_verify_tests tests/mpk_verify_tests.cpp)
target_link_libraries(mpk_verify_tests PRIVATE mpk_verify)
target_compile_options(mpk_verify_tests PRIVATE
    -Wall -Wextra -Wconversion -Werror -pedantic)

enable_testing()
add_test(NAME mpk_verify_tests COMMAND mpk_verify_tests)
