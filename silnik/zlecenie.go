// Jedno zlecenie montażu od początku do końca: tak pracuje strona (przez serwer lokalny) i polecenie `montaz wykonaj`.
// Silnik sam rozpoznaje, czy to rolka mówiona (ktoś mówi do kamery), czy rolka z samych ujęć, czyta opis i uwagi
// użytkownika, a na koniec zapisuje wynik.json: plik rolki, rozpoznany temat, tekst wypowiedzi, opis posta i hashtagi.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Zadanie struct {
	Pliki   []string `json:"pliki"`
	Opis    string   `json:"opis"`   // 1-2 zdania od użytkownika: o czym ma być rolka
	Popraw  []string `json:"popraw"` // kolejne uwagi z pola „Popraw”
	Styl    string   `json:"styl"`   // plik szablonu stylu
	Dane    string   `json:"dane"`   // katalog danych silnika
	Praca   string   `json:"praca"`  // katalog roboczy tego zlecenia
	Wyjscie string   `json:"wyjscie"`
	Rodzaj  string   `json:"rodzaj"` // "" = rozpoznaj, "mowa", "ujecia"
	// wybór na stronie
	Format      string `json:"format"`  // "pion" (9:16, domyślnie) albo "poziom" (16:9)
	Dlugosc     string `json:"dlugosc"` // "krotki" (do 60 s, domyślnie) albo "dlugi"
	BezNapisow  bool   `json:"bezNapisow"`
	BezPrzejsc  bool   `json:"bezPrzejsc"`
	BezAnimacji bool   `json:"bezAnimacji"`
	BezMuzyki   bool   `json:"bezMuzyki"`
	Muzyka      string `json:"muzyka"` // "" = automatycznie, "dynamiczny", "spokojny" albo "styl:Nazwa"
}

type Wynik struct {
	Plik     string   `json:"plik"`
	Rodzaj   string   `json:"rodzaj"` // mowa | ujecia
	Format   string   `json:"format"` // pion | poziom
	Muzyka   string   `json:"muzyka"` // tytuł użytego utworu
	Czas     float64  `json:"czas"`   // długość rolki w sekundach
	Trwalo   float64  `json:"trwalo"` // ile trwał montaż
	Temat    string   `json:"temat"`
	Tekst    string   `json:"tekst"` // co zostało powiedziane (po cięciach)
	Naglowek []string `json:"naglowek"`
	Hasla    []string `json:"hasla"`
	Opis     string   `json:"opis"` // propozycja opisu posta
	Hashtagi []string `json:"hashtagi"`
	Raport   []string `json:"raport"` // co silnik zrobił, zwykłymi zdaniami
}

// Ustawienia: to, co wynika z opisu i z uwag wpisanych w „Popraw”. Opis zmienia treść i tempo rolki, nie styl marki.
type Ustawienia struct {
	Rodzaj      string // wymuszony rodzaj rolki
	Dlugosc     float64
	Ujecie      float64
	SkalaNapisu float64
	BezNapisow  bool
	BezNaglowka bool
	BezMuzyki   bool
	Naglowek    []string
	Pomin       int // ile ujęć z początku pominąć („skróć początek”)
	Notatki     []string
}

var reSekundy = regexp.MustCompile(`(\d{1,2})\s*(s\b|sek)`)
var reNaglowek = regexp.MustCompile(`(?i)(nag[lł][oó]wek|tytu[lł])\s*:\s*(.+)$`)
var rePodzialLinii = regexp.MustCompile(`\s*[|/]\s*`)

