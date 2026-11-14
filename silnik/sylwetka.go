// Sylwetka osoby w kadrze: maska do efektu „napis za osobą”.
// Model MODNet (Apache 2.0) liczony lokalnie przez ONNX Runtime; klatki podaje i maskę zapisuje ffmpeg.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// znajdzSylwetke: biblioteka ONNX Runtime i model w katalogu danych (składniki dodatkowe).
func znajdzSylwetke(dane string) (biblioteka, model string) {
	model = filepath.Join(dane, "modele", "modnet.onnx")
	if _, err := os.Stat(model); err != nil {
		return "", ""
	}
	wzory := []string{"onnxruntime.dll"}
	if runtime.GOOS != "windows" {
		wzory = []string{"libonnxruntime.dylib", "libonnxruntime.so", "libonnxruntime.*.dylib", "libonnxruntime.so.*"}
	}
	for _, w := range wzory {
		if p, _ := filepath.Glob(filepath.Join(dane, "ort", w)); len(p) > 0 {
			return p[0], model
		}
	}
	return "", ""
}

type Sylwetka struct {
	sesja *SesjaORT
	we    []float32
	poprz []float32 // maska poprzedniej klatki (wygładzanie w czasie)
}

func otworzSylwetke(dane string) (*Sylwetka, error) {
	biblioteka, model := znajdzSylwetke(dane)
	if biblioteka == "" {
		return nil, fmt.Errorf("brak składników do wycinania sylwetki")
	}
	watki := runtime.NumCPU() - 2
	if watki < 2 {
		watki = 2
	}
	if watki > 8 {
		watki = 8
	}
	s, err := otworzORT(biblioteka, model, watki)
	if err != nil {
		return nil, err
	}
	return &Sylwetka{sesja: s}, nil
}

// Maska: klatka RGB (w*h*3 bajtów, w i h podzielne przez 32) -> maska osoby (w*h bajtów, 255 = osoba).
// Wynik jest lekko wygładzany względem poprzedniej klatki, żeby krawędź nie migotała.
func (m *Sylwetka) Maska(rgb []byte, w, h int) ([]byte, error) {
	n := w * h
	if len(m.we) != 3*n {
		m.we = make([]float32, 3*n)
		m.poprz = nil
	}
	for i := 0; i < n; i++ {
		m.we[i] = float32(rgb[3*i])/127.5 - 1
		m.we[n+i] = float32(rgb[3*i+1])/127.5 - 1
		m.we[2*n+i] = float32(rgb[3*i+2])/127.5 - 1
	}
	wy, ksztalt, err := m.sesja.Licz(m.we, []int64{1, 3, int64(h), int64(w)})
	if err != nil {
		return nil, err
	}
	if len(wy) != n {
		return nil, fmt.Errorf("model sylwetki zwrócił kształt %v dla kadru %dx%d", ksztalt, w, h)
	}
	if m.poprz != nil {
		for i := range wy {
			// duża zmiana (ruch) przechodzi od razu, drobne drgania krawędzi są tłumione
			r := wy[i] - m.poprz[i]
			if r < 0 {
				r = -r
			}
			if r < 0.25 {
				wy[i] = 0.65*wy[i] + 0.35*m.poprz[i]
			}
		}
	}
	m.poprz = wy
	maska := make([]byte, n)
	for i, v := range wy {
		switch {
		case v <= 0:
		case v >= 1:
			maska[i] = 255
		default:
			maska[i] = byte(v*255 + 0.5)
		}
	}
	return maska, nil
}

// OpisMaski: gdzie w kadrze jest osoba (ułamki wymiarów kadru), uśrednione po fragmencie.
type OpisMaski struct {
	Klatek   int       `json:"klatek"`
	Pokrycie float64   `json:"pokrycie"` // jaka część kadru to osoba (mediana)
	Gora     float64   `json:"gora"`     // górna krawędź sylwetki (czubek głowy), 0 = góra kadru
	Lewo     float64   `json:"lewo"`
	Prawo    float64   `json:"prawo"`
	SrodekX  float64   `json:"srodekX"`
	Stalosc  float64   `json:"stalosc"` // 1 = sylwetka w tym samym miejscu przez cały fragment
	W, H     int       `json:"-"`
	Srednia  []float32 `json:"-"` // średnia maska z całego fragmentu (W x H, wartości 0-1)
	Czas     float64   `json:"czasLiczenia"`
}

