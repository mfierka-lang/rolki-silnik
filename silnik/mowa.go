// Mowa: wyciągnięcie dźwięku, wykrycie fragmentów mówionych (własny detektor głośności, bez dodatkowych modeli)
// i transkrypcja po polsku w whisper.cpp z czasem każdego słowa.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

type Slowo struct {
	Tekst  string  // z interpunkcją, jak w transkrypcji
	Od, Do float64 // sekundy w nagraniu źródłowym
}

type Przedzial struct{ Od, Do float64 }

// wyciagnijAudio: dźwięk nagrania jako WAV mono 16 kHz (format wejściowy whisper.cpp i detektora mowy).
func wyciagnijAudio(n *Narzedzia, plik, wav string) error {
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
	defer stop()
	_, errs, err := uruchom(ctx, "", n.FFmpeg, "-v", "error", "-y", "-i", plik, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav)
	if err != nil {
		return fmt.Errorf("nie udało się wyciągnąć dźwięku: %s", ogon(errs, 200))
	}
	return nil
}

func wczytajWav(plik string) ([]int16, int, error) {
	b, err := os.ReadFile(plik)
	if err != nil {
		return nil, 0, err
	}
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("to nie jest plik WAV")
	}
	czest := 16000
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		rozm := int(binary.LittleEndian.Uint32(b[p+4 : p+8]))
		if id == "fmt " && p+16 <= len(b) {
			czest = int(binary.LittleEndian.Uint32(b[p+12 : p+16]))
		}
		if id == "data" {
			pocz := p + 8
			kon := pocz + rozm
			if kon > len(b) || rozm <= 0 {
				kon = len(b)
			}
			probki := make([]int16, (kon-pocz)/2)
			for i := range probki {
				probki[i] = int16(binary.LittleEndian.Uint16(b[pocz+2*i:]))
			}
			return probki, czest, nil
		}
		p += 8 + rozm + rozm%2
	}
	return nil, 0, fmt.Errorf("plik WAV bez danych")
}

// wykryjMowe: przedziały, w których ktoś mówi. Próg dobierany do nagrania: między poziomem tła (szum drukarek,
// pomieszczenie) a poziomem głosu. Krótkie przerwy wewnątrz słów są sklejane.
// poziomyDzwieku: poziom tła (15. centyl) i głośnych fragmentów (92. centyl) w dB względem pełnej skali, w oknach 20 ms.
// Głos tuż przy telefonie (ktoś mówi zza kamery) jest głośny i wyraźnie nad tłem; rozmowy ekipy w tle są ciche.
func poziomyDzwieku(probki []int16, czest int) (tlo, glos float64) {
	skok := czest / 100
	okno := 2 * skok
	if skok <= 0 || len(probki) < okno*10 {
		return -100, -100
	}
	ile := (len(probki) - okno) / skok
	db := make([]float64, ile)
	for i := 0; i < ile; i++ {
		var s float64
		for _, v := range probki[i*skok : i*skok+okno] {
			f := float64(v) / 32768
			s += f * f
		}
		db[i] = 10 * math.Log10(s/float64(okno)+1e-10)
	}
	sort.Float64s(db)
	return db[len(db)*15/100], db[len(db)*92/100]
}

func wykryjMowe(probki []int16, czest int) []Przedzial {
	mowa, db := wykryjMoweProfil(probki, czest)
	zapamietajProfil(mowa, db) // do stawiania cięć w środku frazy w najcichszym miejscu (powtorki.go)
	return mowa
}

