// Jakość pliku: HD (1080p), 2K (1440p) albo 4K (2160p). Układ napisów i plansz zostaje w siatce stylu
// (1080 x 1920 albo 1920 x 1080), a obraz jest liczony w pikselach wybranej jakości: libass sam przelicza napisy.
// Jakość nie może być wyższa niż to, co jest w nagraniach: powiększanie obrazu nie dodaje szczegółów, tylko waży.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Jakosc struct {
	Klucz string
	Nazwa string
	Skala float64 // ile razy obraz jest większy od siatki stylu
}

var jakosci = []Jakosc{{"hd", "HD", 1}, {"2k", "2K", 4.0 / 3}, {"4k", "4K", 2}}

func jakoscZKlucza(k string) Jakosc {
	k = strings.ToLower(strings.TrimSpace(k))
	for _, j := range jakosci {
		if j.Klucz == k {
			return j
		}
	}
	return jakosci[0]
}

var (
	reJakosc4K = regexp.MustCompile(`(^|[^\p{L}\d])(4\s?k|uhd|ultra\s?hd|2160p?)($|[^\p{L}\d])`)
	reJakosc2K = regexp.MustCompile(`(^|[^\p{L}\d])(2\s?k|qhd|1440p?)($|[^\p{L}\d])`)
	reJakoscHD = regexp.MustCompile(`(^|[^\p{L}\d])(full\s?hd|fhd|1080p?|hd)($|[^\p{L}\d])`)
)

// jakoscZTekstu: "4k", "2k" albo "hd", gdy opis lub uwaga mówi o jakości; inaczej "". Tekst małymi literami.
func jakoscZTekstu(male string) string {
	switch {
	case reJakosc4K.MatchString(male):
		return "4k"
	case reJakosc2K.MatchString(male):
		return "2k"
	case reJakoscHD.MatchString(male):
		return "hd"
	}
	return ""
}

// wymiarObrazu: bok siatki przeliczony na piksele obrazu (zawsze parzysty).
func wymiarObrazu(bok int, skala float64) int {
	if skala <= 0 {
		skala = 1
	}
	return 2 * int(math.Round(float64(bok)*skala/2))
}

// skalaNagrania: ile razy siatka filmu mieści się w największym wycinku nagrania o proporcjach filmu.
// Poziome 4K w filmie poziomym: 2. To samo nagranie przycięte do pionu: 1,125 (zostaje pas 1215 x 2160).
func skalaNagrania(k *Klip, szer, wys int) float64 {
	kh := float64(k.H)
	if kw := kh * float64(szer) / float64(wys); kw > float64(k.W) {
		kh = float64(k.W) * float64(wys) / float64(szer)
	}
	return kh / float64(wys)
}

// największe powiększenie obrazu ponad to, co jest w nagraniu (pion 2K z poziomego 4K to 18%: jeszcze bez straty ostrości)
const zapasJakosci = 1.2

// dobierzJakosc: wybrana jakość ograniczona do tego, co jest w nagraniach (mediana ważona długością nagrań).
// Zwraca jakość, zdanie do raportu ("" przy zwykłym HD) i to, czy nagrania ograniczyły jakość (zdanie trafia wtedy
// także obok przycisków jakości na stronie).
func dobierzJakosc(wybrana Jakosc, klipy []*Klip, szer, wys int) (Jakosc, string, bool) {
	if wybrana.Skala <= 1 || len(klipy) == 0 {
		return jakosci[0], "", false
	}
	// Składanie filmu 4K trzyma w pamięci kilka GB obrazu (ok. 4 GB przy 17 ujęciach, więcej przy dłuższym filmie):
	// na komputerze z 8 GB to za dużo, więc najwyższą jakością jest tam 2K. Zmienna wyłącza limit na czas prób.
	chciana, malaPamiec := wybrana, false
	if ram, ok := pamiecRAM(); ok && ram < 11<<30 && wybrana.Skala > jakosci[1].Skala+0.01 && os.Getenv("ZM_JAKOSC_BEZ_LIMITU") == "" {
		wybrana, malaPamiec = jakosci[1], true
	}
	type pozycja struct{ skala, czas float64 }
	var poz []pozycja
	razem := 0.0
	for _, k := range klipy {
		poz = append(poz, pozycja{skalaNagrania(k, szer, wys), math.Max(0.1, k.Czas)})
		razem += math.Max(0.1, k.Czas)
	}
	sort.Slice(poz, func(a, b int) bool { return poz[a].skala < poz[b].skala })
	material, suma := poz[len(poz)-1].skala, 0.0
	for _, p := range poz {
		if suma += p.czas; suma >= razem/2 {
			material = p.skala
			break
		}
	}
	wynik := jakosci[0]
	for _, j := range jakosci {
		if j.Skala <= wybrana.Skala+1e-9 && j.Skala <= material*zapasJakosci+1e-9 {
			wynik = j
		}
	}
	mw, mh := int(math.Round(material*float64(szer))), int(math.Round(material*float64(wys)))
	ow, oh := wymiarObrazu(szer, wynik.Skala), wymiarObrazu(wys, wynik.Skala)
	switch {
	case malaPamiec && wynik.Klucz == wybrana.Klucz && material*zapasJakosci >= chciana.Skala-1e-9: // nagrania by wystarczyły, zabrakło pamięci
		return wynik, fmt.Sprintf("Wybrana jakość %s, ale ten komputer ma za mało pamięci na składanie filmu w tej jakości (potrzeba ok. 16 GB). Film jest w %s (%d x %d).",
			chciana.Nazwa, wynik.Nazwa, ow, oh), true
	case wynik.Klucz != chciana.Klucz:
		return wynik, fmt.Sprintf("Wybrana jakość %s, ale nagrania po przycięciu do kadru filmu mają ok. %d x %d pikseli. Film jest w %s (%d x %d): większy plik nie miałby więcej szczegółów.",
			chciana.Nazwa, mw, mh, wynik.Nazwa, ow, oh), true
	case material < wynik.Skala*0.97:
		return wynik, fmt.Sprintf("Jakość %s (%d x %d). Nagrania po przycięciu do kadru filmu mają ok. %d x %d pikseli, więc obraz jest lekko powiększony.",
			wynik.Nazwa, ow, oh, mw, mh), true
	}
	return wynik, fmt.Sprintf("Jakość %s (%d x %d).", wynik.Nazwa, ow, oh), false
}

// dopisekJakosci: końcówka nazwy pobieranego pliku ("-2K", "-4K"; nic dla HD), z wyniku zapisanego w katalogu zlecenia.
func dopisekJakosci(katalog string) string {
	var w Wynik
	if b, err := os.ReadFile(filepath.Join(katalog, "wynik.json")); err != nil || json.Unmarshal(b, &w) != nil {
		return ""
	}
	if j := jakoscZKlucza(w.Jakosc); j.Skala > 1 {
		return "-" + j.Nazwa
	}
	return ""
}
