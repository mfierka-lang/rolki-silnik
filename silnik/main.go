// Silnik montażu rolek Zbliżki. Jeden kod dla Windows i macOS.
// Na razie tryb ręczny z linii poleceń (etap 2 briefu):
//
//	montaz montuj --styl ../szablon/styl.json --dane ../_lokalne --wyjscie rolka.mp4 klip1.mp4 [klip2.mp4 ...]
//	montaz font plik.ttf ...     nazwy rodzin zapisane w plikach fontów (do szablonu stylu)
//
// Później dojdą: serwer lokalny dla strony, instalacja i uruchamianie z przeglądarki, samoaktualizacja.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/font/sfnt"
)

// wersja: zmienna (nie stała), żeby testy mogły zbudować program udający nowszą wersję: -X main.wersja=...
var wersja = "0.6.11"

// komunikat: postęp jako wiersze JSON (czytane później przez stronę), a dla człowieka czytelny tekst na stderr.
// zakresPostepu: gdy montaż jest częścią większego zlecenia, jego postęp 0-1 trafia w wycinek paska całości.
var zakresPostepu = [2]float64{0, 1}

func komunikat(etap string, postep float64, tekst string) {
	postep = zakresPostepu[0] + (zakresPostepu[1]-zakresPostepu[0])*postep
	b, _ := json.Marshal(map[string]any{"etap": etap, "postep": postep, "tekst": tekst})
	fmt.Println(string(b))
}

func blad(err error) {
	fmt.Fprintln(os.Stderr, "BŁĄD:", err)
	os.Exit(1)
}

func main() {
	// bez poleceń (dwuklik w program) albo z adresem zblizka-montaz://... (klik na stronie): instalacja przy
	// pierwszym uruchomieniu i start serwera lokalnego z interfejsem
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], schematAdresu+":") {
		adres := ""
		if len(os.Args) >= 2 {
			adres = os.Args[1]
		}
		uruchomZKlikniecia(adres)
		return
	}
	switch os.Args[1] {
	case "montuj":
		poleceniaMontuj(os.Args[2:])
	case "ujecia":
		polecenieUjecia(os.Args[2:], false)
	case "skan":
		polecenieUjecia(os.Args[2:], true)
	case "wykonaj":
		polecenieWykonaj(os.Args[2:])
	case "serwer":
		polecenieSerwer(os.Args[2:])
	case "instaluj":
		polecenieInstaluj(os.Args[2:])
	case "muzyka":
		polecenieMuzyka(os.Args[2:])
	case "sylwetka":
		polecenieSylwetka(os.Args[2:])
	case "naglowek": // próba: nagłówek i ustawienia odczytane z opisu (montaz naglowek KATALOG_DANYCH "opis" ...)
		if len(os.Args) < 4 {
			blad(fmt.Errorf("użycie: montaz naglowek KATALOG_DANYCH \"opis\" ..."))
		}
		nn, err := znajdzNarzedzia(katalogDanych(os.Args[2]))
		if err != nil {
			blad(err)
		}
		kat, _ := os.MkdirTemp("", "zm-naglowek-")
		defer os.RemoveAll(kat)
		var rr *Rozum
		if os.Getenv("ZM_BEZ_MODELU") == "" {
			rr, _ = uruchomRozum(nn, kat, "tekst")
		}
		for _, o := range os.Args[3:] {
			u := zrozumUwagi(o, nil)
			fmt.Printf("%q\n  nagłówek: %q\n  format: %q, długość: %.0f s (podana: %v), rodzaj: %q\n", o, naglowekZOpisu(rr, o), u.Format, u.Dlugosc, u.DlugoscPodana, u.Rodzaj)
		}
		if rr != nil {
			rr.Zamknij()
		}
	case "rodzaj":
		polecenieRodzajCLI(os.Args[2:])
	case "karta": // próba: karta treści dla transkrypcji z pliku (jedno zdanie w linii)
		polecenieKarta(os.Args[2:])
	case "font":
		for _, p := range os.Args[2:] {
			b, err := os.ReadFile(p)
			if err != nil {
				blad(err)
			}
			f, err := sfnt.Parse(b)
			if err != nil {
				blad(err)
			}
			var buf sfnt.Buffer
			rodzina, _ := f.Name(&buf, sfnt.NameIDFamily)
			odmiana, _ := f.Name(&buf, sfnt.NameIDSubfamily)
			pelna, _ := f.Name(&buf, sfnt.NameIDFull)
			typo, _ := f.Name(&buf, sfnt.NameIDTypographicFamily)
			fmt.Printf("%s: rodzina %q, odmiana %q, pełna nazwa %q, rodzina typograficzna %q\n", filepath.Base(p), rodzina, odmiana, pelna, typo)
		}
	case "paczka-mac":
		if len(os.Args) < 4 {
			blad(fmt.Errorf("użycie: montaz paczka-mac program wynik.zip"))
		}
		if err := paczkaMac(os.Args[2], os.Args[3]); err != nil {
			blad(err)
		}
	case "wersja":
		fmt.Println(wersja)
	default:
		blad(fmt.Errorf("nieznane polecenie %q", os.Args[1]))
	}
}

