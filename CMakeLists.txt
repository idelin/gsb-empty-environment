cmake_minimum_required(VERSION 3.10)
project(stroke_rasterizer CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(stroke_rasterizer src/stroke_rasterizer.cpp)
target_include_directories(stroke_rasterizer PUBLIC include)
if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(stroke_rasterizer PRIVATE -Wall -Wextra)
endif()

add_executable(stroke_tests tests/tests.cpp)
target_link_libraries(stroke_tests PRIVATE stroke_rasterizer)

enable_testing()
add_test(NAME stroke_tests COMMAND stroke_tests)
