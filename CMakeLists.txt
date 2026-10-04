cmake_minimum_required(VERSION 3.16)
project(debugloc CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(debugloc src/evaluator.cpp)
target_include_directories(debugloc PUBLIC include)
target_compile_options(debugloc PRIVATE -Wall -Wextra -Wpedantic)

enable_testing()
add_executable(test_evaluator tests/test_evaluator.cpp)
target_link_libraries(test_evaluator PRIVATE debugloc)
add_test(NAME evaluator COMMAND test_evaluator)
