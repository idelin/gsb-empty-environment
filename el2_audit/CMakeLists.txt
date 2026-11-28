cmake_minimum_required(VERSION 3.16)
project(el2_audit LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(el2_audit OBJECT
  src/el2_audit.cc
  include/el2_audit/el2_audit.h
)
target_include_directories(el2_audit PUBLIC
  ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(el2_audit PRIVATE
  -Wall -Wextra -Wconversion -Werror -Wpedantic
)

enable_testing()
add_executable(el2_audit_tests
  tests/test_el2_audit.cc
)
target_link_libraries(el2_audit_tests PRIVATE el2_audit)
target_compile_options(el2_audit_tests PRIVATE
  -Wall -Wextra -Wconversion -Werror -Wpedantic
)
add_test(NAME el2_audit_tests COMMAND el2_audit_tests)
