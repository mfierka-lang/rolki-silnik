// Skan ujęcia: pomiar ruchu kamery, ostrości i miejsca, w którym coś się dzieje. Film jest dekodowany raz,
// w małej rozdzielczości i 12,5 klatki na sekundę (na karcie graficznej, gdy się da). Z pomiaru wybierane są
// najspokojniejsze fragmenty: stały przelot albo statyczny kadr są dobre, szarpnięcia i korekty odpadają.
package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	pigo "github.com/esimov/pigo/core"
)

const skanFPS = 12.5

// co ile próbek zapisywana jest miniatura kadru (ok. 2 na sekundę wystarcza do porównywania ujęć)
const miniCo = 6

type Probka struct {
	T       float64 // sekunda w nagraniu
	Dx, Dy  float64 // przesunięcie kadru od poprzedniej próbki, w ułamku szerokości obrazu
	Ostrosc float64
	Akt     float64 // ile obrazu zmienia się po uwzględnieniu ruchu kamery (ruch ludzi, narzędzi)
	AktX    float64 // środek tej zmiany w poziomie, 0-1
	Mini    []byte  `json:",omitempty"` // miniatura 32 x 18 (do rozpoznawania prawie takich samych ujęć)
	// twarz zwrócona do kamery (sprawdzana co 3. próbkę): wielkość względem krótszego boku kadru i środek (0-1)
	Twarz   float64 `json:",omitempty"`
	TwarzX  float64 `json:",omitempty"`
	TwarzY  float64 `json:",omitempty"`
	TwarzOk bool    `json:",omitempty"` // w tej próbce szukano twarzy
}

// co ile próbek szukana jest twarz (ok. 4 razy na sekundę)
const twarzCo = 3

//go:embed zasoby/facefinder
var kaskadaTwarzy []byte

var wykrywaczTwarzy *pigo.Pigo

// szukajTwarzy: największa twarz zwrócona do kamery (biblioteka pigo, MIT, czysty Go). Zwraca wielkość względem
// krótszego boku obrazu i środek twarzy; 0, gdy nie ma.
func szukajTwarzy(g []byte, w, h int) (wielkosc, x, y float64) {
	if wykrywaczTwarzy == nil {
		p, err := pigo.NewPigo().Unpack(kaskadaTwarzy)
		if err != nil {
			return 0, 0, 0
		}
		wykrywaczTwarzy = p
	}
	bok := min(w, h)
	wyniki := wykrywaczTwarzy.RunCascade(pigo.CascadeParams{
		MinSize: bok / 8, MaxSize: bok, ShiftFactor: 0.1, ScaleFactor: 1.15,
		ImageParams: pigo.ImageParams{Pixels: g, Rows: h, Cols: w, Dim: w},
	}, 0)
	wyniki = wykrywaczTwarzy.ClusterDetections(wyniki, 0.2)
	naj := -1
	for i, d := range wyniki {
		if d.Q >= 6 && (naj < 0 || d.Scale > wyniki[naj].Scale) {
			naj = i
		}
	}
	if naj < 0 {
		return 0, 0, 0
	}
	d := wyniki[naj]
	return float64(d.Scale) / float64(bok), float64(d.Col) / float64(w), float64(d.Row) / float64(h)
}

// udzialTwarzy: w jakiej części nagrania widać dużą twarz zwróconą do kamery (ktoś mówi do obiektywu)
// i gdzie przeciętnie jest jej środek.
func udzialTwarzy(sk *Skan) (udzial, x, y float64) {
	sprawdzone, trafione := 0, 0
	var xs, ys []float64
	for _, p := range sk.Probki {
		if !p.TwarzOk {
			continue
		}
		sprawdzone++
		if p.Twarz >= 0.14 {
			trafione++
			xs, ys = append(xs, p.TwarzX), append(ys, p.TwarzY)
		}
	}
	if sprawdzone == 0 || trafione == 0 {
		return 0, 0.5, 0.4
	}
	return float64(trafione) / float64(sprawdzone), mediana(xs), mediana(ys)
}

type Skan struct {
	Probki   []Probka
	MiniW    int     // szerokość miniatury w punktach
	Sposob   string  // którym sposobem dekodowano
	CzasSkan float64 // sekundy pracy
}

type sposobSkanu struct {
	nazwa string
	przed func(k *Klip) []string
	filtr func(w, h int) string
}

func filtrCPU(w, h int) string {
	return fmt.Sprintf("fps=%g,scale=%d:%d:flags=fast_bilinear,format=gray", skanFPS, w, h)
}

