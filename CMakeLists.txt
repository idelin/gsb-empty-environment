cmake_minimum_required(VERSION 3.16)
project(borrow_authority LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(borrow_authority INTERFACE)
target_include_directories(borrow_authority INTERFACE
    ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_compile_features(borrow_authority INTERFACE cxx_std_17)

enable_testing()

add_executable(borrow_tests tests/test_main.cpp)
target_link_libraries(borrow_tests PRIVATE borrow_authority)
if (NOT MSVC)
    target_compile_options(borrow_tests PRIVATE -Wall -Wextra -Wpedantic)
endif()
add_test(NAME borrow_tests COMMAND borrow_tests)

add_executable(borrow_demo examples/demo.cpp)
target_link_libraries(borrow_demo PRIVATE borrow_authority)
if (NOT MSVC)
    target_compile_options(borrow_demo PRIVATE -Wall -Wextra -Wpedantic)
endif()
