cmake_minimum_required(VERSION 3.16)
project(exactclip CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(exactclip INTERFACE)
target_include_directories(exactclip INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(test_clip tests/test_clip.cpp)
target_link_libraries(test_clip PRIVATE exactclip)
target_compile_options(test_clip PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME clip COMMAND test_clip)
