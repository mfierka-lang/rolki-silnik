// Napisy mowy w stylu marki: porcje po 2-4 słowa, jedno słowo-klucz w zdaniu w kolorze i kroju akcentu,
// tekst na zaokrąglonej karcie, krótkie wejście z lekkim powiększeniem. Format ASS renderowany przez ffmpeg (libass)
// z fontów szablonu, więc na Macu i na Windowsie wygląda identycznie.
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Miara: szerokość tekstu w pikselach dla wielkości fontu w rozumieniu ASS (wysokość linii, nie em).
type Miara struct {
	f     *sfnt.Font
	buf   sfnt.Buffer
	upem  float64
	linia float64 // usWinAscent + usWinDescent w jednostkach fontu
}

func wczytajMiare(plik string) (*Miara, error) {
	b, err := os.ReadFile(plik)
	if err != nil {
		return nil, fmt.Errorf("brak fontu %s", plik)
	}
	f, err := sfnt.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("zły plik fontu %s: %v", plik, err)
	}
	m := &Miara{f: f, upem: 1000, linia: 1200}
	if len(b) > 12 {
		ile := int(binary.BigEndian.Uint16(b[4:6]))
		var asc, desc float64
		for i := 0; i < ile && 12+16*i+16 <= len(b); i++ {
			r := b[12+16*i:]
			tag := string(r[0:4])
			off := int(binary.BigEndian.Uint32(r[8:12]))
			switch {
			case tag == "head" && off+20 <= len(b):
				m.upem = float64(binary.BigEndian.Uint16(b[off+18:]))
			case tag == "OS/2" && off+78 <= len(b):
				if v := float64(binary.BigEndian.Uint16(b[off+74:])) + float64(binary.BigEndian.Uint16(b[off+76:])); v > 0 {
					m.linia = v
				}
			case tag == "hhea" && off+8 <= len(b):
				asc = float64(int16(binary.BigEndian.Uint16(b[off+4:])))
				desc = float64(int16(binary.BigEndian.Uint16(b[off+6:])))
			}
		}
		if m.linia == 1200 && asc-desc > 0 {
			m.linia = asc - desc
		}
	}
	return m, nil
}

func (m *Miara) Szerokosc(tekst string, rozmiar float64) float64 {
	ppem := rozmiar * m.upem / m.linia
	var suma float64
	for _, r := range tekst {
		idx, err := m.f.GlyphIndex(&m.buf, r)
		if err != nil {
			continue
		}
		adv, err := m.f.GlyphAdvance(&m.buf, idx, fixed.I(int(m.upem)), font.HintingNone)
		if err != nil {
			continue
		}
		suma += float64(adv) / 64
	}
	return suma / m.upem * ppem
}

func kolorASS(hex string) string {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		h = "FFFFFF"
	}
	return "&H" + strings.ToUpper(h[4:6]+h[2:4]+h[0:2]) + "&"
}

