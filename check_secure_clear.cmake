# Fails the test if the -O3 assembly contains no guaranteed-clear primitive.
file(READ "${ASM_FILE}" assembly)

set(secure_primitives "memset_s" "SecureZeroMemory" "explicit_bzero")
set(found FALSE)
foreach (primitive IN LISTS secure_primitives)
    if (assembly MATCHES "${primitive}")
        message(STATUS "secure-clear primitive retained under -O3: ${primitive}")
        set(found TRUE)
    endif ()
endforeach ()

if (NOT found)
    message(FATAL_ERROR "no guaranteed-clear primitive found in ${ASM_FILE}")
endif ()
