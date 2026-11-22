// Akcenty kadru z treści: zbliżenia, które wynikają z tego, co osoba mówi (karta treści), a nie z zegara.
// Trzy ruchy (reguły zebrane w _notatki/github-przeglad-2026-11-22.md, sekcja 3):
//   - „punch”: twarde wejście na słowie-akcencie (liczba, mocne słowo), szczyt 1,18-1,30, 0,2-0,5 s, twarde wyjście;
//   - „schodki”: przy wyliczance każdy element to stopień wyżej (1,12 -> 1,28), potem powrót;
//   - „najazd”: wolne zbliżenie do 1,10-1,14 przez całe zdanie-tezę.
//
// Tłem zostaje dotychczasowy rytm dwóch kadrów (szeroki i ciasny na zmianę co ok. 2,5 s). Akcentów jest mało:
// najwyżej jeden na ok. 5 s filmu i co najmniej 3 s przerwy, bo to spokój między nimi sprawia, że działają.
// Nie ma ich na nagłówku, pod przebitką ani w ostatniej chwili filmu.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type AkcentKadru struct {
	Rodzaj  string       `json:"rodzaj"` // punch | schodki | najazd
	Od      float64      `json:"od"`     // w gotowym filmie
	Do      float64      `json:"do"`
	Szczyt  float64      `json:"szczyt"`
	Kroki   [][2]float64 `json:"kroki,omitempty"` // schodki: chwila i skala kolejnych stopni
	Slowo   string       `json:"slowo,omitempty"`
	Zdanie  int          `json:"zdanie"`
	waznosc float64
}

const (
	akcentOdstep   = 3.0 // sekundy spokoju między akcentami
	akcentCoIle    = 5.0 // najwyżej jeden akcent na tyle sekund filmu
	punchMin       = 0.2
	punchMaks      = 0.5
	najazdMin      = 1.4
	najazdMaks     = 7.0
	schodkiOd      = 1.12
	schodkiDo      = 1.28
	najazdPoczatek = 0.015 // najazd zaczyna się tuż nad kadrem szerokim
)

// koniecOdcinka: koniec (w gotowym filmie) odcinka bez cięcia, w którym leży chwila t.
func koniecOdcinka(plan *PlanMowy, t float64) float64 {
	for i, o := range plan.Odcinki {
		do := o.WyOd + (o.Do - o.Od)
		if t >= o.WyOd-1e-6 && (t < do || i == len(plan.Odcinki)-1) {
			return do
		}
	}
	return plan.Czas
}

// akcentyKadru: zdarzenia kadru dla zdań, które zostały w filmie. Karta musi być już przypięta do planu.
func akcentyKadru(k *Karta, plan *PlanMowy, st *Styl, odKiedy float64, przebitki []Przebitka) []AkcentKadru {
	if k == nil || plan == nil || len(st.Kadr.Zblizenia) < 2 { // wyłączone animacje = jeden kadr, bez akcentów
		return nil
	}
	klatka := 1 / float64(st.Klatki)
	doKiedy := plan.Czas - 0.6
	var kand []AkcentKadru
	for i := range k.Zdania {
		z := &k.Zdania[i]
		if z.Waga < 2 || !z.wFilmie() || z.Rola == "dygresja" {
			continue
		}
		czasSlowa := func(nr int) (od, do float64, ok bool) {
			if nr < 0 || nr >= len(z.Slowa) || z.Slowa[nr].Plan < 0 {
				return 0, 0, false
			}
			s := plan.Slowa[z.Slowa[nr].Plan]
			return s.Od, s.Do, true
		}
		waznosc := float64(z.Waga) * 10
		switch z.Rodzaj {
		case "liczba", "slowo":
			od, do, ok := czasSlowa(z.AkcentNr)
			if !ok {
				continue
			}
			szczyt := 1.20
			if z.Waga >= 3 {
				szczyt = 1.26
			}
			if z.Rodzaj == "liczba" {
				// Liczba ma własny akcent: nabijaną kartę na wysokości piersi. Zbliżenie w tej samej chwili powiększało
				// twarz tak, że karta zasłaniała usta (arkusz 22.11.2026), więc przy liczbie kadr zostaje spokojny.
				continue
			}
			a := AkcentKadru{Rodzaj: "punch", Od: od - 0.05, Szczyt: szczyt, Slowo: z.Akcent, Zdanie: z.Nr, waznosc: waznosc}
			a.Do = a.Od + math.Max(punchMin+0.05, math.Min(punchMaks, do-od+0.17))
			a.Do = math.Min(a.Do, koniecOdcinka(plan, od)-klatka) // punch nie przechodzi przez cięcie
			if a.Do-a.Od < punchMin {
				continue
			}
			kand = append(kand, a)
		case "wyliczanka":
			a := AkcentKadru{Rodzaj: "schodki", Slowo: z.Akcent, Zdanie: z.Nr, waznosc: waznosc + 3}
			ost := 0.0
			for _, nr := range z.Kroki {
				od, do, ok := czasSlowa(nr)
				if !ok || (len(a.Kroki) > 0 && od-a.Kroki[len(a.Kroki)-1][0] < 0.25) {
					continue
				}
				a.Kroki = append(a.Kroki, [2]float64{od - 0.05, 0})
				ost = do
			}
			if len(a.Kroki) < 2 {
				continue
			}
			for j := range a.Kroki {
				a.Kroki[j][1] = schodkiOd + (schodkiDo-schodkiOd)*float64(j)/float64(len(a.Kroki)-1)
			}
			a.Od, a.Do, a.Szczyt = a.Kroki[0][0], ost+0.3, schodkiDo
			if _, do, ok := czasSlowa(len(z.Slowa) - 1); ok {
				a.Do = math.Min(a.Do, do+0.12)
			}
			if a.Do-a.Kroki[len(a.Kroki)-1][0] < 0.2 {
				continue
			}
			kand = append(kand, a)
		case "teza":
			if z.Waga < 3 {
				continue
			}
			od, _, ok1 := czasSlowa(0)
			_, do, ok2 := czasSlowa(len(z.Slowa) - 1)
			if !ok1 || !ok2 {
				continue
			}
			a := AkcentKadru{Rodzaj: "najazd", Od: od - 0.05, Do: do + 0.1, Slowo: z.Akcent, Zdanie: z.Nr, waznosc: waznosc + 2}
			if d := a.Do - a.Od; d < najazdMin || d > najazdMaks {
				continue
			}
			a.Szczyt = 1.10 + 0.04*math.Min(1, (a.Do-a.Od-najazdMin)/3)
			kand = append(kand, a)
		}
	}
	// najważniejsze najpierw; akcent wchodzi, gdy ma spokój wokół siebie i nie trafia pod przebitkę
	sort.SliceStable(kand, func(a, b int) bool {
		if kand[a].waznosc != kand[b].waznosc {
			return kand[a].waznosc > kand[b].waznosc
		}
		return kand[a].Od < kand[b].Od
	})
	limit := int(math.Max(1, math.Floor(plan.Czas/akcentCoIle)))
	var out []AkcentKadru
	for _, a := range kand {
		if len(out) >= limit {
			break
		}
		if a.Od < odKiedy || a.Do > doKiedy {
			continue
		}
		wolne := true
		for _, p := range przebitki {
			if a.Od < p.Do+0.15 && a.Do > p.Od-0.15 {
				wolne = false
			}
		}
		for _, b := range out {
			if a.Od < b.Do+akcentOdstep && a.Do > b.Od-akcentOdstep {
				wolne = false
			}
		}
		if wolne {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Od < out[b].Od })
	return out
}