func poleceniaMontuj(arg []string) {
	fs := flag.NewFlagSet("montuj", flag.ExitOnError)
	stylPlik := fs.String("styl", "szablon/styl.json", "szablon stylu")
	dane := fs.String("dane", "", "katalog danych silnika (programy, modele)")
	wyjscie := fs.String("wyjscie", "rolka.mp4", "plik wynikowy")
	praca := fs.String("praca", "", "katalog roboczy (domyślnie tymczasowy)")
	muzyka := fs.String("muzyka", "", "plik z muzyką (opcjonalnie)")
	opis := fs.String("opis", "", "opis rolki od użytkownika")
	hak := fs.String("hak", "", "nagłówek na początek: \"Linia 1|Linia 2\"")
	bezNapisow := fs.Bool("bez-napisow", false, "rolka bez napisów mowy")
	muzykaOd := fs.Float64("muzyka-od", 0, "od której sekundy utworu zacząć")
	muzykaPoziom := fs.Float64("muzyka-poziom", 0, "głośność podkładu względem pełnej (0 = z szablonu)")
	maks := fs.Float64("maks", 0, "największa długość filmu w sekundach (0 = bez limitu)")
	pomin := fs.Int("pomin", 0, "ile zdań z początku wypowiedzi pominąć (uwaga: skróć początek)")
	przebitkiPlik := fs.String("przebitki", "", "plik JSON z listą nagrań bez mowy: ich fragmenty wejdą jako przebitki")
	naglowekAuto := fs.Bool("naglowek-auto", false, "gdy nie ma --hak: nagłówek z opisu, a bez opisu z karty treści")
	teksty := fs.Bool("teksty", false, "przygotuj temat, opis posta i hashtagi (trafiają do mowa.json)")
	fs.Parse(arg)
	pliki := fs.Args()
	if len(pliki) == 0 {
		blad(fmt.Errorf("podaj co najmniej jedno nagranie"))
	}
	start := time.Now()

	n, err := znajdzNarzedzia(katalogDanych(*dane))
	if err != nil {
		blad(err)
	}
	st, err := wczytajStyl(*stylPlik)
	if err != nil {
		blad(err)
	}
	if *praca == "" {
		*praca, err = os.MkdirTemp("", "zblizka-montaz-")
		if err != nil {
			blad(err)
		}
		defer os.RemoveAll(*praca)
	} else if err := os.MkdirAll(*praca, 0o755); err != nil {
		blad(err)
	}
	wy, _ := filepath.Abs(*wyjscie)
	pracaAbs, _ := filepath.Abs(*praca)

	// 1. nagrania
	komunikat("nagrania", 0.02, "Sprawdzam nagrania")
	var klipy []*Klip
	var czasy []float64
	for _, p := range pliki {
		abs, _ := filepath.Abs(p)
		k, err := sonduj(n, abs)
		if err != nil {
			blad(err)
		}
		klipy = append(klipy, k)
		czasy = append(czasy, k.Czas)
	}

	// 2. mowa: wykrycie i transkrypcja każdego nagrania z dźwiękiem
	var frazy []Fraza
	ileSlow := 0
	for i, k := range klipy {
		if !k.MaDzwiek {
			continue
		}
		komunikat("mowa", 0.05+0.45*float64(i)/float64(len(klipy)), fmt.Sprintf("Słucham nagrania %d z %d", i+1, len(klipy)))
		wav := filepath.Join(pracaAbs, fmt.Sprintf("dzwiek%d.wav", i))
		if err := wyciagnijAudio(n, k.Plik, wav); err != nil {
			blad(err)
		}
		probki, czest, err := wczytajWav(wav)
		if err != nil {
			blad(err)
		}
		mowa := wykryjMowe(probki, czest)
		if len(mowa) == 0 {
			continue
		}
		kat := filepath.Join(pracaAbs, fmt.Sprintf("mowa%d", i))
		os.MkdirAll(kat, 0o755)
		slowa, err := transkrybujKlip(n, k, wav, kat, st.Podpowiedz)
		if err != nil {
			blad(err)
		}
		fr := frazyZMowy(i, mowa, slowa, st.Ciecie.PauzaWFrazie)
		for _, f := range fr {
			ileSlow += len(f.Slowa)
		}
		frazy = append(frazy, fr...)
	}
	if ileSlow < 4 {
		blad(fmt.Errorf("w nagraniach nie słychać mowy: rolka produktowa (same ujęcia) będzie obsługiwana w kolejnym kroku"))
	}

	// 3. plan cięć i napisy
	komunikat("plan", 0.52, "Wycinam pauzy i powtórzenia")
	plan := zaplanujMowe(frazy, czasy, st)
	opisPominiecia := ""
	if *pomin > 0 { // „skróć początek”: wypadają pierwsze zdania (najwyżej połowa wypowiedzi)
		zdania := jednostkiMowy(frazy)
		ile := min(*pomin, len(zdania)/2)
		for _, u := range zdania[:ile] {
			for _, f := range u.frazy {
				frazy[f].Odrzucona = "pominięty początek"
			}
		}
		if ile > 0 {
			plan = zaplanujMowe(frazy, czasy, st)
			opisPominiecia = fmt.Sprintf("początek skrócony o zdania z początku: %d", ile)
			plan.Raport = append(plan.Raport, opisPominiecia)
		}
	}
	if *maks > 0 && plan.Czas > *maks {
		// wypowiedź trzeba będzie skrócić: zdania mówione jednym tchem stają się osobnymi częściami do wyboru
		if podzielone := rozdzielNaZdania(frazy); len(podzielone) > len(frazy) {
			frazy = podzielone
			plan = zaplanujMowe(frazy, czasy, st)
			if opisPominiecia != "" {
				plan.Raport = append(plan.Raport, opisPominiecia)
			}
		}
	}
	// 3a. karta treści: silnik najpierw czyta wypowiedź (typ, temat, najważniejsze zdania, słowa-akcenty), dopiero
	// potem montuje. Model treści działa tylko przez tę chwilę: whisper już skończył, a ffmpeg jeszcze nie składa filmu.
	// Odpowiedzi zostają w pamięci silnika, więc poprawka filmu nie uruchamia modelu drugi raz.
	komunikat("karta", 0.505, "Czytam wypowiedź: o czym jest film")
	pamiecTresci := filepath.Join(n.Dane, "mowa")
	lr := &leniwyRozum{n: n, praca: pracaAbs, model: znajdzModelTresci(n.Dane)}
	defer lr.Zamknij()
	karta := przygotujKarte(zdaniaKarty(frazy), st, pamiecTresci, lr.nazwa(), lr.daj, func(t string) { komunikat("karta", 0.51, t) })
	if lr.Blad != nil {
		karta.Uwagi = append(karta.Uwagi, "model treści nie wystartował: "+lr.Blad.Error())
	}
	if *maks > 0 && plan.Czas > *maks {
		// za długa wypowiedź: zostają zdania najważniejsze według karty treści (zamiast ucięcia końcówki)
		komunikat("plan", 0.525, "Skracam wypowiedź do najważniejszych zdań")
		opisSkrotu := skrocTresc(frazy, plan.Czas, *maks, st, karta)
		if opisSkrotu != "" {
			plan = zaplanujMowe(frazy, czasy, st)
			if opisPominiecia != "" {
				plan.Raport = append(plan.Raport, opisPominiecia)
			}
			plan.Raport = append(plan.Raport, opisSkrotu)
		}
	}
	skrocPlan(plan, *maks, float64(st.Klatki)) // ostatnia deska: gdy nadal za długo, cięcie po pełnym zdaniu
	if len(plan.Odcinki) == 0 {
		blad(fmt.Errorf("po wycięciu pauz nic nie zostało"))
	}
	karta.przypnijDoPlanu(plan)
	var tekst []string
	for _, s := range plan.Slowa {
		tekst = append(tekst, s.Tekst)
	}
	// nagłówek: wpisany ręcznie, z opisu użytkownika, a bez opisu tytuł z karty treści (same słowa z wypowiedzi)
	var linieHaka []string
	if *hak != "" {
		linieHaka = strings.Split(*hak, "|")
	} else if *naglowekAuto && !*bezNapisow {
		linieHaka = naglowekFilmu(pamiecTresci, *opis, karta, lr)
	}
	// temat, opis posta i hashtagi: dopóki model treści jest w pamięci
	temat, opisPostaFilmu, hashtagi := "", "", []string(nil)
	if *teksty {
		komunikat("teksty", 0.528, "Przygotowuję opis posta")
		temat, opisPostaFilmu, hashtagi = tekstyFilmu(pamiecTresci, st, karta, strings.Join(tekst, " "), lr)
	}
	lr.Zamknij() // model zwalnia pamięć przed maską osoby, przebitkami i składaniem filmu
	slowaNapisow := plan.Slowa
	if *bezNapisow {
		slowaNapisow = nil
	}
	// środek twarzy w każdym nagraniu: na nim stoi kadr filmu i zbliżenia (skan jest w pamięci silnika)
	twarze := make([][2]float64, len(klipy))
	for i, k := range klipy {
		if sk, err := skanuj(n, k); err == nil {
			if u, x, y := udzialTwarzy(sk); u >= 0.12 {
				twarze[i] = [2]float64{x, y}
			}
		}
	}
	// nagłówek za osobą: maska liczona na tym samym obrazie, który trafi do filmu (pierwsze sekundy)
	hakKoniec := math.Min(2.6, plan.Czas)
	var za *ZaOsoba
	hakNaWierzchu := linieHaka
	if len(linieHaka) > 0 {
		komunikat("napisy", 0.53, "Ustawiam nagłówek za osobą")
		obraz, wejscia, _ := zbudujFiltr(&Zlecenie{Klipy: klipy, Plan: plan, Styl: st, Twarze: twarze})
		var argWe []string
		for _, w := range wejscia {
			argWe = append(argWe, "-i", w)
		}
		granica := st.Napisy.Y - 0.12
		if *bezNapisow {
			granica = 0.8
		}
		za = przygotujZaOsoba(n, st, pracaAbs, argWe, obraz+"[vc]null[vmaska]", linieHaka, hakKoniec, granica)
		if za != nil {
			hakNaWierzchu = nil
		}
	}
	fonty, err := zapiszNapisy(filepath.Join(pracaAbs, "napisy.ass"), slowaNapisow, st, hakNaWierzchu, hakKoniec, karta.akcentyNapisow())
	if err != nil {
		blad(err)
	}
	os.WriteFile(filepath.Join(pracaAbs, "tekst.txt"), []byte(strings.Join(tekst, " ")+"\n"), 0o644)
	var odrz []string
	for _, f := range frazy {
		if f.Odrzucona != "" {
			var w []string
			for _, s := range f.Slowa {
				w = append(w, s.Tekst)
			}
			odrz = append(odrz, fmt.Sprintf("%.2f-%.2f %s: %s", f.Od, f.Do, f.Odrzucona, strings.Join(w, " ")))
		}
	}
	os.WriteFile(filepath.Join(pracaAbs, "wyciete.txt"), []byte(strings.Join(odrz, "\n")+"\n"), 0o644)

	// 3b. przebitki z nagrań bez mowy
	var przebitki []Przebitka
	if *przebitkiPlik != "" {
		var dodatkowe []string
		if b, err := os.ReadFile(*przebitkiPlik); err == nil {
			json.Unmarshal(b, &dodatkowe)
		}
		if len(dodatkowe) > 0 {
			komunikat("przebitki", 0.54, "Wybieram przebitki z pozostałych nagrań")
			przebitki = przygotujPrzebitki(n, st, pracaAbs, dodatkowe, plan, hakKoniec, func(t string) { komunikat("przebitki", 0.545, t) })
		}
	}

	// 3c. akcenty kadru z treści: zbliżenia na słowa wskazane przez kartę (nie na nagłówku i nie pod przebitką)
	odAkcentow := 0.8
	if len(linieHaka) > 0 {
		odAkcentow = hakKoniec + 0.3
	}
	akcenty := akcentyKadru(karta, plan, st, odAkcentow, przebitki)
	if b, err := json.MarshalIndent(map[string]any{"karta": karta, "akcenty": akcenty}, "", " "); err == nil {
		os.WriteFile(filepath.Join(pracaAbs, "karta.json"), b, 0o644)
	}
	zrozumienie := karta.raport()
	if s := opisAkcentow(akcenty); s != "" {
		zrozumienie = append(zrozumienie, s+".")
	}

	// 4. render
	enk := wybierzEnkoder(n, st)
	komunikat("render", 0.55, "Składam rolkę ("+enk.Nazwa+")")
	muz := ""
	if *muzyka != "" {
		muz, _ = filepath.Abs(*muzyka)
	}
	z := &Zlecenie{Klipy: klipy, Plan: plan, Styl: st, Fonty: fonty, Muzyka: muz, MuzykaOd: *muzykaOd, MuzykaPoziom: *muzykaPoziom, Wyjscie: wy, Praca: pracaAbs, Za: za, Przebitki: przebitki, Twarze: twarze, Akcenty: akcenty}
	ostatni := 0.0
	err = renderuj(n, enk, z, func(p float64) {
		if p-ostatni >= 0.1 {
			ostatni = p
			komunikat("render", 0.55+0.44*p, "Składam rolkę")
		}
	})
	if err != nil && enk.Nazwa != "libx264" { // sprzętowy enkoder zawiódł w trakcie: jeszcze raz programowo
		kand := kandydaciEnkoderow(st.skala())
		enk = kand[len(kand)-1]
		komunikat("render", 0.55, "Składam rolkę (kodowanie programowe)")
		err = renderuj(n, enk, z, nil)
	}
	if err != nil {
		blad(err)
	}
	podsumowanie, _ := json.Marshal(map[string]any{"czas": plan.Czas, "raport": plan.Raport, "tekst": strings.Join(tekst, " "), "przebitki": przebitki, "zaOsoba": za != nil,
		"naglowek": linieHaka, "temat": temat, "opis": opisPostaFilmu, "hashtagi": hashtagi, "zrozumienie": zrozumienie, "akcenty": akcenty,
		"karta": map[string]any{"zrodlo": karta.Zrodlo, "model": karta.Model, "typ": karta.Typ, "temat": karta.Temat, "tytul": karta.Tytul, "hak": karta.Hak, "trwalo": karta.Trwalo, "zPamieci": karta.ZPamieci, "startModelu": lr.Start}})
	os.WriteFile(filepath.Join(pracaAbs, "mowa.json"), podsumowanie, 0o644)
	komunikat("gotowe", 1, fmt.Sprintf("Gotowe w %.0f s: %s", time.Since(start).Seconds(), strings.Join(plan.Raport, "; ")))
}