// zrozumUwagi: reguły dla najczęstszych próśb. Każda rozpoznana prośba trafia do raportu, żeby było widać,
// co automat zrozumiał. Kolejne uwagi się sumują („krócej” dwa razy skraca dwa razy).
func zrozumUwagi(opis string, popraw []string) Ustawienia {
	u := Ustawienia{Dlugosc: 24, Ujecie: 2.4, SkalaNapisu: 1}
	wszystko := append([]string{opis}, popraw...)
	for nr, surowe := range wszystko {
		t := " " + normalizujZdanie(surowe) + " "
		ma := func(wzorce ...string) bool {
			for _, w := range wzorce {
				if strings.Contains(t, w) {
					return true
				}
			}
			return false
		}
		zOpisu := nr == 0
		if ma(" mowion", " mowie ", " opowiadam", " lektor", " komentarz glosowy", " gadana") {
			u.Rodzaj = "mowa"
		}
		if ma(" bez mowy", " same ujecia", " bez glosu", " bez gadania", " tylko ujecia") {
			u.Rodzaj = "ujecia"
		}
		if ma(" dynamiczn", " szybk", " szybciej", " energiczn", " zywo ", " zywiej") {
			u.Ujecie = 1.8
			if !zOpisu {
				u.Notatki = append(u.Notatki, "Szybsze tempo: ujęcia po 1,8 s.")
			}
		}
		if ma(" spokojn", " wolniej", " wolno ", " powoli") {
			u.Ujecie = 3.0
			if !zOpisu {
				u.Notatki = append(u.Notatki, "Spokojniejsze tempo: ujęcia po 3 s.")
			}
		}
		if m := reSekundy.FindStringSubmatch(t); m != nil {
			if v, _ := strconv.Atoi(m[1]); v >= 6 && v <= 60 {
				u.Dlugosc = float64(v)
				u.Notatki = append(u.Notatki, fmt.Sprintf("Długość ustawiona na %d s.", v))
			}
		} else if ma(" krocej", " skroc ", " krotsz", " za dlug") && !ma(" poczat") {
			u.Dlugosc *= 0.72
			u.Notatki = append(u.Notatki, "Krótsza rolka.")
		} else if ma(" dluzej", " wydluz", " dluzsz", " za krotk") {
			u.Dlugosc *= 1.3
			u.Notatki = append(u.Notatki, "Dłuższa rolka.")
		}
		if ma(" poczat") && ma(" skroc", " krotsz", " utnij", " wytnij", " bez poczat", " pomin") {
			u.Pomin++
			u.Notatki = append(u.Notatki, "Początek skrócony o jedno ujęcie.")
		}
		if ma(" wieksze napisy", " napisy wieksze", " powieksz napisy", " wiekszy napis", " za male napisy") {
			u.SkalaNapisu *= 1.15
			u.Notatki = append(u.Notatki, "Większe napisy.")
		}
		if ma(" mniejsze napisy", " napisy mniejsze", " zmniejsz napisy", " za duze napisy") {
			u.SkalaNapisu *= 0.87
			u.Notatki = append(u.Notatki, "Mniejsze napisy.")
		}
		if ma(" bez napisow", " usun napisy", " wylacz napisy") {
			u.BezNapisow = true
			u.Notatki = append(u.Notatki, "Bez napisów.")
		}
		if ma(" bez muzyki", " wylacz muzyke", " usun muzyke", " bez podkladu") {
			u.BezMuzyki = true
			u.Notatki = append(u.Notatki, "Bez muzyki.")
		}
		if ma(" bez naglowka", " usun naglowek", " bez tytulu", " usun tytul") {
			u.BezNaglowka = true
			u.Notatki = append(u.Notatki, "Bez nagłówka.")
		}
		if m := reNaglowek.FindStringSubmatch(surowe); m != nil {
			var linie []string
			for _, l := range rePodzialLinii.Split(m[2], 2) {
				if l = czyscTekst(l); l != "" {
					linie = append(linie, l)
				}
			}
			if len(linie) > 0 {
				u.Naglowek, u.BezNaglowka = linie, false
				u.Notatki = append(u.Notatki, "Nagłówek wpisany ręcznie.")
			}
		}
	}
	u.Dlugosc = math.Max(6, math.Min(60, u.Dlugosc))
	return u
}

