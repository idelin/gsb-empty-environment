cmake_minimum_required(VERSION 3.16)
project(sgm_aggregator CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(sgm src/sgm.cpp)
target_include_directories(sgm PUBLIC include)
target_compile_options(sgm PRIVATE -Wall -Wextra)

add_executable(sgm_demo examples/demo.cpp)
target_link_libraries(sgm_demo PRIVATE sgm)

add_executable(sgm_test tests/test_sgm.cpp)
target_link_libraries(sgm_test PRIVATE sgm)

enable_testing()
add_test(NAME sgm_test COMMAND sgm_test)