// wymiarMaski: rozmiar, w którym liczona jest maska (krótszy bok = bok, oba podzielne przez 32).
func wymiarMaski(szer, wys, bok int) (int, int) {
	do32 := func(x float64) int {
		v := int(x/32+0.5) * 32
		if v < 64 {
			v = 64
		}
		return v
	}
	if szer <= wys {
		return do32(float64(bok)), do32(float64(bok) * float64(wys) / float64(szer))
	}
	return do32(float64(bok) * float64(szer) / float64(wys)), do32(float64(bok))
}

// maskaFragmentu: liczy maskę osoby dla obrazu, który daje ffmpeg z argumentami argWejscia i filtrem filtr
// (wynik filtra: etykieta [vmaska] w klatkach fps), i zapisuje ją jako bezstratny film w skali szarości.
func maskaFragmentu(ctx context.Context, n *Narzedzia, praca string, argWejscia []string, filtr string, w, h int, fps float64, czas float64, wyjscie string, postep func(float64)) (*OpisMaski, error) {
	syl, err := otworzSylwetke(n.Dane)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	// graf w pliku (bywa długi: wybór klatek rolki mówionej), tak samo jak przy składaniu filmu
	plikFiltru := filepath.Join(praca, "filtr-maska.txt")
	graf := strings.TrimRight(strings.TrimSpace(filtr), ";") + fmt.Sprintf(";\n[vmaska]fps=%g,scale=%d:%d:flags=bilinear,format=rgb24[vm]\n", fps, w, h)
	if err := os.WriteFile(plikFiltru, []byte(graf), 0o644); err != nil {
		return nil, err
	}
	arg := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, argWejscia...)
	arg = append(arg, "-/filter_complex", "filtr-maska.txt", "-map", "[vm]", "-t", fmt.Sprintf("%.3f", czas), "-f", "rawvideo", "-")
	czytaj := exec.CommandContext(ctx, n.FFmpeg, arg...)
	czytaj.Dir = praca
	ukryjOkno(czytaj)
	bledyCz, _ := os.CreateTemp("", "zm-maska-*.txt")
	defer func() { bledyCz.Close(); os.Remove(bledyCz.Name()) }()
	czytaj.Stderr = bledyCz
	wyj, err := czytaj.StdoutPipe()
	if err != nil {
		return nil, err
	}
	pisz := exec.CommandContext(ctx, n.FFmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "rawvideo", "-pix_fmt", "gray",
		"-s", fmt.Sprintf("%dx%d", w, h), "-r", fmt.Sprintf("%g", fps), "-i", "-", "-c:v", "ffv1", "-level", "3", "-g", "1", "-pix_fmt", "gray", wyjscie)
	ukryjOkno(pisz)
	wej, err := pisz.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := czytaj.Start(); err != nil {
		return nil, err
	}
	if err := pisz.Start(); err != nil {
		czytaj.Process.Kill()
		return nil, err
	}
	bufor := bufio.NewReaderSize(wyj, 1<<20)
	klatka := make([]byte, w*h*3)
	spodziewane := int(czas*fps + 0.5)
	var pokrycia, gory, lewe, prawe, srodki []float64
	suma := make([]float32, w*h)
	var blad error
	for {
		if _, err := io.ReadFull(bufor, klatka); err != nil {
			break
		}
		maska, err := syl.Maska(klatka, w, h)
		if err != nil {
			blad = err
			break
		}
		if _, err := wej.Write(maska); err != nil {
			blad = err
			break
		}
		for i, v := range maska {
			suma[i] += float32(v) / 255
		}
		// położenie sylwetki w tej klatce
		razem, sumaX := 0, 0
		gora, lewo, prawo := -1, w, -1
		for y := 0; y < h; y++ {
			wWierszu := 0
			for x := 0; x < w; x++ {
				if maska[y*w+x] > 127 {
					wWierszu++
					sumaX += x
					if x < lewo {
						lewo = x
					}
					if x > prawo {
						prawo = x
					}
				}
			}
			razem += wWierszu
			if gora < 0 && wWierszu*40 > w { // pierwszy wiersz, w którym osoba zajmuje ponad 2,5% szerokości
				gora = y
			}
		}
		pokrycia = append(pokrycia, float64(razem)/float64(w*h))
		if razem > 0 && gora >= 0 {
			gory = append(gory, float64(gora)/float64(h))
			lewe = append(lewe, float64(lewo)/float64(w))
			prawe = append(prawe, float64(prawo+1)/float64(w))
			srodki = append(srodki, float64(sumaX)/float64(razem)/float64(w))
		}
		if postep != nil && spodziewane > 0 {
			postep(float64(len(pokrycia)) / float64(spodziewane))
		}
	}
	wej.Close()
	czytaj.Process.Kill()
	czytaj.Wait()
	if err := pisz.Wait(); err != nil && blad == nil {
		blad = fmt.Errorf("zapis maski: %v", err)
	}
	if blad != nil {
		return nil, blad
	}
	if len(pokrycia) == 0 {
		b, _ := os.ReadFile(bledyCz.Name())
		return nil, fmt.Errorf("ffmpeg nie podał klatek do maski: %s", ogon(string(b), 300))
	}
	for i := range suma {
		suma[i] /= float32(len(pokrycia))
	}
	o := &OpisMaski{Srednia: suma, Klatek: len(pokrycia), Pokrycie: mediana(pokrycia), Gora: mediana(gory), Lewo: mediana(lewe), Prawo: mediana(prawe),
		SrodekX: mediana(srodki), W: w, H: h, Czas: time.Since(start).Seconds()}
	// stałość: jak bardzo górna krawędź i środek odbiegają od mediany (ruch osoby albo zmiana ujęcia w trakcie)
	if len(gory) > 0 {
		odch := 0.0
		for i := range gory {
			d := gory[i] - o.Gora
			if d < 0 {
				d = -d
			}
			e := srodki[i] - o.SrodekX
			if e < 0 {
				e = -e
			}
			odch += d + e
		}
		o.Stalosc = 1 - odch/float64(len(gory))/0.25
		if o.Stalosc < 0 {
			o.Stalosc = 0
		}
		o.Stalosc *= float64(len(gory)) / float64(len(pokrycia))
	}
	return o, nil
}

