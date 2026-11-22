// Wyróżnienia w filmie mówionym (wzór od właściciela z 20.11.2026: „większe napisy, jak coś się dzieje”):
//   - liczba z jednostką („od 6 zł”, „50 sztuk”, „300 kilogramów”) wyskakuje nad napisami jako duża liczba w kolorze
//     akcentu, która przez 0,7 s nabija się w górę od zera (u wzoru: 1 015 -> 4 799 zł w ok. sekundę);
//   - co kilka sekund słowo-klucz (ze słownika marki albo długie słowo) zamiast zwykłej porcji napisu: samo, wielkie,
//     wersalikami, na tej samej karcie.
package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type liczbaMowy struct {
	od, do    float64
	wartosc   float64
	ulamki    int // miejsca po przecinku (0 = liczba całkowita, nabijana w górę)
	jednostka string
}

var reLiczba = regexp.MustCompile(`^\d+([.,]\d+)?$`)

// jednostki, przy których liczba coś znaczy; wartość = jak ją pokazać
var jednostkiLiczb = map[string]string{
	"zł": "zł", "zl": "zł", "złotych": "zł", "złote": "zł", "złoty": "zł", "pln": "zł", "groszy": "gr",
	"sztuk": "sztuk", "sztuki": "sztuki", "szt": "szt.", "kilogramów": "kg", "kilogramy": "kg", "kg": "kg", "gramów": "g",
	"godzin": "godzin", "godziny": "godziny", "minut": "minut", "minuty": "minuty", "sekund": "sekund", "sekundy": "sekundy",
	"dni": "dni", "dniach": "dni", "tygodni": "tygodni", "lat": "lat", "lata": "lata", "procent": "%", "%": "%",
	"metrów": "m", "metry": "m", "centymetrów": "cm", "cm": "cm", "milimetrów": "mm", "mm": "mm",
	"osób": "osób", "klientów": "klientów", "opinii": "opinii", "razy": "razy", "kolorów": "kolorów", "zamówień": "zamówień",
}

func oczyscSlowo(s string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(s), ".,!?;:…\"'„”()"))
}

// znajdzLiczby: liczby z jednostką w wypowiedzi (czasy w gotowym filmie). Bez jednostki tylko liczby od 10 wzwyż,
// które nie wyglądają na rok. Między liczbami co najmniej 3 s, żadna przed końcem nagłówka.
func znajdzLiczby(slowa []Slowo, odKiedy float64) []liczbaMowy {
	var out []liczbaMowy
	for i := 0; i < len(slowa); i++ {
		t := oczyscSlowo(slowa[i].Tekst)
		if !reLiczba.MatchString(t) {
			continue
		}
		od := slowa[i].Od
		tekst := strings.ReplaceAll(t, ",", ".")
		j := i + 1
		// tysiące zapisane osobno: „4 799”
		for j < len(slowa) && !strings.Contains(tekst, ".") {
			n := oczyscSlowo(slowa[j].Tekst)
			if len(n) != 3 || !reLiczba.MatchString(n) || slowa[j].Od-slowa[j-1].Do > 0.4 {
				break
			}
			tekst += n
			j++
		}
		v, err := strconv.ParseFloat(tekst, 64)
		if err != nil || v <= 0 {
			continue
		}
		ulamki := 0
		if k := strings.IndexByte(tekst, '.'); k >= 0 {
			ulamki = len(tekst) - k - 1
		}
		jedn, koniec := "", slowa[j-1].Do
		if j < len(slowa) {
			if u, ok := jednostkiLiczb[oczyscSlowo(slowa[j].Tekst)]; ok {
				jedn, koniec = u, slowa[j].Do
			}
		}
		if jedn == "" && (v < 10 || (v >= 1900 && v <= 2100 && ulamki == 0)) {
			continue
		}
		if od < odKiedy || (len(out) > 0 && od-out[len(out)-1].od < 3) {
			continue
		}
		out = append(out, liczbaMowy{od: od, do: math.Max(od+1.9, koniec+0.6), wartosc: v, ulamki: ulamki, jednostka: jedn})
		i = j - 1
	}
	for k := 0; k+1 < len(out); k++ {
		out[k].do = math.Min(out[k].do, out[k+1].od-0.1)
	}
	return out
}

// zapisLiczby: liczba w zapisie polskim, tysiące oddzielone twardą spacją ASS (\h), przecinek dziesiętny.
func zapisLiczby(v float64, ulamki int) string {
	s := strconv.FormatFloat(v, 'f', ulamki, 64)
	cz, ul, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range cz {
		if i > 0 && (len(cz)-i)%3 == 0 && len(cz) > 4 {
			b.WriteString(`\h`)
		}
		b.WriteRune(r)
	}
	if ul != "" {
		b.WriteString("," + ul)
	}
	return b.String()
}

