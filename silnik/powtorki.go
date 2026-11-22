// Powtórki i pomyłki w wypowiedzi, wykrywane regułami na słowach z transkrypcji (bez modelu językowego):
// falstart (urwany początek powtórzony zaraz potem), powtórzona cała kwestia (zostaje ostatnie podejście),
// dosłowne powtórzenie kilku słów pod rząd, słowo-klaps („jeszcze raz”, „od nowa”) z porzuconym podejściem przed nim,
// wtrącenia w środku frazy i „rozciągnięte” krótkie słowo, pod którym transkrypcja schowała pauzę albo „yyy”.
// Reguły i progi wyjściowe: cut-video i Smart-Cut (MIT, same zasady, bez kodu), dostrojone na polskiej mowie.
// Zasada ostrożności jak w plan.go: wątpliwe miejsce zostaje. Powtórzenie bez przerwy przed nim („tanio, tanio,
// tanio”) traktuję jako zabieg mówcy, nie pomyłkę.
package main

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const (
	powtPauza      = 0.20 // przerwa przed powtórką, od której powtórzenie kilku słów jest pomyłką, a nie zabiegiem
	powtPauzaSlowo = 0.35 // to samo dla powtórzenia jednego słowa (krótsza przerwa to zwykły przecinek)
	falstartOkno   = 3.5  // ile sekund przed ponownym początkiem mogło zacząć się urwane podejście
	falstartSlow   = 9    // i najwyżej tyle słów
	kwestiaOkno    = 60.0 // powtórzona kwestia: tyle sekund wstecz szukam wcześniejszego podejścia
	kwestiaZdan    = 12   // i najwyżej tyle zdań wstecz
	progFalstart   = 0.80 // jaka część słów dłuższego urwanego podejścia wraca po kolei na początku następnego
	progKwestia    = 0.70 // pokrycie czwórek znaków wcześniejszej kwestii w późniejszej
	progKwestiaDal = 0.80 // to samo, gdy między podejściami jest inna treść
	pauzaZdania    = 0.70 // tak długa przerwa dzieli wypowiedź na kwestie także bez kropki
	rozciagProg    = 1.00 // krótkie słowo trwające co najmniej tyle sekund kryje pauzę albo zgubione „yyy”
	rozciagCiecie  = 0.55 // od takiego nadmiaru powstaje cięcie (krótszy mieści się w zapasach przed i po)
)

// Powody odrzucenia frazy i nazwy zdarzeń liczonych w raporcie.
const (
	ciecieFalstart = "falstart"
	ciecieDoslowne = "dosłowne powtórzenie"
	ciecieKwestia  = "powtórzone podejście"
	ciecieKlaps    = "klaps i porzucone podejście"
	ciecieRozciag  = "rozciągnięte słowo"
	ciecieWtrac    = "wtrącenie"
	ciecieBezSlow  = "bez słów"
)

// wypelniacze: słowa, które wypadają tylko wtedy, gdy stoją same między pauzami albo tuż przed powtórzeniem.
var wypelniacze = map[string]bool{"znaczy": true, "jakby": true, "generalnie": true, "no": true, "sensie": true}

// obokWypelniacza: słowa, które razem z wypełniaczem tworzą samodzielną wstawkę („to znaczy”, „w sensie”, „tak jakby”).
var obokWypelniacza = map[string]bool{"to": true, "w": true, "tak": true, "no": true}

// miedzyPodejsciami: słowa, które mówca wtrąca między urwanym podejściem a następnym („to jest, nie, to jest…”).
var miedzyPodejsciami = map[string]bool{"znaczy": true, "jakby": true, "generalnie": true, "no": true, "sensie": true, "w": true,
	"to": true, "nie": true, "kurde": true, "sorry": true, "przepraszam": true, "czekaj": true, "dobra": true, "okej": true,
	"ok": true, "inaczej": true, "wróć": true, "wroc": true, "tfu": true, "albo": true, "tak": true}

