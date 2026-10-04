cmake_minimum_required(VERSION 3.14)
project(raypoly CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(raypoly INTERFACE)
target_include_directories(raypoly INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(raypoly_tests tests/test_ray_intersect.cpp)
target_link_libraries(raypoly_tests PRIVATE raypoly)
target_compile_options(raypoly_tests PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME raypoly_tests COMMAND raypoly_tests)
