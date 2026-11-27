cmake_minimum_required(VERSION 3.16)
project(w28_e3ee4eba2e CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(vtd_policy INTERFACE)
target_include_directories(vtd_policy INTERFACE
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>)

add_executable(vtd_policy_tests tests/test_vtd_policy.cpp)
target_link_libraries(vtd_policy_tests PRIVATE vtd_policy)
target_compile_options(vtd_policy_tests PRIVATE
  -Wall -Wextra -Wconversion -Werror)

enable_testing()
add_test(NAME vtd_policy_tests COMMAND vtd_policy_tests)
