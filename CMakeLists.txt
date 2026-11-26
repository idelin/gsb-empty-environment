cmake_minimum_required(VERSION 3.16)
project(w27_60fee567c6 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(elfsec STATIC src/verify.cc)
target_include_directories(elfsec PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>)
target_compile_options(elfsec PRIVATE -Wall -Wextra -Wconversion -Werror)

add_executable(elfsec_cli cli/elfsec_main.cc)
target_link_libraries(elfsec_cli PRIVATE elfsec)
target_compile_options(elfsec_cli PRIVATE -Wall -Wextra -Wconversion -Werror)

enable_testing()
add_executable(elfsec_tests tests/test_verify.cc)
target_link_libraries(elfsec_tests PRIVATE elfsec)
target_include_directories(elfsec_tests PRIVATE tests)
target_compile_options(elfsec_tests PRIVATE -Wall -Wextra -Wconversion -Werror)
add_test(NAME elfsec_tests COMMAND elfsec_tests)

add_executable(elfsec_dump_samples tests/dump_samples.cc)
target_include_directories(elfsec_dump_samples PRIVATE tests)
target_compile_options(elfsec_dump_samples PRIVATE -Wall -Wextra -Wconversion -Werror)

add_executable(elfsec_edge_fuzz tests/edge_fuzz.cc)
target_link_libraries(elfsec_edge_fuzz PRIVATE elfsec)
target_compile_options(elfsec_edge_fuzz PRIVATE -Wall -Wextra -Wconversion -Werror)
add_test(NAME elfsec_edge_fuzz COMMAND elfsec_edge_fuzz)