// slowaKlapsa: z czego może się składać fraza-klaps poza samym „jeszcze raz” / „od nowa” / „od początku”.
var slowaKlapsa = map[string]bool{"jeszcze": true, "raz": true, "od": true, "nowa": true, "początku": true, "poczatku": true,
	"dobra": true, "dobrze": true, "okej": true, "ok": true, "no": true, "nie": true, "sorry": true, "przepraszam": true,
	"czekaj": true, "kurde": true, "to": true, "i": true, "a": true, "źle": true, "zle": true, "stop": true, "wróć": true,
	"wroc": true, "powtórka": true, "powtorka": true, "powtarzam": true, "zaczynam": true, "zacznę": true, "zaczne": true,
	"zacznijmy": true, "lecimy": true, "spróbuję": true, "sprobuje": true, "tak": true, "więc": true, "wiec": true}

// spojnikNaPoczatku: słowa, którymi mówca zaczyna drugie podejście, choć pierwsze ich nie miało.
var spojnikNaPoczatku = map[string]bool{"a": true, "i": true, "no": true, "to": true, "więc": true, "wiec": true, "ale": true, "czyli": true}

var rdzenieKlapsa = []string{"jeszcze raz", "od nowa", "od początku", "od poczatku", "powtórka", "powtorka", "powtarzam"}

// toWypelniacz: fraza złożona z samych wypełniaczy (najwyżej 3 słowa), np. „no znaczy”, „w sensie”, „generalnie”.
func toWypelniacz(slowa []string) bool {
	if len(slowa) == 0 || len(slowa) > 3 {
		return false
	}
	rdzen := false
	for _, s := range slowa {
		switch {
		case wypelniacze[s]:
			rdzen = true
		case obokWypelniacza[s]:
		default:
			return false
		}
	}
	return rdzen
}

// toKlaps: fraza między pauzami, którą mówca zapowiada nowe podejście („dobra, jeszcze raz”).
func toKlaps(slowa []string) bool {
	if len(slowa) == 0 || len(slowa) > 6 {
		return false
	}
	for _, s := range slowa {
		if !slowaKlapsa[s] {
			return false
		}
	}
	t := strings.Join(slowa, " ")
	for _, r := range rdzenieKlapsa {
		if strings.Contains(t, r) {
			return true
		}
	}
	return false
}

// slowoPl: słowo wypowiedzi w jednym ciągu przez wszystkie frazy, które zostają.
type slowoPl struct {
	fraza, nr int     // fraza i numer słowa w niej
	klip      int     // nagranie
	n         string  // słowo bez interpunkcji, małymi literami
	od, do    float64 // czasy w nagraniu
	pauza     float64 // cisza (albo wycięta treść) przed słowem
	koniec    bool    // po słowie kończy się zdanie
	poczFrazy bool    // pierwsze słowo frazy
}

type wyciecie struct {
	od, do int // zakres słów [od, do)
	typ    string
}

// splaszcz: słowa fraz, które zostają, jako jeden ciąg. Wtrącenia („yyy”) są pomijane, a ich czas liczy się do pauzy.
func splaszcz(fr []Fraza) []slowoPl {
	var w []slowoPl
	for i, f := range fr {
		if f.Odrzucona != "" {
			continue
		}
		pierwsze := true
		for j, s := range f.Slowa {
			n := normalizuj(s.Tekst)
			if n == "" || wtracenieW(f.Slowa, j) {
				continue
			}
			p := slowoPl{fraza: i, nr: j, klip: f.Klip, n: n, od: s.Od, do: s.Do, koniec: konczyZdanie(s.Tekst), poczFrazy: pierwsze}
			pierwsze = false
			if k := len(w); k == 0 || w[k-1].klip != f.Klip {
				p.pauza = 99
			} else {
				p.pauza = math.Max(0, s.Od-w[k-1].do)
				if w[k-1].fraza != i && fr[w[k-1].fraza].Do < f.Od {
					// między frazami: prawdziwa cisza liczona z granic mowy, nie z czasów słów
					p.pauza = math.Max(p.pauza, f.Od-fr[w[k-1].fraza].Do)
				}
			}
			w = append(w, p)
		}
	}
	return w
}

