// Skracanie wypowiedzi według treści. Gdy osoba mówi dłużej, niż ma trwać film, silnik nie ucina końcówki,
// tylko wybiera całe zdania: początek (zaczepienie widza), najważniejsze zdania ze środka i zakończenie.
// O tym, które zdania są najważniejsze, decyduje karta treści (karta.go: wagi i role zdań od modelu albo z reguł).
// Bez karty wybierają same reguły: zdania z tematycznymi słowami wypowiedzi, słowami-kluczami marki i liczbami
// mają pierwszeństwo. Cięcia wypadają na pauzach między zdaniami.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

type jednostkaMowy struct {
	frazy []int // indeksy fraz składających się na zdanie (albo kilka zdań wypowiedzianych bez pauzy)
	tekst string
	czas  float64
}

// jednostkiMowy: frazy, które zostają po wycięciu pauz i powtórzeń, zebrane w całe zdania.
func jednostkiMowy(fr []Fraza) []jednostkaMowy {
	var out []jednostkaMowy
	var bie jednostkaMowy
	var slowa []string
	zamknij := func() {
		if len(bie.frazy) > 0 {
			bie.tekst = strings.Join(slowa, " ")
			out = append(out, bie)
		}
		bie, slowa = jednostkaMowy{}, nil
	}
	for i, f := range fr {
		if f.Odrzucona != "" || len(f.Slowa) == 0 {
			continue
		}
		if len(bie.frazy) > 0 && fr[bie.frazy[0]].Klip != f.Klip {
			zamknij() // nowe nagranie zaczyna nową myśl
		}
		bie.frazy = append(bie.frazy, i)
		bie.czas += f.Do - f.Od + 0.25
		for _, s := range f.Slowa {
			slowa = append(slowa, s.Tekst)
		}
		if konczyZdanie(f.Slowa[len(f.Slowa)-1].Tekst) || bie.czas > 16 {
			zamknij()
		}
	}
	zamknij()
	return out
}

var lacznikiZdan = []string{"ale", "więc", "wiec", "dlatego", "bo", "czyli", "również", "rowniez", "też", "tez", "natomiast",
	"a", "i", "to", "ten", "ta", "te", "on", "ona", "one", "oni", "wtedy", "potem", "następnie", "nastepnie", "oprócz", "poza", "dodatkowo", "do"}

// zaczynaOdLacznika: zdanie, które bez poprzedniego wisi w powietrzu („Dlatego…”, „Ale…”, „To…”).
func zaczynaOdLacznika(tekst string) bool {
	p := strings.Fields(strings.ToLower(tekst))
	if len(p) == 0 {
		return false
	}
	pierwsze := strings.TrimFunc(p[0], func(r rune) bool { return !unicode.IsLetter(r) })
	for _, l := range lacznikiZdan {
		if pierwsze == l {
			return true
		}
	}
	// zaimek wskazujący na coś z poprzedniego zdania w pierwszych czterech słowach („Bez niego”, „Każdy z nich”, „Używamy do tego”)
	for i := 0; i < len(p) && i < 4; i++ {
		if zaimkiWstecz[strings.TrimFunc(p[i], func(r rune) bool { return !unicode.IsLetter(r) })] {
			return true
		}
	}
	return false
}

var zaimkiWstecz = map[string]bool{"niego": true, "niej": true, "nich": true, "nim": true, "nią": true, "nimi": true, "tego": true, "temu": true, "tym": true, "tej": true, "jego": true, "jej": true, "ich": true}

// dopelnijZalezne: zdanie, które bez poprzedniego wisi w powietrzu, dostaje poprzednie (gdy mieści się w czasie) albo samo wypada.
func dopelnijZalezne(j []jednostkaMowy, zostaje []bool, maks float64) {
	czas := 0.0
	for i, u := range j {
		if zostaje[i] {
			czas += u.czas
		}
	}
	for zmiana := true; zmiana; {
		zmiana = false
		for i := len(j) - 1; i > 0; i-- {
			if !zostaje[i] || zostaje[i-1] || !zaczynaOdLacznika(j[i].tekst) {
				continue
			}
			if czas+j[i-1].czas <= maks*1.04 {
				zostaje[i-1] = true
				czas += j[i-1].czas
			} else if i < len(j)-1 { // ostatnie zdanie (zakończenie) zostaje zawsze
				zostaje[i] = false
				czas -= j[i].czas
			} else {
				continue
			}
			zmiana = true
		}
	}
}

