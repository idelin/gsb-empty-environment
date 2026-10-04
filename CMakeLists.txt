cmake_minimum_required(VERSION 3.16)
project(frame_decoder CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(frame_decoder frame_decoder.cpp)
target_include_directories(frame_decoder PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})

add_executable(frame_decoder_test frame_decoder_test.cpp)
target_link_libraries(frame_decoder_test PRIVATE frame_decoder)

enable_testing()
add_test(NAME frame_decoder_test COMMAND frame_decoder_test)
