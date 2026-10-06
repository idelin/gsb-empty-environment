cmake_minimum_required(VERSION 3.20)
project(swr LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(swr
  src/sliding_window_reader.cpp
  src/mmap_backend.cpp
)
target_include_directories(swr PUBLIC include)
target_compile_options(swr PRIVATE -Wall -Wextra -Wpedantic)

add_executable(swr_tests tests/test_main.cpp)
target_include_directories(swr_tests PRIVATE tests)
target_link_libraries(swr_tests PRIVATE swr)
target_compile_options(swr_tests PRIVATE -Wall -Wextra -Wpedantic)

enable_testing()
add_test(NAME swr_tests COMMAND swr_tests)
