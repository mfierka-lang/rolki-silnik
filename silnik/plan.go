// Plan cięć rolki mówionej: z fragmentów mowy i słów powstaje lista odcinków, które zostają.
// Odpadają: cisza, wtrącenia (yyy, eee, oddech), porzucone podejścia do zdania (zostaje ostatnie, pełne),
// a od 22.11.2026 także falstarty, dosłowne powtórzenia, klapsy („jeszcze raz”) i pauzy ukryte w rozciągniętych
// słowach (powtorki.go; transkrypcja dosłuchuje miejsca, w których Whisper pominął powtórkę: mowa.go).
// Zasada ostrożności: wątpliwy fragment zostaje, bo powtórkę łatwiej wyciąć poprawką niż odzyskać wyciętą treść.
package main

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
)

type Fraza struct {
	Klip      int
	Od, Do    float64 // granice mowy w nagraniu źródłowym
	Slowa     []Slowo
	Odrzucona string // "" albo powód
	// Ciecia: zdarzenia do raportu zapisane przy tej frazie (wtrącenie, falstart, powtórzone podejście...). Zostają
	// przy frazie, więc raport jest taki sam także wtedy, gdy plan liczy się drugi raz (po skróceniu wypowiedzi).
	Ciecia []string
	// TnijPrzed, TnijPo: granica frazy powstała przez wycięcie słów, więc cięcie musi tam być także przy krótkiej przerwie.
	TnijPrzed, TnijPo bool
}

type Odcinek struct {
	Klip   int
	Od, Do float64 // w źródle, wyrównane do klatek
	WyOd   float64 // początek w gotowej rolce
}

type PlanMowy struct {
	Odcinki []Odcinek
	Slowa   []Slowo // czasy w gotowej rolce
	Czas    float64
	Raport  []string
}

var reWtracenie = regexp.MustCompile(`^(y{2,}|e{2,}|m{2,}|h?m{2,}|y+m+|e+m+|a{3,}|y|yhy|ehm?|hmm?)$`)

func normalizuj(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func toWtracenie(s string) bool { return reWtracenie.MatchString(normalizuj(s)) }

func levenshtein(a, b []rune) int {
	p := make([]int, len(b)+1)
	for j := range p {
		p[j] = j
	}
	for i := 1; i <= len(a); i++ {
		poprz := p[0]
		p[0] = i
		for j := 1; j <= len(b); j++ {
			t := p[j]
			k := poprz
			if a[i-1] != b[j-1] {
				k++
			}
			p[j] = min(k, min(p[j]+1, p[j-1]+1))
			poprz = t
		}
	}
	return p[len(b)]
}

// podobne: to samo słowo mimo drobnej różnicy w transkrypcji albo urwania w pół ("tele" i "telefon").
func podobne(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) >= 3 && len(rb) >= 3 && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a)) {
		return true
	}
	d := levenshtein(ra, rb)
	dl := min(len(ra), len(rb))
	return (dl >= 4 && d <= 1) || (dl >= 8 && d <= 2)
}

// frazyZMowy: fragmenty mowy rozdzielone krótką przerwą to jedna fraza. Słowo trafia do frazy, w której się
// kończy (czas końca z transkrypcji jest pewny); początek słowa to koniec poprzedniego albo początek fragmentu mowy.
func frazyZMowy(klip int, mowa []Przedzial, slowa []Slowo, pauzaWFrazie float64) []Fraza {
	var fr []Fraza
	for _, p := range mowa {
		if n := len(fr); n > 0 && p.Od-fr[n-1].Do < pauzaWFrazie {
			fr[n-1].Do = p.Do
			continue
		}
		fr = append(fr, Fraza{Klip: klip, Od: p.Od, Do: p.Do})
	}
	if len(fr) == 0 {
		return nil
	}
	przypiszProfil(klip, mowa)
	rozciagniete := map[[2]int]float64{}
	odleglosc := func(t float64, od, do float64) float64 { return math.Max(0, math.Max(od-t, t-do)) }
	ostatnia := 0
	for _, s := range slowa {
		naj, najOdl := -1, math.Inf(1)
		for i := ostatnia; i < len(fr); i++ { // słowa idą po kolei, więc fraza nie może się cofnąć
			if d := odleglosc(s.Do, fr[i].Od+0.03, fr[i].Do+0.15); d < najOdl {
				naj, najOdl = i, d
			}
		}
		if naj < 0 || najOdl > 0.6 {
			continue // słowo w ciszy: omam transkrypcji
		}
		ostatnia = naj
		fr[naj].Slowa = append(fr[naj].Slowa, s)
	}
	for i := range fr {
		f := &fr[i]
		poprz := f.Od
		for j := range f.Slowa {
			w := &f.Slowa[j]
			w.Do = math.Min(math.Max(w.Do, poprz+0.06), f.Do+0.12)
			w.Od = poprz
			// po przerwie w środku frazy słowo zaczyna się razem z kolejnym fragmentem mowy, nie w ciszy
			for _, m := range mowa {
				if m.Od > poprz+0.05 && m.Od < w.Do-0.05 && m.Do > w.Do-0.4 {
					w.Od = m.Od
				}
			}
			// krótkie słowo trwające sekundę i dłużej kryje pauzę albo niezapisane „yyy”: nadmiar jest pauzą
			if nad := nadmiarSlowa(*w); nad > 0 {
				rozciagniete[[2]int{i, j}] = nad
				w.Od = cichyPunkt(klip, w.Od+nad, 0.06)
				if j == 0 {
					f.Od = math.Max(f.Od, w.Od)
				}
			}
			if w.Do-w.Od > 1.2 { // bardzo długie "słowo" to zwykle cisza przed nim
				w.Od = w.Do - 1.2
			}
			poprz = w.Do
		}
	}
	return rozdzielFrazy(fr, rozciagniete)
}

