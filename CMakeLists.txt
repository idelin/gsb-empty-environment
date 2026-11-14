cmake_minimum_required(VERSION 3.16)

# The sandbox's /usr/bin/ar is an Xcode stub.  Prefer the real command-line
# tools archiver when it is present so a plain CMake build works.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
  set(CMAKE_AR
      "/Library/Developer/CommandLineTools/usr/bin/ar"
      CACHE FILEPATH "static library archiver" FORCE)
  set(CMAKE_RANLIB
      "/Library/Developer/CommandLineTools/usr/bin/ranlib"
      CACHE FILEPATH "static library indexer" FORCE)
endif()

project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
include(GNUInstallDirs)
add_library(netsec netsec.cpp crypto.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>)
target_compile_features(netsec PUBLIC cxx_std_20)
enable_testing()

add_executable(bootchain_tests tests/test_bootchain.cpp)
target_link_libraries(bootchain_tests PRIVATE netsec::netsec)
add_test(NAME bootchain_tests COMMAND bootchain_tests)

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
        FILE netsecTargets.cmake
        NAMESPACE netsec::
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
