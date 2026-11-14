cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

# Some command-line-only macOS images expose xcode-select shims at
# /usr/bin/ar.  Prefer the real CommandLineTools archiver when present so the
# library can be built without a full Xcode installation.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    if(NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar")
        set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
            CACHE FILEPATH "Archiver" FORCE)
    endif()
    if(NOT CMAKE_RANLIB OR CMAKE_RANLIB STREQUAL "/usr/bin/ranlib")
        set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
            CACHE FILEPATH "Ranlib" FORCE)
    endif()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)

include(GNUInstallDirs)
install(TARGETS netsec
    ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
    LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
    RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")

include(CTest)
if(BUILD_TESTING)
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec)
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif()