func teksty(w []slowoPl) []string {
	t := make([]string, len(w))
	for i, s := range w {
		t[i] = s.n
	}
	return t
}

// pokrycieZnakow: jaka część n-znakowych kawałków tekstu a występuje w tekście b (z krotnością).
func pokrycieZnakow(a, b []string, n int) float64 {
	ta, tb := []rune(" "+strings.Join(a, " ")+" "), []rune(" "+strings.Join(b, " ")+" ")
	if len(ta) < n || len(tb) < n {
		return 0
	}
	mam := map[string]int{}
	for i := 0; i+n <= len(tb); i++ {
		mam[string(tb[i:i+n])]++
	}
	traf := 0
	for i := 0; i+n <= len(ta); i++ {
		k := string(ta[i : i+n])
		if mam[k] > 0 {
			mam[k]--
			traf++
		}
	}
	return float64(traf) / float64(len(ta)-n+1)
}

// urwane: słowo przerwane w pół, które wraca w całości („tele”, „telefon”).
func urwane(a, b string) bool {
	return a != b && len([]rune(a)) >= 3 && strings.HasPrefix(b, a)
}

// zgodnePoczatki: dwie kwestie zaczynają się tak samo: pierwsze słowo to samo i co najmniej dwa z trzech pierwszych po kolei.
func zgodnePoczatki(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 || !podobne(a[0], b[0]) {
		return false
	}
	ile, traf, j := min(3, len(a)), 1, 1
	for i := 1; i < ile; i++ {
		for k := j; k < len(b) && k <= j+1; k++ {
			if podobne(a[i], b[k]) {
				traf++
				j = k + 1
				break
			}
		}
	}
	return traf >= min(2, ile)
}

