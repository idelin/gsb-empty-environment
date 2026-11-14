cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

include(GNUInstallDirs)
find_package(Threads REQUIRED)

add_library(netsec netsec.cpp netsec.hpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC
  "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
  "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")
target_compile_features(netsec PUBLIC cxx_std_20)
target_link_libraries(netsec PUBLIC Threads::Threads)

if(MSVC)
  target_compile_options(netsec PRIVATE /W4 /permissive-)
else()
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
        INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
        NAMESPACE netsec::
        FILE netsecConfig.cmake
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

enable_testing()

add_executable(netsec_transition_tests tests/test_transition.cpp)
target_link_libraries(netsec_transition_tests PRIVATE netsec::netsec)
add_test(NAME netsec_transition_tests COMMAND netsec_transition_tests)
