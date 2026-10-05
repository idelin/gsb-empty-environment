cmake_minimum_required(VERSION 3.14)
project(chunked_analyzer CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(chunked_analyzer src/chunked_analyzer.cpp)
target_include_directories(chunked_analyzer PUBLIC include)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(chunked_analyzer PRIVATE -Wall -Wextra -Wpedantic)
endif()

enable_testing()
add_executable(chunked_analyzer_tests tests/test_main.cpp)
target_link_libraries(chunked_analyzer_tests PRIVATE chunked_analyzer)
if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(chunked_analyzer_tests PRIVATE -Wall -Wextra -Wpedantic)
endif()
add_test(NAME chunked_analyzer_tests COMMAND chunked_analyzer_tests)
