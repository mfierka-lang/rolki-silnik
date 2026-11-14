// Rolka z samych ujęć (bez mowy): skan każdego nagrania, wybór najspokojniejszych fragmentów, przygotowanie
// każdego ujęcia jako osobnego pliku 1080 x 1920 (kadr pionowy tam, gdzie coś się dzieje, opcjonalna stabilizacja),
// na koniec złożenie z nagłówkiem. Ujęcia przygotowane osobno nie obciążają pamięci (jeden dekoder 4K naraz)
// i później posłużą jako lekkie kopie do biblioteki przebitek.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func dekodowanieSprzetowe() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"-hwaccel", "d3d11va"}
	case "darwin":
		return []string{"-hwaccel", "videotoolbox"}
	}
	return nil
}

// przygotujUjecie: wycina okno z nagrania, kadruje do pionu i zapisuje plik pośredni wysokiej jakości.
// Przy stabilizacji obraz jest liczony dwa razy (pomiar drgań, potem wyrównanie) na kadrze z zapasem.
func przygotujUjecie(n *Narzedzia, k *Klip, o Okno, st *Styl, praca, nazwa string, stabilizuj bool) error {
	// pionowy kadr o proporcjach rolki, wycięty z pełnej wysokości źródła
	kh := k.H - k.H%2
	kw := int(math.Round(float64(kh)*float64(st.Szer)/float64(st.Wys)/2)) * 2
	if kw > k.W {
		kw = k.W - k.W%2
		kh = int(math.Round(float64(kw)*float64(st.Wys)/float64(st.Szer)/2)) * 2
	}
	x := int(math.Round(o.KadrX*float64(k.W) - float64(kw)/2))
	x = max(0, min(k.W-kw, x))
	y := (k.H - kh) / 2
	kadr := fmt.Sprintf("fps=%d,crop=%d:%d:%d:%d,scale=%d:%d:flags=lanczos", st.Klatki, kw, kh, x, y, st.Szer, st.Wys)
	if o.Predkosc < 1.0 && !stabilizuj && !bezNajazdu {
		// kamera stoi: powolny najazd o 4%, żeby ujęcie żyło (obraz 2 razy większy przed najazdem = płynny ruch)
		klatek := math.Round((o.Do - o.Od) * float64(st.Klatki))
		kadr = fmt.Sprintf(`fps=%d,crop=%d:%d:%d:%d,scale=%d:%d:flags=bicubic,zoompan=z='1+0.04*on/%.0f':x='(iw-iw/zoom)/2':y='(ih-ih/zoom)/2':d=1:s=%dx%d:fps=%d`,
			st.Klatki, kw, kh, x, y, 2*st.Szer, 2*st.Wys, klatek, st.Szer, st.Wys, st.Klatki)
	}
	wejscie := func() []string {
		a := []string{"-v", "error", "-nostdin", "-y", "-ss", liczba(o.Od), "-t", liczba(o.Do - o.Od)}
		a = append(a, dekodowanieSprzetowe()...)
		return append(a, "-i", k.Plik)
	}
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Minute)
	defer stop()
	filtr := kadr
	if stabilizuj {
		trf := nazwa + ".trf"
		a := append(wejscie(), "-map", "0:v:0", "-an", "-vf", kadr+",vidstabdetect=shakiness=5:accuracy=12:stepsize=6:mincontrast=0.25:result="+trf, "-f", "null", "-")
		if _, errs, err := uruchom(ctx, praca, n.FFmpeg, a...); err != nil {
			return fmt.Errorf("pomiar drgań nie powiódł się: %s", ogon(errs, 200))
		}
		filtr += ",vidstabtransform=input=" + trf + ":smoothing=12:optzoom=1:zoomspeed=0.1:interpol=bicubic:crop=black"
	}
	a := append(wejscie(), "-map", "0:v:0")
	if k.MaDzwiek {
		a = append(a, "-map", "0:a:0", "-af", "aresample=48000,aformat=sample_fmts=s16:channel_layouts=stereo", "-c:a", "pcm_s16le")
	}
	a = append(a, "-vf", filtr+",format=yuv420p", "-c:v", "libx264", "-preset", "veryfast", "-crf", "13", "-g", fmt.Sprint(st.Klatki), nazwa+".mkv")
	if _, errs, err := uruchom(ctx, praca, n.FFmpeg, a...); err != nil {
		return fmt.Errorf("przygotowanie ujęcia nie powiodło się: %s", ogon(errs, 300))
	}
	return nil
}