// wybierzZdaniaRegulami: zostają pierwsze i ostatnie zdanie, środek wypełniają zdania o największej wadze na
// sekundę. Zwraca nil, gdy nie ma z czego wybierać.
func wybierzZdaniaRegulami(j []jednostkaMowy, maks float64, st *Styl, wybrane []bool) []bool {
	n := len(j)
	if n < 3 {
		return nil
	}
	zostaje := make([]bool, n)
	czas := 0.0
	if wybrane != nil { // dopełnienie wyboru modelu
		copy(zostaje, wybrane)
		for i, u := range j {
			if zostaje[i] {
				czas += u.czas
			}
		}
	} else {
		zostaje[0], zostaje[n-1] = true, true
		czas = j[0].czas + j[n-1].czas
	}
	if czas > maks {
		if wybrane != nil {
			return zostaje
		}
		return nil
	}
	// słowa tematyczne: rdzenie dłuższych słów, które padają w wypowiedzi więcej niż raz
	ile := map[string]int{}
	rdzenie := make([]map[string]bool, n)
	for i, u := range j {
		rdzenie[i] = map[string]bool{}
		for _, s := range strings.Fields(u.tekst) {
			if r := rdzenSlowa(s); r != "" && len([]rune(normalizuj(s))) >= 5 {
				rdzenie[i][r] = true
			}
		}
		for r := range rdzenie[i] {
			ile[r]++
		}
	}
	waga := make([]float64, n)
	for i, u := range j {
		for r := range rdzenie[i] {
			if ile[r] >= 2 {
				waga[i] += 1
			} else {
				waga[i] += 0.3
			}
		}
		for _, s := range strings.Fields(u.tekst) {
			if ocenaSlowa(s, st.Slownik) > 0 {
				waga[i] += 2
			}
			if strings.IndexFunc(s, unicode.IsDigit) >= 0 {
				waga[i] += 1.5
			}
		}
	}
	kolejnosc := make([]int, 0, n)
	for i := 1; i < n-1; i++ {
		kolejnosc = append(kolejnosc, i)
	}
	sort.SliceStable(kolejnosc, func(a, b int) bool {
		return waga[kolejnosc[a]]/math.Max(j[kolejnosc[a]].czas, 1.5) > waga[kolejnosc[b]]/math.Max(j[kolejnosc[b]].czas, 1.5)
	})
	for _, i := range kolejnosc {
		if zostaje[i] {
			continue
		}
		potrzeba := j[i].czas
		poprzednie := zaczynaOdLacznika(j[i].tekst) && !zostaje[i-1]
		if poprzednie {
			potrzeba += j[i-1].czas // zdanie zaczynające się od „dlatego”, „ale”, „to” idzie razem z poprzednim
		}
		if czas+potrzeba > maks {
			continue
		}
		zostaje[i] = true
		if poprzednie {
			zostaje[i-1] = true
		}
		czas += potrzeba
	}
	return zostaje
}

// wybierzZdaniaKarta: zdania według karty treści. Zostają: zdanie-hak, zakończenie (gdy coś wnosi) i początek
// (gdy nie jest samym powitaniem), potem kolejno zdania o najwyższej wadze; przy tej samej wadze decyduje gęstość
// treści na sekundę. Zdania o wadze 0 (powitania, dygresje) wchodzą na końcu, tylko gdy zostało miejsce.
func wybierzZdaniaKarta(j []jednostkaMowy, k *Karta, maks float64) []bool {
	n := len(j)
	if k == nil || n < 3 {
		return nil
	}
	wagi, punkty, hak := k.wagiJednostek(n)
	zostaje := make([]bool, n)
	czas := 0.0
	wez := func(i int) bool {
		if i < 0 || i >= n || zostaje[i] {
			return true
		}
		potrzeba := j[i].czas
		poprzednie := i > 0 && zaczynaOdLacznika(j[i].tekst) && !zostaje[i-1]
		if poprzednie {
			potrzeba += j[i-1].czas // zdanie zaczynające się od „dlatego”, „ale”, „to” idzie razem z poprzednim
		}
		if czas+potrzeba > maks {
			return false
		}
		zostaje[i] = true
		if poprzednie {
			zostaje[i-1] = true
		}
		czas += potrzeba
		return true
	}
	wez(hak)
	if wagi[n-1] >= 1 {
		wez(n - 1)
	}
	if wagi[0] >= 1 {
		wez(0)
	}
	kolejnosc := make([]int, n)
	for i := range kolejnosc {
		kolejnosc[i] = i
	}
	sort.SliceStable(kolejnosc, func(a, b int) bool {
		x, y := kolejnosc[a], kolejnosc[b]
		if wagi[x] != wagi[y] {
			return wagi[x] > wagi[y]
		}
		return punkty[x]/math.Max(j[x].czas, 1.5) > punkty[y]/math.Max(j[y].czas, 1.5)
	})
	for _, i := range kolejnosc {
		if wagi[i] > 0 {
			wez(i)
		}
	}
	// powitania i dygresje wracają tylko wtedy, gdy bez nich film byłby o ponad połowę krótszy niż zamówiony
	if czas < 0.5*maks {
		for _, i := range kolejnosc {
			wez(i)
		}
	}
	ile := 0
	for _, z := range zostaje {
		if z {
			ile++
		}
	}
	if ile < 2 {
		return nil
	}
	return zostaje
}

