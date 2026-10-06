cmake_minimum_required(VERSION 3.16)
project(idxkey CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_executable(key_test src/key_test.cpp)
target_include_directories(key_test PRIVATE src)
target_compile_options(key_test PRIVATE -Wall -Wextra -O2)

enable_testing()
add_test(NAME key_test COMMAND key_test)
