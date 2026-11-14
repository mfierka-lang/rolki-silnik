// Minimalne połączenie z biblioteką ONNX Runtime (licencja MIT) bez cgo: wywołania przez purego.
// Służy do jednego zadania: model wycinający sylwetkę osoby z kadru (napis „za osobą”). Biblioteka i model są
// składnikami dodatkowymi; bez nich silnik działa jak dotąd, tylko bez tego efektu.
//
// Tablica funkcji OrtApi jest stała między wersjami (nowe funkcje dochodzą na końcu), więc numery poniżej są
// ważne dla każdej wersji biblioteki od 1.14. Źródło numerów: include/onnxruntime/core/session/onnxruntime_c_api.h.
package main

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	ortGetErrorMessage                = 2
	ortCreateEnv                      = 3
	ortCreateSession                  = 7
	ortRun                            = 9
	ortCreateSessionOptions           = 10
	ortSetGraphOptimizationLevel      = 23
	ortSetIntraOpNumThreads           = 24
	ortSessionGetInputName            = 36
	ortSessionGetOutputName           = 37
	ortCreateTensorWithDataAsOrtValue = 49
	ortGetTensorMutableData           = 51
	ortGetDimensionsCount             = 61
	ortGetDimensions                  = 62
	ortGetTensorTypeAndShape          = 65
	ortCreateCpuMemoryInfo            = 69
	ortAllocatorFree                  = 76
	ortGetAllocatorWithDefaultOptions = 78
	ortReleaseStatus                  = 93
	ortReleaseValue                   = 96
	ortReleaseTensorTypeAndShapeInfo  = 99
)

// SesjaORT: załadowany model gotowy do liczenia (jedno wejście, jedno wyjście, liczby float32).
type SesjaORT struct {
	api      uintptr
	env      uintptr
	opcje    uintptr
	sesja    uintptr
	pamiec   uintptr
	nazwaWe  []byte
	nazwaWy  []byte
	wskaznik [6]uintptr // miejsca na wyniki zwracane przez bibliotekę (na stercie, razem z sesją)
}

// wsk: adres pamięci należącej do biblioteki (nie do Go) jako wskaźnik.
func wsk(adres uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&adres)) }

func tekstC(adres uintptr) string {
	if adres == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); i < 4096; i++ {
		z := *(*byte)(unsafe.Add(wsk(adres), i))
		if z == 0 {
			break
		}
		b = append(b, z)
	}
	return string(b)
}

func (o *SesjaORT) funkcja(nr int) uintptr {
	return *(*uintptr)(unsafe.Add(wsk(o.api), uintptr(nr)*unsafe.Sizeof(uintptr(0))))
}

// wolaj: funkcja biblioteki zwracająca status (0 = dobrze). Argumenty-wskaźniki zostają na stercie do końca wywołania.
//
//go:uintptrescapes
func (o *SesjaORT) wolaj(nr int, arg ...uintptr) error {
	status, _, _ := purego.SyscallN(o.funkcja(nr), arg...)
	if status == 0 {
		return nil
	}
	opis, _, _ := purego.SyscallN(o.funkcja(ortGetErrorMessage), status)
	tekst := tekstC(opis)
	purego.SyscallN(o.funkcja(ortReleaseStatus), status)
	return errors.New("ONNX Runtime: " + tekst)
}

