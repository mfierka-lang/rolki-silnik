// Opcje filmu wybierane na stronie: format (pion 9:16 albo poziom 16:9), długość (do 60 s albo dłuższy),
// napisy, przejścia, animacje. Zasady przejść i rytmu wzięte z doktryny ruchu HyperFrames (HeyGen) i skilla
// video-design (przeczytane 14.11.2026): przejście tylko na granicy części filmu, zawsze w jednym kierunku,
// nigdy przenikanie; reszta to czyste cięcia; przed zakończeniem chwila oddechu.
package main

import (
	"fmt"
	"math"
	"strings"
)

// czasPrzejscia: przejście „druk”: następne ujęcie odsłania się od dołu, a po krawędzi jedzie linia dyszy
// (ten sam motyw co wejście napisów).
const czasPrzejscia = 0.3

// bezNajazdu: wyłączone animacje = bez powolnego najazdu na nieruchomych ujęciach.
var bezNajazdu bool

// przejsciaDlaUjec: po których ujęciach (numer od 0) jest przejście. Granice części: po otwarciu i przed
// zakończeniem; w dłuższym filmie dodatkowo co szóste cięcie jako granica rozdziału.
func przejsciaDlaUjec(ile int, dlugi bool) map[int]bool {
	po := map[int]bool{}
	if ile >= 3 {
		po[0] = true
	}
	if ile >= 5 {
		po[ile-2] = true
	}
	if dlugi {
		for i := 6; i < ile-3; i += 6 {
			po[i] = true
		}
	}
	return po
}

// lancuchZlozenia: graf filtrów łączący ujęcia. Ujęcia między przejściami są sklejane na ostro, a grupy łączy
// odsłonięcie od dołu (xfade wipeup) z krótkim przejściem dźwięku. Wyjścia: [vc] i [ac]. Zwraca długość całości.
func lancuchZlozenia(f *strings.Builder, ev, ea []string, czasy []float64, po map[int]bool) float64 {
	type grupa struct {
		v, a string
		czas float64
	}
	var grupy []grupa
	pocz := 0
	for i := range ev {
		if i != len(ev)-1 && !po[i] {
			continue
		}
		g := len(grupy)
		suma := 0.0
		for j := pocz; j <= i; j++ {
			suma += czasy[j]
		}
		fmt.Fprintf(f, "%sconcat=n=%d:v=1:a=0,settb=AVTB[gv%d];\n", strings.Join(ev[pocz:i+1], ""), i-pocz+1, g)
		fmt.Fprintf(f, "%sconcat=n=%d:v=0:a=1[ga%d];\n", strings.Join(ea[pocz:i+1], ""), i-pocz+1, g)
		grupy = append(grupy, grupa{fmt.Sprintf("[gv%d]", g), fmt.Sprintf("[ga%d]", g), suma})
		pocz = i + 1
	}
	v, a, dl := grupy[0].v, grupy[0].a, grupy[0].czas
	for g := 1; g < len(grupy); g++ {
		fmt.Fprintf(f, "%s%sxfade=transition=wipeup:duration=%s:offset=%s[xv%d];\n", v, grupy[g].v, liczba(czasPrzejscia), liczba(dl-czasPrzejscia), g)
		fmt.Fprintf(f, "%s%sacrossfade=d=%s:c1=tri:c2=tri[xa%d];\n", a, grupy[g].a, liczba(czasPrzejscia), g)
		v, a = fmt.Sprintf("[xv%d]", g), fmt.Sprintf("[xa%d]", g)
		dl += grupy[g].czas - czasPrzejscia
	}
	fmt.Fprintf(f, "%snull[vc];\n%sanull[ac];\n", v, a)
	return dl
}

// liniaPrzejscia: linia dyszy przez całą szerokość kadru, jadąca od dołu do góry razem z odsłanianym ujęciem.
func (p *pisarzASS) liniaPrzejscia(t float64) {
	st := p.st
	ms := int(math.Round(czasPrzejscia * 1000))
	tagi := fmt.Sprintf(`\an7\move(0,%d,0,-8,0,%d)\blur4\bord0\shad0\1c%s\p1`, st.Wys, ms, kolorASS(st.Napisy.Akcent.Kolor))
	fmt.Fprintf(&p.b, "Dialogue: %d,%s,%s,T,,0,0,0,,{%s}%s{\\p0}\n", 900+p.warstwa, czasASS(t), czasASS(t+czasPrzejscia), tagi, kartaASS(float64(st.Szer), 8, 0))
	p.warstwa++
}

// skrocPlan: wersja „do 60 sekund” rolki mówionej. Cięcie wypada po ostatnim pełnym zdaniu, które mieści się
// w limicie; bez kropek w transkrypcji tnie równo na limicie.
func skrocPlan(plan *PlanMowy, maks, klatki float64) bool {
	if maks <= 0 || plan.Czas <= maks {
		return false
	}
	t := 0.0
	for _, s := range plan.Slowa {
		if s.Do <= maks-0.2 && konczyZdanie(s.Tekst) {
			t = s.Do + 0.18
		}
	}
	if t < maks/3 {
		t = maks
	}
	t = math.Round(t*klatki) / klatki
	var odcinki []Odcinek
	for _, o := range plan.Odcinki {
		if o.WyOd >= t-1/klatki {
			break
		}
		if o.WyOd+(o.Do-o.Od) > t {
			o.Do = o.Od + math.Round((t-o.WyOd)*klatki)/klatki
		}
		odcinki = append(odcinki, o)
	}
	var slowa []Slowo
	for _, s := range plan.Slowa {
		if s.Do <= t+0.05 {
			slowa = append(slowa, s)
		}
	}
	bylo := plan.Czas
	plan.Odcinki, plan.Slowa, plan.Czas = odcinki, slowa, t
	plan.Raport = append(plan.Raport, fmt.Sprintf("wybrana wersja do %.0f sekund: film skrócony z %.0f s do %.0f s (do końca pełnego zdania)", maks, bylo, t))
	return true
}

// skalaKlatki: wymiary klatki podglądowej dla modelu oglądającego ujęcia, w proporcjach filmu.
func skalaKlatki(st *Styl) string {
	dluzszy := float64(max(st.Szer, st.Wys))
	bok := func(v int) int { return 2 * int(math.Round(768*float64(v)/dluzszy/2)) }
	return fmt.Sprintf("%d:%d", bok(st.Szer), bok(st.Wys))
}
