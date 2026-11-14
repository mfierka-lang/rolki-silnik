// Szablon stylu marki: jeden plik JSON + fonty (docelowo pobierane z panelu, żeby rolka z Maca i z Windowsa
// wyglądała identycznie). Opis od użytkownika zmienia treść rolki, nie te ustawienia.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type StylFont struct {
	Rodzina string  `json:"rodzina"` // nazwa rodziny zapisana w pliku fontu
	Plik    string  `json:"plik"`    // plik w katalogu szablonu (fonty/...)
	Rozmiar float64 `json:"rozmiar"` // wielkość w układzie 1080 x 1920
	Kolor   string  `json:"kolor"`   // #RRGGBB
	Kursywa bool    `json:"kursywa"`
	Gruby   bool    `json:"gruby"`
}

type StylKarta struct {
	Wlaczona bool    `json:"wlaczona"`
	Kolor    string  `json:"kolor"`
	Krycie   float64 `json:"krycie"` // 0-1
	Promien  float64 `json:"promien"`
	OdstepX  float64 `json:"odstepX"`
	OdstepY  float64 `json:"odstepY"`
	Dy       float64 `json:"dy"` // przesunięcie karty względem tekstu (wyrównanie optyczne)
}

// StylHak: nagłówek na początek rolki, dwie linie w blokach koloru.
type StylHak struct {
	Font    StylFont `json:"font"`
	Blok1   string   `json:"blok1"`
	Tekst1  string   `json:"tekst1"`
	Blok2   string   `json:"blok2"`
	Tekst2  string   `json:"tekst2"`
	Y       float64  `json:"y"` // góra pierwszego bloku jako ułamek wysokości
	Promien float64  `json:"promien"`
	OdstepX float64  `json:"odstepX"`
	OdstepY float64  `json:"odstepY"`
	Odstep  float64  `json:"odstep"` // przerwa między blokami
	// największy krój nagłówka: linie są powiększane tak, żeby najdłuższa zajęła ok. 4/5 szerokości kadru
	MaksRozmiar float64 `json:"maksRozmiar"`
}

type Styl struct {
	Nazwa   string `json:"nazwa"`
	Szer    int    `json:"szer"`
	Wys     int    `json:"wys"`
	Klatki  int    `json:"klatki"`
	Katalog string `json:"-"`

	Napisy struct {
		Tekst     StylFont  `json:"tekst"`
		Akcent    StylFont  `json:"akcent"`
		Karta     StylKarta `json:"karta"`
		Y         float64   `json:"y"`         // środek napisu jako ułamek wysokości kadru
		MaksSlow  int       `json:"maksSlow"`  // słów w jednej porcji
		MaksSzer  float64   `json:"maksSzer"`  // największa szerokość tekstu w pikselach
		Wersaliki bool      `json:"wersaliki"` // WIELKIE LITERY
		WejscieMs int       `json:"wejscieMs"` // czas wejścia porcji
		SkalaOd   float64   `json:"skalaOd"`   // skala początkowa wejścia (np. 92)
		Rodzaj    string    `json:"rodzaj"`    // hasła na ujęciach: "karta" albo "bloki"
		Wejscie   string    `json:"wejscie"`   // "druk" (warstwami od dołu, z linią dyszy) albo "wjazd"
		// hasła na ujęciach mają 2-3 słowa, więc mogą być większe niż napisy mowy (czytane na telefonie z ręki)
		SkalaHasel float64 `json:"skalaHasel"`
	} `json:"napisy"`

	Hak StylHak `json:"hak"`

	Kadr struct {
		Zblizenia []float64 `json:"zblizenia"` // kolejne skale kadru zmieniane na cięciach
		Y         float64   `json:"y"`         // 0 = góra, 0.5 = środek: którą część obrazu trzymać przy zbliżeniu
	} `json:"kadr"`

	Ciecie struct {
		PauzaWFrazie float64 `json:"pauzaWFrazie"` // krótsze przerwy zostają jako naturalny oddech
		MaksPauza    float64 `json:"maksPauza"`    // najdłuższa przerwa zostawiona między zdaniami
		ZapasPrzed   float64 `json:"zapasPrzed"`
		ZapasPo      float64 `json:"zapasPo"`
	} `json:"ciecie"`

	Dzwiek struct {
		GlosnoscLUFS   float64 `json:"glosnoscLUFS"`
		MuzykaPoziom   float64 `json:"muzykaPoziom"` // głośność muzyki względem pełnej (0-1)
		Odszumianie    float64 `json:"odszumianie"`  // dB redukcji szumu tła
		MuzykaWyciszMs int     `json:"muzykaWyciszMs"`
	} `json:"dzwiek"`

	Slownik    []string `json:"slownik"`    // rdzenie słów-kluczy wyróżnianych w napisach
	Podpowiedz string   `json:"podpowiedz"` // nazwy własne dla transkrypcji

	// Teksty marki. Hasła trafiają na ujęcia, na których widać daną rzecz (rdzenie w „gdy” są porównywane
	// z opisem ujęcia), wezwanie na ostatnie ujęcie rolki produktowej, a „post” to opis i hashtagi do wklejenia.
	Hasla    []StylHaslo `json:"hasla"`
	Wezwanie string      `json:"wezwanie"`
	Post     struct {
		Kontekst       string   `json:"kontekst"` // dwa zdania o marce dla modelu piszącego
		Wezwanie       string   `json:"wezwanie"`
		Hashtagi       []string `json:"hashtagi"`
		HashtagiTematy []struct {
			Gdy  []string `json:"gdy"`
			Tagi []string `json:"tagi"`
		} `json:"hashtagiTematy"`
	} `json:"post"`
	// katalog z fontami i muzyką, gdy plik stylu leży gdzie indziej (kopia stylu z poprawkami jednego zlecenia)
	Zasoby string `json:"zasoby"`
}

