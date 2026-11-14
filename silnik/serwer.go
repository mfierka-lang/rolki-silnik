// Serwer lokalny silnika: interfejs montażu w przeglądarce i polecenia dla strony panelu.
// Słucha wyłącznie na 127.0.0.1, przyjmuje żądania tylko z własnego okna i z adresów panelu, a po kwadransie
// bezczynności sam się wyłącza (nic nie działa w tle na stałe). Każdy montaż to osobny proces (`montaz wykonaj`):
// można go przerwać, a błąd montażu nie wywraca serwera.
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed interfejs
var plikiInterfejsu embed.FS

//go:embed szablon
var szablonWbudowany embed.FS

const portSilnika = 47615

// panelWbudowany: adresy panelu, z których strona może sterować silnikiem (poza własnym oknem silnika), oddzielone
// przecinkami. Puste w kodzie źródłowym; wydanie dostaje je przy budowie: -ldflags "-X main.panelWbudowany=https://...".
var panelWbudowany = ""

func pochodzeniaPanelu() []string {
	var out []string
	for _, p := range strings.Split(panelWbudowany, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type plikWejsciowy struct {
	ID       string
	Nazwa    string
	Sciezka  string
	Rozmiar  int64
	Wrzucony bool // kopia zrobiona przez silnik (do skasowania), a nie plik wskazany na dysku
}

type stanZlecenia struct {
	ID      string
	Stan    string // praca | gotowe | blad
	Tekst   string
	Postep  float64
	Start   time.Time
	Blad    string
	Wynik   *Wynik
	cmd     *exec.Cmd
	katalog string
}

type stanPrzygotowania struct {
	Trwa   bool    `json:"trwa"`
	Postep float64 `json:"postep"`
	Tekst  string  `json:"tekst"`
	Blad   string  `json:"blad"`
}

type Serwer struct {
	mu        sync.Mutex
	dane      string
	styl      string
	pliki     map[string]*plikWejsciowy
	zlecenie  *stanZlecenia
	ostatnio  time.Time
	przyg     stanPrzygotowania
	sprzet    Sprzet
	dodatkowe []string // dodatkowe dozwolone adresy panelu (flaga --panel, do testów)
}

func losowe(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// zapewnijSzablon: przy pierwszym starcie szablon marki (styl, fonty) wypakowuje się z programu do katalogu danych.
// Później nadpisuje go wersja pobrana z panelu, żeby każdy komputer miał ten sam wygląd.
func zapewnijSzablon(dane string) string {
	cel := filepath.Join(dane, "szablon")
	styl := filepath.Join(cel, "styl.json")
	wersjaPlik := filepath.Join(cel, ".wbudowany")
	znak := wersja + "-" + sumaSzablonu()
	if b, err := os.ReadFile(wersjaPlik); err == nil && string(b) == znak {
		if _, err := os.Stat(styl); err == nil {
			return styl
		}
	}
	if _, err := os.Stat(filepath.Join(cel, ".z-panelu")); err == nil {
		if _, err := os.Stat(styl); err == nil {
			return styl // szablon z panelu ma pierwszeństwo przed wbudowanym
		}
	}
	fs.WalkDir(szablonWbudowany, "szablon", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		do := filepath.Join(dane, filepath.FromSlash(p))
		if d.IsDir() {
			os.MkdirAll(do, 0o755)
			return nil
		}
		if b, err := szablonWbudowany.ReadFile(p); err == nil {
			os.WriteFile(do, b, 0o644)
		}
		return nil
	})
	os.WriteFile(wersjaPlik, []byte(znak), 0o644)
	return styl
}

// sumaSzablonu: skrót treści wbudowanego stylu; zmiana szablonu w nowym wydaniu silnika odświeża kopię na dysku.
func sumaSzablonu() string {
	b, _ := szablonWbudowany.ReadFile("szablon/styl.json")
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:6])
}

