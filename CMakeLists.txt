cmake_minimum_required(VERSION 3.16)
project(sgm_disparity CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(sgm src/sgm.cpp)
target_include_directories(sgm PUBLIC src)
target_compile_options(sgm PRIVATE -Wall -Wextra)

add_executable(sgm_demo src/main.cpp)
target_link_libraries(sgm_demo PRIVATE sgm)

enable_testing()
add_test(NAME sgm_demo COMMAND sgm_demo)
