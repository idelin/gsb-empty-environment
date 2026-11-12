cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec INTERFACE)
target_include_directories(netsec INTERFACE "${CMAKE_CURRENT_SOURCE_DIR}")

add_executable(netsec_tests tests/test_netsec.cpp netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)

add_executable(netsec_demo examples/demo.cpp netsec.cpp)
target_link_libraries(netsec_demo PRIVATE netsec)

enable_testing()
add_test(NAME netsec_tests COMMAND netsec_tests)
