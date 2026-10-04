cmake_minimum_required(VERSION 3.16)
project(fem_assembly CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(fem_assembly INTERFACE)
target_include_directories(fem_assembly INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

enable_testing()
add_executable(test_assembly tests/test_assembly.cpp)
target_link_libraries(test_assembly PRIVATE fem_assembly)
target_compile_options(test_assembly PRIVATE -Wall -Wextra)
add_test(NAME assembly COMMAND test_assembly)
