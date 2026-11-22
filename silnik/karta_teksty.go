// Teksty filmu mówionego, które wynikają z karty treści: nagłówek, temat, opis posta. Model treści pisze je
// w tej samej chwili, w której wypełnia kartę (jedno wczytanie modelu na film), a wynik zostaje w pamięci silnika:
// poprawka filmu i ten sam film w innej jakości dostają te same teksty bez uruchamiania modelu.
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// zPamieciTekstu: wynik pracy modelu zapamiętany pod skrótem danych wejściowych. licz zwraca wynik i to,
// czy warto go zapamiętać (wynik z reguł liczy się od ręki, więc nie trafia do pamięci).
func zPamieciTekstu[T any](katalog, klucz string, licz func() (T, bool)) T {
	var v T
	plik := ""
	if katalog != "" {
		h := sha1.Sum([]byte(wersjaKarty + "|" + klucz))
		plik = filepath.Join(katalog, "tekst-"+hex.EncodeToString(h[:10])+".json")
		if b, err := os.ReadFile(plik); err == nil && json.Unmarshal(b, &v) == nil {
			return v
		}
	}
	v, zapisz := licz()
	if zapisz && plik != "" {
		if b, err := json.Marshal(v); err == nil {
			os.MkdirAll(katalog, 0o755)
			os.WriteFile(plik, b, 0o644)
		}
	}
	return v
}

// linieTytulu: tytuł z karty jako jedna albo dwie linie nagłówka (linia nie kończy się przyimkiem).
func linieTytulu(t string) []string {
	p := strings.Fields(t)
	if len(p) <= 2 || utf8.RuneCountInString(t) <= 14 {
		return []string{t}
	}
	ile := (len(p) + 1) / 2
	for ile > 1 && koncowkaLinii[strings.ToLower(p[ile-1])] {
		ile--
	}
	return []string{strings.Join(p[:ile], " "), strings.Join(p[ile:], " ")}
}

// naglowekFilmu: nagłówek filmu mówionego. Pierwszeństwo ma opis użytkownika (jego słowa), bez opisu wchodzi
// tytuł z karty treści: tylko słowa, które padły w wypowiedzi.
func naglowekFilmu(pamiec, opis string, k *Karta, lr *leniwyRozum) []string {
	if strings.TrimSpace(opis) != "" {
		linie := zPamieciTekstu(pamiec, "naglowek|"+lr.nazwa()+"|"+opis, func() ([]string, bool) {
			r := lr.daj()
			return naglowekZOpisu(r, opis), r != nil
		})
		if len(linie) > 0 {
			return linie
		}
	}
	if k != nil && k.Tytul != "" {
		return linieTytulu(k.Tytul)
	}
	return nil
}

type tekstPosta struct {
	Opis string   `json:"opis"`
	Tagi []string `json:"tagi"`
}

// tekstyFilmu: temat (do pokazania użytkownikowi), opis posta i hashtagi. Temat pochodzi z karty; bez modelu
// to po prostu najważniejsze słowa wypowiedzi. Opis posta: najmocniejsze zdanie osoby + wezwanie z szablonu marki;
// na komputerze z dużym modelem piszącym (od 12 GB pamięci) pisze go ten model, jak dotąd. Model treści opisu
// posta nie pisze: w próbach koloryzował. Hashtagi zawsze z szablonu.
func tekstyFilmu(pamiec string, st *Styl, k *Karta, tekst string, lr *leniwyRozum) (string, string, []string) {
	temat := k.Temat
	if temat == "" && len(k.SlowaKlucze) > 0 {
		temat = "Najważniejsze słowa wypowiedzi: " + strings.Join(k.SlowaKlucze[:min(4, len(k.SlowaKlucze))], ", ")
	}
	poczatek := pierwszeZdania(tekst, 2)
	if k.Hak > 0 && k.Hak <= len(k.Zdania) {
		if z := &k.Zdania[k.Hak-1]; z.wFilmie() && len(z.Slowa) >= 6 {
			poczatek = z.Tekst
		}
	}
	zrodlo := tekst
	if r := []rune(zrodlo); len(r) > 2400 {
		zrodlo = string(r[:2400])
	}
	p := zPamieciTekstu(pamiec, "post|"+filepath.Base(znajdzModelPiszacy(lr.n.Dane))+"|"+st.Post.Kontekst+"|"+poczatek+"|"+zrodlo, func() (tekstPosta, bool) {
		var r *Rozum
		if m := znajdzModelPiszacy(lr.n.Dane); m != "" && m != lr.model {
			lr.Zamknij() // dwa modele naraz nie są potrzebne: karta i nagłówek są już gotowe
			r, _ = uruchomModel(lr.n, lr.praca, "tekst", m, "", 8192)
			defer r.Zamknij()
		} else if m != "" {
			r = lr.daj()
		}
		opis, tagi := napiszPost(r, st, zrodlo, poczatek)
		return tekstPosta{opis, tagi}, r.DobryWTekscie()
	})
	return temat, p.Opis, p.Tagi
}
