cmake_minimum_required(VERSION 3.16)
project(w28_1337ee8ecf CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(webp_audit INTERFACE)
target_include_directories(webp_audit INTERFACE
    ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(test_webp_audit tests/test_webp_audit.cpp)
target_link_libraries(test_webp_audit PRIVATE webp_audit)
target_compile_options(test_webp_audit PRIVATE
    -Wall -Wextra -Wconversion -Werror -pedantic)

enable_testing()
add_test(NAME webp_audit_tests COMMAND test_webp_audit)

add_executable(fuzz_webp_audit tests/fuzz_webp_audit.cpp)
target_link_libraries(fuzz_webp_audit PRIVATE webp_audit)
target_compile_options(fuzz_webp_audit PRIVATE
    -Wall -Wextra -Wconversion -Werror -pedantic)
add_test(NAME webp_audit_fuzz COMMAND fuzz_webp_audit)
