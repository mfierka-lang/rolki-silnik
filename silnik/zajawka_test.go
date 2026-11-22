package main

import (
	"strings"
	"testing"
)

func TestZajawkaZTekstu(t *testing.T) {
	przypadki := []struct {
		opis   string
		popraw []string
		chce   int
	}{
		{"breloki NFC dla restauracji", nil, 0},
		{"film z zajawką o brelokach", nil, 1},
		{"zajawka na początku", nil, 1},
		{"film o brelokach, bez zajawki", nil, -1},
		{"film z zajawką", []string{"bez zajawki"}, -1},
		{"", []string{"większe napisy", "dodaj zajawkę"}, 1},
	}
	for _, p := range przypadki {
		if u := zrozumUwagi(p.opis, p.popraw); u.Zajawka != p.chce {
			t.Errorf("%q %v: zajawka %d, oczekiwano %d", p.opis, p.popraw, u.Zajawka, p.chce)
		}
	}
	for _, opis := range []string{"z zajawką, breloki NFC", "breloki NFC bez zajawki", "zajawka na początku breloki NFC"} {
		if s := reSlowaStylu.ReplaceAllString(opis, " "); strings.Contains(strings.ToLower(s), "zajaw") {
			t.Errorf("%q: słowo o zajawce zostało w nagłówku: %q", opis, s)
		}
	}
}

func TestWyborZajawki(t *testing.T) {
	st := &Styl{Klatki: 30}
	// trzy zdania w jednym odcinku: pierwsze odpada (pierwsze w filmie), drugie zaczyna się łącznikiem, trzecie pasuje
	plan := &PlanMowy{Odcinki: []Odcinek{{Klip: 0, Od: 10, Do: 30, WyOd: 0}}}
	zdania := [][]string{{"Dzisiaj", "pokażę", "wam", "breloki."}, {"Dlatego", "są", "takie", "dobre."}, {"Kosztują", "tylko", "700", "złotych", "taniej."}}
	k := &Karta{}
	t0 := 0.5
	for nr, zd := range zdania {
		z := ZdanieKarty{Nr: nr + 1, Tekst: strings.Join(zd, " "), Waga: 3, Rola: "informacja", AkcentNr: -1}
		for _, s := range zd {
			plan.Slowa = append(plan.Slowa, Slowo{s, t0, t0 + 0.45})
			z.Slowa = append(z.Slowa, SlowoKarty{Tekst: s, Klip: 0, Od: 10 + t0, Do: 10 + t0 + 0.45, Plan: len(plan.Slowa) - 1})
			t0 += 0.5
		}
		k.Zdania = append(k.Zdania, z)
		t0 += 0.6
	}
	plan.Czas = 20
	zj, powod := wybierzZajawke(k, plan, st)
	if zj == nil {
		t.Fatalf("brak zajawki: %s", powod)
	}
	if !strings.HasPrefix(zj.Zdanie, "Kosztują") || zj.Czas < zajawkaMin || zj.Czas > zajawkaMaks || zj.Od < 10 {
		t.Errorf("zła zajawka: %+v", zj)
	}
	if len(zj.Slowa) != 5 || zj.Slowa[0].Od < 0 || zj.Slowa[4].Do > zj.Czas+1e-9 {
		t.Errorf("złe czasy słów zajawki: %+v", zj.Slowa)
	}
	// samo pierwsze zdanie: brak zajawki
	k.Zdania = k.Zdania[:1]
	if zj, _ := wybierzZajawke(k, plan, st); zj != nil {
		t.Errorf("pierwsze zdanie filmu nie może być zajawką")
	}
}