// sposobySkanu: od najszybszego. Pierwszy, który zadziała na danym komputerze, zostaje na resztę nagrań.
func sposobySkanu() []sposobSkanu {
	var s []sposobSkanu
	if runtime.GOOS == "windows" {
		s = append(s, sposobSkanu{"qsv", func(k *Klip) []string {
			dek := map[string]string{"hevc": "hevc_qsv", "h264": "h264_qsv", "av1": "av1_qsv"}[k.Kodek]
			if dek == "" || k.Obrot != 0 {
				return nil
			}
			return []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-c:v", dek}
		}, func(w, h int) string {
			return fmt.Sprintf("vpp_qsv=w=%d:h=%d:format=nv12,hwdownload,format=nv12,fps=%g,format=gray", w, h, skanFPS)
		}})
		s = append(s, sposobSkanu{"cuda", func(k *Klip) []string {
			if k.Obrot != 0 {
				return nil
			}
			return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
		}, func(w, h int) string {
			return fmt.Sprintf("scale_cuda=%d:%d:format=nv12,hwdownload,format=nv12,fps=%g,format=gray", w, h, skanFPS)
		}})
		s = append(s, sposobSkanu{"d3d11va", func(k *Klip) []string { return []string{"-hwaccel", "d3d11va"} }, filtrCPU})
	}
	if runtime.GOOS == "darwin" {
		s = append(s, sposobSkanu{"videotoolbox", func(k *Klip) []string { return []string{"-hwaccel", "videotoolbox"} }, filtrCPU})
	}
	s = append(s, sposobSkanu{"programowo", func(k *Klip) []string { return []string{} }, filtrCPU})
	return s
}

// sposobZawiodl: sposoby skanu, które na tym komputerze nie działają (np. brak karty Intela albo NVIDII). Sposób pominięty
// dla jednego nagrania (np. obrócony film z telefonu) nie jest przez to skreślany dla pozostałych.
var sposobZawiodl = map[string]int{}

func zmniejsz4(src []byte, w, h int, dst []byte) {
	mw, mh := w/4, h/4
	for y := 0; y < mh; y++ {
		for x := 0; x < mw; x++ {
			s := 0
			for j := 0; j < 4; j++ {
				r := (y*4+j)*w + x*4
				s += int(src[r]) + int(src[r+1]) + int(src[r+2]) + int(src[r+3])
			}
			dst[y*mw+x] = byte(s / 16)
		}
	}
}

func sad(a, b []byte, w, h, dx, dy, margines, krok int) int {
	s := 0
	for y := margines; y < h-margines; y += krok {
		ra := y * w
		rb := (y+dy)*w + dx
		for x := margines; x < w-margines; x += krok {
			d := int(a[ra+x]) - int(b[rb+x])
			if d < 0 {
				d = -d
			}
			s += d
		}
	}
	return s
}

// ruchKadru: przesunięcie między klatkami szukane najpierw zgrubnie (obraz 4 razy mniejszy), potem dokładnie,
// z dopasowaniem poniżej piksela. Zwraca też, gdzie po tym przesunięciu obraz nadal się różni (ruch w kadrze).
func ruchKadru(pop, akt []byte, w, h int, popM, aktM []byte) (dx, dy, aktywnosc, aktX float64) {
	mw, mh := w/4, h/4
	const R = 8
	naj, bx, by := math.MaxInt, 0, 0
	for y := -R; y <= R; y++ {
		for x := -R; x <= R; x++ {
			if s := sad(aktM, popM, mw, mh, x, y, R, 1); s < naj {
				naj, bx, by = s, x, y
			}
		}
	}
	const M, r = 36, 3
	var tab [2*r + 1][2*r + 1]int
	naj = math.MaxInt
	fx, fy := 0, 0
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			ox, oy := bx*4+x, by*4+y
			if ox < -M || ox > M || oy < -M || oy > M {
				tab[y+r][x+r] = math.MaxInt / 4
				continue
			}
			s := sad(akt, pop, w, h, ox, oy, M, 2)
			tab[y+r][x+r] = s
			if s < naj {
				naj, fx, fy = s, x, y
			}
		}
	}
	dx, dy = float64(bx*4+fx), float64(by*4+fy)
	parabola := func(a, b, c int) float64 { // położenie minimum między trzema sąsiednimi wynikami
		m := float64(a - 2*b + c)
		if m <= 0 {
			return 0
		}
		return math.Max(-0.5, math.Min(0.5, 0.5*float64(a-c)/m))
	}
	if fx > -r && fx < r {
		dx += parabola(tab[fy+r][fx+r-1], tab[fy+r][fx+r], tab[fy+r][fx+r+1])
	}
	if fy > -r && fy < r {
		dy += parabola(tab[fy+r-1][fx+r], tab[fy+r][fx+r], tab[fy+r+1][fx+r])
	}
	// co zostaje po wyrównaniu kadru: suma różnic w kolumnach
	ox, oy := bx*4+fx, by*4+fy
	kol := make([]float64, w)
	suma, ile := 0.0, 0
	for y := M; y < h-M; y += 2 {
		ra, rb := y*w, (y+oy)*w+ox
		for x := M; x < w-M; x += 2 {
			d := int(akt[ra+x]) - int(pop[rb+x])
			if d < 0 {
				d = -d
			}
			if d > 12 { // poniżej to szum matrycy i kompresji
				kol[x] += float64(d)
				suma += float64(d)
			}
			ile++
		}
	}
	aktywnosc = suma / float64(ile)
	aktX = 0.5
	if suma > 0 {
		sx := 0.0
		for x, v := range kol {
			sx += v * float64(x)
		}
		aktX = sx / suma / float64(w)
	}
	return dx / float64(w), dy / float64(w), aktywnosc, aktX
}

