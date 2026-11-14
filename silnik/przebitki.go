// Przebitki w filmie mówionym: gdy obok nagrania z osobą mówiącą do kamery są inne nagrania (produkt, praca,
// miejsce), ich najlepsze fragmenty przykrywają obraz na 2-3 sekundy, a głos biegnie dalej. Przebitka najchętniej
// zaczyna się na cięciu po wyciętej pauzie (zasłania przeskok) albo w chwili, gdy pada słowo pasujące do tego,
// co na niej widać. Początek z nagłówkiem i końcówka z wezwaniem zostają bez przebitek.
package main

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

type Przebitka struct {
	Plik   string  `json:"plik"` // przygotowane ujęcie w kadrze filmu
	Od     float64 `json:"od"`   // miejsce na osi gotowego filmu
	Do     float64 `json:"do"`
	Zrodlo string  `json:"zrodlo"`
	Slowo  string  `json:"slowo,omitempty"` // słowo z wypowiedzi, do którego przebitka została dopasowana
}

const (
	przebitkaCzas   = 2.4 // sekundy
	przebitkaOdstep = 4.4 // najmniej tyle mówiącej osoby między przebitkami
)

// rdzenSlowa: początek słowa bez polskich znaków (magnesy, magnesów, magnesie -> magne).
func rdzenSlowa(s string) string {
	t := []rune(strings.TrimSpace(normalizujZdanie(s)))
	if len(t) < 4 {
		return ""
	}
	if len(t) > 5 {
		t = t[:5]
	}
	return string(t)
}

