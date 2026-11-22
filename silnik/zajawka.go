// Zajawka na początku filmu mówionego (właściciel 22.11.2026, opcja „Zajawka na początku”, domyślnie wyłączona):
// film zaczyna się od najmocniejszego zdania wypowiedzi (1,5-4 s, ciasny kadr na twarz, napisy jak w filmie),
// potem krótki biały błysk z dźwiękiem i film od początku z nagłówkiem; to zdanie pada drugi raz w swoim miejscu.
//
// Technicznie zajawka to ten sam fragment nagrania otwarty drugi raz jako osobne wejście ffmpeg (`-ss` przed `-i`,
// dekodowane są tylko te sekundy), doklejone na samym końcu grafu przed resztą filmu. Dzięki temu czasy napisów,
// akcentów kadru, przebitek, liczb i nagłówka w głównej części się nie zmieniają (liczą się od początku głównej
// części, a `concat` sam przesuwa jej znaczniki czasu), a całego filmu nie trzeba trzymać w pamięci (pułapka
// `split` + `trim` + `concat`). Dźwięk zajawki idzie przed dźwiękiem filmu do tego samego wyrównania głośności,
// więc zajawka ma tę samą głośność (-14 LUFS) i muzykę co reszta.
package main

import (
	"fmt"
	"math"
	"strings"
)

const (
	zajawkaMin       = 1.5  // najkrótsza zajawka w sekundach
	zajawkaMaks      = 4.0  // najdłuższa
	zajawkaPrzejscie = 0.1  // biały błysk między zajawką a filmem (pełna biel), do tego przenikanie po obu stronach
	zajawkaZoom      = 1.22 // ciasny kadr na twarz (zwykłe zbliżenie w filmie to 1,12)
	zajawkaOdKiedy   = 2.6  // zdanie musi zaczynać się w filmie po nagłówku, żeby nie padło dwa razy pod rząd
)

type Zajawka struct {
	Klip      int     // nagranie źródłowe
	Od, Do    float64 // fragment w nagraniu źródłowym (wyrównany do klatek)
	Czas      float64 // Do - Od
	Przejscie float64 // długość błysku po zajawce
	WFilmie   float64 // kiedy to zdanie pada w głównej części filmu
	Zdanie    string
	Slowa     []Slowo             // czasy od początku zajawki
	Akcenty   map[int]AkcentSlowa // słowa-akcenty karty treści (indeksy w Slowa)
	Napisy    string              // plik ASS zajawki w katalogu pracy ("" = bez napisów)
}

// Calosc: o ile zajawka wydłuża film (zajawka + błysk).
func (z *Zajawka) Calosc() float64 {
	if z == nil {
		return 0
	}
	return z.Czas + z.Przejscie
}