// zlozUjecia: łączy przygotowane ujęcia, nakłada nagłówek, wyrównuje dźwięk z planu i koduje gotowy plik.
func zlozUjecia(n *Narzedzia, enk Enkoder, st *Styl, praca string, pliki []string, maDzwiek []bool, czasy []float64, przejsciaPo map[int]bool, muz *Utwor, maNapisy bool, wyjscie string) error {
	var f strings.Builder
	arg := []string{"-hide_banner", "-y", "-nostats", "-loglevel", "error"}
	razem := 0.0
	var ev, ea []string
	for i, p := range pliki {
		arg = append(arg, "-i", p)
		fmt.Fprintf(&f, "[%d:v]setpts=PTS-STARTPTS,fps=%d,setsar=1[v%d];\n", i, st.Klatki, i)
		ev = append(ev, fmt.Sprintf("[v%d]", i))
		d := czasy[i]
		if maDzwiek[i] {
			fmt.Fprintf(&f, "[%d:a]asetpts=PTS-STARTPTS,apad,atrim=0:%s,afade=t=in:st=0:d=0.04,afade=t=out:st=%s:d=0.06[a%d];\n", i, liczba(d), liczba(math.Max(0, d-0.06)), i)
		} else {
			fmt.Fprintf(&f, "anullsrc=r=48000:cl=stereo,atrim=0:%s[a%d];\n", liczba(d), i)
		}
		ea = append(ea, fmt.Sprintf("[a%d]", i))
		razem += d
	}
	razem = lancuchZlozenia(&f, ev, ea, czasy, przejsciaPo)
	zrodloDzwieku := "[ac]"
	if muz != nil {
		// muzyka od miejsca, w którym gra pełnią; dźwięk z planu zostaje słyszalny, na końcu wyciszenie
		arg = append(arg, "-ss", liczba(muz.Start), "-i", muz.Plik)
		fmt.Fprintf(&f, "[%d:a]aresample=48000,aformat=channel_layouts=stereo,volume=%s,afade=t=in:st=0:d=0.2,afade=t=out:st=%s:d=0.9[mu];\n[ac]volume=0.7[nat];\n[nat][mu]amix=inputs=2:normalize=0:duration=first[mx];\n",
			len(pliki), liczba(wzmocnienieMuzyki(muz, -19)), liczba(math.Max(0, razem-0.9)))
		zrodloDzwieku = "[mx]"
	}
	if maNapisy {
		f.WriteString("[vc]ass=napisy.ass:fontsdir=fonty[v];\n")
	} else {
		f.WriteString("[vc]null[v];\n")
	}
	// dźwięk z planu zostaje (nigdy cisza). Dopóki nie ma muzyki, jest całą ścieżką, więc dostaje pełną głośność;
	// gdy dojdzie muzyka z biblioteki, zejdzie o kilka dB i będzie tłem.
	fmt.Fprintf(&f, "%shighpass=f=45,loudnorm=I=%s:TP=-3:LRA=9,aresample=48000,alimiter=limit=0.8:level=0[a]\n", zrodloDzwieku, liczba(st.Dzwiek.GlosnoscLUFS))
	if err := os.WriteFile(filepath.Join(praca, "filtr.txt"), []byte(f.String()), 0o644); err != nil {
		return err
	}
	arg = append(arg, "-/filter_complex", "filtr.txt", "-map", "[v]", "-map", "[a]", "-r", fmt.Sprint(st.Klatki))
	arg = append(arg, enk.Arg...)
	arg = append(arg, "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-movflags", "+faststart", "-t", liczba(razem), wyjscie)
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Minute)
	defer stop()
	if _, errs, err := uruchom(ctx, praca, n.FFmpeg, arg...); err != nil {
		return fmt.Errorf("składanie nie powiodło się (%s): %s", enk.Nazwa, ogon(errs, 500))
	}
	return nil
}

