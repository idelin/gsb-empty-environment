import ctypes
import ctypes.util

for name in ["krb5", "gssapi", "heimbase", "hcrypto"]:
    print(name, ctypes.util.find_library(name))
try:
    lib = ctypes.CDLL("/usr/lib/libkrb5.dylib")
    for sym in [
        "krb5_domain_x500_decode",
        "krb5_domain_x500_encode",
        "krb5_init_context",
        "krb5_free_context",
        "krb5_free_data",
        "krb5_free_data_contents",
    ]:
        try:
            print(sym, getattr(lib, sym))
        except Exception as e:
            print(sym, "MISSING", e)
except Exception as e:
    print("load fail", e)
