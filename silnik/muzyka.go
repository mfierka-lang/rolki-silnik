// Muzyka: biblioteka utworów w katalogu danych (<dane>/muzyka/*.mp3), analiza rytmu i wybór utworu do filmu.
// Analiza (tempo, siatka uderzeń, głośność, miejsce, od którego utwór „gra pełnią”) liczy się raz na utwór
// i zostaje w <dane>/muzyka/.analiza. Cięcia filmu z ujęć trafiają potem w uderzenia.
// Zasady miksu ze standardu montażu: muzyka jest tłem, dźwięk z nagrania i głos mają być wyraźnie słyszalne,
// nigdy cisza zamiast dźwięku z planu, całość ok. -14 LUFS.
package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Utwor struct {
	Plik    string    `json:"-"`
	Nazwa   string    `json:"nazwa"`
	Czas    float64   `json:"czas"`
	BPM     float64   `json:"bpm"`
	Okres   float64   `json:"okres"`   // odstęp między uderzeniami w sekundach
	Faza    float64   `json:"faza"`    // czas pierwszego uderzenia
	Pewnosc float64   `json:"pewnosc"` // jak wyraźny jest rytm (0-1); poniżej 0,15 cięcia nie są wiązane z rytmem
	RmsDb   float64   `json:"rmsDb"`   // średnia głośność
	Start   float64   `json:"start"`   // od tego miejsca utwór ma pełną energię (po cichym wstępie), wyrównane do uderzenia
	Energia []float64 `json:"energia"` // głośność w kolejnych sekundach (dB)
}

// Podfoldery katalogu muzyka to style (Elektroniczna, Pop, Ulubione...); pliki luzem liczą się jako „Różne”.
func bibliotekaMuzyki(n *Narzedzia) []string {
	var pliki []string
	for _, wzor := range []string{"*.mp3", "*.m4a", "*.wav", "*/*.mp3", "*/*.m4a", "*/*.wav"} {
		p, _ := filepath.Glob(filepath.Join(n.Dane, "muzyka", wzor))
		pliki = append(pliki, p...)
	}
	sort.Strings(pliki)
	return pliki
}

var muzFPS = 11025.0 / 256 // próbek obwiedni na sekundę (ok. 43)

