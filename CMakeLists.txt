cmake_minimum_required(VERSION 3.16)
project(tlpool CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Threads REQUIRED)

add_executable(tlpool_tests tests.cpp)
target_link_libraries(tlpool_tests PRIVATE Threads::Threads)
target_compile_options(tlpool_tests PRIVATE -Wall -Wextra -Werror)

option(TLPOOL_SANITIZERS "Build with ASan+UBSan" OFF)
if(TLPOOL_SANITIZERS)
  target_compile_options(tlpool_tests PRIVATE -fsanitize=address,undefined
                                              -fno-omit-frame-pointer)
  target_link_options(tlpool_tests PRIVATE -fsanitize=address,undefined)
endif()

enable_testing()
add_test(NAME tlpool_tests COMMAND tlpool_tests)