func (s *Serwer) dozwolonePochodzenie(o string) bool {
	if o == "" {
		return true // własne okno silnika i narzędzia lokalne nie wysyłają tego nagłówka przy GET
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if h := u.Hostname(); (h == "127.0.0.1" || h == "localhost") && u.Port() == fmt.Sprint(portSilnika) {
		return true
	}
	for _, p := range append(pochodzeniaPanelu(), s.dodatkowe...) {
		if o == p {
			return true
		}
		// podglądy wdrożeń w Cloudflare Pages: https://<skrót>.<projekt>.pages.dev
		if pu, err := url.Parse(p); err == nil && u.Scheme == "https" && strings.HasSuffix(pu.Hostname(), ".pages.dev") && strings.HasSuffix(u.Hostname(), "."+pu.Hostname()) {
			return true
		}
	}
	return false
}

func odpowiedzJSON(w http.ResponseWriter, kod int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(kod)
	json.NewEncoder(w).Encode(v)
}

func odpowiedzBlad(w http.ResponseWriter, kod int, tekst string) {
	odpowiedzJSON(w, kod, map[string]string{"blad": tekst})
}

// straznik: tylko połączenia na adres lokalny (ochrona przed podmianą DNS) i tylko z dozwolonych stron.
func (s *Serwer) straznik(dalej http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "zły adres", http.StatusForbidden)
			return
		}
		o := r.Header.Get("Origin")
		if !s.dozwolonePochodzenie(o) {
			http.Error(w, "strona bez dostępu do silnika", http.StatusForbidden)
			return
		}
		if o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.mu.Lock()
		s.ostatnio = time.Now()
		s.mu.Unlock()
		dalej.ServeHTTP(w, r)
	})
}

func (s *Serwer) stan(w http.ResponseWriter, r *http.Request) {
	brak, wymagane := brakujaceSkladniki(s.dane)
	var nazwy []string
	for _, b := range brak {
		nazwy = append(nazwy, b.Opis)
	}
	modele := ""
	if m, _ := znajdzModelLLM(s.dane, "tekst"); m != "" {
		modele = "z modelem"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	odp := map[string]any{"ok": true, "wersja": wersja, "system": runtime.GOOS, "gotowy": !wymagane, "komplet": len(brak) == 0, "brak": nazwy, "modele": modele, "przygotowanie": s.przyg, "sprzet": s.sprzet}
	if z := s.zlecenie; z != nil {
		odp["zadanie"] = map[string]any{"id": z.ID, "stan": z.Stan, "sekundy": time.Since(z.Start).Seconds(), "wynik": z.Wynik}
	}
	odpowiedzJSON(w, 200, odp)
}

func (s *Serwer) zarejestruj(sciezka string, wrzucony bool) *plikWejsciowy {
	fi, err := os.Stat(sciezka)
	if err != nil || fi.IsDir() {
		return nil
	}
	p := &plikWejsciowy{ID: losowe(8), Nazwa: filepath.Base(sciezka), Sciezka: sciezka, Rozmiar: fi.Size(), Wrzucony: wrzucony}
	s.mu.Lock()
	s.pliki[p.ID] = p
	s.mu.Unlock()
	return p
}

func (s *Serwer) wybierz(w http.ResponseWriter, r *http.Request) {
	cmd := oknoWyboruPlikow()
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		odpowiedzJSON(w, 200, map[string]any{"pliki": []any{}}) // anulowane okno to nie błąd
		return
	}
	var lista []map[string]any
	for _, linia := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		if linia = strings.TrimSpace(linia); linia == "" {
			continue
		}
		if p := s.zarejestruj(linia, false); p != nil {
			lista = append(lista, map[string]any{"id": p.ID, "nazwa": p.Nazwa, "rozmiar": p.Rozmiar})
		}
	}
	odpowiedzJSON(w, 200, map[string]any{"pliki": lista})
}

// wrzuc: plik przeciągnięty na stronę. Przeglądarka nie zdradza stronie, gdzie plik leży na dysku, więc silnik
// zapisuje własną kopię w katalogu danych (kasowaną przy następnej rolce).
func (s *Serwer) wrzuc(w http.ResponseWriter, r *http.Request) {
	nazwa := filepath.Base(strings.ReplaceAll(r.URL.Query().Get("nazwa"), "\\", "/"))
	if nazwa == "" || nazwa == "." || strings.HasPrefix(nazwa, ".") {
		odpowiedzBlad(w, 400, "brak nazwy pliku")
		return
	}
	kat := filepath.Join(s.dane, "wrzucone", losowe(6))
	if err := os.MkdirAll(kat, 0o755); err != nil {
		odpowiedzBlad(w, 500, err.Error())
		return
	}
	cel := filepath.Join(kat, nazwa)
	f, err := os.Create(cel)
	if err != nil {
		odpowiedzBlad(w, 500, err.Error())
		return
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, 64<<30))
	f.Close()
	if err != nil {
		os.RemoveAll(kat)
		odpowiedzBlad(w, 400, "przesyłanie przerwane")
		return
	}
	p := s.zarejestruj(cel, true)
	if p == nil {
		odpowiedzBlad(w, 500, "nie udało się zapisać pliku")
		return
	}
	odpowiedzJSON(w, 200, map[string]any{"id": p.ID, "nazwa": p.Nazwa, "rozmiar": p.Rozmiar})
}