func czasASS(t float64) string {
	if t < 0 {
		t = 0
	}
	cs := int(math.Round(t * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}

var slowaPuste = map[string]bool{"i": true, "a": true, "w": true, "z": true, "na": true, "do": true, "to": true, "że": true, "się": true, "jak": true,
	"od": true, "po": true, "ale": true, "ma": true, "jest": true, "nie": true, "o": true, "u": true, "za": true, "co": true, "czy": true, "też": true,
	"już": true, "dla": true, "przez": true, "oraz": true, "albo": true, "lub": true, "tak": true, "ten": true, "ta": true, "te": true, "tym": true,
	"tego": true, "są": true, "by": true, "bo": true, "więc": true, "ze": true, "we": true, "go": true, "mu": true, "jej": true, "ich": true, "nas": true,
	"wam": true, "nam": true, "każdy": true, "każda": true, "razu": true, "temu": true, "dzięki": true, "bez": true, "pod": true, "nad": true, "przy": true}

// pokaz: słowo tak, jak ma wyglądać na ekranie (bez przecinków i kropek na końcu, z poprawkami nazw).
func pokaz(tekst string, wersaliki bool) string {
	t := strings.TrimSpace(tekst)
	t = strings.Trim(t, "\"'„”«»()[]{}")
	for len(t) > 1 && strings.ContainsRune(".,;:…", rune(t[len(t)-1])) {
		t = t[:len(t)-1]
	}
	t = strings.NewReplacer("{", "", "}", "", "\\", "").Replace(t)
	switch strings.ToLower(t) {
	case "zbliska.pl", "zbliżka.pl", "zblizka.pl", "zbliska", "zbliżka":
		if strings.Contains(t, ".") {
			t = "zblizka.pl"
		} else {
			t = "Zbliżka"
		}
	}
	if wersaliki {
		t = strings.ToUpper(t)
	}
	return t
}

func konczyZdanie(tekst string) bool {
	t := strings.TrimRight(strings.TrimSpace(tekst), "\"'”»)")
	return strings.HasSuffix(t, ".") || strings.HasSuffix(t, "!") || strings.HasSuffix(t, "?") || strings.HasSuffix(t, "…")
}

func ocenaSlowa(tekst string, slownik []string) int {
	n := normalizuj(tekst)
	if n == "" || slowaPuste[strings.ToLower(strings.Trim(tekst, ".,!?;:…\"'"))] {
		return -100
	}
	oc := 0
	for _, s := range slownik {
		if strings.HasPrefix(n, s) {
			oc += 5
			break
		}
	}
	for _, r := range n {
		if unicode.IsDigit(r) {
			oc += 3
			break
		}
	}
	if len([]rune(n)) >= 8 {
		oc++
	}
	return oc
}

type porcja struct {
	slowa  []string
	akcent int // indeks słowa-klucza w porcji albo -1
	od, do float64
}

// podzielNaPorcje: zdania -> porcje po kilka słów mieszczące się w szerokości; w każdym zdaniu jedno słowo-klucz.
// Podział zdania wybiera najtańszy układ: porcja nie kończy się spójnikiem ani przyimkiem ("i", "na", "nie"),
// ostatnie słowo nie zostaje samo, przecinek to dobre miejsce cięcia.
func podzielNaPorcje(slowa []Slowo, st *Styl, mTekst, mAkcent *Miara) []porcja {
	var porcje []porcja
	n := st.Napisy
	spacja := mTekst.Szerokosc(" ", n.Tekst.Rozmiar)
	poczatekZdania := true
	for i := 0; i < len(slowa); {
		// zdanie: do słowa z kropką albo do dłuższej przerwy
		j := i
		for j < len(slowa) {
			koniec := konczyZdanie(slowa[j].Tekst) || (j+1 < len(slowa) && slowa[j+1].Od-slowa[j].Do > 0.7)
			j++
			if koniec {
				break
			}
		}
		type element struct {
			tekst   string
			szer    float64
			od, do  float64
			klucz   bool
			pusty   bool
			przecin bool
			przerwa bool // dłuższa pauza przed słowem
		}
		var el []element
		klucz, najOc := -1, 0
		for k, s := range slowa[i:j] {
			if oc := ocenaSlowa(s.Tekst, st.Slownik); oc > najOc || (oc == najOc && oc > 0 && k > klucz) {
				klucz, najOc = k, oc
			}
		}
		for k, s := range slowa[i:j] {
			tekst := pokaz(s.Tekst, n.Wersaliki)
			if tekst == "" {
				continue
			}
			if poczatekZdania && !n.Wersaliki { // po wyciętym wtrąceniu zdanie i tak zaczyna się wielką literą
				r := []rune(tekst)
				r[0] = unicode.ToUpper(r[0])
				tekst = string(r)
			}
			poczatekZdania = konczyZdanie(s.Tekst)
			e := element{tekst: tekst, od: s.Od, do: s.Do, klucz: k == klucz}
			e.szer = mTekst.Szerokosc(tekst, n.Tekst.Rozmiar)
			if e.klucz {
				e.szer = mAkcent.Szerokosc(tekst, n.Akcent.Rozmiar)
			}
			e.pusty = slowaPuste[strings.ToLower(strings.Trim(s.Tekst, ".,!?;:…\"'"))]
			e.przecin = strings.HasSuffix(strings.TrimSpace(s.Tekst), ",")
			e.przerwa = len(el) > 0 && s.Od-el[len(el)-1].do > 0.45
			el = append(el, e)
		}
		i = j
		ile := len(el)
		if ile == 0 {
			continue
		}
		// najtańszy podział: koszt[k] = najlepszy koszt dla pierwszych k słów
		koszt := make([]float64, ile+1)
		skad := make([]int, ile+1)
		for k := 1; k <= ile; k++ {
			koszt[k] = math.Inf(1)
			szer := 0.0
			for p := k - 1; p >= 0 && k-p <= n.MaksSlow; p-- { // porcja = słowa p..k-1
				if p < k-1 {
					if el[p+1].przerwa {
						break // pauza w środku porcji: napis wisiałby w ciszy
					}
					szer += spacja
				}
				szer += el[p].szer
				if szer > n.MaksSzer && k-p > 1 {
					break
				}
				c := 1.0
				dl := k - p
				if dl == 1 && ile > 1 {
					c += 1.2
					if len([]rune(el[p].tekst)) >= 10 {
						c -= 0.9
					}
				}
				if k < ile && el[k-1].pusty {
					c += 2.0
				}
				if el[k-1].przecin {
					c -= 0.6
				}
				for q := p; q < k-1; q++ {
					if el[q].przecin {
						c += 0.8
					}
				}
				if koszt[p]+c < koszt[k] {
					koszt[k], skad[k] = koszt[p]+c, p
				}
			}
		}
		var granice []int
		for k := ile; k > 0; k = skad[k] {
			granice = append([]int{k}, granice...)
		}
		p := 0
		for _, k := range granice {
			po := porcja{akcent: -1, od: el[p].od, do: el[k-1].do}
			for q := p; q < k; q++ {
				if el[q].klucz {
					po.akcent = len(po.slowa)
				}
				po.slowa = append(po.slowa, el[q].tekst)
			}
			porcje = append(porcje, po)
			p = k
		}
	}
	// czasy: porcja stoi do następnej, gdy przerwa jest krótka; inaczej znika chwilę po ostatnim słowie
	for k := range porcje {
		p := &porcje[k]
		p.od = math.Max(0, p.od-0.03)
		koniec := p.do + 0.30
		if k+1 < len(porcje) {
			nast := math.Max(0, porcje[k+1].od-0.03)
			if nast-p.do < 0.35 || koniec > nast {
				koniec = nast
			}
		}
		if koniec-p.od < 0.25 {
			koniec = p.od + 0.25
		}
		p.do = koniec
	}
	return porcje
}

// kartaASS: zaokrąglony prostokąt jako rysunek wektorowy ASS (współrzędne od 0, wyśrodkowany przez \an5).
func kartaASS(w, h, r float64) string {
	r = math.Min(r, math.Min(w, h)/2)
	k := r * 0.5523
	f := func(v float64) string { return fmt.Sprintf("%.0f", v) }
	return "m " + f(r) + " 0 l " + f(w-r) + " 0 b " + f(w-r+k) + " 0 " + f(w) + " " + f(r-k) + " " + f(w) + " " + f(r) +
		" l " + f(w) + " " + f(h-r) + " b " + f(w) + " " + f(h-r+k) + " " + f(w-r+k) + " " + f(h) + " " + f(w-r) + " " + f(h) +
		" l " + f(r) + " " + f(h) + " b " + f(r-k) + " " + f(h) + " 0 " + f(h-r+k) + " 0 " + f(h-r) +
		" l 0 " + f(r) + " b 0 " + f(r-k) + " " + f(r-k) + " 0 " + f(r) + " 0"
}

func bw(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// zapiszNapisy: plik ASS z napisami mowy. Zwraca listę plików fontów potrzebnych do renderu.
// Opcjonalny nagłówek (hak) to te same dwa bloki koloru co w rolkach z ujęć, widoczne do chwili hakKoniec.
func zapiszNapisy(plik string, slowa []Slowo, st *Styl, hak []string, hakKoniec float64) ([]string, error) {
	n := st.Napisy
	plikT := filepath.Join(st.Katalog, n.Tekst.Plik)
	plikA := filepath.Join(st.Katalog, n.Akcent.Plik)
	mT, err := wczytajMiare(plikT)
	if err != nil {
		return nil, err
	}
	mA, err := wczytajMiare(plikA)
	if err != nil {
		return nil, err
	}
	porcje := podzielNaPorcje(slowa, st, mT, mA)
	cx, cy := float64(st.Szer)/2, float64(st.Wys)*n.Y
	// to samo wejście co hasła na ujęciach (plansze.go), tylko krótsze: porcja mowy żyje czasem pół sekundy
	pis := &pisarzASS{st: st, czasDruku: n.WejscieMs}
	if pis.czasDruku <= 0 {
		pis.czasDruku = 170
	}
	pis.naglowek()
	plikH := filepath.Join(st.Katalog, st.Hak.Font.Plik)
	if len(hak) > 0 {
		mH, err := wczytajMiare(plikH)
		if err != nil {
			return nil, err
		}
		pis.czasDruku = 0
		pis.naglowekRolki(hak, mH, hakKoniec)
		pis.czasDruku = n.WejscieMs
	}
	spacja := mT.Szerokosc(" ", n.Tekst.Rozmiar)
	bazowy := krojASS(n.Tekst, n.Tekst.Rozmiar, n.Tekst.Kolor)
	akcent := krojASS(n.Akcent, n.Akcent.Rozmiar, n.Akcent.Kolor)
	krycie := n.Karta.Krycie
	if !n.Karta.Wlaczona {
		krycie = 0
	}
	for _, p := range porcje {
		szer := 0.0
		var t strings.Builder
		t.WriteString(bazowy)
		for i, s := range p.slowa {
			if i > 0 {
				t.WriteString(" ")
				szer += spacja
			}
			if i == p.akcent {
				t.WriteString(akcent + s + bazowy)
				szer += mA.Szerokosc(s, n.Akcent.Rozmiar)
			} else {
				t.WriteString(s)
				szer += mT.Szerokosc(s, n.Tekst.Rozmiar)
			}
		}
		if czasASS(p.od) == czasASS(p.do) {
			continue
		}
		kw := szer + 2*n.Karta.OdstepX
		kh := math.Max(n.Tekst.Rozmiar, n.Akcent.Rozmiar)*0.86 + 2*n.Karta.OdstepY
		x1, y1 := cx-kw/2, cy+n.Karta.Dy-kh/2
		pis.element(p.od, p.do, x1, y1, x1+kw, y1+kh, n.Karta.Promien, n.Karta.Kolor, krycie, t.String(), -n.Karta.Dy)
	}
	if err := os.WriteFile(plik, []byte(pis.b.String()), 0o644); err != nil {
		return nil, err
	}
	return []string{plikT, plikA, plikH}, nil
}
