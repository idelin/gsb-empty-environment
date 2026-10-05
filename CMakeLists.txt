cmake_minimum_required(VERSION 3.16)
project(gc_cycles CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(gc src/gc.cpp)
target_include_directories(gc PUBLIC include)
target_compile_options(gc PRIVATE -Wall -Wextra -Wpedantic)

enable_testing()
add_executable(test_gc tests/test_gc.cpp)
target_link_libraries(test_gc PRIVATE gc)
add_test(NAME gc COMMAND test_gc)