// liczbyASS: karty z nabijaną liczbą nad napisami. y = środek karty.
// dol: dolna krawędź karty z liczbą (karta stoi tuż nad napisami).
func (p *pisarzASS) liczbyASS(liczby []liczbaMowy, mT *Miara, dol float64) {
	n := p.st.Napisy
	rL, rJ := n.Tekst.Rozmiar*1.9, n.Tekst.Rozmiar*1.05
	cx := float64(p.st.Szer) / 2
	for _, l := range liczby {
		koncowa := zapisLiczby(l.wartosc, l.ulamki)
		szer := mT.Szerokosc(strings.ReplaceAll(koncowa, `\h`, " "), rL)
		if l.jednostka != "" {
			szer += mT.Szerokosc(" "+l.jednostka, rJ)
		}
		// za szeroka: mniejsze litery
		if maks := n.MaksSzer * 1.1; szer > maks {
			k := maks / szer
			rL, rJ, szer = rL*k, rJ*k, maks
		}
		kw := szer + 2*n.Karta.OdstepX*1.4
		kh := rL*0.86 + 2*n.Karta.OdstepY*1.3
		x1, y1 := cx-kw/2, dol-kh
		tekst := func(liczba string) string {
			t := krojASS(n.Tekst, rL, n.Akcent.Kolor) + liczba
			if l.jednostka != "" {
				t += krojASS(n.Tekst, rJ, n.Tekst.Kolor) + `\h` + l.jednostka
			}
			return t
		}
		// nabijanie: 12 kroków przez 0,7 s, coraz wolniej (koniec łagodny), potem wartość końcowa do końca
		kroki := 12
		if l.ulamki > 0 || l.wartosc < 10 {
			kroki = 1
		}
		trwa := 0.7
		for k := 0; k < kroki; k++ {
			t0 := l.od + trwa*float64(k)/float64(kroki)
			t1 := l.od + trwa*float64(k+1)/float64(kroki)
			if k == kroki-1 {
				t1 = l.do
			}
			u := float64(k+1) / float64(kroki)
			v := l.wartosc * (1 - math.Pow(1-u, 3))
			napis := koncowa
			if k < kroki-1 {
				napis = zapisLiczby(math.Round(v), 0)
			}
			if k == 0 {
				p.element(t0, t1, x1, y1, x1+kw, y1+kh, n.Karta.Promien*1.3, n.Karta.Kolor, n.Karta.Krycie, tekst(napis), -n.Karta.Dy)
				continue
			}
			p.kartaStala(t0, t1, x1, y1, x1+kw, y1+kh, n.Karta.Promien*1.3, n.Karta.Kolor, n.Karta.Krycie, tekst(napis), -n.Karta.Dy)
		}
	}
}

// kartaStala: karta z napisem bez animacji wejścia (kolejne klatki nabijanej liczby).
func (p *pisarzASS) kartaStala(od, do, x1, y1, x2, y2, promien float64, kolor string, krycie float64, tekst string, dyTekstu float64) {
	if czasASS(od) == czasASS(do) {
		return
	}
	w, h := x2-x1, y2-y1
	alfa := fmt.Sprintf("&H%02X&", int(math.Round((1-krycie)*255)))
	fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{\\an7\\pos(%.0f,%.0f)\\bord0\\shad0\\1c%s\\1a%s\\p1}%s{\\p0}\n",
		p.warstwa, czasASS(od), czasASS(do), x1, y1, kolorASS(kolor), alfa, kartaASS(w, h, promien))
	fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{\\an5\\pos(%.0f,%.0f)\\bord0\\shad0}%s\n",
		p.warstwa+1, czasASS(od), czasASS(do), (x1+x2)/2, (y1+y2)/2+dyTekstu, tekst)
	p.warstwa += 3
}

// wybierzWielkieSlowa: porcje, w których słowo-klucz staje się wielkim słowem, co najmniej 6 s od poprzedniego,
// nie w czasie nagłówka ani nabijanej liczby, bez cyfr. Z kartą treści (zKarty): słowo-akcent ważnego zdania
// (waga od 2, od 4 liter), najpierw zdania o wadze 3. Bez karty: słowo ze słownika marki albo długie (od 9 liter).
func wybierzWielkieSlowa(porcje []porcja, slownik []string, odKiedy float64, liczby []liczbaMowy, zKarty bool) map[int]bool {
	wynik := map[int]bool{}
	nadaje := func(p porcja, minWaga int) bool {
		if p.akcent < 0 || p.od < odKiedy || p.do-p.od < 0.45 {
			return false
		}
		s := p.slowa[p.akcent]
		if strings.ContainsAny(s, "0123456789") {
			return false
		}
		if zKarty {
			if p.karta == nil || p.karta.Waga < minWaga || p.karta.Rodzaj == "liczba" || utf8.RuneCountInString(s) < 4 {
				return false
			}
		} else if oc := ocenaSlowa(s, slownik); !(oc >= 5 || (oc >= 1 && utf8.RuneCountInString(s) >= 9)) {
			return false
		}
		for _, l := range liczby {
			if p.od < l.do+0.3 && p.do > l.od-0.3 {
				return false
			}
		}
		return true
	}
	wolne := func(t float64) bool {
		for i := range wynik {
			if math.Abs(porcje[i].od-t) < 6 {
				return false
			}
		}
		return true
	}
	// z kartą dwa przejścia: najpierw najważniejsze zdania, potem ważne w wolnych miejscach
	progi := []int{0}
	if zKarty {
		progi = []int{3, 2}
	}
	for _, prog := range progi {
		for i, p := range porcje {
			if nadaje(p, prog) && wolne(p.od) {
				wynik[i] = true
			}
		}
	}
	return wynik
}
