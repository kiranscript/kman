# Reads a generic password through Security.framework from a binary that is
# NOT /usr/bin/security (python3). argv: service, allow|deny. Never prints the secret.
import ctypes, sys
sec = ctypes.cdll.LoadLibrary("/System/Library/Frameworks/Security.framework/Security")
sec.SecKeychainSetUserInteractionAllowed.argtypes = [ctypes.c_bool]
sec.SecKeychainSetUserInteractionAllowed(sys.argv[2] == "allow")
f = sec.SecKeychainFindGenericPassword
f.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_char_p, ctypes.c_uint32, ctypes.c_char_p,
              ctypes.POINTER(ctypes.c_uint32), ctypes.POINTER(ctypes.c_void_p), ctypes.c_void_p]
f.restype = ctypes.c_int32
svc, acct = sys.argv[1].encode(), b"kman"
n, p = ctypes.c_uint32(), ctypes.c_void_p()
st = f(None, len(svc), svc, len(acct), acct, ctypes.byref(n), ctypes.byref(p), None)
if st == 0:
    v = ctypes.string_at(p, n.value).decode()
    print("SILENT_MATCH" if v == open("/tmp/kman-expected").read() else "MISMATCH")
else:
    print(f"DENIED status={st}")
