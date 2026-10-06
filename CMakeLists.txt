cmake_minimum_required(VERSION 3.16)
project(bounded_queue CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_executable(bounded_queue_tests tests/test_main.cpp)
target_include_directories(bounded_queue_tests PRIVATE src)
target_compile_options(bounded_queue_tests PRIVATE -Wall -Wextra)
find_package(Threads REQUIRED)
target_link_libraries(bounded_queue_tests PRIVATE Threads::Threads)

enable_testing()
add_test(NAME bounded_queue_tests COMMAND bounded_queue_tests)
