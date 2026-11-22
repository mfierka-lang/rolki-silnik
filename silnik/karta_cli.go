// Próba karty treści bez montażu: `montaz karta --dane KATALOG [--model plik.gguf] [--post] zdania.txt ...`
// Plik to transkrypcja, jedno zdanie w linii. Wynik (karta w JSON) trafia obok pliku, a na ekran czas pracy
// i zajęta pamięć modelu. Służy do porównywania modeli i do strojenia poleceń.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// pamiecProcesu: szczytowa pamięć procesu w MB (Windows: szczyt zbioru roboczego; macOS i Linux: bieżąca pamięć rezydentna).
func pamiecProcesu(pid int) float64 {
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	var out string
	if runtime.GOOS == "windows" {
		out, _, _ = uruchom(ctx, "", "powershell", "-NoProfile", "-Command", fmt.Sprintf("(Get-Process -Id %d).PeakWorkingSet64", pid))
	} else {
		out, _, _ = uruchom(ctx, "", "ps", "-o", "rss=", "-p", fmt.Sprint(pid))
	}
	var v float64
	fmt.Sscan(strings.TrimSpace(out), &v)
	if runtime.GOOS != "windows" {
		v *= 1024
	}
	return v / (1 << 20)
}

func polecenieKarta(arg []string) {
	fs := flag.NewFlagSet("karta", flag.ExitOnError)
	dane := fs.String("dane", "", "katalog danych silnika")
	stylPlik := fs.String("styl", "szablon/styl.json", "szablon stylu")
	model := fs.String("model", "", "plik modelu GGUF (domyślnie model treści z katalogu danych)")
	etykieta := fs.String("etykieta", "", "dopisek do nazwy pliku wyniku")
	post := fs.Bool("post", false, "dodatkowo: opis posta i temat pisane przez model (próba jakości polszczyzny)")
	kontekst := fs.Int("kontekst", oknoTresci, "rozmiar okna modelu w tokenach")
	fs.Parse(arg)
	n, err := znajdzNarzedzia(katalogDanych(*dane))
	if err != nil {
		blad(err)
	}
	st, err := wczytajStyl(*stylPlik)
	if err != nil {
		blad(err)
	}
	if *model == "" {
		*model = znajdzModelTresci(n.Dane)
	}
	praca, _ := os.MkdirTemp("", "zm-karta-")
	defer os.RemoveAll(praca)
	var r *Rozum
	t0 := time.Now()
	if *model != "" {
		*model, _ = filepath.Abs(*model)
		if r, err = uruchomModel(n, praca, "tresc", *model, "", *kontekst); err != nil {
			blad(err)
		}
	}
	if r != nil {
		defer r.Zamknij()
		fmt.Printf("model %s: start %.1f s\n", r.Model, time.Since(t0).Seconds())
	} else {
		fmt.Println("bez modelu: same reguły")
	}
	for _, plik := range fs.Args() {
		b, err := os.ReadFile(plik)
		if err != nil {
			blad(err)
		}
		// zdania z pliku jako frazy: każda linia to jedna jednostka mowy, słowa po 0,35 s
		var fr []Fraza
		t := 0.0
		for _, linia := range strings.Split(string(b), "\n") {
			slowa := strings.Fields(linia)
			if len(slowa) == 0 {
				continue
			}
			f := Fraza{Od: t}
			for _, s := range slowa {
				f.Slowa = append(f.Slowa, Slowo{s, t, t + 0.3})
				t += 0.35
			}
			if !konczyZdanie(slowa[len(slowa)-1]) {
				f.Slowa[len(f.Slowa)-1].Tekst += "."
			}
			f.Do = t
			t += 0.4
			fr = append(fr, f)
		}
		zd := zdaniaKarty(fr)
		nazwa := ""
		if r != nil {
			nazwa = r.Model
		}
		t1 := time.Now()
		k := przygotujKarte(zd, st, "", nazwa, func() *Rozum { return r }, nil)
		czas := time.Since(t1).Seconds()
		wynik := map[string]any{"karta": k, "czas": czas, "raport": k.raport()}
		if *post && r != nil {
			tekst := tekstKarty(zd)
			t2 := time.Now()
			opis, _ := napiszPost(r, st, tekst, "")
			wynik["post"], wynik["tematZdaniem"], wynik["czasPosta"] = opis, tematWypowiedzi(r, tekst), time.Since(t2).Seconds()
		}
		wy := strings.TrimSuffix(plik, filepath.Ext(plik)) + ".karta" + *etykieta + ".json"
		j, _ := json.MarshalIndent(wynik, "", " ")
		os.WriteFile(wy, j, 0o644)
		wagi := [4]int{}
		akc := 0
		for _, z := range k.Zdania {
			wagi[z.Waga]++
			if z.AkcentNr >= 0 {
				akc++
			}
		}
		fmt.Printf("%s: %d zdań, %.1f s, źródło %s, typ %s, hak %d, wagi 0/1/2/3 = %d/%d/%d/%d, akcentów %d, uwag %d\n  temat: %s | tytuł: %s\n",
			filepath.Base(plik), len(zd), czas, k.Zrodlo, k.Typ, k.Hak, wagi[0], wagi[1], wagi[2], wagi[3], akc, len(k.Uwagi), k.Temat, k.Tytul)
		for _, u := range k.Uwagi {
			fmt.Println("  uwaga:", u)
		}
	}
	if r != nil && r.cmd != nil && r.cmd.Process != nil {
		fmt.Printf("pamięć modelu (szczyt): %.0f MB, razem %.1f s\n", pamiecProcesu(r.cmd.Process.Pid), time.Since(t0).Seconds())
	}
}
