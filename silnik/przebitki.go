// Przebitki w filmie mówionym: gdy obok nagrania z osobą mówiącą do kamery są inne nagrania (produkt, praca,
// miejsce), ich najlepsze fragmenty przykrywają obraz na 1,6-3,2 sekundy, a głos biegnie dalej. Przebitka najchętniej
// zaczyna się tuż przed cięciem po wyciętej pauzie (zasłania przeskok) albo w chwili, gdy pada słowo pasujące do tego,
// co na niej widać, i kończy się na następnym cięciu albo na końcu słowa. Rozłożone są równo przez cały film (mniej
// więcej połowa czasu to przebitki), drżące ujęcia z ręki są stabilizowane. Początek z nagłówkiem i końcówka
// z wezwaniem zostają bez przebitek.
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
	przebitkaMin    = 1.6 // sekundy
	przebitkaCel    = 2.4
	przebitkaMaks   = 3.2
	przebitkaOdstep = 2.0 // najmniej tyle mówiącej osoby między przebitkami
	przebitkaRytm   = 2.6 // średnio tyle mówiącej osoby między przebitkami
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
	koniec := plan.Czas - 2.4
	if len(pliki) == 0 || koniec-poczatek < przebitkaMin {
		return nil
	}
	ileMiejsc := int((koniec-poczatek+przebitkaRytm)/(przebitkaCel+przebitkaRytm) + 1e-6)
	if ileMiejsc < 1 {
		ileMiejsc = 1
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
		ile := min(4, 1+int(k.Czas/12))
		okna = append(okna, oknaKlipu(len(klipy), k, sk, przebitkaMaks, ile, 4)...)
		klipy = append(klipy, k)
	}
	sort.SliceStable(okna, func(a, b int) bool { return okna[a].Ocena < okna[b].Ocena })
	var wybrane []Okno
	zKlipu := map[int]int{}
	for runda := 1; runda <= 4 && len(wybrane) < ileMiejsc; runda++ {
		for _, o := range okna {
			if len(wybrane) >= ileMiejsc {
				break
			}
			if o.Drgania > 3.0 || o.Ocena > 1.2 || zKlipu[o.Klip] != runda-1 {
				continue
			}
			podobne := false
			for _, w := range wybrane {
				// drugi fragment tego samego nagrania tylko wtedy, gdy widać na nim coś wyraźnie innego (bez powtórek w filmie)
				prog := 0.85
				if w.Klip == o.Klip {
					prog = 0.6
				}
				if (w.Klip == o.Klip && math.Abs(w.Od-o.Od) < 1.5) || podobienstwo(o.Mini, w.Mini, o.MiniW) > prog {
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
	przydzial := rozlozPrzebitki(wybrane, plan, poczatek, koniec, ileMiejsc)

	// 4. przygotowanie plików (kadr filmu, ta sama droga co ujęcia w filmie z ujęć; drżące ujęcia z ręki stabilizowane)
	zaStab := maFiltr(n, "vidstabdetect")
	kl := float64(st.Klatki)
	ile := 0
	for _, m := range przydzial {
		if m.jest {
			ile++
		}
	}
	var out []Przebitka
	for i, o := range wybrane {
		m := przydzial[i]
		if !m.jest {
			continue
		}
		postep(fmt.Sprintf("Przygotowuję przebitkę %d z %d", len(out)+1, ile))
		nazwa := fmt.Sprintf("przebitka%02d", i) // przygotujUjecie samo dodaje .mkv
		o.Do = math.Min(o.Do, o.Od+m.do-m.od+0.2)
		if err := przygotujUjecie(n, klipy[o.Klip], o, st, praca, nazwa, zaStab && o.Drgania > 1.2); err != nil {
			continue
		}
		od := math.Round(m.od*kl) / kl
		do := math.Round(m.do*kl) / kl
		out = append(out, Przebitka{Plik: filepath.Join(praca, nazwa+".mkv"), Od: od, Do: do, Zrodlo: filepath.Base(klipy[o.Klip].Plik), Slowo: m.slowo})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Od < out[b].Od })
	return out
}

type miejscePrzebitki struct {
	od, do float64
	slowo  string
	jest   bool
}

// rozlozPrzebitki: miejsce i długość każdej przebitki. Najpierw te, które pasują do wypowiadanego słowa, potem reszta
// równo przez cały film. Początek dosunięty tuż przed cięcie po wyciętej pauzie (przebitka zasłania przeskok obrazu),
// koniec na następnym cięciu albo na końcu słowa, tak żeby przebitka trwała 1,6-3,2 s.
func rozlozPrzebitki(wybrane []Okno, plan *PlanMowy, poczatek, koniec float64, ileMiejsc int) []miejscePrzebitki {
	var ciecia []float64
	for i, o := range plan.Odcinki {
		if i > 0 {
			ciecia = append(ciecia, o.WyOd)
		}
	}
	var konceSlow []float64
	for i, s := range plan.Slowa {
		if i+1 < len(plan.Slowa) && plan.Slowa[i+1].Od-s.Do >= 0.08 {
			konceSlow = append(konceSlow, s.Do+0.04)
		}
	}
	// długość: koniec na cięciu, potem na końcu słowa, najbliżej 2,4 s
	dlugosc := func(t float64) float64 {
		naj, najR := przebitkaCel, math.Inf(1)
		for _, zestaw := range [][]float64{ciecia, konceSlow} {
			for _, c := range zestaw {
				d := c - t
				if d < przebitkaMin || d > przebitkaMaks {
					continue
				}
				if r := math.Abs(d - przebitkaCel); r < najR {
					naj, najR = d, r
				}
			}
			if !math.IsInf(najR, 1) {
				break
			}
		}
		return math.Min(naj, koniec-t)
	}
	out := make([]miejscePrzebitki, len(wybrane))
	var zajete []miejscePrzebitki
	wolne := func(t, d float64) bool {
		if t < poczatek || d < przebitkaMin-1e-6 || t+d > koniec+1e-6 {
			return false
		}
		for _, m := range zajete {
			if t < m.do+przebitkaOdstep && t+d > m.od-przebitkaOdstep {
				return false
			}
		}
		return true
	}
	zajmij := func(i int, t float64, slowo string) bool {
		d := dlugosc(t)
		if !wolne(t, d) {
			return false
		}
		out[i] = miejscePrzebitki{od: t, do: t + d, slowo: slowo, jest: true}
		zajete = append(zajete, out[i])
		return true
	}
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
			if r := rdzenSlowa(s.Tekst); r != "" && rdzenie[r] && zajmij(i, s.Od-0.15, strings.Trim(s.Tekst, ".,!?;: ")) {
				break
			}
		}
	}
	// b) pozostałe: równo przez cały film (siatka ileMiejsc pól), każda w najbliższym wolnym miejscu przy swoim polu
	var reszta []int
	for i := range wybrane {
		if !out[i].jest {
			reszta = append(reszta, i)
		}
	}
	krok := (koniec - poczatek) / float64(ileMiejsc)
	for j, i := range reszta {
		// pole tej przebitki: przy mniejszej liczbie przebitek niż pól rozkładają się na całą długość filmu
		pole := float64(j) + 0.5
		if len(reszta) < ileMiejsc {
			pole = (float64(j) + 0.5) * float64(ileMiejsc) / float64(len(reszta))
		}
		srodek := poczatek + pole*krok - przebitkaCel/2
		var kandydaci []float64
		for _, c := range ciecia {
			if math.Abs(c-0.25-srodek) <= krok/2 {
				kandydaci = append(kandydaci, c-0.25)
			}
		}
		sort.Slice(kandydaci, func(a, b int) bool { return math.Abs(kandydaci[a]-srodek) < math.Abs(kandydaci[b]-srodek) })
		for d := 0.0; d <= krok/2+1e-6; d += 0.25 {
			kandydaci = append(kandydaci, srodek+d, srodek-d)
		}
		for _, t := range kandydaci {
			if zajmij(i, t, "") {
				break
			}
		}
	}
	return out
}
