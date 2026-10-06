cmake_minimum_required(VERSION 3.16)
project(thread_affine CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_executable(tests src/tests.cpp)
target_include_directories(tests PRIVATE src)
target_compile_options(tests PRIVATE -Wall -Wextra)
find_package(Threads REQUIRED)
target_link_libraries(tests PRIVATE Threads::Threads)

enable_testing()
add_test(NAME thread_affine COMMAND tests)
