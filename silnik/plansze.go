// Napisy do rolki z samych ujęć: nagłówek-hak i krótkie hasła na ujęciach, w jednym pliku ASS.
// Wygląd (napisy.rodzaj w szablonie): "karta" = ciemna zaokrąglona karta, słowo-klucz kursywą w kolorze akcentu;
// "bloki" = każda część hasła w osobnym bloku koloru, tak jak nagłówek.
// Wejście (napisy.wejscie): "druk" = element drukuje się warstwami od dołu, a po jego krawędzi przesuwa się jasna
// linia dyszy (ten sam motyw co na stronie zblizka.pl); "wjazd" = krótkie wysunięcie z dołu.
// Wszystko, co pojawia się w jednej rolce, używa jednego wyglądu i jednego wejścia.
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NapisUjecia: hasło widoczne od Od do Do (sekundy w gotowej rolce). Gwiazdki oznaczają słowo-klucz: "Szkło *wjeżdża*".
type NapisUjecia struct {
	Tekst  string
	Od, Do float64
}

type segment struct {
	tekst  string
	akcent bool
}

// czyscTekst: bez znaków sterujących ASS i bez długich myślników (wymóg briefu).
func czyscTekst(t string) string {
	t = strings.NewReplacer("{", "", "}", "", "\\", "", "—", "-", "–", "-").Replace(t)
	return strings.Join(strings.Fields(t), " ")
}

func segmenty(t string) []segment {
	var out []segment
	for i, cz := range strings.Split(t, "*") {
		if cz = czyscTekst(cz); cz != "" {
			out = append(out, segment{cz, i%2 == 1})
		}
	}
	return out
}

// rozlozNapisy: "2:Dzień *montażu*|3-4:Ekipa *na miejscu*" -> hasła z czasami ujęć (ujęcia liczone od 1).
// Hasło bez numeru trafia na kolejne wolne ujęcie (pierwsze zajmuje nagłówek, gdy jest).
func rozlozNapisy(spec string, czasy []float64, maHak bool) []NapisUjecia {
	pocz := make([]float64, len(czasy)+1)
	for i, c := range czasy {
		pocz[i+1] = pocz[i] + c
	}
	nast := 1
	if maHak {
		nast = 2
	}
	var out []NapisUjecia
	for _, cz := range strings.Split(spec, "|") {
		cz = strings.TrimSpace(cz)
		a, b := nast, nast
		if i := strings.Index(cz, ":"); i > 0 {
			zak := strings.SplitN(strings.TrimSpace(cz[:i]), "-", 2)
			x, e1 := strconv.Atoi(strings.TrimSpace(zak[0]))
			y, e2 := x, error(nil)
			if len(zak) == 2 {
				y, e2 = strconv.Atoi(strings.TrimSpace(zak[1]))
			}
			if e1 == nil && e2 == nil {
				a, b = x, y
				cz = strings.TrimSpace(cz[i+1:])
			}
		}
		nast = b + 1
		if cz == "" || a < 1 || b > len(czasy) || a > b {
			continue
		}
		out = append(out, NapisUjecia{cz, pocz[a-1], pocz[b]})
	}
	return out
}

type pisarzASS struct {
	b         strings.Builder
	st        *Styl
	warstwa   int
	czasDruku int // ms; 0 = 260 (hasła i nagłówek), napisy mowy podają krótszy
}