// wykryjPowtorki: zakresy słów do wycięcia. Kolejność: klapsy, dosłowne powtórzenia, falstarty, powtórzone kwestie,
// na końcu kontrola szwów (to samo słowo albo ta sama para słów po obu stronach cięcia).
func wykryjPowtorki(fr []Fraza) ([]slowoPl, []wyciecie) {
	w := splaszcz(fr)
	n := len(w)
	if n < 2 {
		return w, nil
	}
	t := teksty(w)
	wyc := make([]string, n) // typ wycięcia dla każdego słowa albo ""
	oznacz := func(od, do int, typ string) {
		for i := od; i < do; i++ {
			if wyc[i] == "" {
				wyc[i] = typ
			}
		}
	}
	// odstep: ile „czasu mówienia” dzieli dwa słowa; między nagraniami liczę słowami (ok. 0,4 s na słowo)
	odstep := func(a, b int) float64 {
		if w[a].klip == w[b].klip {
			return w[b].od - w[a].od
		}
		return float64(b-a) * 0.4
	}

	// kwestie: zdania (kropka z transkrypcji), dłuższa pauza albo nowe nagranie
	var zd [][2]int
	pocz := 0
	for i := 1; i <= n; i++ {
		if i == n || w[i-1].koniec || w[i].pauza >= pauzaZdania || w[i].klip != w[i-1].klip {
			zd = append(zd, [2]int{pocz, i})
			pocz = i
		}
	}
	zdanieSlowa := make([]int, n)
	for k, z := range zd {
		for i := z[0]; i < z[1]; i++ {
			zdanieSlowa[i] = k
		}
	}

	// 1. klaps: cała fraza między pauzami typu „dobra, jeszcze raz”. Wypada razem z podejściem, które zapowiada.
	for i := 0; i < n; {
		j := i + 1
		for j < n && w[j].fraza == w[i].fraza {
			j++
		}
		calaFraza := w[i].poczFrazy && len(fr[w[i].fraza].Slowa) > 0 && w[j-1].nr == len(fr[w[i].fraza].Slowa)-1
		if calaFraza && toKlaps(t[i:j]) {
			od := i
			if j < n {
				// najwcześniejsza kwestia z ostatniej minuty, która zaczyna się tak jak to, co pada po klapsie
				for k := zdanieSlowa[i]; k >= 0 && k >= zdanieSlowa[i]-kwestiaZdan; k-- {
					p := zd[k][0]
					if p >= i || odstep(p, j) > kwestiaOkno {
						continue
					}
					if zgodnePoczatki(t[p:i], t[j:min(n, j+6)]) {
						od = p
					}
				}
			}
			if od == i && i > 0 && !w[i-1].koniec && w[i-1].klip == w[i].klip {
				od = zd[zdanieSlowa[i-1]][0] // zdanie urwane przed klapsem
			}
			oznacz(od, j, ciecieKlaps)
		}
		i = j
	}

	// 2. dosłowne powtórzenie 3 i więcej słów pod rząd (między kopiami najwyżej dwa słowa-wstawki)
	for i := 0; i < n; i++ {
		if wyc[i] != "" {
			continue
		}
	szukaj:
		for dl := min(12, (n-i)/2); dl >= 3; dl-- {
			for wst := 0; wst <= 2; wst++ {
				j := i + dl + wst
				if j+dl > n || w[j].klip != w[i].klip {
					continue
				}
				dobre := true
				for k := 0; k < wst && dobre; k++ {
					dobre = miedzyPodejsciami[t[i+dl+k]]
				}
				for k := 0; k < dl && dobre; k++ {
					dobre = podobne(t[i+k], t[j+k]) && (k == dl-1 || t[i+k] == t[j+k] || len([]rune(t[i+k])) >= 4)
				}
				if !dobre {
					continue
				}
				przerwa := w[j].pauza >= powtPauza || wst > 0 || urwane(t[i+dl-1], t[j+dl-1])
				if przerwa {
					typ := ciecieDoslowne
					if w[j].pauza >= 0.3 { // po wyraźnej pauzie to nowe podejście do zdania, nie zająknięcie
						typ = ciecieKwestia
					}
					oznacz(i, j, typ)
					i = j - 1
					break szukaj
				}
			}
		}
	}

	// 3. falstart: po przerwie mówca zaczyna od tych samych słów, co chwilę wcześniej
	for j := 1; j < n; j++ {
		if wyc[j] != "" {
			continue
		}
		// słowa-wstawki tuż przed ponownym początkiem („to jest, znaczy, to jest…”) należą do urwanego podejścia
		kon := j
		for kon > 0 && j-kon < 3 && miedzyPodejsciami[t[kon-1]] && w[kon-1].klip == w[j].klip {
			kon--
		}
		konce := []int{j}
		if kon < j {
			konce = append(konce, kon)
		}
		naj := -1
		for i := j - 1; i >= 0 && j-i <= falstartSlow; i-- {
			if w[i].klip != w[j].klip || wyc[i] != "" || odstep(i, j) > falstartOkno {
				break
			}
			for _, k := range konce { // podejście to słowa [i, k), reszta do j to wstawki
				if k <= i {
					continue
				}
				a := t[i:k]
				b := t[j:min(n, j+len(a)+2)]
				if len(b) < len(a) {
					continue
				}
				przerwa := w[j].pauza
				if k < j {
					przerwa = math.Max(przerwa, w[k].pauza) + 0.2 // wstawka sama jest sygnałem
				}
				if len(a) == 1 {
					// jedno słowo: tylko po wyraźnej przerwie i gdy drugie podejście biegnie dalej bez pauzy
					dalej := j+1 < n && w[j+1].pauza < 0.3 && w[j+1].klip == w[j].klip && !w[j].koniec
					trzeci := (j+1 < n && podobne(t[j+1], a[0])) || (i > 0 && t[i-1] == a[0]) // „tanio, tanio, tanio”: wyliczanka
					if !dalej || trzeci || !(a[0] == b[0] || urwane(a[0], b[0])) {
						continue
					}
					if !(przerwa >= powtPauzaSlowo || (urwane(a[0], b[0]) && przerwa >= powtPauza)) {
						continue
					}
				} else {
					ostUrwane := urwane(a[len(a)-1], b[len(a)-1])
					if !(przerwa >= powtPauza || ostUrwane) {
						continue
					}
					// krótkie podejście musi wrócić całe; w dłuższym może się różnić jedno słowo na pięć
					z := zgodnoscPoczatku(a, b)
					if !(z == 1 || (len(a) >= 5 && z >= progFalstart)) || pokrycieZnakow(a, b, 3) < progFalstart-0.1 {
						continue
					}
					// falstart to urwany początek: gdy w środku podejścia kończy się zdanie albo podejście jest
					// dokończonym zdaniem o innej treści, zostaje (całe powtórzone kwestie ocenia krok 4)
					zamkniete := false
					for m := i; m < k-1; m++ {
						zamkniete = zamkniete || w[m].koniec
					}
					if zamkniete || (z < 1 && w[k-1].koniec) {
						continue
					}
				}
				naj = i
			}
		}
		if naj >= 0 {
			oznacz(naj, j, ciecieFalstart)
		}
	}

	// 4. powtórzona kwestia: wcześniejsze zdanie wraca w ciągu minuty z tym samym początkiem. Zostaje ostatnie podejście.
	zywe := func(k int) (int, int, bool) { // część kwestii, która nie została jeszcze wycięta
		od, do := zd[k][0], zd[k][1]
		for od < do && wyc[od] != "" {
			od++
		}
		for do > od && wyc[do-1] != "" {
			do--
		}
		return od, do, do > od
	}
	pasuje := func(ka, kb int, prog float64) bool {
		a0, a1, okA := zywe(ka)
		b0, _, okB := zywe(kb)
		if !okA || !okB || a1-a0 < 3 {
			return false
		}
		// drugie podejście często zaczyna się spójnikiem, którego w pierwszym nie było („A tutaj…”, „No i…”)
		for prz := 0; prz <= 1; prz++ {
			if prz == 1 && (b0+1 >= n || !spojnikNaPoczatku[t[b0]] || w[b0+1].klip != w[b0].klip) {
				break
			}
			okno := t[b0+prz : min(n, b0+prz+(a1-a0)+3)]
			if len(okno) >= a1-a0-1 && zgodnePoczatki(t[a0:a1], okno) && pokrycieZnakow(t[a0:a1], okno, 4) >= prog {
				return true
			}
		}
		return false
	}
	for kb := 1; kb < len(zd); kb++ {
		b0, _, okB := zywe(kb)
		if !okB {
			continue
		}
		for ka := kb - 1; ka >= 0 && ka >= kb-kwestiaZdan; ka-- {
			a0, a1, okA := zywe(ka)
			if !okA {
				continue
			}
			if odstep(a0, b0) > kwestiaOkno {
				break
			}
			sasiad := true // czy między podejściami nie ma żadnej treści, która zostaje
			for i := a1; i < b0; i++ {
				if wyc[i] == "" {
					sasiad = false
					break
				}
			}
			trafione := false
			switch {
			case sasiad:
				trafione = pasuje(ka, kb, progKwestia)
			case a1-a0 >= 6 && pasuje(ka, kb, progKwestiaDal):
				trafione = true
			case ka+1 < kb && kb+1 < len(zd) && pasuje(ka, kb, progKwestia) && pasuje(ka+1, kb+1, progKwestia):
				trafione = true // mówca cofnął się i powtarza cały fragment zdanie po zdaniu
			}
			if trafione {
				oznacz(a0, a1, ciecieKwestia)
			}
		}
	}

	// 5. kontrola szwów: gdy tuż przed cięciem zostały te same słowa, od których zaczyna się ciąg dalszy, wypadają i one
	for i := 0; i < n; i++ {
		if wyc[i] == "" || (i > 0 && wyc[i-1] != "") {
			continue
		}
		j := i
		for j < n && wyc[j] != "" {
			j++
		}
		if j >= n || w[j].klip != w[i].klip {
			continue
		}
		for k := min(3, min(i, n-j)); k >= 1; k-- {
			if i-k < 0 || wyc[i-k] != "" {
				continue
			}
			te := true
			for m := 0; m < k && te; m++ {
				te = wyc[i-k+m] == "" && w[i-k+m].klip == w[i].klip && t[i-k+m] == t[j+m]
			}
			if te && (k >= 2 || len([]rune(t[i-1])) >= 4) {
				oznacz(i-k, i, wyc[i])
				break
			}
		}
	}

	var wynik []wyciecie
	for i := 0; i < n; {
		if wyc[i] == "" {
			i++
			continue
		}
		j := i
		for j < n && wyc[j] == wyc[i] && w[j].klip == w[i].klip {
			j++
		}
		wynik = append(wynik, wyciecie{i, j, wyc[i]})
		i = j
	}
	return w, wynik
}

