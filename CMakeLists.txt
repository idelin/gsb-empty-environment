cmake_minimum_required(VERSION 3.16)
project(w27_d9cc20da83 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(kpti_audit INTERFACE)
target_include_directories(kpti_audit INTERFACE
  ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(x86_64_tests tests/x86_64_tests.cpp)
target_link_libraries(x86_64_tests PRIVATE kpti_audit)

add_executable(mini16_crosscheck tests/mini16_crosscheck.cpp)
target_link_libraries(mini16_crosscheck PRIVATE kpti_audit)

add_executable(syntax_check tests/syntax_check.cpp)
target_link_libraries(syntax_check PRIVATE kpti_audit)

foreach(target kpti_audit x86_64_tests mini16_crosscheck syntax_check)
  if(NOT target STREQUAL "kpti_audit")
    target_compile_options(${target} PRIVATE
      -Wall -Wextra -Wconversion -Werror)
  endif()
endforeach()

enable_testing()
add_test(NAME x86_64_tests COMMAND x86_64_tests)
add_test(NAME mini16_crosscheck COMMAND mini16_crosscheck)