func ostrosc(g []byte, w, h int) float64 {
	s, n := 0, 0
	for y := 2; y < h-2; y += 2 {
		for x := 2; x < w-2; x += 2 {
			i := y*w + x
			d := 4*int(g[i]) - int(g[i-1]) - int(g[i+1]) - int(g[i-w]) - int(g[i+w])
			if d < 0 {
				d = -d
			}
			s += d
			n++
		}
	}
	return float64(s) / float64(n)
}

// miniatura: obraz 3 razy mniejszy od zgrubnego (dla 16:9 to 32 x 18 punktów).
func miniatura(m []byte, mw, mh int) []byte {
	w, h := mw/3, mh/3
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := 0
			for j := 0; j < 3; j++ {
				r := (y*3+j)*mw + x*3
				s += int(m[r]) + int(m[r+1]) + int(m[r+2])
			}
			out[y*w+x] = byte(s / 9)
		}
	}
	return out
}

// podobienstwo: na ile dwa kadry mają ten sam układ jasnych i ciemnych plam (1 = ten sam kadr, ok. 0 = inny).
// Miniatury są uśredniane do siatki 8 x 4 i porównywane korelacją. Zmierzone na nagraniach z Sopotu: to samo
// statyczne ujęcie w innej sekundzie 0,92-0,98, ta sama scena z innym ruchem ludzi do 0,67, różne ujęcia poniżej 0,5.
// Porównywanie punkt po punkcie nie działało: liście na tle nieba dawały dużą różnicę między identycznymi kadrami.
func podobienstwo(a, b []byte, w int) float64 {
	if len(a) != len(b) || len(a) == 0 || w <= 0 || len(a)%w != 0 {
		return 0
	}
	h := len(a) / w
	bw, bh := 8, 4
	if h > w {
		bw, bh = 4, 8
	}
	siatka := func(m []byte) []float64 {
		out := make([]float64, 0, bw*bh)
		for by := 0; by < bh; by++ {
			for bx := 0; bx < bw; bx++ {
				s, n := 0, 0
				for y := by * h / bh; y < (by+1)*h/bh; y++ {
					for x := bx * w / bw; x < (bx+1)*w/bw; x++ {
						s += int(m[y*w+x])
						n++
					}
				}
				out = append(out, float64(s)/float64(max(n, 1)))
			}
		}
		return out
	}
	ga, gb := siatka(a), siatka(b)
	var ma, mb float64
	for i := range ga {
		ma += ga[i]
		mb += gb[i]
	}
	ma /= float64(len(ga))
	mb /= float64(len(gb))
	var sab, saa, sbb float64
	for i := range ga {
		sab += (ga[i] - ma) * (gb[i] - mb)
		saa += (ga[i] - ma) * (ga[i] - ma)
		sbb += (gb[i] - mb) * (gb[i] - mb)
	}
	return sab / math.Sqrt(saa*sbb+1e-9)
}

