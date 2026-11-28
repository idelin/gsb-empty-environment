cmake_minimum_required(VERSION 3.16)
project(w28_240ae43514 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(gif_audit INTERFACE)
target_include_directories(gif_audit
    INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

option(GIF_AUDIT_BUILD_TESTS "Build gif_audit unit tests" ON)
if(GIF_AUDIT_BUILD_TESTS)
    enable_testing()
    add_executable(gif_audit_tests tests/gif_audit_test.cpp)
    target_link_libraries(gif_audit_tests PRIVATE gif_audit)
    target_compile_options(gif_audit_tests PRIVATE
        -Wall -Wextra -Wconversion -Werror -pedantic)
    add_test(NAME gif_audit_tests COMMAND gif_audit_tests)
endif()
