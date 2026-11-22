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
	Format        string  `json:"format"`  // "pion" (9:16, domyślnie), "poziom" (16:9), "post" (4:5) albo "kwadrat" (1:1)
	Dlugosc       string  `json:"dlugosc"` // "krotki" (do 60 s, domyślnie) albo "dlugi"
	BezNapisow    bool    `json:"bezNapisow"`
	BezPrzejsc    bool    `json:"bezPrzejsc"`
	BezAnimacji   bool    `json:"bezAnimacji"`
	BezMuzyki     bool    `json:"bezMuzyki"`
	PoprzedniCzas float64 `json:"poprzedniCzas,omitempty"` // długość filmu, który jest poprawiany (do „krócej” i „dłużej”)
	Muzyka        string  `json:"muzyka"`                  // "" = automatycznie, "dynamiczny", "spokojny" albo "styl:Nazwa"
	Jakosc        string  `json:"jakosc"`                  // "hd" (domyślnie), "2k" albo "4k"
	JakoscPewna   bool    `json:"jakoscPewna,omitempty"`   // jakość wybrana przyciskiem przy gotowym filmie: ma pierwszeństwo przed opisem
	Ziarno        string  `json:"ziarno,omitempty"`        // ziarno wyboru utworu: poprawka filmu zostaje przy tym samym utworze
	BezUtworu     string  `json:"bezUtworu,omitempty"`     // tytuł utworu, którego nie brać („inna muzyka”)
	Wzor          string  `json:"wzor,omitempty"`          // katalog poprzedniej wersji, gdy to ten sam film w innej jakości
}

type Wynik struct {
	Plik     string   `json:"plik"`
	Rodzaj   string   `json:"rodzaj"` // mowa | ujecia
	Format   string   `json:"format"` // pion | poziom | post | kwadrat
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
	Jakosc   string   `json:"jakosc"` // hd | 2k | 4k: w jakiej jakości film naprawdę powstał
	Szer     int      `json:"szer"`   // rozmiar obrazu w pikselach
	Wys      int      `json:"wys"`
	Rozmiar  int64    `json:"rozmiar"` // wielkość pliku w bajtach
	// zdanie o jakości, gdy ograniczyły ją nagrania (za mała rozdzielczość na wybraną jakość)
	JakoscUwaga string `json:"jakoscUwaga,omitempty"`
}

// Ustawienia: to, co wynika z opisu i z uwag wpisanych w „Popraw”. Opis zmienia treść i tempo rolki, nie styl marki.
type Ustawienia struct {
	Rodzaj        string // wymuszony rodzaj rolki
	Dlugosc       float64
	Ujecie        float64
	SkalaNapisu   float64
	BezNapisow    bool
	BezNaglowka   bool
	BezMuzyki     bool
	Naglowek      []string
	Pomin         int    // ile ujęć z początku pominąć („skróć początek”)
	DlugoscPodana bool   // długość wpisana wprost w opisie (sekundy albo minuty)
	Format        string // "pion", "poziom", "post" (4:5) albo "kwadrat" (1:1), gdy opis mówi o proporcjach
	Kierunek      int    // ostatnia poprawka: -1 = krócej, 1 = dłużej (liczone od długości poprzedniego filmu)
	Jakosc        string // "hd", "2k" albo "4k", gdy opis lub uwaga mówi o jakości
	InnaMuzyka    bool   // ostatnia poprawka prosi o inny utwór
	Notatki       []string
}