// stylZlecenia: gdy uwagi zmieniają wielkość napisów, powstaje kopia stylu tylko dla tego zlecenia.
func stylZlecenia(z *Zadanie, u Ustawienia) string {
	poziom := z.Format == "poziom"
	if u.SkalaNapisu == 1 && !poziom && !z.BezAnimacji {
		return z.Styl
	}
	b, err := os.ReadFile(z.Styl)
	if err != nil {
		return z.Styl
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return z.Styl
	}
	skala := u.SkalaNapisu
	if poziom {
		// film poziomy: kadr 1920 x 1080, napisy mniejsze (wąski pasek u dołu), szersze linie
		m["szer"], m["wys"] = 1920, 1080
		skala *= 0.72
	}
	if z.BezAnimacji {
		m["kadr"] = map[string]any{"zblizenia": []float64{1}, "y": 0.5}
	}
	if n, ok := m["napisy"].(map[string]any); ok {
		if poziom {
			n["maksSzer"], n["y"], n["maksSlow"] = 1250, 0.86, 5
			if k, ok := n["karta"].(map[string]any); ok {
				for _, pole := range []string{"promien", "odstepX", "odstepY", "dy"} {
					if v, ok := k[pole].(float64); ok {
						k[pole] = math.Round(v * 0.72)
					}
				}
			}
		}
		if z.BezAnimacji {
			n["wejscie"] = "brak"
		}
		for _, klucz := range []string{"tekst", "akcent"} {
			if f, ok := n[klucz].(map[string]any); ok {
				if r, ok := f["rozmiar"].(float64); ok {
					f["rozmiar"] = math.Round(r * skala)
				}
			}
		}
	}
	abs, _ := filepath.Abs(filepath.Dir(z.Styl))
	m["zasoby"] = abs
	wy, _ := json.MarshalIndent(m, "", " ")
	plik := filepath.Join(z.Praca, "styl-zlecenia.json")
	if os.WriteFile(plik, wy, 0o644) != nil {
		return z.Styl
	}
	return plik
}

// OcenaMowy: czy w nagraniu ktoś mówi (do kamery albo jako lektor).
type OcenaMowy struct {
	Klip      int
	Udzial    float64 // jaka część nagrania to dźwięk głośniejszy od tła
	Slow      int
	SlowNaSek float64
	Mowione   bool
	Slowa     []Slowo
}

// ocenMowe: pomiar głośności, a gdy wygląda to na mowę, pełna transkrypcja (zapamiętana dla montażu).
func ocenMowe(n *Narzedzia, st *Styl, praca string, i int, k *Klip) OcenaMowy {
	o := OcenaMowy{Klip: i}
	if !k.MaDzwiek || n.Whisper == "" || n.Model == "" {
		return o
	}
	wav := filepath.Join(praca, fmt.Sprintf("ocena%d.wav", i))
	if err := wyciagnijAudio(n, k.Plik, wav); err != nil {
		return o
	}
	defer os.Remove(wav)
	probki, czest, err := wczytajWav(wav)
	if err != nil {
		return o
	}
	glosno := 0.0
	for _, f := range wykryjMowe(probki, czest) {
		glosno += f.Do - f.Od
	}
	o.Udzial = glosno / k.Czas
	if o.Udzial >= 0.25 && glosno >= 3 {
		kat := filepath.Join(praca, fmt.Sprintf("ocena%d", i))
		os.MkdirAll(kat, 0o755)
		defer os.RemoveAll(kat)
		if slowa, err := transkrybujKlip(n, k, wav, kat, st.Podpowiedz); err == nil {
			for _, s := range slowa {
				if !toWtracenie(s.Tekst) && normalizuj(s.Tekst) != "" {
					o.Slow++
				}
			}
			o.Slowa = slowa
			o.SlowNaSek = float64(o.Slow) / k.Czas
			o.Mowione = o.Slow >= 8 && o.SlowNaSek >= 0.9
		}
	}
	return o
}