// skanuj: wynik jest zapamiętywany w katalogu danych (klucz: ścieżka, rozmiar i data pliku), więc drugie użycie
// tego samego nagrania nie kosztuje nic. To zalążek biblioteki przebitek.
func skanuj(n *Narzedzia, k *Klip) (*Skan, error) {
	pamiec := ""
	if _, err := os.Stat(k.Plik); err == nil {
		h := sha1.Sum([]byte(fmt.Sprintf("%s|v5", odciskPliku(k.Plik))))
		pamiec = filepath.Join(n.Dane, "skany", hex.EncodeToString(h[:])+".json")
		if b, err := os.ReadFile(pamiec); err == nil {
			sk := &Skan{}
			if json.Unmarshal(b, sk) == nil && len(sk.Probki) > 2 {
				if sk.MiniW == 0 { // skany zapisane przed dodaniem tego pola
					sk.MiniW = 32
					if k.H > k.W {
						sk.MiniW = 18
					}
				}
				sk.Sposob, sk.CzasSkan = "z pamięci", 0
				return sk, nil
			}
		}
	}
	start := time.Now()
	// rozmiar analizy: dłuższy bok 384 px, wymiary podzielne przez 8
	w, h := 384, 216
	if k.H > k.W {
		w, h = 216, 384
	}
	sposoby := sposobySkanu()
	var ostBlad error
	for si := 0; si < len(sposoby); si++ {
		sp := sposoby[si]
		przed := sp.przed(k)
		// dwa niepowodzenia z rzędu = sposób nie działa na tym komputerze (ostatni, programowy, próbujemy zawsze)
		if przed == nil || (sposobZawiodl[sp.nazwa] >= 2 && si < len(sposoby)-1) {
			continue
		}
		arg := append([]string{"-v", "error", "-nostdin"}, przed...)
		arg = append(arg, "-i", k.Plik, "-map", "0:v:0", "-an", "-vf", sp.filtr(w, h), "-f", "rawvideo", "-")
		cmd := exec.CommandContext(context.Background(), n.FFmpeg, arg...)
		ukryjOkno(cmd)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		sk := &Skan{Sposob: sp.nazwa, MiniW: w / 12}
		rd := bufio.NewReaderSize(out, 1<<20)
		pop, akt := make([]byte, w*h), make([]byte, w*h)
		popM, aktM := make([]byte, w*h/16), make([]byte, w*h/16)
		for i := 0; ; i++ {
			if _, err := io.ReadFull(rd, akt); err != nil {
				break
			}
			zmniejsz4(akt, w, h, aktM)
			p := Probka{T: float64(i) / skanFPS, Ostrosc: ostrosc(akt, w, h), AktX: 0.5}
			if i%miniCo == 0 {
				p.Mini = miniatura(aktM, w/4, h/4)
			}
			if i%twarzCo == 0 {
				p.TwarzOk = true
				p.Twarz, p.TwarzX, p.TwarzY = szukajTwarzy(akt, w, h)
			}
			if i > 0 {
				p.Dx, p.Dy, p.Akt, p.AktX = ruchKadru(pop, akt, w, h, popM, aktM)
			}
			sk.Probki = append(sk.Probki, p)
			pop, akt = akt, pop
			popM, aktM = aktM, popM
		}
		err = cmd.Wait()
		if len(sk.Probki) >= 3 && float64(len(sk.Probki)) > 0.8*k.Czas*skanFPS {
			sposobZawiodl[sp.nazwa] = 0
			sk.CzasSkan = time.Since(start).Seconds()
			if pamiec != "" {
				os.MkdirAll(filepath.Dir(pamiec), 0o755)
				if b, err := json.Marshal(sk); err == nil {
					os.WriteFile(pamiec, b, 0o644)
				}
			}
			return sk, nil
		}
		ostBlad = fmt.Errorf("sposób %s nie zadziałał (%v)", sp.nazwa, err)
		sposobZawiodl[sp.nazwa]++
	}
	return nil, fmt.Errorf("nie udało się przeanalizować %s: %v", k.Plik, ostBlad)
}

// Okno: kandydat na ujęcie do rolki.
type Okno struct {
	Klip     int
	Od, Do   float64
	Drgania  float64 // im mniej, tym spokojniejszy obraz (promile szerokości kadru)
	Predkosc float64 // średnia prędkość kadru (promile szerokości na próbkę)
	Ostrosc  float64 // względem mediany nagrania
	Akt      float64
	KadrX    float64     // gdzie ustawić pionowy kadr (środek, 0-1)
	Ocena    float64     // im mniej, tym lepsze ujęcie (jakość obrazu i to, czy w kadrze coś się dzieje)
	Mini     []byte      `json:"-"`
	MiniW    int         `json:"-"`
	Opis     *OpisUjecia `json:",omitempty"` // co widać (od modelu)
	Rola     string      `json:",omitempty"` // otwarcie, praca, detal, widok, inne
	Jakosc   float64     `json:",omitempty"` // sama praca kamery (1 = spokojna i ostra), bez oceny ruchu w kadrze
}

