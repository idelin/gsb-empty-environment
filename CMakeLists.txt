cmake_minimum_required(VERSION 3.16)
project(area_scaler CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(area_scaler INTERFACE)
target_include_directories(area_scaler INTERFACE include)

enable_testing()
add_executable(test_scaler tests/test_scaler.cpp)
target_link_libraries(test_scaler PRIVATE area_scaler)
target_compile_options(test_scaler PRIVATE -Wall -Wextra)
add_test(NAME scaler COMMAND test_scaler)