var reSekundy = regexp.MustCompile(`(\d{1,3})\s*(s\b|sek)`)
var reMinuty = regexp.MustCompile(`(\d{1,2}(?:[.,]\d)?)\s*min`)
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
		male := strings.ToLower(surowe)
		if f := formatZTekstu(male, ma); f != "" {
			u.Format = f
		}
		if q := jakoscZTekstu(male); q != "" {
			u.Jakosc = q
		}
		if !zOpisu && nr == len(wszystko)-1 && ma(" inna muzyk", " inny utwor", " inna piosenk", " zmien muzyk", " zmien utwor", " inny podklad", " zmien podklad") {
			u.InnaMuzyka = true
			u.Notatki = append(u.Notatki, "Inny utwór w podkładzie.")
		}
		if mm := reMinuty.FindStringSubmatch(male); mm != nil || strings.Contains(t, " poltorej min") || strings.Contains(t, " pol minuty") {
			v := 1.5
			if strings.Contains(t, " pol minuty") {
				v = 0.5
			}
			if mm != nil {
				v, _ = strconv.ParseFloat(strings.Replace(mm[1], ",", ".", 1), 64)
			}
			if v >= 0.2 && v <= 10 {
				u.Dlugosc, u.DlugoscPodana = math.Round(v*60), true
				u.Notatki = append(u.Notatki, fmt.Sprintf("Długość ustawiona na %.0f s.", u.Dlugosc))
			}
		} else if m := reSekundy.FindStringSubmatch(t); m != nil {
			if v, _ := strconv.Atoi(m[1]); v >= 6 && v <= 600 {
				u.Dlugosc, u.DlugoscPodana = float64(v), true
				u.Notatki = append(u.Notatki, fmt.Sprintf("Długość ustawiona na %d s.", v))
			}
		} else if ma(" krocej", " skroc ", " krotsz", " za dlug") && !ma(" poczat") {
			u.Dlugosc *= 0.72
			u.Notatki = append(u.Notatki, "Krótszy film.")
			if nr > 0 && nr == len(wszystko)-1 {
				u.Kierunek = -1
			}
		} else if ma(" dluzej", " wydluz", " dluzsz", " za krotk") {
			u.Dlugosc *= 1.3
			u.Notatki = append(u.Notatki, "Dłuższy film.")
			if nr > 0 && nr == len(wszystko)-1 {
				u.Kierunek = 1
			}
		}
		if ma(" poczat") && ma(" skroc", " krotsz", " utnij", " wytnij", " bez poczat", " pomin") {
			u.Pomin++
			u.Notatki = append(u.Notatki, "Początek skrócony (o jedno ujęcie albo jedno zdanie).")
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
	u.Dlugosc = math.Max(6, math.Min(600, u.Dlugosc))
	return u
}