// zastosujPowtorki: wycięcia przeniesione na frazy. Cała fraza dostaje powód odrzucenia; gdy wycięcie obejmuje początek
// albo koniec frazy, fraza jest skracana i dostaje twardą granicę (cięcie także wtedy, gdy przerwa jest krótka).
// Wycięcie ze środka frazy jest pomijane: takie miejsca dzieli wcześniej rozdzielFrazy.
func zastosujPowtorki(fr []Fraza) {
	w, wyc := wykryjPowtorki(fr)
	if len(wyc) == 0 {
		return
	}
	typ := map[int][]string{} // fraza -> typ wycięcia każdego słowa
	for _, c := range wyc {
		for i := c.od; i < c.do; i++ {
			if typ[w[i].fraza] == nil {
				typ[w[i].fraza] = make([]string, len(fr[w[i].fraza].Slowa))
			}
			typ[w[i].fraza][w[i].nr] = c.typ
		}
	}
	zmienione := map[int]bool{}
	for i := range fr {
		t := typ[i]
		if t == nil {
			continue
		}
		f := &fr[i]
		pocz, kon := 0, len(t)
		for pocz < kon && t[pocz] != "" {
			pocz++
		}
		for kon > pocz && t[kon-1] != "" {
			kon--
		}
		switch {
		case pocz == len(t):
			f.Odrzucona = t[0]
			zmienione[i] = true
		case pocz > 0 || kon < len(t):
			if kon < len(t) {
				f.Do = math.Min(f.Do, cichyPunkt(f.Klip, f.Slowa[kon-1].Do+0.03, 0.08))
				f.TnijPo = true
			}
			if pocz > 0 {
				f.Od = math.Max(f.Od, cichyPunkt(f.Klip, f.Slowa[pocz].Od, 0.08))
				f.TnijPrzed = true
			}
			f.Slowa = append([]Slowo(nil), f.Slowa[pocz:kon]...)
			f.Slowa[0].Od = math.Max(f.Slowa[0].Od, f.Od)
			zmienione[i] = true
		}
	}
	// jedno wycięcie to jedno zdarzenie w raporcie, choćby obejmowało kilka fraz
	for _, c := range wyc {
		for i := c.od; i < c.do; i++ {
			if zmienione[w[i].fraza] {
				fr[w[i].fraza].Ciecia = append(fr[w[i].fraza].Ciecia, c.typ)
				break
			}
		}
	}
}