func polecenieUjecia(arg []string, tylkoSkan bool) {
	fs := flag.NewFlagSet("ujecia", flag.ExitOnError)
	stylPlik := fs.String("styl", "szablon/styl.json", "szablon stylu")
	dane := fs.String("dane", "", "katalog danych silnika")
	wyjscie := fs.String("wyjscie", "rolka.mp4", "plik wynikowy")
	praca := fs.String("praca", "", "katalog roboczy")
	hak := fs.String("hak", "", "nagłówek na początek: \"Linia 1|Linia 2\"")
	napisySpec := fs.String("napisy", "", "hasła na ujęciach: \"2:Dzień *montażu*|3-4:Ekipa *na miejscu*\" (gwiazdki = słowo-klucz)")
	rodzaj := fs.String("rodzaj", "", "wygląd haseł: karta albo bloki (puste = z szablonu)")
	wejscie := fs.String("wejscie", "", "wejście napisów: druk albo wjazd (puste = z szablonu)")
	dlugosc := fs.Float64("dlugosc", 24, "docelowa długość rolki w sekundach")
	ujecie := fs.Float64("ujecie", 2.4, "długość jednego ujęcia w sekundach")
	stab := fs.String("stabilizuj", "auto", "stabilizacja ujęć: auto, tak, nie")
	progDrgan := fs.Float64("prog", 0, "największe dopuszczalne drgania (0 = z szablonu)")
	progPodob := fs.Float64("podobne", 0.85, "powyżej tego podobieństwa (0-1) dwa kadry uznaję za prawie takie same")
	progOceny := fs.Float64("ocena", 1.2, "najgorsza ocena ujęcia, które jeszcze może wejść do rolki")
	przejscia := fs.Bool("przejscia", false, "przejścia na granicach części filmu (odsłonięcie od dołu z linią dyszy)")
	najazdWyl := fs.Bool("bez-najazdu", false, "bez powolnego najazdu na nieruchomych ujęciach")
	muzykaFlaga := fs.String("muzyka", "", "muzyka: auto (z biblioteki) albo plik; puste = bez muzyki")
	nastroj := fs.String("nastroj", "", "nastrój muzyki: dynamiczny, spokojny albo pusty")
	pomin := fs.Int("pomin", 0, "ile ujęć z początku pominąć (uwaga: skróć początek)")
	zRozumem := fs.Bool("rozum", false, "opisz ujęcia modelem (kolejność: plan ogólny na początek, spokojny widok na koniec; hasła z szablonu)")
	fs.Parse(arg)
	pliki := fs.Args()
	if len(pliki) == 0 {
		blad(fmt.Errorf("podaj nagrania"))
	}
	t0 := time.Now()
	n, err := znajdzNarzedzia(katalogDanych(*dane))
	if err != nil {
		blad(err)
	}
	st, err := wczytajStyl(*stylPlik)
	if err != nil {
		blad(err)
	}
	if *praca == "" {
		*praca, _ = os.MkdirTemp("", "zblizka-ujecia-")
		defer os.RemoveAll(*praca)
	}
	pracaAbs, _ := filepath.Abs(*praca)
	os.MkdirAll(pracaAbs, 0o755)

	// 1. skan
	var klipy []*Klip
	var skany []*Skan
	sumaNagran := 0.0
	for i, p := range pliki {
		abs, _ := filepath.Abs(p)
		k, err := sonduj(n, abs)
		if err != nil {
			blad(err)
		}
		komunikat("skan", 0.6*float64(i)/float64(len(pliki)), fmt.Sprintf("Oglądam nagranie %d z %d", i+1, len(pliki)))
		sk, err := skanuj(n, k)
		if err != nil {
			blad(err)
		}
		klipy, skany = append(klipy, k), append(skany, sk)
		sumaNagran += k.Czas
	}
	tSkan := time.Since(t0).Seconds()

	// 2. kandydaci z każdego nagrania
	var okna []Okno
	for i, k := range klipy {
		ile := 1
		if k.Czas > 20 {
			ile = 2
		}
		if k.Czas > 60 {
			ile = 3
		}
		odstep := 6.0
		if *dlugosc > 60 { // dłuższy film: więcej ujęć z każdego nagrania, gęściej
			ile, odstep = max(1, min(8, int(k.Czas/8))), 3
		}
		okna = append(okna, oknaKlipu(i, k, skany[i], *ujecie, ile, odstep)...)
	}
	if tylkoSkan {
		fmt.Printf("%-34s %7s %6s | okna: od-do (drgania, prędkość, ostrość, ruch w kadrze, kadr X)\n", "nagranie", "czas", "skan")
		for i, k := range klipy {
			var dr []float64
			for j := 1; j < len(skany[i].Probki); j++ {
				p, q := skany[i].Probki[j], skany[i].Probki[j-1]
				dr = append(dr, math.Hypot(p.Dx-q.Dx, p.Dy-q.Dy)*1000)
			}
			twarz, _, _ := udzialTwarzy(skany[i])
			fmt.Printf("%-34s %6.1fs %5.1fs | twarz do kamery %3.0f%% | mediana zmian ruchu %.2f |", filepath.Base(k.Plik), k.Czas, skany[i].CzasSkan, twarz*100, mediana(dr))
			for _, o := range okna {
				if o.Klip == i {
					fmt.Printf("  %.1f-%.1f (%.2f, %.1f, %.2f, %.1f, %.2f; ocena %.2f)", o.Od, o.Do, o.Drgania, o.Predkosc, o.Ostrosc, o.Akt, o.KadrX, o.Ocena)
				}
			}
			fmt.Println()
		}
		// najbliższy inny kandydat dla każdego okna: do strojenia progu „prawie takie same”
		fmt.Print("podobieństwo (okno: najbardziej podobne inne, wynik 0-1):")
		for a := range okna {
			naj, kto := -1.0, -1
			for b := range okna {
				if d := podobienstwo(okna[a].Mini, okna[b].Mini, okna[a].MiniW); a != b && d > naj {
					naj, kto = d, b
				}
			}
			fmt.Printf("  %d:%d %.2f", a+1, kto+1, naj)
		}
		fmt.Println()
		fmt.Printf("razem %.0f s nagrań, skan %.1f s (%s, %.1fx szybciej niż czas nagrań)\n", sumaNagran, tSkan, skany[0].Sposob, sumaNagran/tSkan)
		return
	}

	// 2b. co widać na kandydatach: opisy z pamięci, a brakujące od modelu (uruchamianego tylko wtedy, gdy trzeba).
	// Opisywane są po dwa najlepsze okna z każdego nagrania, najwyżej dwa razy tyle, ile ujęć wejdzie do rolki.
	tRozum, opisanych := 0.0, 0
	nazwaRozumu := ""
	if *zRozumem {
		tr := time.Now()
		sort.SliceStable(okna, func(a, b int) bool { return okna[a].Ocena < okna[b].Ocena })
		limit := 2 * int(math.Round(*dlugosc / *ujecie))
		zKl := map[int]int{}
		var doOpisu []int
		for i := range okna {
			if zKl[okna[i].Klip] < 2 && len(doOpisu) < limit {
				zKl[okna[i].Klip]++
				doOpisu = append(doOpisu, i)
			}
		}
		var brak []int
		for _, i := range doOpisu {
			if op, _ := opiszUjecie(nil, n, klipy[okna[i].Klip], okna[i], st); op != nil {
				okna[i].Opis, okna[i].Rola = op, rolaUjecia(op)
			} else {
				brak = append(brak, i)
			}
		}
		if len(brak) > 0 {
			komunikat("rozum", 0.6, "Uruchamiam model, który rozpoznaje, co widać na ujęciach")
			r, err := uruchomRozum(n, pracaAbs, "obraz")
			if err != nil {
				komunikat("rozum", 0.6, "Model nie wystartował, wybieram ujęcia bez opisów: "+err.Error())
			}
			if r != nil {
				nazwaRozumu = r.Model
				for nr, i := range brak {
					komunikat("rozum", 0.6+0.12*float64(nr)/float64(len(brak)), fmt.Sprintf("Sprawdzam, co widać na ujęciu %d z %d", nr+1, len(brak)))
					if op, _ := opiszUjecie(r, n, klipy[okna[i].Klip], okna[i], st); op != nil {
						okna[i].Opis, okna[i].Rola = op, rolaUjecia(op)
						opisanych++
					}
				}
				r.Zamknij()
			}
		}
		tRozum = time.Since(tr).Seconds()
	}

	// Muzyka: utwór z biblioteki dobrany do nastroju albo stylu. Gdy ma wyraźny rytm, cięcia wypadają co pełne
	// takty (2, 4 albo 8 uderzeń: to, co najbliżej zadanej długości ujęcia), a liczba ujęć wynika z tego kroku.
	var utwor *Utwor
	if *muzykaFlaga != "" {
		komunikat("muzyka", 0.74, "Dobieram muzykę i liczę jej rytm")
		if *muzykaFlaga == "auto" {
			utwor = wybierzUtwor(n, *nastroj, filepath.Base(pracaAbs), *dlugosc*1.1+4, nil)
		} else {
			utwor, _ = analizujUtwor(n, *muzykaFlaga)
		}
		if utwor != nil && utwor.Pewnosc >= 0.15 && utwor.Okres > 0.2 {
			naj := 0.0
			for _, k := range []float64{2, 4, 8} {
				if naj == 0 || math.Abs(k*utwor.Okres-*ujecie) < math.Abs(naj-*ujecie) {
					naj = k * utwor.Okres
				}
			}
			*ujecie = naj
		}
	}

	// 3. wybór: najspokojniejsze okna, tyle, ile mieści się w docelowej długości, w kolejności nagrań
	prog := *progDrgan
	if prog <= 0 {
		prog = 3.0
	}
	// Najpierw najlepsze ujęcie z każdego nagrania, dopiero potem drugie i trzecie z tego samego: rolka ma pokazać
	// różne rzeczy. Kadr prawie taki sam jak już wybrany odpada (w rolce wyglądałby jak przeskok).
	sort.SliceStable(okna, func(a, b int) bool { return okna[a].Ocena < okna[b].Ocena })
	ile := int(math.Round(*dlugosc / *ujecie))
	var wybrane []Okno
	zKlipu := map[int]int{}
	sprawdzone := make([]bool, len(okna))
	podobnych, widokow := 0, 0
	rundy := 3
	if *dlugosc > 60 {
		rundy = 8
	}
	bezNajazdu = *najazdWyl
	for runda := 1; runda <= rundy && len(wybrane) < ile; runda++ {
		for i, o := range okna {
			if len(wybrane) >= ile {
				break
			}
			if sprawdzone[i] || o.Drgania > prog || o.Ocena > *progOceny || zKlipu[o.Klip] >= runda {
				continue
			}
			// nieruchomy widok bez ludzi: jeden wystarczy (na zakończenie); kolejne dopiero, gdy brakuje materiału
			if o.Rola == "widok" && widokow >= 1 && runda < rundy {
				continue
			}
			sprawdzone[i] = true
			podobne := false
			for _, w := range wybrane {
				if podobienstwo(o.Mini, w.Mini, o.MiniW) > *progPodob {
					podobne = true
					break
				}
			}
			if podobne {
				podobnych++
				continue
			}
			wybrane = append(wybrane, o)
			zKlipu[o.Klip]++
			if o.Rola == "widok" {
				widokow++
			}
		}
	}
	if len(wybrane) == 0 {
		blad(fmt.Errorf("żadne ujęcie nie jest wystarczająco dobre (najlepsze ma ocenę %.2f przy progu %.2f)", okna[0].Ocena, *progOceny))
	}
	// Kolejność: tak jak nagrywano, ale gdy wiadomo, co widać, rolkę otwiera plan ogólny miejsca (najlepszy z nich),
	// a zamyka spokojny widok bez ludzi (efekt pracy). Reszta zostaje w kolejności nagrań.
	otwarcie := -1
	for i, o := range wybrane {
		if o.Rola == "otwarcie" && (otwarcie < 0 || o.Ocena < wybrane[otwarcie].Ocena) {
			otwarcie = i
		}
	}
	miejsce := func(i int) int {
		switch {
		case i == otwarcie:
			return 0
		case wybrane[i].Rola == "widok":
			return 2
		}
		return 1
	}
	kolejnosc := make([]int, len(wybrane))
	for i := range kolejnosc {
		kolejnosc[i] = i
	}
	sort.SliceStable(kolejnosc, func(a, b int) bool {
		x, y := kolejnosc[a], kolejnosc[b]
		if miejsce(x) != miejsce(y) {
			return miejsce(x) < miejsce(y)
		}
		if wybrane[x].Klip != wybrane[y].Klip {
			return wybrane[x].Klip < wybrane[y].Klip
		}
		return wybrane[x].Od < wybrane[y].Od
	})
	ulozone := make([]Okno, len(wybrane))
	for i, j := range kolejnosc {
		ulozone[i] = wybrane[j]
	}
	wybrane = ulozone
	for i := 0; i < *pomin && len(wybrane) > 3; i++ {
		wybrane = wybrane[1:]
	}
	przejsciaPo := map[int]bool{}
	if *przejscia {
		przejsciaPo = przejsciaDlaUjec(len(wybrane), *dlugosc > 60)
	}
	kl := float64(st.Klatki)
	granica := func(i int) float64 { return math.Round(float64(i)**ujecie*kl) / kl } // cięcia na wspólnej siatce, bez sumowania zaokrągleń
	for i := range wybrane {
		dl := granica(i+1) - granica(i)
		if przejsciaPo[i] && i < len(wybrane)-1 {
			dl += czasPrzejscia // przejście zjada tyle z ujęcia, a cięcie ma zostać na uderzeniu
		}
		wybrane[i].Od = math.Round(wybrane[i].Od*kl) / kl
		if nadmiar := wybrane[i].Od + dl - (klipy[wybrane[i].Klip].Czas - 0.1); nadmiar > 0 {
			wybrane[i].Od = math.Max(0, math.Round((wybrane[i].Od-nadmiar)*kl)/kl)
		}
		wybrane[i].Do = wybrane[i].Od + dl
	}
	// chwila oddechu przed końcem: ostatnie ujęcie (efekt, wezwanie) trwa o 0,6 s dłużej, gdy nagranie na to pozwala
	if ost := len(wybrane) - 1; ost >= 2 && wybrane[ost].Do+0.6 < klipy[wybrane[ost].Klip].Czas-0.2 {
		wybrane[ost].Do += 0.6
	}
	js, _ := json.MarshalIndent(wybrane, "", " ")
	os.WriteFile(filepath.Join(pracaAbs, "wybor.json"), js, 0o644)

	// 4. przygotowanie ujęć
	t1 := time.Now()
	var seg []string
	var maDzw []bool
	var czasy []float64
	stabilizowane := 0
	for i, o := range wybrane {
		komunikat("ujecia", 0.6+0.3*float64(i)/float64(len(wybrane)), fmt.Sprintf("Przygotowuję ujęcie %d z %d", i+1, len(wybrane)))
		nazwa := fmt.Sprintf("ujecie%02d", i)
		// stabilizacja tylko tam, gdzie drgania są widoczne; spokojnym ujęciom mogłaby zaszkodzić
		s := (*stab == "tak" || (*stab == "auto" && o.Drgania > 1.2)) && maFiltr(n, "vidstabdetect")
		if s {
			stabilizowane++
		}
		if err := przygotujUjecie(n, klipy[o.Klip], o, st, pracaAbs, nazwa, s); err != nil {
			blad(err)
		}
		seg = append(seg, nazwa+".mkv")
		maDzw = append(maDzw, klipy[o.Klip].MaDzwiek)
		czasy = append(czasy, o.Do-o.Od)
	}
	tUj := time.Since(t1).Seconds()

	// przejścia skracają oś czasu (dwa ujęcia nakładają się na chwilę): napisy liczone są na osi po przejściach
	czasyEf := append([]float64(nil), czasy...)
	var chwilePrzejsc []float64
	naOsi := 0.0
	for i := range czasyEf {
		if przejsciaPo[i] && i < len(czasyEf)-1 {
			czasyEf[i] -= czasPrzejscia
			chwilePrzejsc = append(chwilePrzejsc, naOsi+czasyEf[i])
		}
		naOsi += czasyEf[i]
	}

	// 5. nagłówek i złożenie
	t2 := time.Now()
	maNapisy := false
	if *rodzaj != "" {
		st.Napisy.Rodzaj = *rodzaj
	}
	if *wejscie != "" {
		st.Napisy.Wejscie = *wejscie
	}
	var linieHaka []string
	if *hak != "" {
		linieHaka = strings.Split(*hak, "|")
	}
	var hasla []NapisUjecia
	if *napisySpec == "auto" {
		// hasła z szablonu marki dla ujęć, na których widać daną rzecz; na ostatnim ujęciu wezwanie
		pocz := 0.0
		uzyte := map[int]bool{}
		for i, o := range wybrane {
			tekst := ""
			switch {
			case i == 0 && len(linieHaka) > 0:
			case i == len(wybrane)-1 && st.Wezwanie != "" && len(wybrane) > 2:
				tekst = st.Wezwanie
			default:
				tekst = dopasujHaslo(st, o.Opis, uzyte)
			}
			if tekst != "" {
				hasla = append(hasla, NapisUjecia{tekst, pocz, pocz + czasyEf[i]})
			}
			pocz += czasyEf[i]
		}
	} else {
		hasla = rozlozNapisy(*napisySpec, czasyEf, *hak != "")
	}
	var teksty []string
	for _, h := range hasla {
		teksty = append(teksty, strings.ReplaceAll(h.Tekst, "*", ""))
	}
	rap, _ := json.MarshalIndent(map[string]any{"ujecia": wybrane, "hasla": teksty, "naglowek": linieHaka, "rozum": nazwaRozumu,
		"czas": naOsi, "nagran": len(klipy), "material": sumaNagran, "muzyka": utwor}, "", " ")
	os.WriteFile(filepath.Join(pracaAbs, "ujecia.json"), rap, 0o644)
	if len(linieHaka) > 0 || len(hasla) > 0 || len(chwilePrzejsc) > 0 {
		fonty, err := zapiszNapisyUjec(filepath.Join(pracaAbs, "napisy.ass"), linieHaka, czasyEf[0], hasla, chwilePrzejsc, st)
		if err != nil {
			blad(err)
		}
		os.MkdirAll(filepath.Join(pracaAbs, "fonty"), 0o755)
		for _, p := range fonty {
			kopiuj(p, filepath.Join(pracaAbs, "fonty", filepath.Base(p)))
		}
		maNapisy = true
	}
	enk := wybierzEnkoder(n)
	komunikat("render", 0.92, "Składam rolkę ("+enk.Nazwa+")")
	wy, _ := filepath.Abs(*wyjscie)
	if err := zlozUjecia(n, enk, st, pracaAbs, seg, maDzw, czasy, przejsciaPo, utwor, maNapisy, wy); err != nil {
		if enk.Nazwa == "libx264" {
			blad(err)
		}
		kand := kandydaciEnkoderow()
		if err := zlozUjecia(n, kand[len(kand)-1], st, pracaAbs, seg, maDzw, czasy, przejsciaPo, utwor, maNapisy, wy); err != nil {
			blad(err)
		}
	}
	tZl := time.Since(t2).Seconds()
	komunikat("gotowe", 1, fmt.Sprintf("Gotowe: %d ujęć (%d stabilizowanych, %d odrzuconych jako prawie takie same) z %d nagrań (%.0f s materiału), rolka %.1f s. Czas: skan %.0f s (%s), opisy ujęć %.0f s (%d nowych), ujęcia %.0f s, składanie %.0f s, razem %.0f s",
		len(wybrane), stabilizowane, podobnych, len(klipy), sumaNagran, float64(len(wybrane))**ujecie, tSkan, skany[0].Sposob, tRozum, opisanych, tUj, tZl, time.Since(t0).Seconds()))
}
