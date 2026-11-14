package main

import (
	"flag"
	"fmt"
)

// polecenieMuzyka: przegląd biblioteki muzyki z wynikami analizy (`montaz muzyka --dane KATALOG`).
func polecenieMuzyka(arg []string) {
	fs := flag.NewFlagSet("muzyka", flag.ExitOnError)
	dane := fs.String("dane", "", "katalog danych silnika")
	fs.Parse(arg)
	n, err := znajdzNarzedzia(katalogDanych(*dane))
	if err != nil {
		blad(err)
	}
	pliki := bibliotekaMuzyki(n)
	fmt.Printf("%-52s %6s %6s %5s %6s %6s\n", "utwór", "czas", "tempo", "rytm", "start", "głośn.")
	for _, p := range pliki {
		u, err := analizujUtwor(n, p)
		if err != nil {
			fmt.Println("  błąd:", err)
			continue
		}
		nazwa := []rune(u.Nazwa)
		if len(nazwa) > 50 {
			nazwa = nazwa[:50]
		}
		fmt.Printf("%-52s %5.0fs %6.1f %5.2f %5.1fs %6.1f\n", string(nazwa), u.Czas, u.BPM, u.Pewnosc, u.Start, u.RmsDb)
	}
	fmt.Println("utworów:", len(pliki))
}