// przygotujPrzebitki: wybiera fragmenty z nagrań dodatkowych, rozkłada je na osi filmu i przygotowuje pliki.
func przygotujPrzebitki(n *Narzedzia, st *Styl, praca string, pliki []string, plan *PlanMowy, hakKoniec float64, postep func(string)) []Przebitka {
	poczatek := math.Max(hakKoniec+0.4, 3.0)
	koniec := plan.Czas - 2.6
	if len(pliki) == 0 || koniec-poczatek < przebitkaCzas {
		return nil
	}
	ileMiejsc := int((koniec-poczatek+przebitkaOdstep)/(przebitkaCzas+przebitkaOdstep) + 1e-6)
	if ileMiejsc < 1 {
		return nil
	}

	// 1. kandydaci: najlepsze fragmenty każdego nagrania (obejrzane już wcześniej: wynik skanu jest w pamięci silnika)
	var klipy []*Klip
	var okna []Okno
	for _, p := range pliki {
		k, err := sonduj(n, p)
		if err != nil {
			continue
		}
		sk, err := skanuj(n, k)
		if err != nil {
			continue
		}
		ile := 1
		if k.Czas > 20 {
			ile = 2
		}
		if k.Czas > 60 {
			ile = 3
		}
		okna = append(okna, oknaKlipu(len(klipy), k, sk, przebitkaCzas, ile, 6)...)
		klipy = append(klipy, k)
	}
	sort.SliceStable(okna, func(a, b int) bool { return okna[a].Ocena < okna[b].Ocena })
	var wybrane []Okno
	zKlipu := map[int]int{}
	for runda := 1; runda <= 3 && len(wybrane) < ileMiejsc; runda++ {
		for _, o := range okna {
			if len(wybrane) >= ileMiejsc {
				break
			}
			if o.Drgania > 3.0 || o.Ocena > 1.2 || zKlipu[o.Klip] != runda-1 {
				continue
			}
			podobne := false
			for _, w := range wybrane {
				if (w.Klip == o.Klip && math.Abs(w.Od-o.Od) < 1) || podobienstwo(o.Mini, w.Mini, o.MiniW) > 0.85 {
					podobne = true
					break
				}
			}
			if !podobne {
				zKlipu[o.Klip]++
				wybrane = append(wybrane, o)
			}
		}
	}
	if len(wybrane) == 0 {
		return nil
	}

	// 2. co widać na wybranych fragmentach (opisy z pamięci; model oglądający startuje tylko dla brakujących)
	var brak []int
	for i := range wybrane {
		if op, _ := opiszUjecie(nil, n, klipy[wybrane[i].Klip], wybrane[i], st); op != nil {
			wybrane[i].Opis = op
		} else {
			brak = append(brak, i)
		}
	}
	if len(brak) > 0 {
		postep("Sprawdzam, co widać na przebitkach")
		if r, err := uruchomRozum(n, praca, "obraz"); err == nil && r != nil {
			for _, i := range brak {
				if op, _ := opiszUjecie(r, n, klipy[wybrane[i].Klip], wybrane[i], st); op != nil {
					wybrane[i].Opis = op
				}
			}
			r.Zamknij()
		}
	}

	// 3. miejsca na osi filmu
	type miejsce struct {
		od    float64
		slowo string
	}
	zajete := []miejsce{}
	wolne := func(t float64) bool {
		if t < poczatek || t+przebitkaCzas > koniec {
			return false
		}
		for _, m := range zajete {
			if math.Abs(m.od-t) < przebitkaCzas+przebitkaOdstep {
				return false
			}
		}
		return true
	}
	przydzial := make([]miejsce, len(wybrane))
	ma := make([]bool, len(wybrane))
	// a) słowo z wypowiedzi pasujące do tego, co widać na przebitce
	for i, o := range wybrane {
		if o.Opis == nil {
			continue
		}
		rdzenie := map[string]bool{}
		for _, s := range o.Opis.Slowa {
			if r := rdzenSlowa(s); r != "" {
				rdzenie[r] = true
			}
		}
		for _, s := range plan.Slowa {
			if r := rdzenSlowa(s.Tekst); r != "" && rdzenie[r] && wolne(s.Od-0.15) {
				przydzial[i], ma[i] = miejsce{s.Od - 0.15, strings.Trim(s.Tekst, ".,!?;: ")}, true
				zajete = append(zajete, przydzial[i])
				break
			}
		}
	}
	// b) pozostałe: równo rozłożone, dosunięte do najbliższego cięcia po wyciętej pauzie (przebitka je zasłania)
	var ciecia []float64
	for i, o := range plan.Odcinki {
		if i > 0 {
			ciecia = append(ciecia, o.WyOd)
		}
	}
	for i := range wybrane {
		if ma[i] {
			continue
		}
		for t := poczatek; t+przebitkaCzas <= koniec+1e-6; t += 0.5 {
			if !wolne(t) {
				continue
			}
			naj := t
			for _, c := range ciecia {
				if kand := c - 0.25; math.Abs(kand-t) < 1.6 && wolne(kand) {
					naj = kand
					break
				}
			}
			przydzial[i], ma[i] = miejsce{naj, ""}, true
			zajete = append(zajete, przydzial[i])
			break
		}
	}

	// 4. przygotowanie plików (kadr filmu, ta sama droga co ujęcia w filmie z ujęć)
	kl := float64(st.Klatki)
	var out []Przebitka
	for i, o := range wybrane {
		if !ma[i] {
			continue
		}
		postep(fmt.Sprintf("Przygotowuję przebitkę %d z %d", len(out)+1, len(zajete)))
		nazwa := fmt.Sprintf("przebitka%02d", i) // przygotujUjecie samo dodaje .mkv
		if err := przygotujUjecie(n, klipy[o.Klip], o, st, praca, nazwa, false); err != nil {
			continue
		}
		od := math.Round(przydzial[i].od*kl) / kl
		out = append(out, Przebitka{Plik: filepath.Join(praca, nazwa+".mkv"), Od: od, Do: od + math.Floor(przebitkaCzas*kl)/kl,
			Zrodlo: filepath.Base(klipy[o.Klip].Plik), Slowo: przydzial[i].slowo})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Od < out[b].Od })
	return out
}