// analizujUtwor: dekodowanie do mono 11 kHz, obwiednia „uderzeń” (przyrosty energii, osobno bas i całość),
// tempo z autokorelacji w zakresie 70-180 uderzeń na minutę, faza z najlepszego dopasowania siatki.
func analizujUtwor(n *Narzedzia, plik string) (*Utwor, error) {
	znak := plik
	if fi, err := os.Stat(plik); err == nil {
		znak = fmt.Sprintf("%s|%d|%d|v2", filepath.Base(plik), fi.Size(), fi.ModTime().Unix())
	}
	h := sha1.Sum([]byte(znak))
	pamiec := filepath.Join(n.Dane, "muzyka", ".analiza", hex.EncodeToString(h[:8])+".json")
	if b, err := os.ReadFile(pamiec); err == nil {
		u := &Utwor{}
		if json.Unmarshal(b, u) == nil && u.Czas > 0 {
			u.Plik = plik
			return u, nil
		}
	}
	cmd := exec.CommandContext(context.Background(), n.FFmpeg, "-v", "error", "-nostdin", "-i", plik, "-vn", "-ac", "1", "-ar", "11025", "-f", "s16le", "-")
	ukryjOkno(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	rd := bufio.NewReaderSize(out, 1<<20)
	const skok = 256
	bufor := make([]byte, skok*2)
	var eCal, eBas []float64
	var sumaKw float64
	var ile int64
	nisko := 0.0 // filtr dolnoprzepustowy jednobiegunowy ok. 150 Hz (stopa, bas)
	const alfa = 0.082
	for {
		if _, err := io.ReadFull(rd, bufor); err != nil {
			break
		}
		var sc, sb float64
		for i := 0; i < skok; i++ {
			v := float64(int16(binary.LittleEndian.Uint16(bufor[2*i:]))) / 32768
			nisko += alfa * (v - nisko)
			sc += v * v
			sb += nisko * nisko
		}
		sumaKw += sc
		ile += skok
		eCal = append(eCal, math.Log1p(1000*math.Sqrt(sc/skok)))
		eBas = append(eBas, math.Log1p(1000*math.Sqrt(sb/skok)))
	}
	cmd.Wait()
	if len(eCal) < int(20*muzFPS) {
		return nil, fmt.Errorf("utwór %s jest za krótki albo nie dał się odczytać", filepath.Base(plik))
	}
	u := &Utwor{Plik: plik, Nazwa: strings.TrimSuffix(filepath.Base(plik), filepath.Ext(plik)), Czas: float64(ile) / 11025}
	u.RmsDb = 10 * math.Log10(sumaKw/float64(ile)+1e-12)
	// obwiednia uderzeń: dodatnie przyrosty energii
	fl := make([]float64, len(eCal))
	for i := 1; i < len(eCal); i++ {
		fl[i] = math.Max(0, eBas[i]-eBas[i-1]) + 0.5*math.Max(0, eCal[i]-eCal[i-1])
	}
	sr := 0.0
	for _, v := range fl {
		sr += v
	}
	sr /= float64(len(fl))
	for i := range fl {
		fl[i] -= sr
	}
	// tempo: autokorelacja; lekka preferencja okolic 120 uderzeń na minutę (unika połowy i podwojenia tempa)
	korel := func(lag int) float64 {
		s := 0.0
		for i := lag; i < len(fl); i++ {
			s += fl[i] * fl[i-lag]
		}
		return s / float64(len(fl)-lag)
	}
	k0 := korel(0) + 1e-12
	najLag, najW, najSur := 0, -1.0, 0.0
	od, do := int(muzFPS*60/180), int(muzFPS*60/70)
	wyniki := make([]float64, do+2)
	for lag := od; lag <= do+1; lag++ {
		wyniki[lag] = korel(lag) / k0
	}
	for lag := od; lag <= do; lag++ {
		bpm := 60 * muzFPS / float64(lag)
		waga := math.Exp(-0.5 * math.Pow(math.Log2(bpm/120)/0.9, 2))
		if w := wyniki[lag] * waga; w > najW {
			najW, najLag, najSur = w, lag, wyniki[lag]
		}
	}
	okres := float64(najLag)
	if najLag > od && najLag < do { // dokładność poniżej jednej próbki obwiedni
		a, b, c := wyniki[najLag-1], wyniki[najLag], wyniki[najLag+1]
		if m := a - 2*b + c; m < 0 {
			okres += math.Max(-0.5, math.Min(0.5, 0.5*(a-c)/m))
		}
	}
	u.Okres, u.BPM, u.Pewnosc = okres/muzFPS, 60*muzFPS/okres, math.Max(0, najSur)
	// faza: przesunięcie siatki, przy którym uderzenia trafiają w największe przyrosty
	najF, najS := 0.0, -1e18
	for f := 0.0; f < okres; f += 0.5 {
		s := 0.0
		for t := f; int(t+0.5) < len(fl); t += okres {
			i := int(t + 0.5)
			s += fl[i]
			if i > 0 {
				s += 0.5 * fl[i-1]
			}
			if i+1 < len(fl) {
				s += 0.5 * fl[i+1]
			}
		}
		if s > najS {
			najS, najF = s, f
		}
	}
	u.Faza = najF / muzFPS
	// energia w kolejnych sekundach i początek „pełnej” części utworu
	naSek := int(muzFPS + 0.5)
	for i := 0; i+naSek <= len(eCal); i += naSek {
		s := 0.0
		for j := i; j < i+naSek; j++ {
			s += eCal[j]
		}
		u.Energia = append(u.Energia, math.Round(s/float64(naSek)*100)/100)
	}
	posort := append([]float64(nil), u.Energia...)
	sort.Float64s(posort)
	prog := 0.82 * posort[len(posort)*9/10]
	u.Start = 0
	for s := 0; s+4 <= len(u.Energia) && float64(s) < u.Czas*0.5; s++ {
		if (u.Energia[s]+u.Energia[s+1]+u.Energia[s+2]+u.Energia[s+3])/4 >= prog {
			u.Start = float64(s)
			break
		}
	}
	if u.Okres > 0 { // start na uderzeniu
		u.Start = u.Faza + math.Round((u.Start-u.Faza)/u.Okres)*u.Okres
		if u.Start < 0 {
			u.Start = u.Faza
		}
	}
	os.MkdirAll(filepath.Dir(pamiec), 0o755)
	if b, err := json.Marshal(u); err == nil {
		os.WriteFile(pamiec, b, 0o644)
	}
	return u, nil
}

// pomijanyUtwor: tytuł utworu z poprzedniej wersji filmu, gdy użytkownik poprosił o inną muzykę.
var pomijanyUtwor string

// wybierzUtwor: utwór do filmu. nastroj: "dynamiczny" (szybsze tempo), "spokojny" (wolniejsze), "" (środek).
// Wśród pasujących wybór zależy od ziarna (np. numeru zlecenia), więc kolejne filmy dostają różne utwory,
// a poprawka tego samego filmu ten sam. Utwór musi wystarczyć na cały film.
func wybierzUtwor(n *Narzedzia, nastroj, ziarno string, minCzas float64, postep func(i, ile int)) *Utwor {
	pliki := bibliotekaMuzyki(n)
	// nastrój w postaci „styl:Nazwa” ogranicza wybór do jednego folderu biblioteki
	if strings.HasPrefix(nastroj, "styl:") {
		styl := strings.TrimPrefix(nastroj, "styl:")
		var zeStylu []string
		for _, p := range pliki {
			if strings.EqualFold(stylUtworu(n, p), styl) {
				zeStylu = append(zeStylu, p)
			}
		}
		if len(zeStylu) > 0 {
			pliki = zeStylu
		}
		nastroj = "dowolny"
	}
	var kandydaci []*Utwor
	for i, p := range pliki {
		if postep != nil {
			postep(i, len(pliki))
		}
		u, err := analizujUtwor(n, p)
		if err != nil || u.Czas-u.Start < minCzas+2 {
			continue
		}
		kandydaci = append(kandydaci, u)
	}
	if len(kandydaci) == 0 {
		return nil
	}
	pasuje := func(u *Utwor) bool {
		switch nastroj {
		case "dynamiczny":
			return u.BPM >= 112
		case "spokojny":
			return u.BPM <= 104
		case "dowolny":
			return true
		}
		return u.BPM >= 92 && u.BPM <= 128
	}
	var wybor []*Utwor
	for _, u := range kandydaci {
		if pasuje(u) && u.Pewnosc >= 0.1 {
			wybor = append(wybor, u)
		}
	}
	if len(wybor) == 0 {
		wybor = kandydaci
	}
	if pomijanyUtwor != "" && len(wybor) > 1 {
		var inne []*Utwor
		for _, u := range wybor {
			if u.Nazwa != pomijanyUtwor {
				inne = append(inne, u)
			}
		}
		if len(inne) > 0 {
			wybor = inne
		}
	}
	h := fnv.New32a()
	h.Write([]byte(ziarno))
	return wybor[int(h.Sum32())%len(wybor)]
}

// stylUtworu: nazwa podfolderu, w którym leży utwór ("Różne" dla plików luzem).
func stylUtworu(n *Narzedzia, plik string) string {
	kat := filepath.Dir(plik)
	if filepath.Clean(kat) == filepath.Clean(filepath.Join(n.Dane, "muzyka")) {
		return "Różne"
	}
	return filepath.Base(kat)
}

// nazwaStylu: nazwa folderu stylu tak, jak ma ją zobaczyć człowiek (foldery są bez polskich znaków,
// żeby te same nazwy działały na każdym systemie i w adresach panelu).
func nazwaStylu(folder string) string {
	switch strings.ToLower(folder) {
	case "rozne":
		return "Różne"
	case "hiphop", "hip-hop":
		return "Hip-hop"
	case "lofi":
		return "Lo-fi"
	}
	return strings.ReplaceAll(folder, "_", " ")
}

// styleMuzyki: style dostępne w bibliotece z liczbą utworów (do listy wyboru na stronie).
func styleMuzyki(n *Narzedzia) []map[string]any {
	ile := map[string]int{}
	var kolejnosc []string
	for _, p := range bibliotekaMuzyki(n) {
		s := stylUtworu(n, p)
		if ile[s] == 0 {
			kolejnosc = append(kolejnosc, s)
		}
		ile[s]++
	}
	sort.Strings(kolejnosc)
	var out []map[string]any
	for _, s := range kolejnosc {
		out = append(out, map[string]any{"styl": s, "nazwa": nazwaStylu(s), "ile": ile[s]})
	}
	return out
}

// wzmocnienieMuzyki: mnożnik głośności, po którym utwór ma zadaną średnią głośność (dB poniżej pełnej skali).
func wzmocnienieMuzyki(u *Utwor, celDb float64) float64 {
	return math.Pow(10, (celDb-u.RmsDb)/20)
}
