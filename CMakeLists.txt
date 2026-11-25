cmake_minimum_required(VERSION 3.16)
project(ps_70d0de8e00 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(tlv_reader INTERFACE)
target_include_directories(tlv_reader INTERFACE include)

enable_testing()
add_executable(tlv_reader_test tests/tlv_reader_test.cpp)
target_link_libraries(tlv_reader_test PRIVATE tlv_reader)
target_compile_options(tlv_reader_test PRIVATE -Wall -Wextra -Wpedantic)
add_test(NAME tlv_reader_test COMMAND tlv_reader_test)
