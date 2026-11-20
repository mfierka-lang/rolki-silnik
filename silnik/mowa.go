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
	skok := czest / 100 // 10 ms
	okno := 2 * skok
	if len(probki) < okno*10 {
		return nil
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
		return nil
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
	return czyste
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

// transkrybuj: whisper.cpp, język polski, pełny wynik z czasem każdego kawałka słowa. Czas końca słowa pochodzi
// z dopasowania DTW (dokładność ok. 0,05-0,1 s; zwykłe czasy whispera potrafią się mylić o 2 s na początku zdania).
// DTW wymaga wyłączenia "flash attention" (-nfa). Podpowiedź ze słownikiem marki pomaga w pisowni nazw.
// Słowo dostaje tylko pewny czas końca (Do); początek (Od) ustala potem frazyZMowy na podstawie wykrytej mowy.
func transkrybuj(n *Narzedzia, wav, katalog, podpowiedz string) ([]Slowo, error) {
	if n.Whisper == "" || n.Model == "" {
		return nil, fmt.Errorf("brak whisper.cpp albo modelu mowy w katalogu danych (%s)", n.Dane)
	}
	watki := runtime.NumCPU()
	if watki > 8 {
		watki = 8
	}
	if watki < 2 {
		watki = 2
	}
	prefiks := filepath.Join(katalog, "mowa")
	arg := []string{"-m", n.Model, "-f", wav, "-l", "pl", "-t", fmt.Sprint(watki), "-ojf", "-of", prefiks, "-np", "-sns"}
	if p := presetDTW(n.Model); p != "" {
		arg = append(arg, "-nfa", "--dtw", p)
	}
	if podpowiedz != "" {
		arg = append(arg, "--prompt", podpowiedz)
	}
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
