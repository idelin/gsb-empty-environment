cmake_minimum_required(VERSION 3.16)
project(debugloc CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(debugloc src/debugloc.cpp)
target_include_directories(debugloc PUBLIC include)
target_compile_options(debugloc PRIVATE -Wall -Wextra -Wpedantic)

enable_testing()
add_executable(debugloc_tests tests/test_debugloc.cpp)
target_link_libraries(debugloc_tests PRIVATE debugloc)
add_test(NAME debugloc_tests COMMAND debugloc_tests)