func (s *Serwer) montuj(w http.ResponseWriter, r *http.Request) {
	var we struct {
		Pliki      []string `json:"pliki"`
		Opis       string   `json:"opis"`
		Popraw     string   `json:"popraw"`
		Poprzednie string   `json:"poprzednie"`
		Format     string   `json:"format"`
		Dlugosc    string   `json:"dlugosc"`
		Napisy     *bool    `json:"napisy"`
		Przejscia  *bool    `json:"przejscia"`
		Animacje   *bool    `json:"animacje"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&we); err != nil {
		odpowiedzBlad(w, 400, "nieczytelne zlecenie")
		return
	}
	s.mu.Lock()
	if s.zlecenie != nil && s.zlecenie.Stan == "praca" {
		s.mu.Unlock()
		odpowiedzBlad(w, 409, "Silnik właśnie montuje inną rolkę")
		return
	}
	wylaczone := func(v *bool) bool { return v != nil && !*v }
	z := &Zadanie{Opis: strings.TrimSpace(we.Opis), Styl: s.styl, Dane: s.dane, Format: we.Format, Dlugosc: we.Dlugosc,
		BezNapisow: wylaczone(we.Napisy), BezPrzejsc: wylaczone(we.Przejscia), BezAnimacji: wylaczone(we.Animacje)}
	if we.Poprzednie != "" { // poprawka: te same nagrania i opis, do tego kolejna uwaga
		if b, err := os.ReadFile(filepath.Join(s.dane, "zlecenia", filepath.Base(we.Poprzednie), "zadanie.json")); err == nil {
			var stare Zadanie
			if json.Unmarshal(b, &stare) == nil {
				z.Pliki, z.Opis, z.Popraw = stare.Pliki, stare.Opis, stare.Popraw
				z.Format, z.Dlugosc, z.BezNapisow, z.BezPrzejsc, z.BezAnimacji = stare.Format, stare.Dlugosc, stare.BezNapisow, stare.BezPrzejsc, stare.BezAnimacji
			}
		}
		if t := strings.TrimSpace(we.Popraw); t != "" {
			z.Popraw = append(z.Popraw, t)
		}
	} else {
		for _, id := range we.Pliki {
			if p := s.pliki[id]; p != nil {
				z.Pliki = append(z.Pliki, p.Sciezka)
			}
		}
	}
	s.mu.Unlock()
	if len(z.Pliki) == 0 {
		odpowiedzBlad(w, 400, "Dodaj co najmniej jedno nagranie")
		return
	}
	id, err := s.startZlecenia(z)
	if err != nil {
		odpowiedzBlad(w, 500, err.Error())
		return
	}
	odpowiedzJSON(w, 200, map[string]string{"id": id})
}

func (s *Serwer) startZlecenia(z *Zadanie) (string, error) {
	id := time.Now().Format("20060102-150405")
	kat := filepath.Join(s.dane, "zlecenia", id)
	if err := os.MkdirAll(kat, 0o755); err != nil {
		return "", err
	}
	z.Praca = kat
	z.Wyjscie = filepath.Join(kat, "rolka.mp4")
	b, _ := json.MarshalIndent(z, "", " ")
	plikZadania := filepath.Join(kat, "zadanie.json")
	if err := os.WriteFile(plikZadania, b, 0o644); err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(exe, "wykonaj", plikZadania)
	ukryjOkno(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	bledy, _ := os.Create(filepath.Join(kat, "bledy.txt"))
	cmd.Stderr = bledy
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("nie udało się uruchomić montażu: %v", err)
	}
	zl := &stanZlecenia{ID: id, Stan: "praca", Tekst: "Zaczynam", Start: time.Now(), cmd: cmd, katalog: kat}
	s.mu.Lock()
	s.zlecenie = zl
	s.mu.Unlock()
	go func() {
		runtime.LockOSThread() // blokada uśpienia w Windows jest przypisana do wątku
		defer runtime.UnlockOSThread()
		nieUsypiaj(true, cmd.Process.Pid)
		sk := bufio.NewScanner(out)
		sk.Buffer(make([]byte, 1<<16), 1<<20)
		for sk.Scan() {
			var k struct {
				Postep float64 `json:"postep"`
				Tekst  string  `json:"tekst"`
			}
			if json.Unmarshal(sk.Bytes(), &k) == nil && k.Tekst != "" {
				s.mu.Lock()
				zl.Postep, zl.Tekst = k.Postep, k.Tekst
				s.mu.Unlock()
			}
		}
		err := cmd.Wait()
		nieUsypiaj(false, 0)
		if bledy != nil {
			bledy.Close()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if zl.Stan != "praca" { // przerwane przez użytkownika
			return
		}
		wb, errW := os.ReadFile(filepath.Join(kat, "wynik.json"))
		if err == nil && errW == nil {
			wyn := &Wynik{}
			if json.Unmarshal(wb, wyn) == nil {
				zl.Wynik, zl.Stan, zl.Postep = wyn, "gotowe", 1
				return
			}
		}
		zl.Stan = "blad"
		tekst, _ := os.ReadFile(filepath.Join(kat, "bledy.txt"))
		zl.Blad = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ogon(string(tekst), 400)), "BŁĄD:"))
		if zl.Blad == "" {
			zl.Blad = "Montaż zakończył się niespodziewanie."
		}
	}()
	go s.sprzatnijZlecenia(id)
	return id, nil
}

// sprzatnijZlecenia: zostaje 6 ostatnich rolek z plikami roboczymi; starsze katalogi są kasowane.
func (s *Serwer) sprzatnijZlecenia(biezace string) {
	wpisy, _ := os.ReadDir(filepath.Join(s.dane, "zlecenia"))
	var nazwy []string
	for _, w := range wpisy {
		if w.IsDir() && w.Name() != biezace {
			nazwy = append(nazwy, w.Name())
		}
	}
	sort.Strings(nazwy)
	for i := 0; i < len(nazwy)-5; i++ {
		os.RemoveAll(filepath.Join(s.dane, "zlecenia", nazwy[i]))
	}
}

func (s *Serwer) postep(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	z := s.zlecenie
	if z == nil {
		odpowiedzJSON(w, 200, map[string]any{"stan": "brak"})
		return
	}
	odpowiedzJSON(w, 200, map[string]any{"id": z.ID, "stan": z.Stan, "postep": z.Postep, "tekst": z.Tekst, "sekundy": time.Since(z.Start).Seconds(), "blad": z.Blad, "wynik": z.Wynik})
}

func (s *Serwer) przerwij(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if z := s.zlecenie; z != nil && z.Stan == "praca" {
		z.Stan, z.Blad = "blad", "Przerwane"
		if z.cmd != nil && z.cmd.Process != nil {
			zabijDrzewo(z.cmd.Process.Pid)
		}
	}
	s.mu.Unlock()
	odpowiedzJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Serwer) rolka(w http.ResponseWriter, r *http.Request) {
	reszta := strings.TrimPrefix(r.URL.Path, "/api/rolka/")
	pobierz := strings.HasSuffix(reszta, "/pobierz")
	id := filepath.Base(strings.TrimSuffix(strings.TrimSuffix(reszta, "/pobierz"), ".mp4"))
	plik := filepath.Join(s.dane, "zlecenia", id, "rolka.mp4")
	f, err := os.Open(plik)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	if pobierz {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Zblizka-rolka-%s.mp4"`, id))
	}
	http.ServeContent(w, r, "rolka.mp4", fi.ModTime(), f)
}