// rozdzielNaZdania: frazy wypowiedziane jednym tchem dzielone na zdania tam, gdzie po kropce jest choćby krótka
// przerwa w głosie (wykryta przez detektor mowy). Bez tego osoba mówiąca płynnie daje bloki po kilkadziesiąt
// sekund i skracanie może wyrzucać tylko całe bloki. Sąsiednie kawałki plan i tak skleja bez cięcia, więc
// podział jest widoczny dopiero wtedy, gdy któreś zdanie wypada.
func rozdzielNaZdania(fr []Fraza) []Fraza {
	var out []Fraza
	for _, f := range fr {
		if f.Odrzucona != "" || len(f.Slowa) < 6 {
			out = append(out, f)
			continue
		}
		start, od := 0, f.Od
		for i := 0; i+1 < len(f.Slowa); i++ {
			przerwa := f.Slowa[i+1].Od - f.Slowa[i].Do
			if !konczyZdanie(f.Slowa[i].Tekst) || przerwa < 0.05 || i+1-start < 3 || len(f.Slowa)-(i+1) < 3 || f.Slowa[i].Do-od < 1.2 {
				continue
			}
			srodek := f.Slowa[i].Do + przerwa/2
			cz := f // kopia zachowuje pozostałe pola frazy
			cz.Od, cz.Do, cz.Slowa = od, math.Min(srodek, f.Slowa[i].Do+0.1), f.Slowa[start:i+1:i+1]
			out = append(out, cz)
			od, start = math.Max(srodek, f.Slowa[i+1].Od-0.08), i+1
		}
		cz := f
		cz.Od, cz.Slowa = od, f.Slowa[start:]
		out = append(out, cz)
	}
	return out
}

func sumaCzasu(j []jednostkaMowy) float64 {
	s := 0.0
	for _, u := range j {
		s += u.czas
	}
	return s
}

// skrocTresc: oznacza zdania, które wypadają, jako wycięte („skrót”). Zwraca opis do raportu albo "" (bez zmian).
// Po tej funkcji plan trzeba policzyć jeszcze raz. Karta treści (k) decyduje, które zdania są najważniejsze;
// bez karty wybierają same reguły.
func skrocTresc(fr []Fraza, czasPlanu, maks float64, st *Styl, k *Karta) string {
	j := jednostkiMowy(fr)
	if len(j) < 3 || maks <= 0 || czasPlanu <= maks {
		return ""
	}
	// czasy zdań są przybliżone (pauzy między zdaniami bywają dłuższe niż przyjęte), stąd poprawka do czasu planu
	cel := maks * math.Min(1, sumaCzasu(j)/czasPlanu) * 0.97
	kto := "karta treści"
	zostaje := wybierzZdaniaKarta(j, k, cel)
	if zostaje == nil {
		kto = "reguły"
		zostaje = wybierzZdaniaRegulami(j, cel, st, nil)
	} else if k.Zrodlo != "model" {
		kto = "karta treści z reguł"
	}
	if zostaje == nil {
		return ""
	}
	dopelnijZalezne(j, zostaje, cel)
	wyciete := 0
	for i, u := range j {
		if zostaje[i] {
			continue
		}
		wyciete++
		for _, f := range u.frazy {
			fr[f].Odrzucona = "skrót"
		}
	}
	if wyciete == 0 {
		return ""
	}
	return fmt.Sprintf("wypowiedź skrócona według treści do ok. %.0f s: zostało %d z %d części wypowiedzi, najważniejsze według wag (wybór: %s)", maks, len(j)-wyciete, len(j), kto)
}
