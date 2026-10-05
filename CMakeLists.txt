cmake_minimum_required(VERSION 3.16)
project(rational_resampler CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(rational_resampler src/rational_resampler.cpp)
target_include_directories(rational_resampler PUBLIC include)
target_compile_options(rational_resampler PRIVATE -Wall -Wextra)

enable_testing()
add_executable(test_resampler tests/test_resampler.cpp)
target_link_libraries(test_resampler PRIVATE rational_resampler)
add_test(NAME resampler COMMAND test_resampler)