// wykryjMoweProfil: przedziały mowy i poziom głośności nagrania w dB co 10 ms.
func wykryjMoweProfil(probki []int16, czest int) ([]Przedzial, []float64) {
	skok := czest / 100 // 10 ms
	okno := 2 * skok
	if len(probki) < okno*10 {
		return nil, nil
	}
	ile := (len(probki) - okno) / skok
	db := make([]float64, ile)
	for i := 0; i < ile; i++ {
		var s float64
		for _, v := range probki[i*skok : i*skok+okno] {
			f := float64(v) / 32768
			s += f * f
		}
		db[i] = 10 * math.Log10(s/float64(okno)+1e-10)
	}
	pos := append([]float64(nil), db...)
	sort.Float64s(pos)
	tlo := pos[len(pos)*15/100]
	glos := pos[len(pos)*92/100]
	if glos < -55 { // w nagraniu praktycznie nie ma dźwięku
		return nil, nil
	}
	prog := tlo + math.Max(6, math.Min(15, 0.4*(glos-tlo)))
	prog = math.Max(prog, -55)
	prog = math.Min(prog, glos-10)
	var wynik []Przedzial
	wMowie := false
	start, cisza, glosno := 0, 0, 0
	for i, v := range db {
		if !wMowie {
			if v > prog {
				glosno++
				if glosno >= 3 {
					wMowie, start, cisza = true, i-glosno+1, 0
				}
			} else {
				glosno = 0
			}
			continue
		}
		if v < prog-3 {
			cisza++
			if cisza >= 18 { // 180 ms ciszy kończy fragment
				wynik = append(wynik, Przedzial{float64(start) / 100, float64(i-cisza+1)/100 + 0.02})
				wMowie, glosno = false, 0
			}
		} else {
			cisza = 0
		}
	}
	if wMowie {
		wynik = append(wynik, Przedzial{float64(start) / 100, float64(ile) / 100})
	}
	// bardzo krótkie trzaski odpadają
	var czyste []Przedzial
	for _, p := range wynik {
		if p.Do-p.Od >= 0.09 {
			czyste = append(czyste, p)
		}
	}
	return czyste, db
}

type whisperJSON struct {
	Transcription []struct {
		Tokens []struct {
			Text    string `json:"text"`
			Offsets struct {
				From int `json:"from"`
				To   int `json:"to"`
			} `json:"offsets"`
			TDtw int `json:"t_dtw"`
		} `json:"tokens"`
	} `json:"transcription"`
}

// presetDTW: nazwa układu głowic uwagi dla modelu (dokładne czasy słów liczone metodą DTW).
func presetDTW(model string) string {
	m := strings.ToLower(filepath.Base(model))
	for _, p := range [][2]string{{"large-v3-turbo", "large.v3.turbo"}, {"large-v3", "large.v3"}, {"large-v2", "large.v2"}, {"medium", "medium"}, {"small", "small"}, {"base", "base"}, {"tiny", "tiny"}} {
		if strings.Contains(m, p[0]) {
			return p[1]
		}
	}
	return ""
}

// argumentyWhispera: wspólne ustawienia obu przebiegów (całe nagranie i dosłuchiwanie okien).
func argumentyWhispera(n *Narzedzia, podpowiedz string) []string {
	watki := runtime.NumCPU()
	if watki > 8 {
		watki = 8
	}
	if watki < 2 {
		watki = 2
	}
	arg := []string{"-m", n.Model, "-l", "pl", "-t", fmt.Sprint(watki), "-ojf", "-np", "-sns"}
	if p := presetDTW(n.Model); p != "" {
		arg = append(arg, "-nfa", "--dtw", p)
	}
	if podpowiedz != "" {
		arg = append(arg, "--prompt", podpowiedz)
	}
	return arg
}

// transkrybuj: whisper.cpp, język polski, pełny wynik z czasem każdego kawałka słowa. Czas końca słowa pochodzi
// z dopasowania DTW (dokładność ok. 0,05-0,1 s; zwykłe czasy whispera potrafią się mylić o 2 s na początku zdania).
// DTW wymaga wyłączenia "flash attention" (-nfa). Podpowiedź ze słownikiem marki pomaga w pisowni nazw.
// Słowo dostaje tylko pewny czas końca (Do); początek (Od) ustala potem frazyZMowy na podstawie wykrytej mowy.
func transkrybuj(n *Narzedzia, wav, katalog, podpowiedz string) ([]Slowo, error) {
	if n.Whisper == "" || n.Model == "" {
		return nil, fmt.Errorf("brak whisper.cpp albo modelu mowy w katalogu danych (%s)", n.Dane)
	}
	prefiks := filepath.Join(katalog, "mowa")
	wspolne := argumentyWhispera(n, podpowiedz)
	arg := append(append([]string{}, wspolne...), "-f", wav, "-of", prefiks)
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Minute)
	defer stop()
	_, errs, err := uruchom(ctx, filepath.Dir(n.Whisper), n.Whisper, arg...)
	if err != nil {
		return nil, fmt.Errorf("transkrypcja nie powiodła się: %s", ogon(errs, 300))
	}
	b, err := os.ReadFile(prefiks + ".json")
	if err != nil {
		return nil, fmt.Errorf("brak wyniku transkrypcji: %v", err)
	}
	slowa, err := slowaZWhispera(b)
	if err != nil || os.Getenv("ZM_BEZ_DOSLUCHU") != "" {
		return slowa, err
	}
	return dosluchaj(n, wav, katalog, wspolne, slowa), nil
}

