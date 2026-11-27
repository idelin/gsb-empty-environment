cmake_minimum_required(VERSION 3.16)
project(w27_68bbd9200f CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(ctir INTERFACE)
target_include_directories(ctir INTERFACE
    ${CMAKE_CURRENT_SOURCE_DIR}/include)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(ctir INTERFACE
        -Wall -Wextra -Wconversion -Werror -pedantic)
endif()

enable_testing()

add_executable(ct_tests tests/test_constant_time.cpp)
target_link_libraries(ct_tests PRIVATE ctir)
add_test(NAME constant_time_tests COMMAND ct_tests)

add_executable(render_demo tests/render_demo.cpp)
target_link_libraries(render_demo PRIVATE ctir)