func wczytajZadanie(plik string) (*Zadanie, error) {
	b, err := os.ReadFile(plik)
	if err != nil {
		return nil, err
	}
	z := &Zadanie{}
	if err := json.Unmarshal(b, z); err != nil {
		return nil, fmt.Errorf("nieczytelne zlecenie: %v", err)
	}
	if len(z.Pliki) == 0 {
		return nil, fmt.Errorf("zlecenie bez nagrań")
	}
	if z.Styl == "" {
		z.Styl = filepath.Join(katalogDanych(z.Dane), "szablon", "styl.json")
	}
	if z.Praca == "" {
		z.Praca = filepath.Dir(plik)
	}
	if z.Wyjscie == "" {
		z.Wyjscie = filepath.Join(z.Praca, "rolka.mp4")
	}
	return z, nil
}

var reHashtag = regexp.MustCompile(`^#[\p{L}\d]{2,24}$`)

// napiszPost: opis posta od modelu piszącego, sprawdzony i uzupełniony stałym wezwaniem i hashtagami marki.
// Gdy modelu nie ma albo odpowiedź jest zła, opis powstaje z szablonu (opisPosta).
func napiszPost(r *Rozum, st *Styl, zrodlo, poczatek string) (string, []string) {
	szablonOpis, szablonTagi := opisPosta(st, poczatek, zrodlo)
	if !r.DobryWTekscie() || strings.TrimSpace(zrodlo) == "" {
		return szablonOpis, szablonTagi
	}
	system := "Piszesz krótkie opisy postów na Instagram i TikTok dla marki. " + st.Post.Kontekst +
		" Piszesz po polsku, konkretnie, bez przesady, bez emotikon i bez długich myślników. Nie wymyślasz faktów, cen ani liczb, których nie ma w materiale."
	pyt := "Materiał (to, co mówi osoba w rolce, albo opis rolki):\n\n" + zrodlo +
		"\n\nNapisz opis posta. Odpowiedz dokładnie w dwóch liniach:\nOPIS: 2-3 krótkie zdania\nHASHTAGI: 4-7 hashtagów po polsku oddzielonych spacjami"
	odp, err := r.pytajTekst(system, pyt, 260)
	if err != nil {
		return szablonOpis, szablonTagi
	}
	var o struct {
		Opis     string
		Hashtagi []string
	}
	// hashtagi bierzemy wyłącznie z szablonu marki: model potrafi zrobić w nich literówkę
	o.Opis, _ = rozbierzPost(odp)
	opis := czyscTekst(o.Opis)
	if n := len([]rune(opis)); n < 30 || strings.ContainsAny(opis, "{}[]<>") {
		return szablonOpis, szablonTagi
	}
	if st.Post.Wezwanie != "" && !strings.Contains(strings.ToLower(opis), "zblizka.pl") {
		opis += "\n\n" + st.Post.Wezwanie
	}
	tagi := append([]string(nil), szablonTagi...)
	jest := map[string]bool{}
	for _, t := range tagi {
		jest[strings.ToLower(t)] = true
	}
	for _, t := range o.Hashtagi {
		t = strings.TrimSpace(t)
		if reHashtag.MatchString(t) && !jest[strings.ToLower(t)] && len(tagi) < 10 {
			tagi = append(tagi, t)
			jest[strings.ToLower(t)] = true
		}
	}
	return opis, tagi
}

func pierwszeZdania(tekst string, ile int) string {
	var out []string
	zdanie := ""
	for _, s := range strings.Fields(tekst) {
		zdanie += s + " "
		if konczyZdanie(s) {
			out = append(out, strings.TrimSpace(zdanie))
			zdanie = ""
			if len(out) >= ile {
				break
			}
		}
	}
	if len(out) == 0 {
		return strings.TrimSpace(zdanie)
	}
	return strings.Join(out, " ")
}

