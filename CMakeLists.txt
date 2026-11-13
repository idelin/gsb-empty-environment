cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

add_executable(tcache_driver driver.cpp)
target_link_libraries(tcache_driver PRIVATE netsec)

add_executable(tcache_tests tests.cpp)
target_link_libraries(tcache_tests PRIVATE netsec)

enable_testing()
add_test(NAME tcache_tests COMMAND tcache_tests)
