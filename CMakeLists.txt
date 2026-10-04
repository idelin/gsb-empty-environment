cmake_minimum_required(VERSION 3.16)
project(chunked_reader CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(chunked_reader
  src/chunked_reader.cpp
  src/chunked_writer.cpp
)
target_include_directories(chunked_reader PUBLIC include)
target_compile_options(chunked_reader PRIVATE -Wall -Wextra)

add_executable(chunked_reader_tests tests/test_main.cpp)
target_link_libraries(chunked_reader_tests PRIVATE chunked_reader)
target_compile_options(chunked_reader_tests PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME chunked_reader_tests COMMAND chunked_reader_tests)
