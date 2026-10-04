cmake_minimum_required(VERSION 3.16)
project(event_lib CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(event INTERFACE)
target_include_directories(event INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(test_event tests/test_event.cpp)
target_link_libraries(test_event PRIVATE event pthread)
target_compile_options(test_event PRIVATE -Wall -Wextra -Werror)

enable_testing()
add_test(NAME event_tests COMMAND test_event)
