cmake_minimum_required(VERSION 3.16)
project(ddd_10528_v4 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(certrev INTERFACE)
add_library(certrev::certrev ALIAS certrev)
add_library(${PROJECT_NAME} ALIAS certrev)
target_include_directories(certrev INTERFACE
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(certrev INTERFACE -Wall -Wextra -Wpedantic)
endif()

enable_testing()
add_executable(certrev_tests tests/test_revocation.cpp)
target_link_libraries(certrev_tests PRIVATE certrev)
add_test(NAME certrev_tests COMMAND certrev_tests)

add_executable(admission_host_example examples/admission_host.cpp)
target_link_libraries(admission_host_example PRIVATE certrev)
add_test(NAME admission_host_example COMMAND admission_host_example)

include(GNUInstallDirs)
install(TARGETS certrev EXPORT certrevTargets)
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(EXPORT certrevTargets
    NAMESPACE certrev::
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/certrev
)