// wybierzZajawke: najmocniejsze zdanie z karty treści, które nadaje się na zajawkę. Warunki: zdanie zostało w filmie
// w całości i bez cięcia w środku, trwa 1,5-4 s, jest zrozumiałe samo (nie zaczyna się łącznikiem ani powitaniem),
// jest ważne (waga od 2, nie dygresja i nie wezwanie), nie jest pierwszym zdaniem filmu i pada dopiero po nagłówku.
// Wygrywa najwyższa waga, potem liczba albo cena, pytanie, zdanie-hak z karty. Zwraca nil i powód, gdy nic nie pasuje.
func wybierzZajawke(k *Karta, plan *PlanMowy, st *Styl) (*Zajawka, string) {
	if k == nil || len(k.Zdania) == 0 || plan == nil || len(plan.Slowa) == 0 {
		return nil, "wypowiedź nie ma zdań do wyboru"
	}
	kl := float64(st.Klatki)
	pierwsze := -1
	for i := range k.Zdania {
		if k.Zdania[i].wFilmie() {
			pierwsze = i
			break
		}
	}
	type kandydat struct {
		ocena      float64
		zd         int
		p0, p1, oi int
		s0, s1     float64
	}
	var naj *kandydat
	powod := "żadne zdanie nie trwa 1,5-4 s, nie jest zrozumiałe samo albo nie jest dość ważne"
	for i := range k.Zdania {
		z := &k.Zdania[i]
		if i == pierwsze || len(z.Slowa) < 3 {
			continue
		}
		if z.Waga < 2 || z.Rola == "wezwanie" || z.Rola == "dygresja" {
			continue
		}
		if zaczynaOdLacznika(z.Tekst) || zaczynaOdPowitania(z) {
			continue
		}
		// całe zdanie w filmie, słowa po kolei (bez cięcia w środku zdania)
		p0, p1, ok := z.Slowa[0].Plan, z.Slowa[len(z.Slowa)-1].Plan, true
		for j, s := range z.Slowa {
			if s.Plan < 0 || (j > 0 && s.Plan != z.Slowa[j-1].Plan+1) {
				ok = false
				break
			}
		}
		if !ok || p0 < 0 || p1 >= len(plan.Slowa) {
			continue
		}
		s, e := plan.Slowa[p0].Od, plan.Slowa[p1].Do
		if s < zajawkaOdKiedy {
			continue
		}
		oi := -1
		for j, o := range plan.Odcinki {
			if o.WyOd <= s+1e-6 && s < o.WyOd+(o.Do-o.Od) {
				oi = j
			}
		}
		if oi < 0 {
			continue
		}
		o := plan.Odcinki[oi]
		koniecOdc := o.WyOd + (o.Do - o.Od)
		if e > koniecOdc+1e-6 { // zdanie przechodzi przez cięcie
			continue
		}
		// zapas: oddech przed pierwszym słowem i wybrzmienie po ostatnim, bez sąsiednich słów
		s0 := math.Max(o.WyOd, s-0.1)
		if p0 > 0 && plan.Slowa[p0-1].Do > o.WyOd {
			s0 = math.Max(s0, math.Min(s, plan.Slowa[p0-1].Do+0.03))
		}
		s1 := math.Min(koniecOdc, e+0.22)
		if p1+1 < len(plan.Slowa) && plan.Slowa[p1+1].Od < koniecOdc {
			s1 = math.Min(s1, math.Max(e, plan.Slowa[p1+1].Od-0.03))
		}
		s0 = math.Ceil(s0*kl-1e-6) / kl
		s1 = math.Floor(s1*kl+1e-6) / kl
		d := s1 - s0
		if d < zajawkaMin-1e-6 || d > zajawkaMaks+1e-6 {
			continue
		}
		ocena := float64(z.Waga)*10 + z.punkty
		if liczbaWZdaniu(z) >= 0 || z.Rodzaj == "liczba" {
			ocena += 4
		}
		if strings.HasSuffix(strings.TrimSpace(z.Tekst), "?") {
			ocena += 3
		}
		if z.Nr == k.Hak {
			ocena += 2
		}
		ocena -= 0.4 * math.Abs(d-2.8) // najlepiej ok. 3 s: krótko, ale z całą myślą
		if naj == nil || ocena > naj.ocena {
			naj = &kandydat{ocena, i, p0, p1, oi, s0, s1}
		}
	}
	if naj == nil {
		if pierwsze >= 0 && len(k.Zdania) == 1 {
			powod = "w filmie jest tylko jedno zdanie"
		}
		return nil, powod
	}
	o := plan.Odcinki[naj.oi]
	zaj := &Zajawka{Klip: o.Klip, Przejscie: zajawkaPrzejscie, WFilmie: naj.s0, Zdanie: k.Zdania[naj.zd].Tekst}
	zaj.Od = math.Round((o.Od+naj.s0-o.WyOd)*kl) / kl
	zaj.Czas = math.Round((naj.s1-naj.s0)*kl) / kl
	zaj.Do = zaj.Od + zaj.Czas
	akcFilmu := k.akcentyNapisow()
	zaj.Akcenty = map[int]AkcentSlowa{}
	for p := naj.p0; p <= naj.p1; p++ {
		s := plan.Slowa[p]
		zaj.Slowa = append(zaj.Slowa, Slowo{s.Tekst, math.Max(0, s.Od-naj.s0), math.Min(zaj.Czas, s.Do-naj.s0)})
		if a, ok := akcFilmu[p]; ok {
			zaj.Akcenty[len(zaj.Slowa)-1] = a
		}
	}
	if len(zaj.Akcenty) == 0 {
		zaj.Akcenty = nil
	}
	return zaj, ""
}

// kopiujFrazy: kopia fraz z własnymi listami słów i cięć (planowanie i skracanie zmieniają frazy w miejscu).
func kopiujFrazy(fr []Fraza) []Fraza {
	out := make([]Fraza, len(fr))
	for i, f := range fr {
		f.Slowa = append([]Slowo(nil), f.Slowa...)
		f.Ciecia = append([]string(nil), f.Ciecia...)
		out[i] = f
	}
	return out
}

// raportZajawki: zdanie dla użytkownika („Co zrobił automat”).
func raportZajawki(z *Zajawka, powod string) string {
	if z == nil {
		return "Nie znalazłem zdania na zajawkę (" + powod + "), więc film zaczyna się zwykle."
	}
	return fmt.Sprintf("Zajawka na początku (%s s): %s. Potem błysk i film od początku z nagłówkiem; to zdanie pada jeszcze raz w swoim miejscu (ok. %.0f. sekunda filmu).",
		strings.Replace(fmt.Sprintf("%.1f", z.Czas), ".", ",", 1), cytat(z.Zdanie, 110), z.WFilmie+z.Calosc())
}