func slowaZnormalizowane(fr []Fraza, indeksy []int) []string {
	var w []string
	for _, i := range indeksy {
		for _, s := range fr[i].Slowa {
			if n := normalizuj(s.Tekst); n != "" {
				w = append(w, n)
			}
		}
	}
	return w
}

// zgodnoscPoczatku: jaka część słów wcześniejszego podejścia powtarza się po kolei na początku późniejszego.
func zgodnoscPoczatku(wczesniej, pozniej []string) float64 {
	if len(wczesniej) == 0 || len(pozniej) == 0 || !podobne(wczesniej[0], pozniej[0]) {
		return 0
	}
	okno := pozniej
	if len(okno) > len(wczesniej)+2 {
		okno = okno[:len(wczesniej)+2]
	}
	traf, j := 0, 0
	for _, w := range wczesniej {
		for k := j; k < len(okno) && k <= j+2; k++ {
			if podobne(w, okno[k]) {
				traf++
				j = k + 1
				break
			}
		}
	}
	return float64(traf) / float64(len(wczesniej))
}

func zaplanujMowe(fr []Fraza, czasyKlipow []float64, st *Styl) *PlanMowy {
	plan := &PlanMowy{}
	kl := float64(st.Klatki)

	// 1. frazy bez słów (yyy, oddech, stuk), same wtrącenia i wypełniacze stojące samotnie między pauzami („no znaczy”)
	for i := range fr {
		f := &fr[i]
		if f.Odrzucona != "" {
			continue
		}
		var prawdziwe []Slowo
		for k, s := range f.Slowa {
			if !wtracenieW(f.Slowa, k) {
				prawdziwe = append(prawdziwe, s)
			}
		}
		switch {
		case len(f.Slowa) == 0:
			f.Odrzucona = ciecieBezSlow
			f.Ciecia = append(f.Ciecia, ciecieBezSlow)
		case len(prawdziwe) == 0:
			f.Odrzucona = ciecieWtrac
			f.Ciecia = append(f.Ciecia, ciecieWtrac)
		case toWypelniacz(slowaZnormalizowane(fr, []int{i})):
			f.Odrzucona = ciecieWtrac
			f.Ciecia = append(f.Ciecia, ciecieWtrac)
		default:
			// wtrącenie na początku albo na końcu frazy: przesuwamy granicę za nie
			if toWtracenie(f.Slowa[0].Tekst) {
				f.Od = math.Max(f.Od, prawdziwe[0].Od-0.06)
				f.Ciecia = append(f.Ciecia, ciecieWtrac)
			}
			if wtracenieW(f.Slowa, len(f.Slowa)-1) {
				f.Do = math.Min(f.Do, prawdziwe[len(prawdziwe)-1].Do+0.08)
				f.Ciecia = append(f.Ciecia, ciecieWtrac)
			}
			f.Slowa = prawdziwe
		}
	}

	// 1b. powtórki wykrywane na słowach: klaps z porzuconym podejściem, dosłowne powtórzenie, falstart, powtórzona kwestia
	zastosujPowtorki(fr)

	// 2. porzucone podejścia: gdy fraza zaczyna się tak samo jak wcześniejsza (do 4 fraz wstecz), a wcześniejsze
	// podejście prawie w całości powtarza się na jej początku, wcześniejsze odpada
	var aktywne []int
	for j := range fr {
		if fr[j].Odrzucona != "" {
			continue
		}
		pozniej := slowaZnormalizowane(fr, []int{j})
		for k := j + 1; k < len(fr) && len(pozniej) < 14; k++ { // dalszy ciąg zdania bywa w następnej frazie
			if fr[k].Odrzucona == "" {
				pozniej = append(pozniej, slowaZnormalizowane(fr, []int{k})...)
			}
		}
		wytnijOd := -1
		for cof := 1; cof <= 4 && cof <= len(aktywne); cof++ {
			zakres := aktywne[len(aktywne)-cof:]
			if fr[zakres[0]].Klip == fr[j].Klip && fr[j].Od-fr[zakres[0]].Od > 30 {
				break
			}
			wcz := slowaZnormalizowane(fr, zakres)
			if len(wcz) == 0 || len(wcz) > len(pozniej)+1 {
				continue
			}
			z := zgodnoscPoczatku(wcz, pozniej)
			dosc := (len(wcz) >= 2 && z >= 0.75) || (len(wcz) == 1 && z == 1 && len([]rune(wcz[0])) >= 5)
			// ostrożność: dokończone zdanie, które tylko zaczyna się podobnie („To jest szybkie. To jest tanie.”),
			// i samotne słowo powtarzane dla efektu („Tanio. Tanio. Tanio.”) to nie są porzucone podejścia
			ost := fr[zakres[len(zakres)-1]].Slowa
			if z < 1 && len(ost) > 0 && konczyZdanie(ost[len(ost)-1].Tekst) {
				dosc = false
			}
			if len(wcz) == 1 && len(fr[j].Slowa) < 2 {
				dosc = false
			}
			if dosc {
				wytnijOd = len(aktywne) - cof
			}
		}
		if wytnijOd >= 0 {
			for _, i := range aktywne[wytnijOd:] {
				fr[i].Odrzucona = ciecieKwestia
			}
			fr[aktywne[wytnijOd]].Ciecia = append(fr[aktywne[wytnijOd]].Ciecia, ciecieKwestia)
			aktywne = aktywne[:wytnijOd]
		}
		aktywne = append(aktywne, j)
	}

	// 3. odcinki: zapas przed i po, bez wchodzenia w sąsiednie (także wycięte) frazy; bliskie frazy bez cięcia
	type surowy struct {
		klip   int
		od, do float64
		frazy  []int
	}
	var sur []surowy
	for _, j := range aktywne {
		f := fr[j]
		od, do := f.Od-st.Ciecie.ZapasPrzed, f.Do+st.Ciecie.ZapasPo
		if j > 0 && fr[j-1].Klip == f.Klip {
			od = math.Max(od, (fr[j-1].Do+f.Od)/2)
		}
		if j+1 < len(fr) && fr[j+1].Klip == f.Klip {
			do = math.Min(do, (f.Do+fr[j+1].Od)/2)
		}
		if f.TnijPrzed { // przed frazą stały wycięte słowa: bez zapasu, żeby nie wróciła ich końcówka
			od = math.Max(od, f.Od-0.02)
		}
		if f.TnijPo {
			do = math.Min(do, f.Do+0.03)
		}
		od = math.Max(0, od)
		do = math.Min(do, czasyKlipow[f.Klip])
		if n := len(sur); n > 0 {
			p := &sur[n-1]
			sasiad := p.klip == f.Klip && p.frazy[len(p.frazy)-1] == j-1 && !f.TnijPrzed && !fr[j-1].TnijPo
			if sasiad && od-p.do <= st.Ciecie.MaksPauza {
				p.do = do
				p.frazy = append(p.frazy, j)
				continue
			}
		}
		sur = append(sur, surowy{f.Klip, od, do, []int{j}})
	}
	wy := 0.0
	for _, s := range sur {
		od := math.Floor(s.od*kl+1e-6) / kl
		do := math.Ceil(s.do*kl-1e-6) / kl
		if n := len(plan.Odcinki); n > 0 && plan.Odcinki[n-1].Klip == s.klip && od < plan.Odcinki[n-1].Do {
			od = plan.Odcinki[n-1].Do
		}
		if do-od < 2/kl {
			continue
		}
		o := Odcinek{Klip: s.klip, Od: od, Do: do, WyOd: wy}
		for _, j := range s.frazy {
			for _, w := range fr[j].Slowa {
				nOd := math.Min(math.Max(w.Od, od), do-0.04) - od + wy
				nDo := math.Min(math.Max(w.Do, od+0.04), do) - od + wy
				if nDo <= nOd {
					nDo = nOd + 0.04
				}
				plan.Slowa = append(plan.Slowa, Slowo{w.Tekst, nOd, nDo})
			}
		}
		plan.Odcinki = append(plan.Odcinki, o)
		wy += do - od
	}
	plan.Czas = wy

	zrodlo := 0.0
	for _, c := range czasyKlipow {
		zrodlo += c
	}
	plan.Raport = append(plan.Raport, fmt.Sprintf("nagranie %.1f s -> rolka %.1f s, cięć: %d", zrodlo, plan.Czas, max(0, len(plan.Odcinki)-1)))
	ile := map[string]int{}
	for _, f := range fr {
		for _, c := range f.Ciecia {
			ile[c]++
		}
	}
	for _, r := range []struct {
		opis string
		n    int
	}{
		{"wycięte wtrącenia i odgłosy bez słów", ile[ciecieBezSlow] + ile[ciecieWtrac]},
		{"wycięte powtórzone podejścia do zdania", ile[ciecieKwestia] + ile[ciecieKlaps]},
		{"wycięte falstarty (urwane początki zdania)", ile[ciecieFalstart]},
		{"wycięte dosłowne powtórzenia słów", ile[ciecieDoslowne]},
		{"skrócone pauzy ukryte w rozciągniętych słowach", ile[ciecieRozciag]},
	} {
		if r.n > 0 {
			plan.Raport = append(plan.Raport, fmt.Sprintf("%s: %d", r.opis, r.n))
		}
	}
	return plan
}
