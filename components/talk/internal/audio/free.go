package audio

// #include <stdlib.h>
import "C"
import "unsafe"

func freeDeviceID(p unsafe.Pointer) { C.free(p) }