func (p *pisarzASS) naglowek() {
	st := p.st
	fmt.Fprintf(&p.b, "[Script Info]\nScriptType: v4.00+\nPlayResX: %d\nPlayResY: %d\nWrapStyle: 2\nScaledBorderAndShadow: yes\nYCbCr Matrix: TV.709\n\n", st.Szer, st.Wys)
	p.b.WriteString("[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(&p.b, "Style: T,%s,%.0f,&H00FFFFFF&,&H00FFFFFF&,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,0,0,5,0,0,0,1\n\n", st.Napisy.Tekst.Rodzina, st.Napisy.Tekst.Rozmiar)
	p.b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
}

// element: zaokrąglony prostokąt z tekstem na środku. Wchodzi w chwili od (wejście z szablonu) i znika w do
// bez wyjścia: hasła kończą się na cięciu.
func (p *pisarzASS) element(od, do, x1, y1, x2, y2, promien float64, kolor string, krycie float64, tekst string, dyTekstu float64) {
	w, h := x2-x1, y2-y1
	cx, cy := (x1+x2)/2, (y1+y2)/2
	poz := func(x, y float64) string { return fmt.Sprintf(`\pos(%.0f,%.0f)`, x, y) }
	dod := ""
	if p.st.Napisy.Wejscie == "druk" {
		D := p.czasDruku // ms; na stronie druk też jest szybki
		if D <= 0 {
			D = 260
		}
		dod = fmt.Sprintf(`\clip(%.0f,%.0f,%.0f,%.0f)\t(0,%d,\clip(%.0f,%.0f,%.0f,%.0f))`,
			x1-4, y2+2, x2+4, y2+4, D, x1-4, y1-4, x2+4, y2+4)
		// linia dyszy: jedzie po górnej krawędzi wydrukowanej części i gaśnie
		linia := fmt.Sprintf(`\an7\move(%.0f,%.0f,%.0f,%.0f,0,%d)\fad(0,90)\blur3\bord0\shad0\1c%s\p1`,
			x1, y2-3, x1, y1-3, D, kolorASS(p.st.Napisy.Akcent.Kolor))
		fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{%s}%s{\\p0}\n", p.warstwa+2, czasASS(od), czasASS(math.Min(do, od+float64(D)/1000+0.09)), linia, kartaASS(w, 6, 3))
	} else if p.st.Napisy.Wejscie != "brak" {
		poz = func(x, y float64) string {
			return fmt.Sprintf(`\move(%.0f,%.0f,%.0f,%.0f,0,240)\fad(130,0)`, x, y+46, x, y)
		}
	}
	alfa := fmt.Sprintf("&H%02X&", int(math.Round((1-krycie)*255)))
	blok := `\an7` + poz(x1, y1) + dod + `\bord0\shad0\1c` + kolorASS(kolor) + `\1a` + alfa + `\p1`
	fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{%s}%s{\\p0}\n", p.warstwa, czasASS(od), czasASS(do), blok, kartaASS(w, h, promien))
	napis := `\an5` + poz(cx, cy+dyTekstu) + dod + `\bord0\shad0`
	fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{%s}%s\n", p.warstwa+1, czasASS(od), czasASS(do), napis, tekst)
	p.warstwa += 3
}

func krojASS(f StylFont, rozmiar float64, kolor string) string {
	return fmt.Sprintf(`{\fn%s\b%s\i%s\fs%.0f\1c%s}`, f.Rodzina, bw(f.Gruby), bw(f.Kursywa), rozmiar, kolorASS(kolor))
}

// bloki: linie jedna pod drugą, każda w swoim bloku koloru (jasny, akcent, jasny...), wchodzą po kolei.
// gora = górna krawędź pierwszego bloku; gdy srodek = true, całość jest wyśrodkowana w pionie wokół gora.
func (p *pisarzASS) bloki(linie []segment, m *Miara, rozmiar, od, do, gora float64, srodek bool) {
	h := p.st.Hak
	k := rozmiar / h.Font.Rozmiar
	odstX, odstY, promien, przerwa := h.OdstepX*k, h.OdstepY*k, h.Promien*k, h.Odstep*k
	blokH := rozmiar*0.86 + 2*odstY
	if srodek {
		gora -= (float64(len(linie))*blokH + float64(len(linie)-1)*przerwa) / 2
	}
	cx := float64(p.st.Szer) / 2
	for i, l := range linie {
		blok, kolor := h.Blok1, h.Tekst1
		if l.akcent {
			blok, kolor = h.Blok2, h.Tekst2
		}
		szer := m.Szerokosc(l.tekst, rozmiar)
		start := od + float64(i)*4/float64(p.st.Klatki)
		p.element(start, do, cx-szer/2-odstX, gora, cx+szer/2+odstX, gora+blokH, promien, blok, 1, krojASS(h.Font, rozmiar, kolor)+l.tekst, 0)
		gora += blokH + przerwa
	}
}

// naglowekRolki: obie linie tej samej wielkości, najdłuższa zajmuje ok. 4/5 szerokości kadru.
func (p *pisarzASS) naglowekRolki(hak []string, mH *Miara, koniec float64) {
	st := p.st
	var linie []segment
	maks := 0.0
	for _, l := range hak {
		if t := czyscTekst(strings.ReplaceAll(l, "*", "")); t != "" {
			linie = append(linie, segment{t, len(linie)%2 == 1})
			maks = math.Max(maks, mH.Szerokosc(t, 100))
		}
	}
	if len(linie) == 0 || maks <= 0 {
		return
	}
	// szerokość bloku = tekst + 2 odstępy, a odstęp rośnie razem z krojem
	rozmiar := math.Min(st.Hak.MaksRozmiar, st.Hak.Szerokosc*float64(st.Szer)/(maks/100+2*st.Hak.OdstepX/st.Hak.Font.Rozmiar))
	p.bloki(linie, mH, rozmiar, 0.12, koniec, float64(st.Wys)*st.Hak.Y, false)
}

// zapiszNapisyUjec: nagłówek (linie haka, do końca pierwszego ujęcia) i hasła na ujęciach. Zwraca pliki fontów.
func zapiszNapisyUjec(plik string, hak []string, hakKoniec float64, napisy []NapisUjecia, przejscia []float64, st *Styl) ([]string, error) {
	n := st.Napisy
	plikH := filepath.Join(st.Katalog, st.Hak.Font.Plik)
	plikT := filepath.Join(st.Katalog, n.Tekst.Plik)
	plikA := filepath.Join(st.Katalog, n.Akcent.Plik)
	mH, err := wczytajMiare(plikH)
	if err != nil {
		return nil, err
	}
	mT, err := wczytajMiare(plikT)
	if err != nil {
		return nil, err
	}
	mA, err := wczytajMiare(plikA)
	if err != nil {
		return nil, err
	}
	p := &pisarzASS{st: st}
	p.naglowek()
	szerKadru := float64(st.Szer)

	p.naglowekRolki(hak, mH, hakKoniec)
	for _, t := range przejscia {
		p.liniaPrzejscia(t)
	}

	// hasła na ujęciach
	cy := float64(st.Wys) * n.Y
	wejscie := 3 / float64(st.Klatki) // hasło wchodzi tuż po cięciu, nie razem z nim
	for _, nu := range napisy {
		seg := segmenty(nu.Tekst)
		if len(seg) == 0 || nu.Do-nu.Od < 0.4 {
			continue
		}
		od := nu.Od
		if od > 0 {
			od += wejscie
		}
		if n.Rodzaj == "bloki" {
			rozmiar := n.Tekst.Rozmiar * 0.9 * n.SkalaHasel
			for _, s := range seg {
				if w := mH.Szerokosc(s.tekst, rozmiar) + 2*st.Hak.OdstepX; w > 0.86*szerKadru {
					rozmiar *= 0.86 * szerKadru / w
				}
			}
			p.bloki(seg, mH, rozmiar, od, nu.Do, cy, true)
			continue
		}
		// karta: jedna linia, słowo-klucz innym krojem i kolorem; za długie hasło dostaje mniejszy krój zamiast łamania
		sk := n.SkalaHasel
		rT, rA := n.Tekst.Rozmiar*sk, n.Akcent.Rozmiar*sk
		var t strings.Builder
		var szer float64
		for proba := 0; proba < 2; proba++ {
			t.Reset()
			szer = 0
			for i, s := range seg {
				if i > 0 {
					t.WriteString(krojASS(n.Tekst, rT, n.Tekst.Kolor) + " ")
					szer += mT.Szerokosc(" ", rT)
				}
				if s.akcent {
					t.WriteString(krojASS(n.Akcent, rA, n.Akcent.Kolor) + s.tekst)
					szer += mA.Szerokosc(s.tekst, rA)
				} else {
					t.WriteString(krojASS(n.Tekst, rT, n.Tekst.Kolor) + s.tekst)
					szer += mT.Szerokosc(s.tekst, rT)
				}
			}
			if szer <= n.MaksSzer {
				break
			}
			rT, rA = rT*n.MaksSzer/szer, rA*n.MaksSzer/szer
		}
		kw := szer + 2*n.Karta.OdstepX*sk
		kh := math.Max(rT, rA)*0.86 + 2*n.Karta.OdstepY*sk
		x1, y1 := szerKadru/2-kw/2, cy+n.Karta.Dy*sk-kh/2
		p.element(od, nu.Do, x1, y1, x1+kw, y1+kh, n.Karta.Promien*sk, n.Karta.Kolor, n.Karta.Krycie, t.String(), -n.Karta.Dy*sk)
	}
	if err := os.WriteFile(plik, []byte(p.b.String()), 0o644); err != nil {
		return nil, err
	}
	return []string{plikH, plikT, plikA}, nil
}
