cmake_minimum_required(VERSION 3.16)
project(w27_69defca333 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(ept_nested INTERFACE)
target_include_directories(ept_nested INTERFACE
    "${CMAKE_CURRENT_SOURCE_DIR}")

option(EPT_NESTED_BUILD_TESTS "Build EPT nested verification tests" ON)
if(EPT_NESTED_BUILD_TESTS)
    enable_testing()
    add_executable(ept_nested_tests tests/ept_nested_tests.cpp)
    target_link_libraries(ept_nested_tests PRIVATE ept_nested)
    target_compile_options(ept_nested_tests PRIVATE
        -Wall -Wextra -Wpedantic -Wconversion -Wsign-conversion -Werror)
    add_test(NAME ept_nested_tests COMMAND ept_nested_tests)

    add_executable(ept_audit_demo examples/ept_audit_demo.cpp)
    target_link_libraries(ept_audit_demo PRIVATE ept_nested)
    target_compile_options(ept_audit_demo PRIVATE
        -Wall -Wextra -Wpedantic -Wconversion -Wsign-conversion -Werror)
    add_test(NAME ept_audit_demo COMMAND ept_audit_demo)
endif()
