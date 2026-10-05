cmake_minimum_required(VERSION 3.14)
project(binrw CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(binrw INTERFACE)
target_include_directories(binrw INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(binrw INTERFACE -Wall -Wextra -Wpedantic)
endif()

enable_testing()
add_executable(binrw_tests tests/test_binrw.cpp)
target_link_libraries(binrw_tests PRIVATE binrw)
add_test(NAME binrw_tests COMMAND binrw_tests)
