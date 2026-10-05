cmake_minimum_required(VERSION 3.16)
project(graydiff LANGUAGES CXX)

add_library(graydiff INTERFACE)
target_include_directories(graydiff INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_compile_features(graydiff INTERFACE cxx_std_17)

add_executable(graydiff_demo examples/demo.cpp)
target_link_libraries(graydiff_demo PRIVATE graydiff)

add_executable(graydiff_tests tests/test_main.cpp)
target_link_libraries(graydiff_tests PRIVATE graydiff)

foreach(t graydiff_demo graydiff_tests)
    target_compile_options(${t} PRIVATE -Wall -Wextra -Wpedantic)
endforeach()

enable_testing()
add_test(NAME graydiff_tests COMMAND graydiff_tests)
