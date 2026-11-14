//go:build !windows

package main

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

func ladujBiblioteke(plik string) (uintptr, error) {
	return purego.Dlopen(plik, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func symbolBiblioteki(uchwyt uintptr, nazwa string) (uintptr, error) {
	return purego.Dlsym(uchwyt, nazwa)
}

// sciezkaDlaBiblioteki: poza Windows ONNX Runtime przyjmuje ścieżki jako zwykły tekst zakończony zerem (UTF-8).
func sciezkaDlaBiblioteki(plik string) unsafe.Pointer {
	b := append([]byte(plik), 0)
	return unsafe.Pointer(&b[0])
}