// stylZlecenia: gdy uwagi zmieniają wielkość napisów, powstaje kopia stylu tylko dla tego zlecenia.
func stylZlecenia(z *Zadanie, u Ustawienia, skalaObrazu float64) string {
	poziom := z.Format == "poziom"
	nizszy := z.Format == "post" || z.Format == "kwadrat" // szerokość jak w rolce, kadr niższy: napisy bez zmian
	if u.SkalaNapisu == 1 && !poziom && !nizszy && !z.BezAnimacji && skalaObrazu <= 1 {
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
	if nizszy {
		m["szer"], m["wys"] = siatkaFormatu(z.Format)
	}
	if skalaObrazu > 1 {
		m["skala"] = skalaObrazu
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
	Tlo, Glos float64 // poziom tła i głośnych fragmentów (dB względem pełnej skali)
}

// ocenMowe: pomiar głośności, a gdy wygląda to na mowę, pełna transkrypcja (zapamiętana dla montażu).
func ocenMowe(n *Narzedzia, st *Styl, praca string, i int, k *Klip) OcenaMowy {
	return ocenDzwiek(n, st, praca, i, k, true)
}

// ocenDzwiek: jak ocenMowe; bez słuchania (sluchaj = false) tylko pomiar głośności, bez transkrypcji.
func ocenDzwiek(n *Narzedzia, st *Styl, praca string, i int, k *Klip, sluchaj bool) OcenaMowy {
	o := OcenaMowy{Klip: i}
	if !k.MaDzwiek {
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
	o.Tlo, o.Glos = poziomyDzwieku(probki, czest)
	if sluchaj && n.Whisper != "" && n.Model != "" && o.Udzial >= 0.25 && glosno >= 3 {
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

// mozeBycWypowiedzia: czy warto słuchać nagrania (tryb automatyczny). Twarz do kamery przez większość czasu albo
// przez część dłuższego nagrania (osoba mówi i co chwilę pokazuje produkt). Nagrania bez twarzy są słuchane tylko
// wtedy, gdy pomiar głośności wskazuje głos tuż przy telefonie (glosPrzyTelefonie): rozmowy ekipy w tle odpadają
// bez transkrypcji, więc pierwsze oglądanie materiału się nie wydłuża.
func mozeBycWypowiedzia(twarz, czas float64) bool {
	return twarz >= 0.4 || (twarz >= 0.12 && czas >= 6)
}

// glosPrzyTelefonie: ktoś mówi zza kamery tuż przy telefonie: głośno (92. centyl >= -24 dB), wyraźnie nad tłem
// (>= 14 dB) i przez co najmniej połowę nagrania. Pomiar 20.11.2026 na 27 nagraniach z montażu szyby: rozmowy ekipy
// i hałas narzędzi mają -21 do -39 dB, ale wtedy mowa jest rzadka (najwyżej 0,85 słowa/s przy -21 dB).
func glosPrzyTelefonie(o OcenaMowy) bool {
	return o.Glos >= -24 && o.Glos-o.Tlo >= 14 && o.Udzial >= 0.5
}

// czyWypowiedz: czy nagranie to wypowiedź do filmu mówionego (tryb automatyczny). Twarz przez większość nagrania
// i gęsta mowa: tak. Osoba, która mówi i co chwilę pokazuje produkt (twarz rzadziej, 12-40% nagrania): tak, gdy
// mowa jest gęsta, wyraźna i nie składa się z powtórek (whisper w hałasie „słyszy” w kółko to samo zdanie).
// Rozmowy ekipy w tle są rzadkie i ciche, więc nie przechodzą (sprawdzone na 27 nagraniach z montażu szyby).
// udzialRoznychSlow: jaka część słów jest różna (whisper w hałasie powtarza w kółko to samo zdanie).
func udzialRoznychSlow(o OcenaMowy) float64 {
	if o.Slow == 0 {
		return 0
	}
	rozne := map[string]bool{}
	for _, s := range o.Slowa {
		if t := normalizuj(s.Tekst); t != "" && !toWtracenie(s.Tekst) {
			rozne[t] = true
		}
	}
	return float64(len(rozne)) / float64(o.Slow)
}

func czyWypowiedz(o OcenaMowy, twarz float64) bool {
	if o.Slow < 8 || o.SlowNaSek < 0.9 {
		return false
	}
	if twarz >= 0.4 {
		return true
	}
	rozne := map[string]bool{}
	for _, s := range o.Slowa {
		if t := normalizuj(s.Tekst); t != "" && !toWtracenie(s.Tekst) {
			rozne[t] = true
		}
	}
	if float64(len(rozne)) < 0.45*float64(o.Slow) {
		return false
	}
	if twarz >= 0.12 && o.Slow >= 15 && o.SlowNaSek >= 1.3 && o.Udzial >= 0.4 {
		return true
	}
	// głos bez twarzy w kadrze (zza kamery nad produktem albo twarz, której automat nie rozpoznał): wyraźna mowa blisko
	// telefonu. Pomiar 22.11.2026: rozmowy ekipy na 24 nagraniach z montażu mają głos -26 dB i ciszej albo poniżej
	// 0,9 słowa/s; narracja właściciela do telefonu -22 dB i 1,5 słowa/s (stary próg 1,6 ją odrzucał).
	return o.Glos >= -24 && o.Glos-o.Tlo >= 10 && o.Slow >= 12 && o.SlowNaSek >= 1.2
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
	// model, który zamiast opisu tłumaczy się, że nie ma materiału o marce: wtedy szablon
	for _, odmowa := range []string{"nie mam ", "nie mogę", "nie moge", "proszę dostarcz", "prosze dostarcz", "nie komentuj", "brak informacji", "nie posiadam"} {
		if strings.Contains(strings.ToLower(opis), odmowa) {
			return szablonOpis, szablonTagi
		}
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
	// To, co użytkownik napisał w opisie, ma pierwszeństwo przed przełącznikami na stronie: proporcje i długość.
	if ust.Format != "" && ust.Format != z.Format && !(ust.Format == "pion" && z.Format == "") {
		z.Format = ust.Format
		ust.Notatki = append(ust.Notatki, zdanieFormatu(ust.Format))
	}
	// „krócej” i „dłużej” w poprawce: względem tego, co naprawdę wyszło poprzednio
	if z.PoprzedniCzas > 0 && ust.Kierunek != 0 {
		ust.Dlugosc = math.Max(6, z.PoprzedniCzas*0.72)
		if ust.Kierunek > 0 {
			ust.Dlugosc = math.Min(600, z.PoprzedniCzas*1.3)
		}
		ust.DlugoscPodana = true
	}
	if ust.DlugoscPodana && ust.Dlugosc > 60 {
		z.Dlugosc = "dlugi"
	}
	if ust.DlugoscPodana && ust.Dlugosc <= 60 && z.Dlugosc == "dlugi" {
		z.Dlugosc = "krotki"
	}
	// parametry nagrań (szybkie): od ich rozdzielczości zależy, jaka jakość pliku ma sens
	var klipy []*Klip
	for _, p := range z.Pliki {
		k, err := sonduj(n, p)
		if err != nil {
			blad(err)
		}
		klipy = append(klipy, k)
	}
	// jakość: przełącznik na stronie, a gdy opis albo uwaga mówi o jakości, to one (chyba że jakość wybrano przyciskiem
	// przy gotowym filmie). Nigdy wyżej, niż pozwalają nagrania.
	jak := jakoscZKlucza(z.Jakosc)
	if ust.Jakosc != "" && !z.JakoscPewna {
		jak = jakoscZKlucza(ust.Jakosc)
	}
	siatkaSzer, siatkaWys := siatkaFormatu(z.Format)
	jak, notkaJakosci, jakoscOgraniczona := dobierzJakosc(jak, klipy, siatkaSzer, siatkaWys)
	// ten sam film w innej jakości: teksty z poprzedniej wersji (model piszący za każdym razem pisze trochę inaczej)
	var wzor *Wynik
	if z.Wzor != "" {
		if b, err := os.ReadFile(filepath.Join(z.Wzor, "wynik.json")); err == nil {
			var p Wynik
			if json.Unmarshal(b, &p) == nil {
				wzor = &p
			}
		}
	}
	ziarno := z.Ziarno
	if ziarno == "" {
		ziarno = filepath.Base(z.Praca)
	}
	pomijanyUtwor = z.BezUtworu
	stylPlik := stylZlecenia(z, ust, jak.Skala)
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
	w.Jakosc, w.Szer, w.Wys = jak.Klucz, st.ObrazSzer(), st.ObrazWys()
	if notkaJakosci != "" {
		w.Raport = append(w.Raport, notkaJakosci)
	}
	if jakoscOgraniczona {
		w.JakoscUwaga = notkaJakosci
	}
	w.Format = formatZKlucza(z.Format)

	// 1. nagrania: parametry i „obejrzenie” (ruch kamery, ostrość, twarze). Wynik zostaje w pamięci silnika.
	var skany []*Skan
	for i, k := range klipy {
		komunikat("skan", 0.02+0.26*float64(i)/float64(len(z.Pliki)), fmt.Sprintf("Oglądam nagranie %d z %d", i+1, len(z.Pliki)))
		sk, err := skanuj(n, k)
		if err != nil {
			blad(err)
		}
		skany = append(skany, sk)
	}

	// 2. rodzaj rolki. Mówiona: w nagraniu przez większość czasu widać twarz zwróconą do kamery i słychać mowę.
	// Sam dźwięk nie wystarcza (rozmowy w tle, hałas narzędzi), chyba że opis mówi wprost, że to rolka mówiona.
	rodzaj := z.Rodzaj
	if ust.Rodzaj != "" {
		rodzaj = ust.Rodzaj
	}
	var mowione []int
	var tekstMowy []string
	var oceny []OcenaMowy
	if rodzaj != "ujecia" {
		maTwarz := false // jest nagranie z osobą mówiącą do kamery: reszta to przebitki, głosu zza kamery nie szukamy
		for i, k := range klipy {
			if t, _, _ := udzialTwarzy(skany[i]); k.MaDzwiek && t >= 0.4 {
				maTwarz = true
			}
		}
		for i, k := range klipy {
			twarz, _, _ := udzialTwarzy(skany[i])
			if !k.MaDzwiek {
				continue
			}
			// Słuchamy każdego nagrania, w którym słychać głos (właściciel 22.11.2026: „silnik ma sam zrobić transkrypcję
			// i zrozumieć, o czym jest film, dopiero wtedy montować”). Bez słuchania zostają tylko nagrania ciche i bardzo krótkie.
			if rodzaj != "mowa" && !mozeBycWypowiedzia(twarz, k.Czas) {
				if maTwarz || k.Czas < 4 || k.Czas > 600 {
					continue
				}
				if d := ocenDzwiek(n, st, z.Praca, i, k, false); d.Udzial < 0.25 || d.Glos < -32 {
					continue
				}
			}
			if n.Whisper == "" || n.Model == "" {
				if d := ocenDzwiek(n, st, z.Praca, i, k, false); rodzaj == "mowa" || twarz >= 0.4 || glosPrzyTelefonie(d) {
					blad(fmt.Errorf("w nagraniu słychać, że ktoś mówi, ale silnik nie ma jeszcze narzędzia do rozpoznawania mowy. Uruchom silnik ponownie i wybierz „Pobierz i przygotuj”. Jeśli film ma być bez mowy, wpisz w opisie: same ujęcia"))
				}
				continue
			}
			komunikat("mowa", 0.28+0.1*float64(i)/float64(len(klipy)), fmt.Sprintf("Słucham, co mówi osoba w nagraniu %d", i+1))
			o := ocenMowe(n, st, z.Praca, i, k)
			if rodzaj != "mowa" {
				o.Mowione = czyWypowiedz(o, twarz)
			}
			oceny = append(oceny, o)
		}
		// wypowiedź to nagrania z gęstą mową. Gdy opis wymusza film mówiony, a żadne nagranie nie spełnia progu,
		// zostaje jedno z najgęstszą mową; rozmowy w tle na ujęciach roboczych nie są wypowiedzią.
		naj := -1
		for j, o := range oceny {
			if o.Mowione {
				mowione = append(mowione, o.Klip)
			}
			if o.Slow >= 4 && (naj < 0 || o.SlowNaSek > oceny[naj].SlowNaSek) {
				naj = j
			}
		}
		if len(mowione) == 0 && rodzaj == "mowa" && naj >= 0 {
			mowione = append(mowione, oceny[naj].Klip)
		}
		// Z treści: gdy w co najmniej połowie nagrań z dźwiękiem ktoś mówi dużo i składnie, to jest film mówiony, nawet
		// jeśli twarzy nie widać, a głos jest cichszy (jedno albo dwa nagrania osoby opowiadającej o produkcie).
		if len(mowione) == 0 && rodzaj != "mowa" {
			zDzwiekiem := 0
			for _, k := range klipy {
				if k.MaDzwiek {
					zDzwiekiem++
				}
			}
			var geste []int
			for _, o := range oceny {
				if o.Slow >= 15 && o.SlowNaSek >= 1.2 && udzialRoznychSlow(o) >= 0.45 {
					geste = append(geste, o.Klip)
				}
			}
			if len(geste) > 0 && 2*len(geste) >= zDzwiekiem {
				mowione = geste
			}
		}
		// gdy w którymś z tych nagrań widać twarz do kamery, wypowiedzią są tylko nagrania z twarzą (reszta to rozmowy ekipy w tle)
		if len(mowione) > 1 {
			twarze := map[int]float64{}
			najT := 0.0
			for _, i := range mowione {
				twarze[i], _, _ = udzialTwarzy(skany[i])
				najT = math.Max(najT, twarze[i])
			}
			if najT >= 0.2 {
				var zTwarza []int
				for _, i := range mowione {
					if twarze[i] >= 0.5*najT {
						zTwarza = append(zTwarza, i)
					}
				}
				mowione = zTwarza
			}
		}
		jest := map[int]bool{}
		for _, i := range mowione {
			jest[i] = true
		}
		for _, o := range oceny {
			if !jest[o.Klip] {
				continue
			}
			for _, s := range o.Slowa {
				if !toWtracenie(s.Tekst) { // bez yyy i eee: ten tekst trafia do opisu posta
					tekstMowy = append(tekstMowy, s.Tekst)
				}
			}
		}
	}
	// mowa słyszalna, ale nie uznana za wypowiedź: raport mówi to wprost (wcześniej znikała bez słowa)
	if len(mowione) == 0 {
		for _, o := range oceny {
			if o.Slow >= 8 {
				w.Raport = append(w.Raport, fmt.Sprintf("W nagraniu %s słychać mowę, ale nie wygląda na wypowiedź do kamery, więc film jest z samych ujęć. Jeśli to ma być film mówiony, wpisz w opisie albo w „Popraw”: mówiona.", filepath.Base(klipy[o.Klip].Plik)))
				break
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
	if wzor != nil {
		if len(naglowek) == 0 && !ust.BezNaglowka {
			naglowek = wzor.Naglowek
		}
		w.Temat, w.Opis, w.Hashtagi = wzor.Temat, wzor.Opis, wzor.Hashtagi
	} else if len(mowione) > 0 {
		// film mówiony: nagłówek, temat i opis posta powstają w montażu razem z kartą treści (jedno wczytanie modelu)
	} else if potrzebnyTekst {
		komunikat("teksty", 0.38, "Czytam opis i przygotowuję teksty")
		var r *Rozum
		var errR error
		// duży model piszący (opis posta), a gdy go nie ma, model treści: nagłówek z opisu pisze lepiej niż mały model od obrazów
		if m := znajdzModelPiszacy(dane); m != "" {
			r, errR = uruchomModel(n, z.Praca, "tekst", m, "", 8192)
		} else if m := znajdzModelTresci(dane); m != "" {
			r, errR = uruchomModel(n, z.Praca, "tekst", m, "", oknoTresci)
		} else if os.Getenv("ZM_BEZ_MODELU") == "" {
			r, errR = uruchomRozum(n, z.Praca, "tekst")
		}
		if errR != nil {
			w.Raport = append(w.Raport, "Model piszący nie wystartował, teksty pochodzą z szablonu.")
		}
		if len(naglowek) == 0 && !ust.BezNaglowka {
			naglowek = naglowekZOpisu(r, z.Opis)
		}
		w.Opis, w.Hashtagi = napiszPost(r, st, z.Opis, "")
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
		a := []string{"--styl", stylPlik, "--dane", dane, "--praca", z.Praca, "--wyjscie", z.Wyjscie, "--hak", hak, "--pomin", fmt.Sprint(ust.Pomin)}
		if ust.BezNapisow {
			a = append(a, "--bez-napisow")
		}
		switch {
		case ust.DlugoscPodana:
			a = append(a, "--maks", liczba(ust.Dlugosc))
		case !dlugi:
			a = append(a, "--maks", "60")
		}
		if zMuzyka {
			komunikat("muzyka", 0.41, "Dobieram muzykę")
			if u := wybierzUtwor(n, nastroj, ziarno, 40, nil); u != nil {
				// podkład pod mowę: ok. 8 dB pod głosem przed ściszaniem i ok. 14 dB pod głosem, gdy osoba mówi (pomiar
				// 20.11.2026: wcześniej 26 dB, muzyki prawie nie było słychać), liczone z głośności tego utworu
				poziom := math.Max(0.08, math.Min(0.5, wzmocnienieMuzyki(u, -25)))
				a = append(a, "--muzyka", u.Plik, "--muzyka-od", liczba(u.Start), "--muzyka-poziom", liczba(poziom))
				w.Muzyka = u.Nazwa
			}
		}
		// nagrania bez mowy do kamery: ich fragmenty wejdą jako przebitki
		jestMowione := map[int]bool{}
		for _, i := range mowione {
			jestMowione[i] = true
		}
		var dodatkowe []string
		for i, k := range klipy {
			if !jestMowione[i] {
				dodatkowe = append(dodatkowe, k.Plik)
			}
		}
		if len(dodatkowe) > 0 {
			lista := filepath.Join(z.Praca, "przebitki.json")
			b, _ := json.Marshal(dodatkowe)
			if os.WriteFile(lista, b, 0o644) == nil {
				a = append(a, "--przebitki", lista)
			}
		}
		if wzor == nil {
			if hak == "" && !ust.BezNaglowka && !ust.BezNapisow {
				a = append(a, "--naglowek-auto", "--opis", z.Opis)
			}
			a = append(a, "--teksty")
		}
		for _, i := range mowione {
			a = append(a, klipy[i].Plik)
		}
		poleceniaMontuj(a)
		var m struct {
			Czas        float64     `json:"czas"`
			Raport      []string    `json:"raport"`
			Tekst       string      `json:"tekst"`
			Przebitki   []Przebitka `json:"przebitki"`
			ZaOsoba     bool        `json:"zaOsoba"`
			Naglowek    []string    `json:"naglowek"`
			Temat       string      `json:"temat"`
			Opis        string      `json:"opis"`
			Hashtagi    []string    `json:"hashtagi"`
			Zrozumienie []string    `json:"zrozumienie"` // co silnik zrozumiał z wypowiedzi (karta treści)
		}
		if b, err := os.ReadFile(filepath.Join(z.Praca, "mowa.json")); err == nil {
			json.Unmarshal(b, &m)
		}
		w.Czas, w.Tekst = m.Czas, m.Tekst
		if wzor == nil {
			w.Temat, w.Opis, w.Hashtagi, w.Naglowek = m.Temat, m.Opis, m.Hashtagi, m.Naglowek
		}
		w.Raport = append(w.Raport, m.Zrozumienie...)
		for _, r := range m.Raport {
			w.Raport = append(w.Raport, zdanieRaportu(r))
		}
		if poza := len(klipy) - len(mowione); poza > 0 {
			if len(m.Przebitki) > 0 {
				dopasowane := 0
				for _, p := range m.Przebitki {
					if p.Slowo != "" {
						dopasowane++
					}
				}
				w.Raport = append(w.Raport, fmt.Sprintf("Przebitki: %d (z %d nagrań bez mowy do kamery), w tym dopasowane do wypowiadanych słów: %d.", len(m.Przebitki), poza, dopasowane))
			} else {
				w.Raport = append(w.Raport, fmt.Sprintf("Nagrań bez mowy do kamery: %d. Nie weszły jako przebitki (film za krótki albo ujęcia za słabe).", poza))
			}
		}
		if m.ZaOsoba {
			w.Raport = append(w.Raport, "Nagłówek stoi za osobą.")
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
			a = append(a, "--muzyka", "auto", "--nastroj", nastroj, "--ziarno", ziarno)
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
		if (ust.DlugoscPodana || dlugi) && u.Czas < 0.8*ust.Dlugosc {
			w.Raport = append(w.Raport, fmt.Sprintf("Film ma %.0f s zamiast ok. %.0f s: z tych nagrań nie da się wybrać więcej różnych, dobrych ujęć (ujęć nie powtarzam). Więcej nagrań da dłuższy film.", u.Czas, ust.Dlugosc))
		}
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
	if fi, err := os.Stat(z.Wyjscie); err == nil {
		w.Rozmiar = fi.Size()
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
	wszystkie := fs.Bool("wszystkie", false, "słuchaj też nagrań bez twarzy (pomiar)")
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
		if k.MaDzwiek {
			o := ocenDzwiek(n, st, praca, i, k, false)
			if *wszystkie || mozeBycWypowiedzia(twarz, k.Czas) || (k.Czas >= 4 && o.Udzial >= 0.25 && o.Glos >= -32) {
				o = ocenMowe(n, st, praca, i, k)
			}
			linia += fmt.Sprintf("  tło %4.0f dB  głos %4.0f dB  głośno %3.0f%%  słów %d (%.2f/s)  wypowiedź=%v", o.Tlo, o.Glos, o.Udzial*100, o.Slow, o.SlowNaSek, czyWypowiedz(o, twarz))
		}
		fmt.Println(linia)
	}
}