// Dosłuchiwanie. Whisper pisze „czysty” tekst: gdy mówca urywa zdanie i zaczyna je od nowa, w transkrypcji zostaje
// jedno podejście, a słowa rozciągają się nad drugim (zmierzone na polskiej mowie z wklejonymi pomyłkami: z 6 powtórek
// zapisana była 1). Powtórki nie widać wtedy w tekście, więc nie da się jej wyciąć. Ślad zostaje w czasach: między
// końcami dwóch kolejnych słów mieści się dużo więcej mowy, niż trwa słowo. Takie miejsce transkrybuję jeszcze raz,
// osobno każdy kawałek mowy między pauzami: w krótkim oknie bez ciągu dalszego Whisper zapisuje to, co naprawdę padło.

// dosluchLuka: tyle sekund między końcem jednego słowa a spodziewanym początkiem następnego budzi podejrzenie.
var dosluchLuka = 0.30

// oknoDosluchu: wycinek nagrania do ponownej transkrypcji i zakres czasu, w którym nowe słowa zastępują stare.
type oknoDosluchu struct {
	od, do   float64 // dźwięk okna
	zOd, zDo float64 // wymieniane są słowa, które kończą się w tym zakresie
}

// oknaDosluchu: wycinki do ponownej transkrypcji. Okno to kawałek mowy między pauzami (z wykryjMowe), a gdy kawałek
// jest długi, tylko kilka sekund wokół podejrzanego miejsca. Okno ucięte w środku mowy zaczyna się albo kończy na
// końcu słowa, a słowa z ostatniej sekundy przy uciętym brzegu zostają stare (tam nowy zapis bywa urwany).
func oknaDosluchu(mowa []Przedzial, slowa []Slowo) []oknoDosluchu {
	const wokol = 4.0 // sekund mowy przed i po podejrzanym miejscu
	zakresy := map[int][]Przedzial{}
	for k, s := range slowa {
		litery := []rune(normalizuj(s.Tekst))
		if len(litery) == 0 || strings.IndexFunc(s.Tekst, unicode.IsDigit) >= 0 {
			continue // liczba zapisana cyframi trwa dłużej, niż wynika z liczby znaków
		}
		poprz := 0.0
		if k > 0 {
			poprz = slowa[k-1].Do
		}
		pocz := s.Do - (0.20 + 0.06*float64(len(litery))) // spodziewany początek słowa
		if pocz-poprz < dosluchLuka {
			continue
		}
		niezapisane := 0.0
		var kawalki []int
		for i, m := range mowa {
			if m.Do < poprz-0.5 || m.Od > s.Do { // pół sekundy zapasu: słowo przed pauzą bywa datowane już w ciszy
				continue
			}
			kawalki = append(kawalki, i)
			niezapisane += math.Max(0, math.Min(m.Do, pocz)-math.Max(m.Od, poprz+0.1))
		}
		// w luce musi być słychać mowę bez słów (co najmniej ćwierć sekundy) i pauzę, na której da się ją rozdzielić
		if niezapisane < 0.25 || len(kawalki) < 2 {
			continue
		}
		for _, i := range kawalki {
			zakresy[i] = append(zakresy[i], Przedzial{math.Max(mowa[i].Od, poprz-wokol), math.Min(mowa[i].Do, s.Do+wokol)})
		}
	}
	// szew: miejsce między dwoma słowami odległymi o co najmniej 0,28 s, najbliżej czasu t po stronie wskazanej znakiem
	szew := func(t float64, kierunek int) (float64, bool) {
		for n := 0; n < len(slowa)-1; n++ {
			i := n
			if kierunek < 0 {
				i = len(slowa) - 2 - n
			}
			a, b := slowa[i].Do, slowa[i+1].Do
			if b-a < 0.28 || (kierunek > 0 && a < t) || (kierunek < 0 && a > t) {
				continue
			}
			if math.Abs(a-t) > 2.5 {
				return 0, false
			}
			return a + 0.12, true
		}
		return 0, false
	}
	koniecSlowa := func(t float64, kierunek int) float64 { // koniec słowa najbliższy t po wskazanej stronie
		naj := t
		for _, s := range slowa {
			if kierunek < 0 && s.Do <= t {
				naj = s.Do
			}
			if kierunek > 0 && s.Do >= t {
				return s.Do
			}
		}
		return naj
	}
	var okna []oknoDosluchu
	for i, m := range mowa {
		z := zakresy[i]
		sort.Slice(z, func(a, b int) bool { return z[a].Od < z[b].Od })
		for k := 0; k < len(z); k++ {
			lo, hi := z[k].Od, z[k].Do
			for k+1 < len(z) && z[k+1].Od <= hi+1 {
				hi = math.Max(hi, z[k+1].Do)
				k++
			}
			o := oknoDosluchu{od: m.Od - 0.08, do: m.Do + 0.12, zOd: m.Od + 0.03, zDo: m.Do + 0.15}
			dobre := true
			if lo > m.Od+1.5 { // początek kawałka jest daleko: okno zaczyna się na końcu słowa
				o.od = koniecSlowa(lo, -1) + 0.02
				o.zOd, dobre = szew(o.od+0.8, 1)
			}
			if hi < m.Do-1.5 && dobre {
				o.do = koniecSlowa(hi, 1) + 0.06
				o.zDo, dobre = szew(o.do-0.8, -1)
			}
			if dobre && o.zDo > o.zOd && o.do-o.od <= 25 {
				okna = append(okna, o)
			}
		}
	}
	return okna
}

