cmake_minimum_required(VERSION 3.16)
project(lockmgr CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE RelWithDebInfo)
endif()

add_library(lockmgr src/lock_manager.cpp)
target_include_directories(lockmgr PUBLIC include)
target_compile_options(lockmgr PRIVATE -Wall -Wextra -Wpedantic)
find_package(Threads REQUIRED)
target_link_libraries(lockmgr PUBLIC Threads::Threads)

enable_testing()

add_executable(interleaving_test tests/interleaving_test.cpp)
target_link_libraries(interleaving_test PRIVATE lockmgr)
target_compile_options(interleaving_test PRIVATE -Wall -Wextra)

add_executable(model_test tests/model_test.cpp)
target_link_libraries(model_test PRIVATE lockmgr)
target_compile_options(model_test PRIVATE -Wall -Wextra)

add_test(NAME interleaving COMMAND interleaving_test)
add_test(NAME model COMMAND model_test)
