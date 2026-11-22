// Render rolki mówionej: jedno wywołanie ffmpeg. Obraz: wybór klatek zostających odcinków (bez buforowania całego
// nagrania w pamięci), kadr 9:16, zbliżenia zmieniane na cięciach, napisy. Dźwięk: odcinki cięte co do próbki,
// odszumianie, wyrównanie głośności, muzyka ściszana pod głos.
package main

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Zlecenie struct {
	Klipy        []*Klip
	Plan         *PlanMowy
	Styl         *Styl
	Fonty        []string // pliki fontów do skopiowania do katalogu pracy
	Muzyka       string   // opcjonalnie
	MuzykaOd     float64  // od której sekundy utworu zacząć (po cichym wstępie)
	MuzykaPoziom float64  // głośność podkładu policzona dla tego utworu (0 = wartość z szablonu)
	Wyjscie      string
	Praca        string        // katalog roboczy: napisy.ass, fonty/, filtr.txt
	Za           *ZaOsoba      // nagłówek za osobą (nil = zwykły nagłówek w napisy.ass)
	Przebitki    []Przebitka   // ujęcia z innych nagrań przykrywające obraz na czas wypowiedzi
	Twarze       [][2]float64  // środek twarzy w każdym nagraniu (0-1 względem obrazu źródła; 0 = środek kadru)
	Akcenty      []AkcentKadru // zbliżenia wynikające z treści (karta treści): punch, schodki, najazd
	Zajawka      *Zajawka      // najmocniejsze zdanie na początek filmu (nil = film zaczyna się zwykle)
}

// wejscieZajawki: numer wejścia ffmpeg z fragmentem nagrania na zajawkę (ostatnie, po przebitkach).
func (z *Zlecenie) wejscieZajawki(wejscia int) int {
	n := wejscia + len(z.Przebitki)
	if z.Muzyka != "" {
		n++
	}
	if z.Za != nil {
		n++
	}
	return n
}

