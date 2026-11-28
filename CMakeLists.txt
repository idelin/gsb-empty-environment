cmake_minimum_required(VERSION 3.16)
project(w28_ccab866c55 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Header-only, zero-dependency verification library.
add_library(tdx_audit INTERFACE)
target_include_directories(tdx_audit INTERFACE
  ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_compile_features(tdx_audit INTERFACE cxx_std_17)

enable_testing()

add_executable(tdx_audit_tests tests/test_tdx_audit.cpp)
target_link_libraries(tdx_audit_tests PRIVATE tdx_audit)
if(MSVC)
  target_compile_options(tdx_audit_tests PRIVATE /W4 /WX)
else()
  target_compile_options(tdx_audit_tests PRIVATE
    -Wall -Wextra -Wconversion -Werror -pedantic)
endif()

add_test(NAME tdx_audit_tests COMMAND tdx_audit_tests)
