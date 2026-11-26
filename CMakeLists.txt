cmake_minimum_required(VERSION 3.20)
project(ebpf_verifier VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(ebpf_verifier STATIC
    src/tnum.cpp
    src/scalar.cpp
    src/alu.cpp
    src/branch.cpp
    src/verifier.cpp
)
add_library(ebpf::verifier ALIAS ebpf_verifier)
set_target_properties(ebpf_verifier PROPERTIES EXPORT_NAME verifier)

target_include_directories(ebpf_verifier
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
    PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_compile_features(ebpf_verifier PUBLIC cxx_std_20)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
    target_compile_options(ebpf_verifier PRIVATE -Wall -Wextra)
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(ebpf_verifier_tests tests/test_domain.cpp)
    target_link_libraries(ebpf_verifier_tests PRIVATE ebpf::verifier)
    target_compile_features(ebpf_verifier_tests PRIVATE cxx_std_20)
    if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
        target_compile_options(ebpf_verifier_tests PRIVATE -Wall -Wextra)
    endif()
    add_test(NAME ebpf_verifier_tests COMMAND ebpf_verifier_tests)
endif()

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

configure_package_config_file(
    cmake/ebpf_verifierConfig.cmake.in
    ${CMAKE_CURRENT_BINARY_DIR}/ebpf_verifierConfig.cmake
    INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/ebpf_verifier
)
write_basic_package_version_file(
    ${CMAKE_CURRENT_BINARY_DIR}/ebpf_verifierConfigVersion.cmake
    VERSION ${PROJECT_VERSION}
    COMPATIBILITY SameMajorVersion
)

install(TARGETS ebpf_verifier
        EXPORT ebpf_verifierTargets
        ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR})
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(EXPORT ebpf_verifierTargets
        NAMESPACE ebpf::
        DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/ebpf_verifier)
install(FILES
        ${CMAKE_CURRENT_BINARY_DIR}/ebpf_verifierConfig.cmake
        ${CMAKE_CURRENT_BINARY_DIR}/ebpf_verifierConfigVersion.cmake
        DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/ebpf_verifier)
