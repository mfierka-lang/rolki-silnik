// Skracanie wypowiedzi według treści. Gdy osoba mówi dłużej, niż ma trwać film, silnik nie ucina końcówki,
// tylko wybiera całe zdania: początek (zaczepienie widza), najważniejsze zdania ze środka i zakończenie.
// Wybiera model piszący (gdy jest na komputerze), a bez niego reguły: zdania z tematycznymi słowami wypowiedzi,
// słowami-kluczami marki i liczbami mają pierwszeństwo. Cięcia wypadają na pauzach między zdaniami.
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// pamiecSkrotow: katalog, w którym zostaje wybór zdań zrobiony przez model. Model za każdym razem wybiera trochę
// inaczej, a poprawka filmu i ten sam film w innej jakości mają mieć te same zdania.
var pamiecSkrotow string

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

var schematSkrotu = map[string]any{"type": "object", "properties": map[string]any{
	"zostaja": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "maxItems": 60}}, "required": []string{"zostaja"}}

// wybierzZdaniaModelem: model piszący dostaje ponumerowane zdania z czasem trwania i wybiera te, które zostają.
func wybierzZdaniaModelem(r *Rozum, j []jednostkaMowy, maks float64) []bool {
	if r == nil || !r.DobryWTekscie() || len(j) < 3 || len(j) > 80 {
		return nil
	}
	var b strings.Builder
	for i, u := range j {
		fmt.Fprintf(&b, "[%d] (%.0f s) %s\n", i+1, math.Max(1, u.czas), u.tekst)
	}
	pyt := fmt.Sprintf("To zdania z nagrania, w kolejności wypowiedzi. Film ma trwać najwyżej %.0f sekund, a całość jest za długa. "+
		"Wybierz zdania, które zostają, tak żeby widz zrozumiał wypowiedź: mocny początek, najważniejsze informacje i zakończenie. "+
		"Nie wybieraj zdania, które bez poprzedniego nie ma sensu, chyba że poprzednie też zostaje. Suma czasów wybranych zdań nie może przekroczyć %.0f sekund. "+
		"Odpowiedz w JSON: {\"zostaja\":[numery zdań rosnąco]}.\n\n%s", maks, maks, b.String())
	var o struct {
		Zostaja []int `json:"zostaja"`
	}
	plikPamieci, zPamieci := "", false
	if pamiecSkrotow != "" {
		h := sha1.Sum([]byte(pyt))
		plikPamieci = filepath.Join(pamiecSkrotow, "skrot-"+hex.EncodeToString(h[:10])+".json")
		if b, err := os.ReadFile(plikPamieci); err == nil && json.Unmarshal(b, &o) == nil && len(o.Zostaja) > 0 {
			zPamieci = true
		}
	}
	if !zPamieci {
		if err := r.pytaj("Jesteś montażystą. Skracasz wypowiedź, wybierając całe zdania. Niczego nie dopisujesz.", pyt, nil, schematSkrotu, 220, &o); err != nil {
			return nil
		}
	}
	zostaje := make([]bool, len(j))
	czas, wybranych := 0.0, 0
	for _, nr := range o.Zostaja {
		if nr < 1 || nr > len(j) || zostaje[nr-1] {
			continue
		}
		zostaje[nr-1] = true
		czas += j[nr-1].czas
		wybranych++
	}
	// odpowiedź musi mieścić się w czasie i nie może być wydmuszką
	if wybranych < 2 || czas > maks*1.06 || czas < math.Min(maks, sumaCzasu(j))*0.45 {
		return nil
	}
	if !zPamieci && plikPamieci != "" {
		if b, err := json.Marshal(o); err == nil {
			os.MkdirAll(pamiecSkrotow, 0o755)
			os.WriteFile(plikPamieci, b, 0o644)
		}
	}
	return zostaje
}

func sumaCzasu(j []jednostkaMowy) float64 {
	s := 0.0
	for _, u := range j {
		s += u.czas
	}
	return s
}

// skrocTresc: oznacza zdania, które wypadają, jako wycięte („skrót”). Zwraca opis do raportu albo "" (bez zmian).
// Po tej funkcji plan trzeba policzyć jeszcze raz.
func skrocTresc(fr []Fraza, czasPlanu, maks float64, st *Styl, r *Rozum) string {
	j := jednostkiMowy(fr)
	if len(j) < 3 || maks <= 0 || czasPlanu <= maks {
		return ""
	}
	// czasy zdań są przybliżone (pauzy między zdaniami bywają dłuższe niż przyjęte), stąd poprawka do czasu planu
	cel := maks * math.Min(1, sumaCzasu(j)/czasPlanu) * 0.97
	kto := "model piszący"
	zostaje := wybierzZdaniaModelem(r, j, cel)
	if zostaje == nil {
		kto = "reguły"
		zostaje = wybierzZdaniaRegulami(j, cel, st, nil)
	}
	if zostaje == nil {
		return ""
	}
	dopelnijZalezne(j, zostaje, cel)
	if kto != "reguły" { // model bywa zbyt oszczędny: wolny czas wypełniają kolejne najważniejsze zdania
		zostaje = wybierzZdaniaRegulami(j, cel, st, zostaje)
		dopelnijZalezne(j, zostaje, cel)
	}
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
	return fmt.Sprintf("wypowiedź skrócona według treści do ok. %.0f s: zostało %d z %d zdań, z początkiem i zakończeniem (wybór: %s)", maks, len(j)-wyciete, len(j), kto)
}
