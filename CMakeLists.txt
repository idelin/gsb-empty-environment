cmake_minimum_required(VERSION 3.16)
project(bmff_audit LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some macOS CI images expose CommandLineTools without a selected Xcode.
# Prefer the CommandLineTools archiver when the default /usr/bin/ar is a stub.
if(APPLE AND CMAKE_AR STREQUAL "/usr/bin/ar")
    find_program(BMFF_CLT_AR
        NAMES ar
        PATHS /Library/Developer/CommandLineTools/usr/bin
        NO_DEFAULT_PATH
    )
    find_program(BMFF_CLT_RANLIB
        NAMES ranlib
        PATHS /Library/Developer/CommandLineTools/usr/bin
        NO_DEFAULT_PATH
    )
    if(BMFF_CLT_AR AND BMFF_CLT_RANLIB)
        set(CMAKE_AR "${BMFF_CLT_AR}" CACHE FILEPATH
            "Archiver selected from CommandLineTools" FORCE)
        set(CMAKE_RANLIB "${BMFF_CLT_RANLIB}" CACHE FILEPATH
            "Ranlib selected from CommandLineTools" FORCE)
    endif()
endif()

add_library(bmff_audit STATIC src/bmff_audit.cpp)
add_library(bmff::audit ALIAS bmff_audit)
set_target_properties(bmff_audit PROPERTIES POSITION_INDEPENDENT_CODE ON)
target_compile_features(bmff_audit PUBLIC cxx_std_17)
target_include_directories(bmff_audit
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
)

if(MSVC)
    target_compile_options(bmff_audit PRIVATE /W4 /WX)
else()
    target_compile_options(bmff_audit PRIVATE
        -Wall -Wextra -Wconversion -Werror
    )
endif()

add_executable(bmff_audit_cli tools/bmff_audit_cli.cpp)
target_link_libraries(bmff_audit_cli PRIVATE bmff_audit)

if(MSVC)
    target_compile_options(bmff_audit_cli PRIVATE /W4 /WX)
else()
    target_compile_options(bmff_audit_cli PRIVATE
        -Wall -Wextra -Wconversion -Werror
    )
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(bmff_audit_tests tests/bmff_audit_tests.cpp)
    target_link_libraries(bmff_audit_tests PRIVATE bmff_audit)
    if(MSVC)
        target_compile_options(bmff_audit_tests PRIVATE /W4 /WX)
    else()
        target_compile_options(bmff_audit_tests PRIVATE
            -Wall -Wextra -Wconversion -Werror
        )
    endif()
    add_test(NAME bmff_audit_tests COMMAND bmff_audit_tests)
endif()

install(TARGETS bmff_audit
    ARCHIVE DESTINATION lib
    LIBRARY DESTINATION lib
    RUNTIME DESTINATION bin
)
install(FILES include/bmff/audit.hpp DESTINATION include/bmff)
