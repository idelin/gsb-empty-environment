cmake_minimum_required(VERSION 3.16)
project(binrw CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(binrw INTERFACE)
target_include_directories(binrw INTERFACE include)

enable_testing()
add_executable(test_binrw tests/test_binrw.cpp)
target_link_libraries(test_binrw PRIVATE binrw)
target_compile_options(test_binrw PRIVATE -Wall -Wextra -Wpedantic)
add_test(NAME binrw COMMAND test_binrw)
