cmake_minimum_required(VERSION 3.16)
project(fem_assembly CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(fem INTERFACE)
target_include_directories(fem INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(test_assembly tests/test_assembly.cpp)
target_link_libraries(test_assembly PRIVATE fem)
target_compile_options(test_assembly PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME assembly COMMAND test_assembly)
