cmake_minimum_required(VERSION 3.16)
project(chunked_labeler CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(chunked_labeler src/chunked_labeler.cpp)
target_include_directories(chunked_labeler PUBLIC include)

add_executable(run_tests tests/test_main.cpp)
target_link_libraries(run_tests PRIVATE chunked_labeler)
target_include_directories(run_tests PRIVATE tests)

enable_testing()
add_test(NAME chunked_labeler_tests COMMAND run_tests)
