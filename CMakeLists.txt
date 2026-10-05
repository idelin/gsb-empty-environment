cmake_minimum_required(VERSION 3.16)
project(exact_clip CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(exact_clip INTERFACE)
target_include_directories(exact_clip INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(test_clip tests/test_clip.cpp)
target_link_libraries(test_clip PRIVATE exact_clip)

enable_testing()
add_test(NAME clip COMMAND test_clip)