// skladnikiZoomu: fragmenty wyrażenia zbliżenia dla jednego odcinka (czas lokalny strumienia = czas filmu + przes).
// punch i schodki to skoki ponad tło, najazd zastępuje tło na czas zdania.
type skladnikiZoomu struct {
	skoki, najazdCzas, najazdSkala []string
}

func (s *skladnikiZoomu) dodaj(akc []AkcentKadru, wyOd, wyDo, przes, klatka float64) {
	okno := func(a, b float64) (string, bool) {
		a, b = math.Max(a, wyOd), math.Min(b, wyDo)
		if b-a < klatka {
			return "", false
		}
		return fmt.Sprintf("between(it,%s,%s)", liczba3(a+przes-0.004), liczba3(b+przes-0.012)), true
	}
	for _, e := range akc {
		if e.Do <= wyOd || e.Od >= wyDo {
			continue
		}
		switch e.Rodzaj {
		case "punch":
			if o, ok := okno(e.Od, e.Do); ok {
				s.skoki = append(s.skoki, o+"*"+liczba3(e.Szczyt-1))
			}
		case "schodki":
			for j, kr := range e.Kroki {
				do := e.Do
				if j+1 < len(e.Kroki) {
					do = e.Kroki[j+1][0]
				}
				if o, ok := okno(kr[0], do); ok {
					s.skoki = append(s.skoki, o+"*"+liczba3(kr[1]-1))
				}
			}
		case "najazd":
			if o, ok := okno(e.Od, e.Do); ok {
				s.najazdCzas = append(s.najazdCzas, o)
				nachylenie := (e.Szczyt - 1 - najazdPoczatek) / (e.Do - e.Od)
				s.najazdSkala = append(s.najazdSkala, fmt.Sprintf("%s*(%s+%s*(it-%s))", o, liczba3(najazdPoczatek), liczba(math.Round(nachylenie*1e5)/1e5), liczba3(e.Od+przes)))
			}
		}
	}
}

// wyrazenie: pełne wyrażenie zbliżenia: tło (kadry na zmianę) + najazd zamiast tła + skoki jako maksimum.
func (s *skladnikiZoomu) wyrazenie(tlo []string) string {
	baza := "0"
	if len(tlo) > 0 {
		baza = strings.Join(tlo, "+")
	}
	if len(s.najazdCzas) > 0 {
		baza = "(" + baza + ")*(1-min(1," + strings.Join(s.najazdCzas, "+") + "))+" + strings.Join(s.najazdSkala, "+")
	}
	z := "1"
	if baza != "0" {
		z = "1+" + baza
	}
	if len(s.skoki) > 0 {
		z = "max(" + z + ",1+" + strings.Join(s.skoki, "+") + ")"
	}
	return z
}

func liczba3(v float64) string { return liczba(math.Round(v*1000) / 1000) }

// opisAkcentow: zdanie do raportu („Co zrobił automat”).
func opisAkcentow(akc []AkcentKadru) string {
	if len(akc) == 0 {
		return ""
	}
	var punch, schodki, najazd []string
	for _, a := range akc {
		switch a.Rodzaj {
		case "punch":
			punch = append(punch, a.Slowo)
		case "schodki":
			schodki = append(schodki, a.Slowo)
		case "najazd":
			najazd = append(najazd, a.Slowo)
		}
	}
	var cz []string
	if len(punch) > 0 {
		cz = append(cz, "szybkie zbliżenie na słowa: "+strings.Join(punch, ", "))
	}
	if len(schodki) > 0 {
		cz = append(cz, fmt.Sprintf("zbliżenie schodkami przy wyliczance: %d", len(schodki)))
	}
	if len(najazd) > 0 {
		cz = append(cz, fmt.Sprintf("powolny najazd na najważniejsze zdanie: %d", len(najazd)))
	}
	return "Akcenty kadru z treści: " + strings.Join(cz, "; ")
}
