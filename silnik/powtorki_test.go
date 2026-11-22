package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// frazyTestowe: wypowiedź zapisana jednym napisem. Słowa trwają 0,3 s, „|” to pauza 0,5 s (nowa fraza),
// „_” przed słowem to krótka przerwa 0,25 s w środku frazy.
func frazyTestowe(tekst string) []Fraza {
	var fr []Fraza
	t := 0.0
	for _, cz := range strings.Split(tekst, "|") {
		f := Fraza{Od: t}
		for _, s := range strings.Fields(cz) {
			if strings.HasPrefix(s, "_") {
				s = s[1:]
				t += 0.25
			}
			f.Slowa = append(f.Slowa, Slowo{Tekst: s, Od: t, Do: t + 0.3})
			t += 0.3
		}
		f.Do = t
		if len(f.Slowa) > 0 {
			fr = append(fr, f)
		}
		t += 0.5
	}
	return fr
}

func tekstPlanu(tekst string) (string, []string) {
	st := &Styl{Klatki: 30}
	st.Ciecie.PauzaWFrazie, st.Ciecie.MaksPauza, st.Ciecie.ZapasPrzed, st.Ciecie.ZapasPo = 0.30, 0.28, 0.08, 0.15
	fr := rozdzielFrazy(frazyTestowe(tekst), nil)
	plan := zaplanujMowe(fr, []float64{1000}, st)
	var w []string
	for _, s := range plan.Slowa {
		w = append(w, s.Tekst)
	}
	return strings.Join(w, " "), plan.Raport
}

func TestPowtorki(t *testing.T) {
	for _, p := range []struct{ nazwa, wejscie, wynik string }{
		{"falstart po pauzie", "To jest | to jest nasz nowy brelok.", "to jest nasz nowy brelok."},
		{"falstart w środku frazy", "I wtedy ten brelok _ten brelok można przyłożyć do telefonu.", "I wtedy ten brelok można przyłożyć do telefonu."},
		{"falstart z urwanym słowem", "Przyłóż tele _telefon do breloka.", "Przyłóż telefon do breloka."},
		{"falstart ze wstawką", "Ten brelok ma znaczy _ten brelok ma chip w środku.", "ten brelok ma chip w środku."},
		{"jedno słowo po pauzie", "Dzisiaj | dzisiaj pokażę wam nowy brelok.", "dzisiaj pokażę wam nowy brelok."},
		{"powtórzona kwestia", "Nasz brelok ma w środku chip NFC. | Nasz brelok ma w środku mały chip NFC. | Działa bez baterii.", "Nasz brelok ma w środku mały chip NFC. Działa bez baterii."},
		{"klaps", "Nasz brelok ma w środku | dobra, jeszcze raz. | Nasz brelok ma w środku chip. | Działa bez baterii.", "Nasz brelok ma w środku chip. Działa bez baterii."},
		{"klaps po całym zdaniu", "Witam w naszej pracowni druku. | Jeszcze raz. | Witam was w naszej pracowni druku 3D.", "Witam was w naszej pracowni druku 3D."},
		{"dosłowne powtórzenie", "Zamów breloki na naszej stronie _na naszej stronie już dziś.", "Zamów breloki na naszej stronie już dziś."},
		{"wypełniacz sam", "Brelok kosztuje sześć złotych. | No znaczy | Działa od razu po wyjęciu z pudełka.", "Brelok kosztuje sześć złotych. Działa od razu po wyjęciu z pudełka."},
		{"wtrącenie w środku", "Brelok yyy kosztuje sześć złotych.", "Brelok kosztuje sześć złotych."},
		// to ma zostać bez zmian
		{"wyliczanka bez pauzy", "U nas jest tanio, tanio, tanio i szybko.", "U nas jest tanio, tanio, tanio i szybko."},
		{"wyliczanka z pauzami", "Tanio. | Tanio. | Tanio. | I szybko.", "Tanio. Tanio. Tanio. I szybko."},
		{"powtórzenie bez pauzy", "To jest szybkie, to jest tanie i to jest nasze.", "To jest szybkie, to jest tanie i to jest nasze."},
		{"anafora", "To jest bardzo szybkie. | To jest bardzo tanie. | To jest po prostu dobre.", "To jest bardzo szybkie. To jest bardzo tanie. To jest po prostu dobre."},
		{"jeszcze raz w zdaniu", "Przyłóż telefon jeszcze raz do breloka.", "Przyłóż telefon jeszcze raz do breloka."},
		{"wypełniacz w zdaniu", "To jest jakby mały komputer w breloku, znaczy chip.", "To jest jakby mały komputer w breloku, znaczy chip."},
		{"refren", "Chcesz więcej opinii? | Zamów brelok u nas. | Chcesz więcej klientów? | Zamów brelok u nas.", "Chcesz więcej opinii? Zamów brelok u nas. Chcesz więcej klientów? Zamów brelok u nas."},
		{"podobne zdania", "Na trzecim miejscu jest złoty zegarek. | Na drugim miejscu jest srebrny pierścionek.", "Na trzecim miejscu jest złoty zegarek. Na drugim miejscu jest srebrny pierścionek."},
	} {
		wynik, raport := tekstPlanu(p.wejscie)
		if wynik != p.wynik {
			t.Errorf("%s:\n  wejście: %s\n  jest:    %s\n  ma być:  %s\n  raport:  %v", p.nazwa, p.wejscie, wynik, p.wynik, raport)
		}
	}
}

