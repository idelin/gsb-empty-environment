cmake_minimum_required(VERSION 3.16)
project(lfr CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(lfr src/reader.cpp)
target_include_directories(lfr PUBLIC include)
target_compile_options(lfr PRIVATE -Wall -Wextra)

add_executable(lfr_tests tests/test_main.cpp)
target_include_directories(lfr_tests PRIVATE tests)
target_link_libraries(lfr_tests PRIVATE lfr pthread)
target_compile_options(lfr_tests PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME lfr_tests COMMAND lfr_tests)