type StylHaslo struct {
	Gdy   []string `json:"gdy"`
	Napis string   `json:"napis"`
}

func wczytajStyl(plik string) (*Styl, error) {
	b, err := os.ReadFile(plik)
	if err != nil {
		return nil, fmt.Errorf("brak szablonu stylu: %v", err)
	}
	s := &Styl{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("błąd w szablonie stylu %s: %v", plik, err)
	}
	s.Katalog = filepath.Dir(plik)
	if s.Zasoby != "" {
		s.Katalog = s.Zasoby
	}
	if s.Szer == 0 {
		s.Szer, s.Wys = 1080, 1920
	}
	if s.Klatki == 0 {
		s.Klatki = 30
	}
	if s.Napisy.MaksSlow == 0 {
		s.Napisy.MaksSlow = 3
	}
	if s.Napisy.MaksSzer == 0 {
		s.Napisy.MaksSzer = float64(s.Szer) * 0.82
	}
	if s.Napisy.Y == 0 {
		s.Napisy.Y = 0.66
	}
	if s.Napisy.SkalaOd == 0 {
		s.Napisy.SkalaOd = 100
	}
	if s.Hak.Font.Plik == "" {
		s.Hak = StylHak{Font: s.Napisy.Tekst, Blok1: s.Napisy.Tekst.Kolor, Tekst1: s.Napisy.Karta.Kolor, Blok2: s.Napisy.Akcent.Kolor, Tekst2: s.Napisy.Karta.Kolor, Y: 0.16, Promien: 14, OdstepX: 30, OdstepY: 16, Odstep: 10}
		s.Hak.Font.Rozmiar = s.Napisy.Tekst.Rozmiar * 1.08
	}
	if s.Hak.MaksRozmiar == 0 {
		s.Hak.MaksRozmiar = 136
	}
	if s.Napisy.Rodzaj == "" {
		s.Napisy.Rodzaj = "karta"
	}
	if s.Napisy.Wejscie == "" {
		s.Napisy.Wejscie = "druk"
	}
	if s.Napisy.SkalaHasel == 0 {
		s.Napisy.SkalaHasel = 1.3
	}
	if len(s.Kadr.Zblizenia) == 0 {
		s.Kadr.Zblizenia = []float64{1}
	}
	if s.Kadr.Y == 0 {
		s.Kadr.Y = 0.5
	}
	if s.Ciecie.PauzaWFrazie == 0 {
		s.Ciecie.PauzaWFrazie = 0.30
	}
	if s.Ciecie.MaksPauza == 0 {
		s.Ciecie.MaksPauza = 0.28
	}
	if s.Ciecie.ZapasPrzed == 0 {
		s.Ciecie.ZapasPrzed = 0.08
	}
	if s.Ciecie.ZapasPo == 0 {
		s.Ciecie.ZapasPo = 0.15
	}
	if s.Dzwiek.GlosnoscLUFS == 0 {
		s.Dzwiek.GlosnoscLUFS = -14
	}
	return s, nil
}