// otworzORT: ładuje bibliotekę i model. watki = liczba wątków procesora na jedno liczenie.
func otworzORT(biblioteka, model string, watki int) (*SesjaORT, error) {
	uchwyt, err := ladujBiblioteke(biblioteka)
	if err != nil {
		return nil, fmt.Errorf("nie udało się załadować %s: %v", biblioteka, err)
	}
	podstawa, err := symbolBiblioteki(uchwyt, "OrtGetApiBase")
	if err != nil {
		return nil, err
	}
	baza, _, _ := purego.SyscallN(podstawa)
	if baza == 0 {
		return nil, errors.New("ONNX Runtime: brak tablicy funkcji")
	}
	o := &SesjaORT{}
	o.api, _, _ = purego.SyscallN(*(*uintptr)(wsk(baza)), 14) // GetApi(14): funkcje dostępne od wersji 1.14
	if o.api == 0 {
		return nil, errors.New("ONNX Runtime: biblioteka jest za stara")
	}
	nazwa := []byte("zm\x00")
	if err := o.wolaj(ortCreateEnv, 3, uintptr(unsafe.Pointer(&nazwa[0])), uintptr(unsafe.Pointer(&o.env))); err != nil {
		return nil, err
	}
	if err := o.wolaj(ortCreateSessionOptions, uintptr(unsafe.Pointer(&o.opcje))); err != nil {
		return nil, err
	}
	if watki > 0 {
		o.wolaj(ortSetIntraOpNumThreads, o.opcje, uintptr(watki))
	}
	o.wolaj(ortSetGraphOptimizationLevel, o.opcje, 99)
	sciezka := sciezkaDlaBiblioteki(model)
	if err := o.wolaj(ortCreateSession, o.env, uintptr(sciezka), o.opcje, uintptr(unsafe.Pointer(&o.sesja))); err != nil {
		return nil, err
	}
	runtime.KeepAlive(sciezka)
	if err := o.wolaj(ortCreateCpuMemoryInfo, 1, 0, uintptr(unsafe.Pointer(&o.pamiec))); err != nil {
		return nil, err
	}
	// nazwy wejścia i wyjścia modelu
	var alokator uintptr
	if err := o.wolaj(ortGetAllocatorWithDefaultOptions, uintptr(unsafe.Pointer(&alokator))); err != nil {
		return nil, err
	}
	for i, nr := range []int{ortSessionGetInputName, ortSessionGetOutputName} {
		var adres uintptr
		if err := o.wolaj(nr, o.sesja, 0, alokator, uintptr(unsafe.Pointer(&adres))); err != nil {
			return nil, err
		}
		tekst := append([]byte(tekstC(adres)), 0)
		purego.SyscallN(o.funkcja(ortAllocatorFree), alokator, adres)
		if i == 0 {
			o.nazwaWe = tekst
		} else {
			o.nazwaWy = tekst
		}
	}
	return o, nil
}

// Licz: jedno przejście modelu. dane mają kształt ksztalt (np. 1,3,wys,szer); wynik to kopia wyjścia i jego kształt.
func (o *SesjaORT) Licz(dane []float32, ksztalt []int64) ([]float32, []int64, error) {
	var tensor uintptr
	if err := o.wolaj(ortCreateTensorWithDataAsOrtValue, o.pamiec, uintptr(unsafe.Pointer(&dane[0])), uintptr(len(dane)*4),
		uintptr(unsafe.Pointer(&ksztalt[0])), uintptr(len(ksztalt)), 1, uintptr(unsafe.Pointer(&tensor))); err != nil {
		return nil, nil, err
	}
	defer func() { purego.SyscallN(o.funkcja(ortReleaseValue), tensor) }()
	nazwyWe := []*byte{&o.nazwaWe[0]}
	nazwyWy := []*byte{&o.nazwaWy[0]}
	wartosciWe := []uintptr{tensor}
	wartosciWy := []uintptr{0}
	if err := o.wolaj(ortRun, o.sesja, 0, uintptr(unsafe.Pointer(&nazwyWe[0])), uintptr(unsafe.Pointer(&wartosciWe[0])), 1,
		uintptr(unsafe.Pointer(&nazwyWy[0])), 1, uintptr(unsafe.Pointer(&wartosciWy[0]))); err != nil {
		return nil, nil, err
	}
	wyjscie := wartosciWy[0]
	defer func() { purego.SyscallN(o.funkcja(ortReleaseValue), wyjscie) }()
	var opis uintptr
	if err := o.wolaj(ortGetTensorTypeAndShape, wyjscie, uintptr(unsafe.Pointer(&opis))); err != nil {
		return nil, nil, err
	}
	defer func() { purego.SyscallN(o.funkcja(ortReleaseTensorTypeAndShapeInfo), opis) }()
	var ile uintptr
	if err := o.wolaj(ortGetDimensionsCount, opis, uintptr(unsafe.Pointer(&ile))); err != nil || ile == 0 || ile > 8 {
		return nil, nil, fmt.Errorf("ONNX Runtime: nieoczekiwany kształt wyniku")
	}
	wymiary := make([]int64, ile)
	if err := o.wolaj(ortGetDimensions, opis, uintptr(unsafe.Pointer(&wymiary[0])), ile); err != nil {
		return nil, nil, err
	}
	razem := 1
	for _, w := range wymiary {
		if w <= 0 || w > 1<<16 {
			return nil, nil, fmt.Errorf("ONNX Runtime: nieoczekiwany kształt wyniku %v", wymiary)
		}
		razem *= int(w)
	}
	var adres uintptr
	if err := o.wolaj(ortGetTensorMutableData, wyjscie, uintptr(unsafe.Pointer(&adres))); err != nil {
		return nil, nil, err
	}
	wynik := make([]float32, razem)
	copy(wynik, unsafe.Slice((*float32)(wsk(adres)), razem))
	runtime.KeepAlive(dane)
	runtime.KeepAlive(ksztalt)
	runtime.KeepAlive(nazwyWe)
	runtime.KeepAlive(nazwyWy)
	return wynik, wymiary, nil
}
