cmake_minimum_required(VERSION 3.16)
project(cycle_gc CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(cycle_gc INTERFACE)
target_include_directories(cycle_gc INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(gc_tests tests/gc_tests.cpp)
target_link_libraries(gc_tests PRIVATE cycle_gc)
target_compile_options(gc_tests PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME gc_tests COMMAND gc_tests)