func liczba(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// podzialKadru: granice kawałków z jednym kadrem w odcinku (czas od początku odcinka, z 0 i długością na brzegach).
// Kawałek trwa najmniej 1,6 s, nowy zaczyna się na początku słowa po ok. 2,5 s.
func podzialKadru(slowa []Slowo, wyOd, dl float64) []float64 {
	g := []float64{0}
	for _, s := range slowa {
		t := s.Od - wyOd
		if t >= g[len(g)-1]+2.5 && t <= dl-1.6 {
			g = append(g, t)
		}
	}
	return append(g, dl)
}

func kopiuj(z, do string) error {
	b, err := os.ReadFile(z)
	if err != nil {
		return err
	}
	return os.WriteFile(do, b, 0o644)
}

// kadrNaTwarz: wyrażenia ffmpeg dla kadru filmu (crop) i zbliżenia (zoompan) ustawionych na twarz w nagraniu ki
// (wcześniej na środek: ciasny kadr potrafił ucinać głowę). Bez twarzy: środek kadru.
func kadrNaTwarz(z *Zlecenie, ki int, klip *Klip) (kadrX, zx, zy, ky string) {
	st := z.Styl
	ky = liczba(st.Kadr.Y)
	kadrX, zx, zy = "(iw-ow)/2", "(iw-iw/zoom)/2", "(ih-ih/zoom)*"+ky
	if ki < len(z.Twarze) && z.Twarze[ki][0] > 0 && klip.W > 0 && klip.H > 0 {
		fx, fy := z.Twarze[ki][0], z.Twarze[ki][1]
		W, H := float64(klip.W), float64(klip.H)
		cw := math.Min(W, H*float64(st.Szer)/float64(st.Wys))
		ch := math.Min(H, W*float64(st.Wys)/float64(st.Szer))
		cx := math.Max(0, math.Min(W-cw, fx*W-cw/2))
		cy := (H - ch) * st.Kadr.Y
		tx, ty := (fx*W-cx)/cw, (fy*H-cy)/ch
		kadrX = fmt.Sprintf("max(0,min(iw-ow,%s*iw-ow/2))", liczba(fx))
		zx = fmt.Sprintf("max(0,min(iw-iw/zoom,%s*iw-iw/zoom/2))", liczba(tx))
		zy = fmt.Sprintf("max(0,min(ih-ih/zoom,%s*ih-0.4*ih/zoom))", liczba(ty))
	}
	return
}

// zbudujFiltr: graf filtrów ffmpeg i lista plików wejściowych (tylko klipy, z których coś zostaje).
func zbudujFiltr(z *Zlecenie) (string, []string, float64) {
	st := z.Styl
	kl := float64(st.Klatki)
	var f, fa strings.Builder // obraz i dźwięk osobno: sam obraz służy też do liczenia maski osoby
	var wejscia []string
	var etykietyV, etykietyA []string
	g := 0 // numer odcinka w całej rolce (do naprzemiennych zbliżeń)
	for ki, klip := range z.Klipy {
		var odc []Odcinek
		for _, o := range z.Plan.Odcinki {
			if o.Klip == ki {
				odc = append(odc, o)
			}
		}
		if len(odc) == 0 {
			continue
		}
		we := len(wejscia)
		wejscia = append(wejscia, klip.Plik)

		// obraz: klatki odcinków, potem kadr i zbliżenia
		var wybor, zoom []string
		var akcenty skladnikiZoomu
		klatka := 0
		for _, o := range odc {
			a := int(math.Round(o.Od * kl))
			b := int(math.Round(o.Do * kl))
			wybor = append(wybor, fmt.Sprintf("between(n,%d,%d)", a, b-1))
			// kadr zmienia się na każdym cięciu, a w dłuższym kawałku bez cięcia także co ok. 2,5 s na początku słowa
			// (wzór właściciela z 20.11.2026: szeroki i ciasny kadr na zmianę, twardo, mediana co 2,6 s)
			kawalki := podzialKadru(z.Plan.Slowa, o.WyOd, float64(b-a)/kl)
			akcenty.dodaj(z.Akcenty, o.WyOd, o.WyOd+float64(b-a)/kl, float64(klatka)/kl-o.WyOd, 1/kl)
			for k := 0; k+1 < len(kawalki); k++ {
				zb := st.Kadr.Zblizenia[g%len(st.Kadr.Zblizenia)]
				if zb != 1 {
					p0 := float64(klatka)/kl + kawalki[k]
					p1 := float64(klatka)/kl + kawalki[k+1]
					zoom = append(zoom, fmt.Sprintf("between(it,%s,%s)*%s", liczba(p0-0.004), liczba(p1-0.012), liczba(zb-1)))
				}
				g++
			}
			klatka += b - a
		}
		// tło: szeroki i ciasny kadr na zmianę; akcenty z treści: skoki ponad tło, najazd zamiast tła
		zexpr := akcenty.wyrazenie(zoom)
		fmt.Fprintf(&f, "[%d:v]fps=%d,select='%s',setpts=N/%d/TB", we, st.Klatki, strings.Join(wybor, "+"), st.Klatki)
		if klip.HDR { // HLG/PQ z telefonu -> zwykłe kolory (bez tego obraz jest wyprany)
			f.WriteString(",zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv")
		}
		kadrX, zx, zy, ky := kadrNaTwarz(z, ki, klip)
		fmt.Fprintf(&f, ",crop=w='2*floor(min(iw,ih*%d/%d)/2)':h='2*floor(min(ih,iw*%d/%d)/2)':x='%s':y='(ih-oh)*%s'", st.Szer, st.Wys, st.Wys, st.Szer, kadrX, ky)
		fmt.Fprintf(&f, ",zoompan=z='%s':x='%s':y='%s':d=1:s=%dx%d:fps=%d,setsar=1,format=yuv420p[v%d];\n", zexpr, zx, zy, st.ObrazSzer(), st.ObrazWys(), st.Klatki, we)
		etykietyV = append(etykietyV, fmt.Sprintf("[v%d]", we))

		// dźwięk: każdy odcinek osobno (dokładność do próbki), krótkie wyciszenia na brzegach przeciw trzaskom
		fmt.Fprintf(&fa, "[%d:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=mono,asplit=%d", we, len(odc))
		for i := range odc {
			fmt.Fprintf(&fa, "[x%d_%d]", we, i)
		}
		fa.WriteString(";\n")
		for i, o := range odc {
			d := o.Do - o.Od
			fmt.Fprintf(&fa, "[x%d_%d]atrim=start=%s:end=%s,asetpts=PTS-STARTPTS,afade=t=in:st=0:d=0.006,afade=t=out:st=%s:d=0.012[y%d_%d];\n",
				we, i, liczba(o.Od), liczba(o.Do), liczba(math.Max(0, d-0.012)), we, i)
			etykietyA = append(etykietyA, fmt.Sprintf("[y%d_%d]", we, i))
		}
	}
	czas := z.Plan.Czas
	surowy := "[vc]"
	if len(z.Przebitki) > 0 && z.Wyjscie != "" {
		surowy = "[vb0]"
	}
	if len(etykietyV) > 1 {
		fmt.Fprintf(&f, "%sconcat=n=%d:v=1:a=0%s;\n", strings.Join(etykietyV, ""), len(etykietyV), surowy)
	} else {
		fmt.Fprintf(&f, "%snull%s;\n", etykietyV[0], surowy)
	}
	if surowy != "[vc]" {
		// wejścia ffmpeg: nagrania, potem muzyka, maska osoby, na końcu przebitki
		pierwsze := len(wejscia)
		if z.Muzyka != "" {
			pierwsze++
		}
		if z.Za != nil {
			pierwsze++
		}
		for j, p := range z.Przebitki {
			wy := fmt.Sprintf("[vb%d]", j+1)
			if j == len(z.Przebitki)-1 {
				wy = "[vc]"
			}
			fmt.Fprintf(&f, "[%d:v]fps=%d,setpts=PTS-STARTPTS+%s/TB,format=yuv420p,setsar=1[pb%d];\n[vb%d][pb%d]overlay=enable='between(t,%s,%s)':eof_action=pass%s;\n",
				pierwsze+j, st.Klatki, liczba(p.Od), j, j, j, liczba(p.Od), liczba(p.Do-0.5/kl), wy)
		}
	}
	if z.Wyjscie == "" { // sam obraz przed napisami (do maski osoby)
		return f.String(), wejscia, czas
	}
	// z zajawką główna część kończy się na [vg], a zajawka z błyskiem dokleja się przed nią (czasy w głównej części bez zmian)
	gotowy := "[v]"
	if z.Zajawka != nil {
		gotowy = "[vg]"
	}
	if z.Za != nil {
		wejscieMaski := len(wejscia)
		if z.Muzyka != "" {
			wejscieMaski++
		}
		f.WriteString(z.Za.filtr(st, wejscieMaski, "[vc]"))
		f.WriteString("[vz]ass=napisy.ass:fontsdir=fonty" + gotowy + ";\n")
	} else {
		f.WriteString("[vc]ass=napisy.ass:fontsdir=fonty" + gotowy + ";\n")
	}
	if z.Zajawka != nil {
		weZ := z.wejscieZajawki(len(wejscia))
		f.WriteString(z.Zajawka.filtrObrazu(z, weZ, "[vg]", "[v]"))
		g, et := z.Zajawka.filtrDzwieku(weZ)
		f.WriteString(g)
		etykietyA = append(et, etykietyA...)
		czas += z.Zajawka.Calosc()
	}
	f.WriteString(fa.String())

	fmt.Fprintf(&f, "%sconcat=n=%d:v=0:a=1[g0];\n", strings.Join(etykietyA, ""), len(etykietyA))
	d := st.Dzwiek
	glos := "highpass=f=85"
	if d.Odszumianie > 0 {
		glos += fmt.Sprintf(",afftdn=nr=%s:nf=-30", liczba(d.Odszumianie))
	}
	// głośność liczona już na dwóch kanałach: ten sam głos w obu kanałach jest o 3 dB głośniejszy niż mono
	glos += fmt.Sprintf(",speechnorm=e=4:r=0.0005:l=1,pan=stereo|c0=c0|c1=c0,loudnorm=I=%s:TP=-1.5:LRA=7,aresample=48000", liczba(d.GlosnoscLUFS))
	if z.Muzyka != "" {
		wm := len(wejscia)
		wycisz := float64(d.MuzykaWyciszMs) / 1000
		if wycisz <= 0 {
			wycisz = 0.5
		}
		poziomMuzyki := d.MuzykaPoziom
		if z.MuzykaPoziom > 0 {
			poziomMuzyki = z.MuzykaPoziom
		}
		fmt.Fprintf(&f, "[g0]%s,asplit=2[g1][g2];\n", glos)
		fmt.Fprintf(&f, "[%d:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,atrim=0:%s,volume=%s,afade=t=out:st=%s:d=%s[m0];\n",
			wm, liczba(czas), liczba(poziomMuzyki), liczba(math.Max(0, czas-wycisz)), liczba(wycisz))
		f.WriteString("[m0][g2]sidechaincompress=threshold=0.06:ratio=2.5:attack=30:release=450[m1];\n")
		if z.Zajawka != nil {
			f.WriteString(z.Zajawka.filtrBlysku("[zjs]"))
			f.WriteString("[g1][m1][zjs]amix=inputs=3:normalize=0:duration=first,alimiter=limit=0.89:level=0[a]\n")
		} else {
			f.WriteString("[g1][m1]amix=inputs=2:normalize=0:duration=first,alimiter=limit=0.89:level=0[a]\n")
		}
	} else if z.Zajawka != nil {
		f.WriteString(z.Zajawka.filtrBlysku("[zjs]"))
		fmt.Fprintf(&f, "[g0]%s[g1];\n[g1][zjs]amix=inputs=2:normalize=0:duration=first,alimiter=limit=0.89:level=0[a]\n", glos)
	} else {
		fmt.Fprintf(&f, "[g0]%s,alimiter=limit=0.89:level=0[a]\n", glos)
	}
	return f.String(), wejscia, czas
}

// renderuj: przygotowuje katalog pracy i uruchamia ffmpeg; postep dostaje ułamek 0-1.
func renderuj(n *Narzedzia, enk Enkoder, z *Zlecenie, postep func(float64)) error {
	fonty := filepath.Join(z.Praca, "fonty")
	if err := os.MkdirAll(fonty, 0o755); err != nil {
		return err
	}
	for _, p := range z.Fonty {
		if err := kopiuj(p, filepath.Join(fonty, filepath.Base(p))); err != nil {
			return fmt.Errorf("nie udało się przygotować fontu: %v", err)
		}
	}
	filtr, wejscia, czas := zbudujFiltr(z)
	if err := os.WriteFile(filepath.Join(z.Praca, "filtr.txt"), []byte(filtr), 0o644); err != nil {
		return err
	}
	arg := []string{"-hide_banner", "-y", "-nostats", "-loglevel", "error", "-progress", "pipe:1"}
	for _, w := range wejscia {
		arg = append(arg, "-i", w)
	}
	if z.Muzyka != "" {
		arg = append(arg, "-ss", liczba(z.MuzykaOd), "-stream_loop", "-1", "-i", z.Muzyka)
	}
	if z.Za != nil {
		arg = append(arg, "-i", z.Za.Maska)
	}
	for _, p := range z.Przebitki {
		arg = append(arg, "-i", p.Plik)
	}
	if zj := z.Zajawka; zj != nil { // ten sam fragment nagrania drugi raz: dekodowane są tylko te sekundy
		arg = append(arg, "-ss", liczba(zj.Od), "-t", liczba(zj.Czas+0.5), "-i", z.Klipy[zj.Klip].Plik)
	}
	arg = append(arg, "-/filter_complex", "filtr.txt", "-map", "[v]", "-map", "[a]", "-r", fmt.Sprint(z.Styl.Klatki))
	arg = append(arg, enk.Arg...)
	arg = append(arg, "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-movflags", "+faststart", "-t", liczba(czas), z.Wyjscie)

	cmd := exec.CommandContext(context.Background(), n.FFmpeg, arg...)
	cmd.Dir = z.Praca
	ukryjOkno(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var bledy strings.Builder
	cmd.Stderr = &bledy
	if err := cmd.Start(); err != nil {
		return err
	}
	sk := bufio.NewScanner(out)
	for sk.Scan() {
		linia := sk.Text()
		if v, ok := strings.CutPrefix(linia, "out_time_us="); ok && postep != nil && czas > 0 {
			if us, e := strconv.ParseFloat(v, 64); e == nil {
				postep(math.Min(1, math.Max(0, us/1e6/czas)))
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("render nie powiódł się (%s): %s", enk.Nazwa, ogon(bledy.String(), 600))
	}
	return nil
}
