cmake_minimum_required(VERSION 3.16)
project(gray_reconstruction CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(reconstruction src/reconstruction.cpp)
target_include_directories(reconstruction PUBLIC src)

add_executable(test_reconstruction tests/test_reconstruction.cpp)
target_link_libraries(test_reconstruction PRIVATE reconstruction)

enable_testing()
add_test(NAME reconstruction COMMAND test_reconstruction)
