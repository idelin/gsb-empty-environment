cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(test_secure_buffer test_secure_buffer.cpp)
target_link_libraries(test_secure_buffer PRIVATE netsec)
target_compile_options(test_secure_buffer PRIVATE -Wall -Wextra -Wpedantic)
add_test(NAME secure_buffer_tests COMMAND test_secure_buffer)

# Assembly-level proof that the secure clear survives -O3. zero_check.cpp has
# no main(): it only exists to emit the clearing routine as assembly.
add_custom_command(
    OUTPUT "${CMAKE_BINARY_DIR}/zero_check_o3.s"
    COMMAND ${CMAKE_CXX_COMPILER}
            ${CMAKE_CXX17_STANDARD_COMPILE_OPTION} -O3 -DNDEBUG -S
            "${CMAKE_CURRENT_SOURCE_DIR}/zero_check.cpp"
            -o "${CMAKE_BINARY_DIR}/zero_check_o3.s"
    DEPENDS zero_check.cpp netsec.hpp
    COMMENT "Emitting -O3 assembly for the secure-clear primitive"
    VERBATIM)
add_custom_target(zero_check_assembly ALL
    DEPENDS "${CMAKE_BINARY_DIR}/zero_check_o3.s")
add_test(
    NAME secure_clear_not_optimized_out
    COMMAND ${CMAKE_COMMAND}
            -DASM_FILE=zero_check_o3.s
            -P "${CMAKE_CURRENT_SOURCE_DIR}/check_secure_clear.cmake")
set_tests_properties(secure_clear_not_optimized_out PROPERTIES
    WORKING_DIRECTORY "${CMAKE_BINARY_DIR}")