// polecenieSylwetka: próba z linii poleceń.
//
//	montaz sylwetka --dane KATALOG [--od 0 --czas 3 --bok 384] film.mp4 maska.mkv
func polecenieSylwetka(arg []string) {
	fs := flag.NewFlagSet("sylwetka", flag.ExitOnError)
	dane := fs.String("dane", "", "katalog danych silnika")
	od := fs.Float64("od", 0, "początek fragmentu (s)")
	czas := fs.Float64("czas", 3, "długość fragmentu (s)")
	bok := fs.Int("bok", 384, "krótszy bok maski w pikselach")
	fps := fs.Float64("fps", 30, "klatki na sekundę")
	fs.Parse(arg)
	if fs.NArg() < 2 {
		blad(fmt.Errorf("użycie: montaz sylwetka --dane KATALOG film.mp4 maska.mkv"))
	}
	n, err := znajdzNarzedzia(katalogDanych(*dane))
	if err != nil {
		blad(err)
	}
	info, err := sonduj(n, fs.Arg(0))
	if err != nil {
		blad(err)
	}
	w, h := wymiarMaski(info.W, info.H, *bok)
	kat, _ := os.MkdirTemp("", "zm-sylwetka-")
	defer os.RemoveAll(kat)
	film, _ := filepath.Abs(fs.Arg(0))
	wynik, _ := filepath.Abs(fs.Arg(1))
	o, err := maskaFragmentu(context.Background(), n, kat, []string{"-ss", fmt.Sprintf("%.3f", *od), "-i", film}, "[0:v]null[vmaska]", w, h, *fps, *czas, wynik, nil)
	if err != nil {
		blad(err)
	}
	fmt.Printf("maska %dx%d, %d klatek w %.1f s (%.0f ms na klatkę)\n", w, h, o.Klatek, o.Czas, o.Czas*1000/float64(o.Klatek))
	fmt.Printf("osoba: %.0f%% kadru, czubek głowy na %.0f%% wysokości, od %.0f%% do %.0f%% szerokości, środek %.0f%%, stałość %.2f\n",
		o.Pokrycie*100, o.Gora*100, o.Lewo*100, o.Prawo*100, o.SrodekX*100, o.Stalosc)
}
