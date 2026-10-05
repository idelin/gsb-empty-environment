cmake_minimum_required(VERSION 3.16)
project(ray_intersect CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(ray_intersect src/ray_intersect.cpp)
target_include_directories(ray_intersect PUBLIC include)
target_compile_options(ray_intersect PRIVATE -Wall -Wextra)

add_executable(ray_tests tests/tests.cpp)
target_link_libraries(ray_tests PRIVATE ray_intersect)

enable_testing()
add_test(NAME ray_tests COMMAND ray_tests)
