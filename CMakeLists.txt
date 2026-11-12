cmake_minimum_required(VERSION 3.16)

# The macOS /usr/bin/ar and /usr/bin/ranlib shims invoke xcode-select, which
# fails in headless CI even when the CommandLineTools SDK is present. Prefer the
# real tools when they exist so a plain `cmake -S . -B build` works there.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    if(NOT DEFINED CMAKE_AR OR CMAKE_AR STREQUAL "" OR
       CMAKE_AR STREQUAL "/usr/bin/ar")
        set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
            CACHE FILEPATH "Archiver" FORCE)
    endif()
    if(NOT DEFINED CMAKE_RANLIB OR CMAKE_RANLIB STREQUAL "" OR
       CMAKE_RANLIB STREQUAL "/usr/bin/ranlib")
        set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
            CACHE FILEPATH "Ranlib" FORCE)
    endif()
endif()

project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

include(GNUInstallDirs)

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
        "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")
add_library(netsec::netsec ALIAS netsec)

enable_testing()

add_executable(netsec_tests tests/test_netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
        FILE netsecTargets.cmake
        NAMESPACE netsec::
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

file(WRITE "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
     "include(\"\${CMAKE_CURRENT_LIST_DIR}/netsecTargets.cmake\")\n")
install(FILES "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
