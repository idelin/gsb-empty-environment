cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
# OBJECT 库：检测逻辑只需一个源文件，导入方直接编译 netsec.cpp 即可，
# 也避免受限环境下归档器（ar）不可用导致无法构建。
add_library(netsec OBJECT netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(test_detector tests/test_detector.cpp)
target_link_libraries(test_detector PRIVATE netsec)
add_test(NAME detector COMMAND test_detector)

add_executable(can_driver_demo examples/can_driver_demo.cpp)
target_link_libraries(can_driver_demo PRIVATE netsec)