// nadmiarSlowa: ile sekund „za długo” trwa krótkie słowo. Whisper przypisuje słowu także to, co stało przed nim
// i czego nie zapisał (pauza z szumem, „yyy”, zgubiona powtórka). Liczby, skróty i długie słowa są pomijane.
func nadmiarSlowa(s Slowo) float64 {
	n := []rune(normalizuj(s.Tekst))
	if len(n) == 0 || len(n) > 5 || s.Do-s.Od < rozciagProg {
		return 0
	}
	wielkie := 0
	for _, r := range s.Tekst {
		if unicode.IsDigit(r) {
			return 0
		}
		if unicode.IsUpper(r) {
			wielkie++
		}
	}
	if wielkie > 1 { // skrótowiec czytany literami („NFC”, „UV”)
		return 0
	}
	zwykle := 0.30 + 0.07*float64(len(n))
	return math.Max(0, s.Do-s.Od-zwykle)
}

// rozdzielFrazy: frazy jednego nagrania dzielone tam, gdzie trzeba będzie ciąć w środku frazy: wokół wtrąceń („yyy”
// zapisane przez transkrypcję), przed rozciągniętym słowem i na granicach wykrytych powtórek. Sam podział niczego nie
// wycina (sąsiednie części bez przerwy sklejają się z powrotem w jeden odcinek); wycina dopiero zaplanujMowe.
// rozciagniete[{fraza, słowo}] = nadmiar czasu słowa w sekundach (z nadmiarSlowa).
func rozdzielFrazy(fr []Fraza, rozciagniete map[[2]int]float64) []Fraza {
	type punkt struct {
		odstep  bool // nowa część zaczyna się później niż kończy poprzednia (rozciągnięte słowo)
		zdarzen bool
	}
	// podziel: pkt[nr] = nowa część zaczyna się od słowa nr
	podziel := func(f Fraza, pkt map[int]punkt) []Fraza {
		var nry []int
		for nr := range pkt {
			if nr > 0 && nr < len(f.Slowa) {
				nry = append(nry, nr)
			}
		}
		if len(nry) == 0 {
			return []Fraza{f}
		}
		sort.Ints(nry)
		var cz []Fraza
		pocz, od := 0, f.Od
		ciecia := f.Ciecia
		for k := 0; k <= len(nry); k++ {
			kon := len(f.Slowa)
			if k < len(nry) {
				kon = nry[k]
			}
			nowa := Fraza{Klip: f.Klip, Od: od, Do: f.Do, Slowa: append([]Slowo(nil), f.Slowa[pocz:kon]...), Ciecia: ciecia}
			ciecia = nil
			if k < len(nry) {
				granica := cichyPunkt(f.Klip, f.Slowa[kon-1].Do, 0.08)
				granica = math.Min(math.Max(granica, f.Slowa[pocz].Od+0.04), f.Slowa[kon].Do-0.04)
				granica = math.Max(granica, od)
				nowa.Do = granica
				od = granica
				if p := pkt[kon]; p.odstep {
					od = math.Max(granica, f.Slowa[kon].Od)
					if p.zdarzen {
						ciecia = []string{ciecieRozciag}
					}
				}
			}
			nowa.Slowa[0].Od = math.Max(nowa.Slowa[0].Od, nowa.Od)
			if l := len(nowa.Slowa) - 1; nowa.Slowa[l].Do > nowa.Do+0.12 {
				nowa.Slowa[l].Do = math.Max(nowa.Do, nowa.Slowa[l].Od+0.04)
			}
			cz = append(cz, nowa)
			pocz = kon
		}
		return cz
	}

	// a) wtrącenia w środku frazy i rozciągnięte słowa
	var po []Fraza
	for i, f := range fr {
		pkt := map[int]punkt{}
		for j := range f.Slowa {
			if wtracenieW(f.Slowa, j) && len(f.Slowa) > 1 {
				if _, jest := pkt[j]; !jest {
					pkt[j] = punkt{}
				}
				pkt[j+1] = punkt{}
			}
			if nad := rozciagniete[[2]int{i, j}]; j > 0 && nad > 0 {
				pkt[j] = punkt{odstep: true, zdarzen: nad >= rozciagCiecie}
			}
		}
		if nad := rozciagniete[[2]int{i, 0}]; nad >= rozciagCiecie && len(f.Slowa) > 0 {
			f.Ciecia = append(f.Ciecia, ciecieRozciag)
		}
		po = append(po, podziel(f, pkt)...)
	}

	// b) granice powtórek (drugi przebieg: po pierwszym podziale kontrola szwów może dołożyć granicę)
	for runda := 0; runda < 2; runda++ {
		w, wyc := wykryjPowtorki(po)
		punkty := map[int]map[int]punkt{}
		dodaj := func(s slowoPl) {
			if s.nr == 0 {
				return
			}
			if punkty[s.fraza] == nil {
				punkty[s.fraza] = map[int]punkt{}
			}
			punkty[s.fraza][s.nr] = punkt{}
		}
		for _, c := range wyc {
			dodaj(w[c.od])
			if c.do < len(w) {
				dodaj(w[c.do])
			}
		}
		if len(punkty) == 0 {
			break
		}
		var nowe []Fraza
		for i, f := range po {
			nowe = append(nowe, podziel(f, punkty[i])...)
		}
		po = nowe
	}
	return po
}

