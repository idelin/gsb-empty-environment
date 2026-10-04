cmake_minimum_required(VERSION 3.16)
project(grayrecon CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(grayrecon src/reconstruct.cpp)
target_include_directories(grayrecon PUBLIC include)
target_compile_options(grayrecon PRIVATE -Wall -Wextra)

add_executable(test_reconstruct tests/test_reconstruct.cpp)
target_link_libraries(test_reconstruct PRIVATE grayrecon)

enable_testing()
add_test(NAME reconstruct COMMAND test_reconstruct)