func polecenieWykonaj(arg []string) {
	if len(arg) < 1 {
		blad(fmt.Errorf("podaj plik zlecenia"))
	}
	z, err := wczytajZadanie(arg[0])
	if err != nil {
		blad(err)
	}
	t0 := time.Now()
	dane := katalogDanych(z.Dane)
	n, err := znajdzNarzedzia(dane)
	if err != nil {
		blad(err)
	}
	os.MkdirAll(z.Praca, 0o755)
	ust := zrozumUwagi(z.Opis, z.Popraw)
	stylPlik := stylZlecenia(z, ust)
	st, err := wczytajStyl(stylPlik)
	if err != nil {
		blad(err)
	}
	if z.BezNapisow {
		ust.BezNapisow = true
	}
	dlugi := z.Dlugosc == "dlugi"
	// muzyka: gdy w bibliotece są utwory i nikt jej nie wyłączył; nastrój wynika z tempa podanego w opisie
	zMuzyka := !z.BezMuzyki && !ust.BezMuzyki && len(bibliotekaMuzyki(n)) > 0
	nastroj := ""
	switch ust.Ujecie {
	case 1.8:
		nastroj = "dynamiczny"
	case 3.0:
		nastroj = "spokojny"
	}
	if z.Muzyka != "" { // wybór na stronie ma pierwszeństwo przed tempem z opisu
		nastroj = z.Muzyka
	}
	if dlugi && ust.Dlugosc == 24 { // dłuższy film z ujęć: do 2 minut, spokojniejsze ujęcia
		ust.Dlugosc = 120
		if ust.Ujecie == 2.4 {
			ust.Ujecie = 3.4
		}
	}
	if !dlugi && ust.Dlugosc > 60 {
		ust.Dlugosc = 60
	}
	w := &Wynik{Plik: z.Wyjscie, Raport: append([]string(nil), ust.Notatki...)}
	w.Format = "pion"
	if z.Format == "poziom" {
		w.Format = "poziom"
	}

	// 1. nagrania: parametry i „obejrzenie” (ruch kamery, ostrość, twarze). Wynik zostaje w pamięci silnika.
	var klipy []*Klip
	var skany []*Skan
	for i, p := range z.Pliki {
		k, err := sonduj(n, p)
		if err != nil {
			blad(err)
		}
		komunikat("skan", 0.02+0.26*float64(i)/float64(len(z.Pliki)), fmt.Sprintf("Oglądam nagranie %d z %d", i+1, len(z.Pliki)))
		sk, err := skanuj(n, k)
		if err != nil {
			blad(err)
		}
		klipy, skany = append(klipy, k), append(skany, sk)
	}

	// 2. rodzaj rolki. Mówiona: w nagraniu przez większość czasu widać twarz zwróconą do kamery i słychać mowę.
	// Sam dźwięk nie wystarcza (rozmowy w tle, hałas narzędzi), chyba że opis mówi wprost, że to rolka mówiona.
	rodzaj := z.Rodzaj
	if ust.Rodzaj != "" {
		rodzaj = ust.Rodzaj
	}
	var mowione []int
	var tekstMowy []string
	if rodzaj != "ujecia" {
		for i, k := range klipy {
			twarz, _, _ := udzialTwarzy(skany[i])
			if !k.MaDzwiek || (rodzaj != "mowa" && twarz < 0.4) {
				continue
			}
			komunikat("mowa", 0.28+0.1*float64(i)/float64(len(klipy)), fmt.Sprintf("Słucham, co mówi osoba w nagraniu %d", i+1))
			o := ocenMowe(n, st, z.Praca, i, k)
			if o.Mowione || (rodzaj == "mowa" && o.Slow >= 4) {
				mowione = append(mowione, i)
				for _, s := range o.Slowa {
					if !toWtracenie(s.Tekst) { // bez yyy i eee: ten tekst trafia do opisu posta
						tekstMowy = append(tekstMowy, s.Tekst)
					}
				}
			}
		}
	}
	if rodzaj == "mowa" && len(mowione) == 0 {
		blad(fmt.Errorf("w nagraniach nie udało się rozpoznać mowy. Jeśli to mają być same ujęcia, wpisz w opisie: same ujęcia"))
	}

	// 3. teksty: nagłówek z opisu, temat wypowiedzi, opis posta. Model piszący działa tylko przez tę chwilę.
	naglowek := ust.Naglowek
	zrodloPosta := z.Opis
	if len(mowione) > 0 {
		zrodloPosta = strings.Join(tekstMowy, " ")
	}
	potrzebnyTekst := (len(naglowek) == 0 && !ust.BezNaglowka && z.Opis != "") || strings.TrimSpace(zrodloPosta) != ""
	if potrzebnyTekst {
		komunikat("teksty", 0.38, "Czytam opis i przygotowuję teksty")
		r, errR := uruchomRozum(n, z.Praca, "tekst")
		if errR != nil {
			w.Raport = append(w.Raport, "Model piszący nie wystartował, teksty pochodzą z szablonu.")
		}
		if len(naglowek) == 0 && !ust.BezNaglowka {
			naglowek = naglowekZOpisu(r, z.Opis)
		}
		if len(mowione) > 0 {
			w.Temat = tematWypowiedzi(r, zrodloPosta)
			w.Opis, w.Hashtagi = napiszPost(r, st, zrodloPosta, pierwszeZdania(zrodloPosta, 2))
		} else {
			w.Opis, w.Hashtagi = napiszPost(r, st, z.Opis, "")
		}
		r.Zamknij()
	} else {
		w.Opis, w.Hashtagi = opisPosta(st, "", z.Opis)
	}
	if ust.BezNaglowka || ust.BezNapisow {
		naglowek = nil
	}
	w.Naglowek = naglowek
	hak := strings.Join(naglowek, "|")

	// 4. montaż
	zakresPostepu = [2]float64{0.42, 0.98}
	if len(mowione) > 0 {
		w.Rodzaj = "mowa"
		a := []string{"--styl", stylPlik, "--dane", dane, "--praca", z.Praca, "--wyjscie", z.Wyjscie, "--hak", hak}
		if ust.BezNapisow {
			a = append(a, "--bez-napisow")
		}
		if !dlugi {
			a = append(a, "--maks", "60")
		}
		if zMuzyka {
			komunikat("muzyka", 0.41, "Dobieram muzykę")
			if u := wybierzUtwor(n, nastroj, filepath.Base(z.Praca), 40, nil); u != nil {
				a = append(a, "--muzyka", u.Plik, "--muzyka-od", liczba(u.Start))
				w.Muzyka = u.Nazwa
			}
		}
		for _, i := range mowione {
			a = append(a, klipy[i].Plik)
		}
		poleceniaMontuj(a)
		var m struct {
			Czas   float64  `json:"czas"`
			Raport []string `json:"raport"`
			Tekst  string   `json:"tekst"`
		}
		if b, err := os.ReadFile(filepath.Join(z.Praca, "mowa.json")); err == nil {
			json.Unmarshal(b, &m)
		}
		w.Czas, w.Tekst = m.Czas, m.Tekst
		for _, r := range m.Raport {
			w.Raport = append(w.Raport, zdanieRaportu(r))
		}
		if poza := len(klipy) - len(mowione); poza > 0 {
			w.Raport = append(w.Raport, fmt.Sprintf("Nagrań bez mowy do kamery: %d. Na razie nie są wstawiane jako przebitki.", poza))
		}
	} else {
		w.Rodzaj = "ujecia"
		napisy := "auto"
		if ust.BezNapisow {
			napisy = ""
		}
		a := []string{"--styl", stylPlik, "--dane", dane, "--praca", z.Praca, "--wyjscie", z.Wyjscie, "--rozum", "--napisy", napisy, "--hak", hak,
			"--dlugosc", liczba(ust.Dlugosc), "--ujecie", liczba(ust.Ujecie), "--pomin", fmt.Sprint(ust.Pomin)}
		if !z.BezPrzejsc {
			a = append(a, "--przejscia")
		}
		if zMuzyka {
			a = append(a, "--muzyka", "auto", "--nastroj", nastroj)
		}
		if z.BezAnimacji {
			a = append(a, "--bez-najazdu")
		}
		for _, k := range klipy {
			a = append(a, k.Plik)
		}
		polecenieUjecia(a, false)
		var u struct {
			Ujecia []Okno   `json:"ujecia"`
			Hasla  []string `json:"hasla"`
			Czas   float64  `json:"czas"`
			Nagran int      `json:"nagran"`
			Muzyka *Utwor   `json:"muzyka"`
		}
		if b, err := os.ReadFile(filepath.Join(z.Praca, "ujecia.json")); err == nil {
			json.Unmarshal(b, &u)
		}
		w.Czas, w.Hasla = u.Czas, u.Hasla
		w.Raport = append(w.Raport, fmt.Sprintf("Wybrane ujęcia: %d z %d nagrań.", len(u.Ujecia), u.Nagran))
		if u.Muzyka != nil {
			w.Muzyka = u.Muzyka.Nazwa
		}
		var kol []string
		for i, o := range u.Ujecia {
			if o.Opis != nil && i < 12 {
				kol = append(kol, fmt.Sprintf("%d. %s", i+1, skrocZdanie(o.Opis.Opis, 70)))
			}
		}
		if len(kol) > 0 {
			w.Raport = append(w.Raport, "Co widać na kolejnych ujęciach: "+strings.Join(kol, " "))
		}
	}
	if w.Muzyka != "" {
		w.Raport = append(w.Raport, "Muzyka: "+w.Muzyka+".")
	}
	w.Trwalo = time.Since(t0).Seconds()
	b, _ := json.MarshalIndent(w, "", " ")
	if err := os.WriteFile(filepath.Join(z.Praca, "wynik.json"), b, 0o644); err != nil {
		blad(err)
	}
	zakresPostepu = [2]float64{0, 1}
	komunikat("gotowe", 1, "Gotowe")
}