// TestPowtorkiNagranie: próba na prawdziwym nagraniu (pomijana bez zmiennych). ZM_TEST_WAV = dźwięk 16 kHz mono,
// ZM_TEST_JSON = gotowy wynik whisper.cpp (-ojf) dla tego dźwięku (bez niego silnik transkrybuje sam),
// ZM_TEST_DOSLUCH = katalog danych silnika (włącza drugą turę transkrypcji), ZM_TEST_OKNA = tylko policz okna
// dosłuchu, ZM_TEST_WYNIK = plik na listę odcinków i wycięć, ZM_TEST_SLOWA = plik na słowa po dosłuchu.
func TestPowtorkiNagranie(t *testing.T) {
	wav, js := os.Getenv("ZM_TEST_WAV"), os.Getenv("ZM_TEST_JSON")
	if wav == "" {
		t.Skip("brak ZM_TEST_WAV")
	}
	probki, czest, err := wczytajWav(wav)
	if err != nil {
		t.Fatal(err)
	}
	var slowa []Slowo
	if js == "" { // pełna transkrypcja jak w silniku (z dosłuchiwaniem); ZM_TEST_DOSLUCH = katalog danych silnika
		n, _ := znajdzNarzedzia(os.Getenv("ZM_TEST_DOSLUCH"))
		if slowa, err = transkrybuj(n, wav, t.TempDir(), os.Getenv("ZM_TEST_PODPOWIEDZ")); err != nil {
			t.Fatal(err)
		}
	} else {
		b, err := os.ReadFile(js)
		if err != nil {
			t.Fatal(err)
		}
		if slowa, err = slowaZWhispera(b); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("ZM_TEST_OKNA") != "" { // same okna dosłuchu, bez drugiej transkrypcji
		mowa, _ := wykryjMoweProfil(probki, czest)
		razem := 0.0
		okna := oknaDosluchu(mowa, slowa)
		for _, o := range okna {
			razem += o.do - o.od
		}
		fmt.Println("OKNA:", len(okna), "sekund:", int(razem))
		return
	}
	if dane := os.Getenv("ZM_TEST_DOSLUCH"); dane != "" && js != "" { // druga tura transkrypcji: katalog danych silnika
		n, _ := znajdzNarzedzia(dane)
		przed := len(slowa)
		mowa, _ := wykryjMoweProfil(probki, czest)
		fmt.Println("OKNA:", len(oknaDosluchu(mowa, slowa)))
		slowa = dosluchaj(n, wav, t.TempDir(), argumentyWhispera(n, ""), slowa)
		if cel := os.Getenv("ZM_TEST_SLOWA"); cel != "" {
			b, _ := json.Marshal(slowa)
			os.WriteFile(cel, b, 0o644)
		}
		fmt.Println("DOSLUCH: słów", przed, "->", len(slowa))
	}
	st := &Styl{Klatki: 30}
	st.Ciecie.PauzaWFrazie, st.Ciecie.MaksPauza, st.Ciecie.ZapasPrzed, st.Ciecie.ZapasPo = 0.30, 0.28, 0.08, 0.15
	fr := frazyZMowy(0, wykryjMowe(probki, czest), slowa, st.Ciecie.PauzaWFrazie)
	plan := zaplanujMowe(fr, []float64{float64(len(probki)) / float64(czest)}, st)
	type wyciete struct {
		Od, Do float64
		Powod  string
		Tekst  string
	}
	var wyc []wyciete
	for _, f := range fr {
		var w []string
		for _, s := range f.Slowa {
			w = append(w, s.Tekst)
		}
		if f.Odrzucona != "" && f.Odrzucona != ciecieBezSlow {
			wyc = append(wyc, wyciete{f.Od, f.Do, f.Odrzucona, strings.Join(w, " ")})
		}
		for _, c := range f.Ciecia {
			if c == ciecieRozciag {
				wyc = append(wyc, wyciete{f.Od, f.Do, c, strings.Join(w, " ")})
			}
		}
	}
	var tekst []string
	for _, s := range plan.Slowa {
		tekst = append(tekst, s.Tekst)
	}
	for _, r := range plan.Raport {
		fmt.Println("RAPORT:", r)
	}
	for _, w := range wyc {
		fmt.Printf("WYCIETE %.2f-%.2f [%s] %s\n", w.Od, w.Do, w.Powod, w.Tekst)
	}
	fmt.Println("TEKST:", strings.Join(tekst, " "))
	if cel := os.Getenv("ZM_TEST_WYNIK"); cel != "" {
		out, _ := json.Marshal(map[string]any{"odcinki": plan.Odcinki, "wyciete": wyc, "tekst": strings.Join(tekst, " "), "raport": plan.Raport})
		os.WriteFile(cel, out, 0o644)
	}
}
