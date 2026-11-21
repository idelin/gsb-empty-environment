cmake_minimum_required(VERSION 3.20)
project(rdp_auth_negotiation VERSION 1.0.0 LANGUAGES CXX)

add_library(rdp_auth STATIC
    src/channel.cpp
    src/crypto.cpp
    src/negotiator.cpp
    src/types.cpp
)
add_library(rdp::auth ALIAS rdp_auth)

target_compile_features(rdp_auth PUBLIC cxx_std_20)
set_target_properties(rdp_auth PROPERTIES CXX_EXTENSIONS OFF)
target_include_directories(rdp_auth PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
    $<INSTALL_INTERFACE:include>
)
if(MSVC)
    target_compile_options(rdp_auth PRIVATE /W4)
else()
    target_compile_options(rdp_auth PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(rdp_auth_tests tests/test_negotiation.cpp)
    target_link_libraries(rdp_auth_tests PRIVATE rdp::auth)
    add_test(NAME rdp_auth_tests COMMAND rdp_auth_tests)
endif()

include(GNUInstallDirs)
install(TARGETS rdp_auth EXPORT rdp_authTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
)
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(EXPORT rdp_authTargets
    NAMESPACE rdp::
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/rdp_auth
)
