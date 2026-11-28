cmake_minimum_required(VERSION 3.16)
project(w28_4fd504a183 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(ext4_audit OBJECT src/audit.cpp)
target_include_directories(ext4_audit PUBLIC include)
target_compile_options(
  ext4_audit PRIVATE
  -Wall -Wextra -Wconversion -Werror)

add_executable(ext4_audit_tests tests/test_audit.cpp)
target_include_directories(ext4_audit_tests PRIVATE include tests)
target_link_libraries(ext4_audit_tests PRIVATE ext4_audit)
target_compile_options(
  ext4_audit_tests PRIVATE
  -Wall -Wextra -Wconversion -Werror)

add_executable(ext4_audit_cli examples/audit_cli.cpp)
target_link_libraries(ext4_audit_cli PRIVATE ext4_audit)
target_compile_options(
  ext4_audit_cli PRIVATE
  -Wall -Wextra -Wconversion -Werror)

enable_testing()
add_test(NAME ext4_audit_tests COMMAND ext4_audit_tests)