func zdanieRaportu(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	s = string(r)
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func skrocZdanie(s string, maks int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maks {
		return string(r)
	}
	ciecie := maks
	for i := maks; i > maks-20 && i > 0; i-- {
		if r[i] == ' ' {
			ciecie = i
			break
		}
	}
	return strings.TrimRight(string(r[:ciecie]), " ,.;") + "..."
}

func polecenieRodzajCLI(arg []string) {
	fs := flag.NewFlagSet("rodzaj", flag.ExitOnError)
	stylPlik := fs.String("styl", "szablon/styl.json", "szablon stylu")
	dane := fs.String("dane", "", "katalog danych silnika")
	fs.Parse(arg)
	n, err := znajdzNarzedzia(katalogDanych(*dane))
	if err != nil {
		blad(err)
	}
	st, err := wczytajStyl(*stylPlik)
	if err != nil {
		blad(err)
	}
	praca, _ := os.MkdirTemp("", "zblizka-rodzaj-")
	defer os.RemoveAll(praca)
	for i, p := range fs.Args() {
		abs, _ := filepath.Abs(p)
		k, err := sonduj(n, abs)
		if err != nil {
			blad(err)
		}
		sk, err := skanuj(n, k)
		if err != nil {
			blad(err)
		}
		twarz, tx, ty := udzialTwarzy(sk)
		linia := fmt.Sprintf("%-32s %6.1fs  twarz do kamery %3.0f%% (środek %.2f, %.2f)", filepath.Base(p), k.Czas, twarz*100, tx, ty)
		if twarz >= 0.4 {
			o := ocenMowe(n, st, praca, i, k)
			linia += fmt.Sprintf("  głośno %3.0f%%  słów %d (%.2f/s)  mówione=%v", o.Udzial*100, o.Slow, o.SlowNaSek, o.Mowione)
		}
		fmt.Println(linia)
	}
}