func zapiszWav(plik string, probki []int16, czest int) error {
	b := make([]byte, 44+2*len(probki))
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(probki)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(czest))
	binary.LittleEndian.PutUint32(b[28:], uint32(2*czest))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(probki)))
	for i, v := range probki {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return os.WriteFile(plik, b, 0o644)
}

// wspolnePoKolei: ile słów ciągu a występuje po kolei w ciągu b (najdłuższy wspólny podciąg, słowa „podobne”).
func wspolnePoKolei(a, b []string) int {
	p := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		poprz := 0
		for j := 1; j <= len(b); j++ {
			t := p[j]
			if podobne(a[i-1], b[j-1]) {
				p[j] = poprz + 1
			} else {
				p[j] = max(p[j], p[j-1])
			}
			poprz = t
		}
	}
	return p[len(b)]
}

// dosluchaj: druga transkrypcja podejrzanych wycinków. Nowy zapis wycinka zastępuje stary tylko wtedy, gdy ma więcej
// słów i zawiera prawie wszystkie dotychczasowe (zabezpieczenie przed zdaniami zmyślonymi w hałasie).
// Każdy błąd po drodze zostawia pierwszą transkrypcję bez zmian.
func dosluchaj(n *Narzedzia, wav, katalog string, wspolne []string, slowa []Slowo) []Slowo {
	probki, czest, err := wczytajWav(wav)
	if err != nil || czest <= 0 {
		return slowa
	}
	mowa, _ := wykryjMoweProfil(probki, czest)
	okna := oknaDosluchu(mowa, slowa)
	razem := 0.0
	for _, o := range okna {
		razem += o.do - o.od
	}
	// dużo podejrzanych miejsc to nie pomyłki mówcy, tylko hałas albo muzyka: wtedy druga tura nic nie da
	if len(okna) == 0 || len(okna) > 30 || razem > 0.8*float64(len(probki))/float64(czest) {
		return slowa
	}
	cisza := int(0.3 * float64(czest)) // cisza doklejana przed i po oknie
	// Każde okno to osobny plik, więc tekst jednego nie jest kontekstem następnego. Bez -mc 0: ta flaga wyłącza także
	// podpowiedź ze słownikiem marki (zmierzone: „magnez” zamiast „magnes”).
	arg := append([]string{}, wspolne...)
	var pliki []string
	przesuniecie := make([]float64, len(okna))
	for i, o := range okna {
		od := min(len(probki), max(0, int(o.od*float64(czest))))
		do := max(od, min(len(probki), int(o.do*float64(czest))))
		okno := make([]int16, cisza, (do-od)+2*cisza)
		okno = append(okno, probki[od:do]...)
		okno = append(okno, make([]int16, cisza)...)
		plik := filepath.Join(katalog, fmt.Sprintf("okno%d.wav", i))
		if zapiszWav(plik, okno, czest) != nil {
			return slowa
		}
		przesuniecie[i] = float64(od-cisza) / float64(czest)
		pliki = append(pliki, plik)
		arg = append(arg, "-f", plik)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
	defer stop()
	_, _, err = uruchom(ctx, filepath.Dir(n.Whisper), n.Whisper, arg...)
	for i, plik := range pliki {
		b, errPlik := os.ReadFile(plik + ".json")
		os.Remove(plik)
		os.Remove(plik + ".json")
		if err != nil || errPlik != nil {
			continue
		}
		wszystkie, errSlowa := slowaZWhispera(b)
		if errSlowa != nil {
			continue
		}
		o := okna[i]
		var nowe []Slowo
		for _, s := range wszystkie {
			s.Do += przesuniecie[i]
			if s.Do >= o.zOd && s.Do <= o.zDo {
				nowe = append(nowe, s)
			}
		}
		a := 0
		for a < len(slowa) && slowa[a].Do < o.zOd {
			a++
		}
		z := a
		for z < len(slowa) && slowa[z].Do <= o.zDo {
			z++
		}
		// słowa z brzegów starego zapisu, których nowy nie ma (okno bywa ucięte w pół słowa), zostają
		if len(nowe) > 0 {
			pocz := a
			for pocz < z && slowa[pocz].Do < nowe[0].Do-0.15 && !podobne(normalizuj(slowa[pocz].Tekst), normalizuj(nowe[0].Tekst)) {
				pocz++
			}
			kon := z
			for kon > pocz && slowa[kon-1].Do > nowe[len(nowe)-1].Do+0.15 {
				kon--
			}
			nowe = append(append(append([]Slowo{}, slowa[a:pocz]...), nowe...), slowa[kon:z]...)
		}
		var st, nw []string
		for _, s := range slowa[a:z] {
			st = append(st, normalizuj(s.Tekst))
		}
		for _, s := range nowe {
			nw = append(nw, normalizuj(s.Tekst))
		}
		if len(st) == 0 || len(nw) <= len(st) || len(nw) > 2*len(st)+6 || wspolnePoKolei(st, nw)*10 < len(st)*7 {
			continue
		}
		slowa = append(append(append([]Slowo{}, slowa[:a]...), nowe...), slowa[z:]...)
	}
	return slowa
}

// slowaZWhispera: wynik whisper.cpp (JSON z -ojf) jako lista słów z czasem końca.
func slowaZWhispera(b []byte) ([]Slowo, error) {
	var w whisperJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	var slowa []Slowo
	for _, seg := range w.Transcription {
		nowe := true // pierwszy kawałek zdania zaczyna słowo
		for _, t := range seg.Tokens {
			if strings.HasPrefix(t.Text, "[_") || t.Text == "" {
				continue
			}
			koniec := float64(t.Offsets.To) / 1000
			if t.TDtw >= 0 {
				koniec = float64(t.TDtw) / 100
			}
			if strings.HasPrefix(t.Text, " ") || nowe || len(slowa) == 0 {
				tekst := strings.TrimSpace(t.Text)
				if tekst == "" {
					nowe = true
					continue
				}
				slowa = append(slowa, Slowo{Tekst: tekst, Od: -1, Do: koniec})
				nowe = false
				continue
			}
			ost := &slowa[len(slowa)-1]
			ost.Tekst += t.Text
			// kropka i przecinek dostają czas w ciszy po słowie, więc nie przesuwają jego końca
			if koniec > ost.Do && strings.IndexFunc(t.Text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
				ost.Do = koniec
			}
		}
	}
	// odgłosy opisane w nawiasach i sama interpunkcja nie są słowami
	var czyste []Slowo
	for _, s := range slowa {
		if strings.HasPrefix(s.Tekst, "[") || strings.HasPrefix(s.Tekst, "(") || strings.HasPrefix(s.Tekst, "*") {
			continue
		}
		if strings.Trim(s.Tekst, ".,!?;:…-–—\"'") == "" {
			continue
		}
		czyste = append(czyste, s)
	}
	return czyste, nil
}
