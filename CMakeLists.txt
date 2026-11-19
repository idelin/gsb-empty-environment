cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(connection_id_manager_test
  tests/connection_id_manager_test.cpp)
target_link_libraries(connection_id_manager_test PRIVATE netsec)
add_test(NAME connection_id_manager_test
  COMMAND connection_id_manager_test)