func mediana(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// oknaKlipu: najlepsze fragmenty jednego nagrania o długości dl sekund. Kolejne okna z tego samego nagrania
// muszą być od siebie odsunięte (odstep), żeby w rolce nie było dwóch prawie takich samych ujęć.
func oknaKlipu(ki int, k *Klip, sk *Skan, dl float64, ile int, odstep float64) []Okno {
	n := len(sk.Probki)
	L := int(math.Round(dl * skanFPS))
	brzeg := int(math.Round(0.6 * skanFPS)) // początek i koniec nagrania to zwykle ruch ręki przy włączaniu
	if n < L+2*brzeg+2 {
		brzeg = max(1, (n-L)/2)
	}
	if n < L+2 {
		return nil
	}
	var ostr []float64
	for _, p := range sk.Probki {
		ostr = append(ostr, p.Ostrosc)
	}
	medOstr := math.Max(mediana(ostr), 1e-6)
	type kand struct {
		i     int
		ocena float64
		o     Okno
	}
	var kandydaci []kand
	for i := brzeg; i+L <= n-brzeg; i++ {
		var sa, sv, so, sakt float64
		var xs []float64
		for j := i + 1; j < i+L; j++ {
			p, q := sk.Probki[j], sk.Probki[j-1]
			ax, ay := p.Dx-q.Dx, p.Dy-q.Dy
			sa += ax*ax + ay*ay
			sv += math.Hypot(p.Dx, p.Dy)
			so += p.Ostrosc
			sakt += p.Akt
			if p.Akt > 0.4 {
				xs = append(xs, p.AktX)
			}
		}
		m := float64(L - 1)
		o := Okno{Klip: ki, Od: float64(i) / skanFPS, Do: float64(i+L) / skanFPS,
			Drgania: math.Sqrt(sa/m) * 1000, Predkosc: sv / m * 1000, Ostrosc: so / m / medOstr, Akt: sakt / m, KadrX: 0.5}
		if len(xs) > L/3 {
			o.KadrX = mediana(xs)
		}
		// Ocena (im mniej, tym lepiej). Spokój kamery to warunek, a nie cel: poniżej ok. 0,5 drgań nie widać,
		// więc dalej nie nagradzamy (inaczej wygrywają martwe kadry: pusty widok, odłożona kamera).
		// O kolejności decyduje to, czy w kadrze coś się dzieje: ruch ludzi i narzędzi. Gdy kamera sama jedzie,
		// część tej zmiany to paralaksa, stąd dzielenie przez prędkość. Gdy nigdzie nic się nie rusza
		// (produkt na stole, dron), ruch jest wszędzie mały i o kolejności znów decyduje jakość.
		jakosc := 1 + math.Max(0, o.Drgania-0.5)*2
		jakosc *= 1 + math.Max(0, 1-o.Ostrosc)*2
		if o.Predkosc > 12 {
			jakosc *= 1 + (o.Predkosc-12)/12
		}
		ruch := o.Akt / (1 + o.Predkosc/3)
		ocena := jakosc / (0.5 + math.Min(ruch, 8))
		o.Ocena = ocena
		o.Jakosc = jakosc
		o.MiniW = sk.MiniW
		for d := 0; d < L/2; d++ { // miniatura z okolic środka okna
			if m := sk.Probki[i+L/2-d].Mini; m != nil {
				o.Mini = m
				break
			}
			if m := sk.Probki[i+L/2+d].Mini; m != nil {
				o.Mini = m
				break
			}
		}
		kandydaci = append(kandydaci, kand{i, ocena, o})
	}
	sort.Slice(kandydaci, func(a, b int) bool { return kandydaci[a].ocena < kandydaci[b].ocena })
	var wynik []Okno
	odstepProbek := int(math.Round(odstep * skanFPS))
	for _, c := range kandydaci {
		if len(wynik) >= ile {
			break
		}
		kolizja := false
		for _, w := range wynik {
			if math.Abs(w.Od*skanFPS-float64(c.i)) < float64(L+odstepProbek) {
				kolizja = true
				break
			}
		}
		if !kolizja {
			wynik = append(wynik, c.o)
		}
	}
	return wynik
}