func (s *Serwer) pokaz(w http.ResponseWriter, r *http.Request) {
	var we struct {
		ID string `json:"id"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&we)
	plik := filepath.Join(s.dane, "zlecenia", filepath.Base(we.ID), "rolka.mp4")
	if _, err := os.Stat(plik); err != nil {
		odpowiedzBlad(w, 404, "nie ma takiej rolki")
		return
	}
	pokazWFolderze(plik)
	odpowiedzJSON(w, 200, map[string]bool{"ok": true})
}

// nowa: początek pracy nad kolejną rolką; kopie nagrań przeciągniętych na stronę nie są już potrzebne.
func (s *Serwer) nowa(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.zlecenie == nil || s.zlecenie.Stan != "praca" {
		s.pliki = map[string]*plikWejsciowy{}
		s.zlecenie = nil
		go os.RemoveAll(filepath.Join(s.dane, "wrzucone"))
	}
	s.mu.Unlock()
	odpowiedzJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Serwer) przygotuj(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.przyg.Trwa {
		s.mu.Unlock()
		odpowiedzJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	s.przyg = stanPrzygotowania{Trwa: true, Tekst: "Zaczynam pobieranie"}
	s.mu.Unlock()
	go func() {
		err := przygotujNarzedzia(s.dane, func(p float64, tekst string) {
			s.mu.Lock()
			s.przyg.Postep, s.przyg.Tekst = p, tekst
			s.ostatnio = time.Now() // długie pobieranie to nie bezczynność
			s.mu.Unlock()
		})
		s.mu.Lock()
		s.przyg.Trwa = false
		if err != nil {
			s.przyg.Blad = err.Error()
		} else {
			s.przyg.Postep, s.przyg.Tekst, s.przyg.Blad = 1, "Gotowe", ""
		}
		s.mu.Unlock()
	}()
	odpowiedzJSON(w, 200, map[string]bool{"ok": true})
}

func polecenieSerwer(arg []string) {
	fs2 := flag.NewFlagSet("serwer", flag.ExitOnError)
	dane := fs2.String("dane", "", "katalog danych silnika")
	stylPlik := fs2.String("styl", "", "szablon stylu (domyślnie z katalogu danych)")
	okno := fs2.Bool("okno", true, "otwórz interfejs w przeglądarce")
	panel := fs2.String("panel", "", "dodatkowy dozwolony adres panelu (do testów), np. http://localhost:8789")
	fs2.Parse(arg)
	uruchomSerwer(katalogDanych(*dane), *stylPlik, *okno, *panel)
}

func uruchomSerwer(dane, styl string, okno bool, panel string) {
	if abs, err := filepath.Abs(dane); err == nil {
		dane = abs
	}
	os.MkdirAll(dane, 0o755)
	adres := fmt.Sprintf("127.0.0.1:%d", portSilnika)
	l, err := net.Listen("tcp", adres)
	if err != nil {
		// silnik już działa (np. drugi klik w skrót): pokazujemy jego okno i kończymy
		if okno {
			otworzAdres("http://" + adres + "/")
		}
		return
	}
	if styl == "" {
		styl = zapewnijSzablon(dane)
	} else if abs, err := filepath.Abs(styl); err == nil {
		styl = abs
	}
	s := &Serwer{dane: dane, styl: styl, pliki: map[string]*plikWejsciowy{}, ostatnio: time.Now()}
	s.sprzet = odczytajSprzet(dane)
	if panel != "" {
		s.dodatkowe = strings.Split(panel, ",")
	}
	mux := http.NewServeMux()
	tylko := func(metoda string, h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != metoda {
				odpowiedzBlad(w, 405, "zła metoda")
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/stan", tylko("GET", s.stan))
	mux.HandleFunc("/api/wybierz", tylko("POST", s.wybierz))
	mux.HandleFunc("/api/wrzuc", tylko("POST", s.wrzuc))
	mux.HandleFunc("/api/montuj", tylko("POST", s.montuj))
	mux.HandleFunc("/api/postep", tylko("GET", s.postep))
	mux.HandleFunc("/api/przerwij", tylko("POST", s.przerwij))
	mux.HandleFunc("/api/rolka/", tylko("GET", s.rolka))
	mux.HandleFunc("/api/pokaz", tylko("POST", s.pokaz))
	mux.HandleFunc("/api/nowa", tylko("POST", s.nowa))
	mux.HandleFunc("/api/przygotuj", tylko("POST", s.przygotuj))
	mux.HandleFunc("/api/zamknij", tylko("POST", func(w http.ResponseWriter, r *http.Request) {
		odpowiedzJSON(w, 200, map[string]bool{"ok": true})
		go func() { time.Sleep(300 * time.Millisecond); os.Exit(0) }()
	}))
	ui, _ := fs.Sub(plikiInterfejsu, "interfejs")
	pliki := http.FileServer(http.FS(ui))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Cache-Control", "no-cache")
		pliki.ServeHTTP(w, r)
	}))
	// bezczynność: bez żądań i bez pracy przez kwadrans silnik kończy działanie
	go func() {
		for range time.Tick(30 * time.Second) {
			s.mu.Lock()
			zajety := (s.zlecenie != nil && s.zlecenie.Stan == "praca") || s.przyg.Trwa
			cisza := time.Since(s.ostatnio)
			s.mu.Unlock()
			if !zajety && cisza > 15*time.Minute {
				os.Exit(0)
			}
		}
	}()
	if okno {
		go func() { time.Sleep(300 * time.Millisecond); otworzAdres("http://" + adres + "/") }()
	}
	fmt.Println("Silnik montażu Zbliżki", wersja, "działa pod adresem http://"+adres+"/")
	srv := &http.Server{Handler: s.straznik(mux), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(l); err != nil {
		blad(err)
	}
}