// Profil głośności nagrań (z wykryjMowe): pozwala postawić cięcie w środku frazy w najcichszym miejscu w pobliżu
// końca słowa, a nie dokładnie w czasie z transkrypcji (dokładność ok. 0,1 s). wykryjMowe nie zna numeru nagrania,
// więc zostawia profil „ostatni”, a frazyZMowy przypisuje go do nagrania, gdy dostaje te same przedziały mowy.
var profile struct {
	mu      sync.Mutex
	ostatni []float32  // poziom w dB co 10 ms
	klucz   *Przedzial // pierwszy przedział mowy zwrócony razem z profilem
	klip    map[int][]float32
}

func zapamietajProfil(mowa []Przedzial, db []float64) {
	if len(mowa) == 0 {
		return
	}
	p := make([]float32, len(db))
	for i, v := range db {
		p[i] = float32(v)
	}
	profile.mu.Lock()
	profile.ostatni, profile.klucz = p, &mowa[0]
	profile.mu.Unlock()
}

func przypiszProfil(klip int, mowa []Przedzial) {
	profile.mu.Lock()
	defer profile.mu.Unlock()
	if profile.klip == nil {
		profile.klip = map[int][]float32{}
	}
	delete(profile.klip, klip)
	if len(mowa) > 0 && profile.klucz == &mowa[0] {
		profile.klip[klip] = profile.ostatni
	}
}

