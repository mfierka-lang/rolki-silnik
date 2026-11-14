//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func ladujBiblioteke(plik string) (uintptr, error) {
	h, err := syscall.LoadLibrary(plik)
	return uintptr(h), err
}

func symbolBiblioteki(uchwyt uintptr, nazwa string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(uchwyt), nazwa)
}

// sciezkaDlaBiblioteki: ONNX Runtime w Windows przyjmuje ścieżki jako szerokie znaki (UTF-16).
func sciezkaDlaBiblioteki(plik string) unsafe.Pointer {
	p, err := syscall.UTF16PtrFromString(plik)
	if err != nil {
		return nil
	}
	return unsafe.Pointer(p)
}
