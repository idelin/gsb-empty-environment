cmake_minimum_required(VERSION 3.16)
project(frame_decoder CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(frame_decoder src/frame_decoder.cpp)
target_include_directories(frame_decoder PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_compile_options(frame_decoder PRIVATE -Wall -Wextra)

add_executable(test_decoder src/test_decoder.cpp)
target_link_libraries(test_decoder PRIVATE frame_decoder)
target_compile_options(test_decoder PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME decoder COMMAND test_decoder)
