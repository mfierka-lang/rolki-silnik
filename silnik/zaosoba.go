// Nagłówek „za osobą”: duże litery nagłówka stoją za sylwetką człowieka z otwarcia filmu, a głowa zasłania
// dół liter. Efekt jest używany tylko tam, gdzie ma sens (osoba z głową i ramionami w kadrze, stoi w miejscu,
// jest nad nią miejsce na litery, litery zostają czytelne); w innym wypadku nagłówek wygląda jak zawsze.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ZaOsoba struct {
	Maska      string  // film z maską osoby (skala szarości), tyle klatek, ile trwa nagłówek
	Klatek     int     // przez ile klatek filmu działa efekt
	Rozmiar    float64 // wielkość liter
	Zasloniete float64 // jaka część liter jest za osobą (średnio)
	Opis       *OpisMaski
}

// literyNaglowka: gdzie stoją litery. Wymiary w pikselach filmu.
type literyNaglowka struct {
	linie    []segment
	szer     []float64
	rozmiar  float64
	srodki   []float64 // środek każdej linii w pionie
	zaslon   float64
	wysLinii float64
}

// Proporcje kroju nagłówka (Bricolage Grotesque): linia bazowa leży 0,33 wielkości pod środkiem linii,
// a wielkie litery sięgają 0,37 wielkości nad środek.
const (
	zaBaza = 0.33
	zaGora = 0.37
)

// pokryciePola: średnia wartość maski (0-1) w prostokącie podanym w ułamkach kadru.
func pokryciePola(o *OpisMaski, x1, y1, x2, y2 float64) float64 {
	if o.Srednia == nil {
		return 0
	}
	ax, bx := int(math.Max(0, x1)*float64(o.W)), int(math.Min(1, x2)*float64(o.W))
	ay, by := int(math.Max(0, y1)*float64(o.H)), int(math.Min(1, y2)*float64(o.H))
	if bx <= ax || by <= ay {
		return 0
	}
	suma := 0.0
	for y := ay; y < by; y++ {
		for x := ax; x < bx; x++ {
			suma += float64(o.Srednia[y*o.W+x])
		}
	}
	return suma / float64((bx-ax)*(by-ay))
}

// ukladNaglowkaZaOsoba: największe litery, które mieszczą się nad głową i są przez nią zasłonięte tylko trochę.
// dolnaGranica: niżej (ułamek wysokości) nagłówek nie może schodzić, bo tam stoją napisy mowy albo hasła.
func ukladNaglowkaZaOsoba(st *Styl, linie []segment, m *Miara, o *OpisMaski, dolnaGranica float64) *literyNaglowka {
	if len(linie) == 0 || len(linie) > 3 || o == nil {
		return nil
	}
	// osoba musi być wyraźna (nie mignięcie w tle), nie może wypełniać całego kadru i ma stać w miejscu
	if o.Pokrycie < 0.06 || o.Pokrycie > 0.62 || o.Stalosc < 0.45 {
		return nil
	}
	W, H := float64(st.Szer), float64(st.Wys)
	pion := st.Wys > st.Szer
	maks := 0.0
	for _, l := range linie {
		maks = math.Max(maks, m.Szerokosc(l.tekst, 100))
	}
	if maks <= 0 {
		return nil
	}
	limit, margines := 0.125*H, 0.085*H
	if !pion {
		limit, margines = 0.21*H, 0.05*H
	}
	najwiekszy := math.Min(0.90*W/(maks/100), limit)
	najmniejszy := math.Max(st.Hak.Font.Rozmiar*1.1, 0.5*najwiekszy)
	n := float64(len(linie))
	for rozmiar := najwiekszy; rozmiar >= najmniejszy; rozmiar *= 0.92 {
		wysLinii := 0.9 * rozmiar
		// wg: jak głęboko głowa wchodzi w ostatnią linię (część wielkości liter); zaczynamy od lekkiego zasłonięcia
		for _, wg := range []float64{0.38, 0.50, 0.62} {
			baza := o.Gora*H + wg*rozmiar // linia bazowa ostatniej linii
			ostatni := baza - zaBaza*rozmiar
			pierwszy := ostatni - (n-1)*wysLinii
			if pierwszy-zaGora*rozmiar < margines || baza > dolnaGranica*H {
				continue
			}
			u := &literyNaglowka{linie: linie, rozmiar: rozmiar, wysLinii: wysLinii}
			pole, zaslon := 0.0, 0.0
			for i, l := range linie {
				cy := pierwszy + float64(i)*wysLinii
				szer := m.Szerokosc(l.tekst, rozmiar)
				u.srodki = append(u.srodki, cy)
				u.szer = append(u.szer, szer)
				p := szer * (zaGora + zaBaza) * rozmiar
				pole += p
				zaslon += p * pokryciePola(o, (W-szer)/2/W, (cy-zaGora*rozmiar)/H, (W+szer)/2/W, (cy+zaBaza*rozmiar)/H)
			}
			u.zaslon = zaslon / pole
			// za mało: osoba nie dotyka liter i efektu nie widać; za dużo: nagłówka nie da się przeczytać
			if u.zaslon >= 0.03 && u.zaslon <= 0.36 {
				return u
			}
		}
	}
	return nil
}