// cichyPunkt: najcichsze miejsce (średnia z 30 ms) w odległości do promienia od czasu t. Bez profilu zwraca t.
func cichyPunkt(klip int, t, promien float64) float64 {
	profile.mu.Lock()
	p := profile.klip[klip]
	profile.mu.Unlock()
	if len(p) < 4 {
		return t
	}
	sr := int(math.Round(t * 100))
	pr := int(math.Round(promien * 100))
	naj, najV := -1, float32(math.Inf(1))
	for i := max(1, sr-pr); i <= sr+pr && i+1 < len(p); i++ {
		// przy tej samej głośności wygrywa miejsce bliżej czasu z transkrypcji
		v := (p[i-1]+p[i]+p[i+1])/3 + float32(math.Abs(float64(i-sr)))*0.05
		if v < najV {
			naj, najV = i, v
		}
	}
	if naj < 0 {
		return t
	}
	return float64(naj)/100 + 0.01
}

// wtracenieW: słowo nr jest wtrąceniem („yyy”, „mmm”). „mm” po liczbie to milimetry, nie pomruk.
func wtracenieW(sl []Slowo, nr int) bool {
	if !toWtracenie(sl[nr].Tekst) {
		return false
	}
	return nr == 0 || strings.IndexFunc(sl[nr-1].Tekst, unicode.IsDigit) < 0
}