// filtrObrazu: zajawka (ciasny kadr na twarz, napisy, wyjście w biel), biały błysk i główna część wchodząca z bieli.
// we = numer wejścia ffmpeg z fragmentem nagrania, glowny = etykieta gotowej głównej części, wynik = etykieta całości.
func (zj *Zajawka) filtrObrazu(z *Zlecenie, we int, glowny, wynik string) string {
	st := z.Styl
	kl := st.Klatki
	klip := z.Klipy[zj.Klip]
	n := int(math.Round(zj.Czas * float64(kl)))
	var f strings.Builder
	fmt.Fprintf(&f, "[%d:v]fps=%d,trim=end_frame=%d,setpts=N/%d/TB", we, kl, n, kl)
	if klip.HDR {
		f.WriteString(",zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv")
	}
	kadrX, zx, zy, ky := kadrNaTwarz(z, zj.Klip, klip)
	fmt.Fprintf(&f, ",crop=w='2*floor(min(iw,ih*%d/%d)/2)':h='2*floor(min(ih,iw*%d/%d)/2)':x='%s':y='(ih-oh)*%s'", st.Szer, st.Wys, st.Wys, st.Szer, kadrX, ky)
	zoom := 1.0 // bez rozpoznanej twarzy (głos zza kamery nad produktem) bez dodatkowego zbliżenia: nie ucinać produktu
	if zj.Klip < len(z.Twarze) && z.Twarze[zj.Klip][0] > 0 {
		zoom = zajawkaZoom
	}
	fmt.Fprintf(&f, ",zoompan=z='%s':x='%s':y='%s':d=1:s=%dx%d:fps=%d,setsar=1,format=yuv420p", liczba(zoom), zx, zy, st.ObrazSzer(), st.ObrazWys(), kl)
	if zj.Napisy != "" {
		fmt.Fprintf(&f, ",ass=%s:fontsdir=fonty", zj.Napisy)
	}
	wyjscie := math.Min(0.12, zj.Czas/3)
	fmt.Fprintf(&f, ",fade=t=out:st=%s:d=%s:color=white[zjv];\n", liczba(zj.Czas-wyjscie), liczba(wyjscie))
	fmt.Fprintf(&f, "color=c=white:s=%dx%d:r=%d:d=%s,format=yuv420p,setsar=1[zjb];\n", st.ObrazSzer(), st.ObrazWys(), kl, liczba(zj.Przejscie))
	fmt.Fprintf(&f, "%sfade=t=in:st=0:d=0.2:color=white[zjm];\n", glowny)
	fmt.Fprintf(&f, "[zjv][zjb][zjm]concat=n=3:v=1:a=0%s;\n", wynik)
	return f.String()
}

// filtrDzwieku: głos zajawki i cisza na czas błysku (idą przed dźwiękiem filmu do wspólnego wyrównania głośności).
// Zwraca fragment grafu i etykiety do doklejenia na początek listy odcinków dźwięku.
func (zj *Zajawka) filtrDzwieku(we int) (string, []string) {
	d := zj.Czas
	g := fmt.Sprintf("[%d:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=mono,atrim=0:%s,asetpts=PTS-STARTPTS,afade=t=in:st=0:d=0.006,afade=t=out:st=%s:d=0.06[zja];\n",
		we, liczba(d), liczba(math.Max(0, d-0.06)))
	g += fmt.Sprintf("anullsrc=r=48000:cl=mono,atrim=0:%s,aformat=sample_fmts=fltp:channel_layouts=mono[zjc];\n", liczba(zj.Przejscie))
	return g, []string{"[zja]", "[zjc]"}
}

// filtrBlysku: krótki dźwięk przejścia generowany w ffmpeg (bez cudzych plików): narastający szum powietrza
// zakończony miękkim, niskim stuknięciem w chwili błysku. Sam dźwięk: szczyt ok. -13 dBFS, średnio ok. -26 dB
// (głos z muzyką ok. -17 dB), gra w chwili ciszy między zajawką a filmem.
func (zj *Zajawka) filtrBlysku(etykieta string) string {
	narastanie := 0.3
	srodek := zj.Czas + zj.Przejscie/2
	start := math.Max(0, srodek-narastanie)
	ms := int(math.Round(start * 1000))
	var f strings.Builder
	fmt.Fprintf(&f, "anoisesrc=d=0.75:c=pink:r=48000:a=0.5:s=7,highpass=f=500,lowpass=f=6500,afade=t=in:st=0:d=%s:curve=cub,afade=t=out:st=%s:d=0.45:curve=exp,volume=1.3[zjs1];\n",
		liczba(narastanie), liczba(narastanie))
	fmt.Fprintf(&f, "sine=f=85:r=48000:d=0.3,afade=t=in:st=0:d=0.004,afade=t=out:st=0.004:d=0.29:curve=qsin,volume=0.7,adelay=delays=%d:all=1[zjs2];\n", int(math.Round(narastanie*1000)))
	fmt.Fprintf(&f, "[zjs1][zjs2]amix=inputs=2:normalize=0:duration=longest,aformat=sample_fmts=fltp:channel_layouts=mono,pan=stereo|c0=c0|c1=c0,adelay=delays=%d:all=1%s;\n", ms, etykieta)
	return f.String()
}