// zapiszNaglowekZaOsoba: plik napisów z samym nagłówkiem (duże litery bez bloków, miękki cień, wejście „druk”).
func zapiszNaglowekZaOsoba(plik string, st *Styl, u *literyNaglowka, koniec float64) error {
	p := &pisarzASS{st: st}
	p.naglowek()
	cx := float64(st.Szer) / 2
	S := u.rozmiar
	const D = 300 // ms: czas „druku” jednej linii
	for i, l := range u.linie {
		kolor := st.Hak.Blok1
		if l.akcent {
			kolor = st.Hak.Blok2
		}
		od := 0.12 + float64(i)*4/float64(st.Klatki)
		cy := u.srodki[i]
		x1, x2 := cx-u.szer[i]/2-0.06*S, cx+u.szer[i]/2+0.06*S
		y1, y2 := cy-0.52*S, cy+0.52*S
		druk := ""
		if st.Napisy.Wejscie == "druk" {
			druk = fmt.Sprintf(`\clip(%.0f,%.0f,%.0f,%.0f)\t(0,%d,\clip(%.0f,%.0f,%.0f,%.0f))`, x1, y2, x2, y2+2, D, x1, y1, x2, y2+0.1*S)
			linia := fmt.Sprintf(`\an7\move(%.0f,%.0f,%.0f,%.0f,0,%d)\fad(0,90)\blur3\bord0\shad0\1c%s\p1`, x1, y2-3, x1, y1-3, D, kolorASS(st.Napisy.Akcent.Kolor))
			fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{%s}%s{\\p0}\n", 3+3*i, czasASS(od), czasASS(math.Min(koniec, od+float64(D)/1000+0.09)), linia, kartaASS(x2-x1, 6, 3))
		} else if st.Napisy.Wejscie != "brak" {
			druk = `\fad(160,0)`
		}
		// cień: te same litery, ciemne i rozmyte, lekko niżej (czytelność na jasnym tle bez obrysu)
		fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{\\an5\\pos(%.0f,%.0f)%s\\bord0\\shad0\\blur%.0f\\1a&H70&}%s%s\n",
			1+3*i, czasASS(od), czasASS(koniec), cx, cy+0.03*S, druk, math.Max(4, 0.05*S), krojASS(st.Hak.Font, S, "#000000"), l.tekst)
		fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{\\an5\\pos(%.0f,%.0f)%s\\bord0\\shad0}%s%s\n",
			2+3*i, czasASS(od), czasASS(koniec), cx, cy, druk, krojASS(st.Hak.Font, S, kolor), l.tekst)
	}
	return os.WriteFile(plik, []byte(p.b.String()), 0o644)
}

// przygotujZaOsoba: liczy maskę osoby dla pierwszych sekund filmu i sprawdza, czy nagłówek może stanąć za nią.
// argWejscia + filtrObrazu to ten sam obraz, który trafi do filmu (wyjście filtra: [vmaska]).
// Zwraca nil, gdy efektu nie da się zrobić dobrze: wtedy nagłówek zostaje w zwykłej postaci.
func przygotujZaOsoba(n *Narzedzia, st *Styl, praca string, argWejscia []string, filtrObrazu string, hak []string, koniec, dolnaGranica float64) *ZaOsoba {
	if b, _ := znajdzSylwetke(n.Dane); b == "" || st.Napisy.Wejscie == "brak" {
		return nil
	}
	var linie []segment
	for _, l := range hak {
		if t := czyscTekst(strings.ReplaceAll(l, "*", "")); t != "" {
			linie = append(linie, segment{t, len(linie)%2 == 1})
		}
	}
	fps := float64(st.Klatki)
	klatek := int(math.Floor(koniec*fps + 1e-6))
	if len(linie) == 0 || klatek < int(0.9*fps) {
		return nil
	}
	m, err := wczytajMiare(filepath.Join(st.Katalog, st.Hak.Font.Plik))
	if err != nil {
		return nil
	}
	w, h := wymiarMaski(st.Szer, st.Wys, 384)
	maska := filepath.Join(praca, "maska.mkv")
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
	defer stop()
	o, err := maskaFragmentu(ctx, n, praca, argWejscia, filtrObrazu, w, h, fps, float64(klatek)/fps, maska, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nagłówek za osobą pominięty:", err)
		os.Remove(maska)
		return nil
	}
	u := ukladNaglowkaZaOsoba(st, linie, m, o, dolnaGranica)
	if u == nil || o.Klatek < klatek-2 {
		os.Remove(maska)
		return nil
	}
	if err := zapiszNaglowekZaOsoba(filepath.Join(praca, "naglowek-za.ass"), st, u, float64(klatek)/fps); err != nil {
		os.Remove(maska)
		return nil
	}
	return &ZaOsoba{Maska: maska, Klatek: klatek, Rozmiar: u.rozmiar, Zasloniete: u.zaslon, Opis: o}
}

// filtr: fragment grafu ffmpeg. Wejście [vc] (cały film przed napisami), wyjście [vz]. Początek filmu dostaje
// nagłówek, a na wierzch wraca wycięta maską osoba; reszta filmu przechodzi bez zmian.
func (z *ZaOsoba) filtr(st *Styl, wejscieMaski int, obraz string) string {
	return fmt.Sprintf(obraz+"split=2[zh][zt];\n"+
		"[zh]trim=end_frame=%d,setpts=PTS-STARTPTS,split=2[zh1][zh2];\n"+
		"[zt]trim=start_frame=%d,setpts=PTS-STARTPTS,format=yuv420p,setsar=1[zt1];\n"+
		"[zh1]ass=naglowek-za.ass:fontsdir=fonty[zh3];\n"+
		"[%d:v]fps=%d,scale=%d:%d:flags=bicubic,format=gray,gblur=sigma=%s[zm];\n"+
		"[zh2][zm]alphamerge[zos];\n"+
		"[zh3][zos]overlay=format=auto:eof_action=pass,format=yuv420p,setsar=1[zh4];\n"+
		"[zh4][zt1]concat=n=2:v=1:a=0[vz];\n",
		z.Klatek, z.Klatek, wejscieMaski, st.Klatki, st.ObrazSzer(), st.ObrazWys(), liczba(1.6*st.skala()))
}
