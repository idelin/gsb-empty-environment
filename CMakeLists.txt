cmake_minimum_required(VERSION 3.16)
project(w28_d0b4079f06 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(reghive_audit STATIC
    src/reghive_audit.cpp
)
target_include_directories(reghive_audit PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(reghive_audit PRIVATE
    -Wall -Wextra -Wconversion -Wsign-conversion -Werror
)

enable_testing()
add_executable(test_reghive_audit
    tests/test_reghive_audit.cpp
)
target_include_directories(test_reghive_audit PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
    ${CMAKE_CURRENT_SOURCE_DIR}/tests
)
target_compile_options(test_reghive_audit PRIVATE
    -Wall -Wextra -Wconversion -Wsign-conversion -Werror
)
target_link_libraries(test_reghive_audit PRIVATE reghive_audit)
add_test(NAME reghive_audit_suite COMMAND test_reghive_audit)
