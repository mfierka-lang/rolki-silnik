// Synchronizacja z panelem: biblioteka muzyki i nowe wersje silnika.
// Strona panelu (po zalogowaniu) dostaje od panelu krótki żeton i przekazuje go silnikowi; silnik pobiera pliki
// wyłącznie z adresu panelu, z którego przyszło polecenie (ten sam, który wolno mu słuchać), i nie podąża za
// przekierowaniami, więc żeton nie wypłynie pod obcy adres. Nowa wersja programu musi mieć podpis Ed25519 złożony
// kluczem wydań: bez niego silnik jej nie uruchomi, nawet gdyby ktoś podmienił pliki w panelu.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// kluczWydan: klucz publiczny Ed25519 (base64) do sprawdzania podpisu nowych wersji silnika. Pusty w kodzie
// źródłowym (wersje robocze nie aktualizują się same); wydanie dostaje go przy budowie: -X main.kluczWydan=...
var kluczWydan = ""

type stanSynchro struct {
	Trwa   bool    `json:"trwa"`
	Tekst  string  `json:"tekst"`
	Postep float64 `json:"postep"`
	Blad   string  `json:"blad"`
	Nowa   string  `json:"nowa"`  // wersja silnika pobrana i czekająca na uruchomienie
	Kiedy  int64   `json:"kiedy"` // czas ostatniej udanej synchronizacji
}

type plikPanelu struct {
	P string `json:"p"` // miejsce w bibliotece: "Styl/Utwór.mp3"
	U string `json:"u"` // adres w panelu względem /silnik/
	R int64  `json:"r"` // rozmiar w bajtach
	S string `json:"s"` // sha256
}

type programPanelu struct {
	U      string `json:"u"`
	R      int64  `json:"r"`
	S      string `json:"s"`
	Podpis string `json:"podpis"`
}

type manifestPanelu struct {
	Wersja   string                   `json:"wersja"`
	Programy map[string]programPanelu `json:"programy"`
	Muzyka   []plikPanelu             `json:"muzyka"`
}

// zapisProgramu: opis pobranej nowej wersji silnika (katalog danych, program/wersja.json).
type zapisProgramu struct {
	Wersja string `json:"wersja"`
	Plik   string `json:"plik"`
	Sha256 string `json:"sha256"`
	Podpis string `json:"podpis"`
}

// nowszaWersja: czy wersja a (np. "0.5.1") jest nowsza od b.
func nowszaWersja(a, b string) bool {
	liczby := func(s string) [3]int {
		var w [3]int
		for i, cz := range strings.SplitN(strings.TrimSpace(s), ".", 3) {
			w[i], _ = strconv.Atoi(strings.TrimFunc(cz, func(r rune) bool { return r < '0' || r > '9' }))
		}
		return w
	}
	if strings.TrimSpace(a) == "" {
		return false
	}
	x, y := liczby(a), liczby(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// podpisProgramuOk: podpis obejmuje wersję, system i skrót pliku, więc starego programu nie da się podać jako nowego.
func podpisProgramuOk(wer, suma, podpis string) bool {
	klucz, err := base64.StdEncoding.DecodeString(kluczWydan)
	if err != nil || len(klucz) != ed25519.PublicKeySize {
		return false
	}
	p, err := base64.StdEncoding.DecodeString(podpis)
	if err != nil || len(p) != ed25519.SignatureSize {
		return false
	}
	tresc := "zblizka-montaz|" + wer + "|" + runtime.GOOS + "-" + runtime.GOARCH + "|" + suma
	return ed25519.Verify(ed25519.PublicKey(klucz), []byte(tresc), p)
}

// bladTresci: pobrany plik nie zgadza się ze spisem (rozmiar albo skrót).
type bladTresci string

func (b bladTresci) Error() string { return string(b) }

var reAdresPliku = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

func dobryAdresPliku(u string) bool {
	return len(u) < 200 && reAdresPliku.MatchString(u) && !strings.Contains(u, "..")
}

// dobraSciezkaMuzyki: "Utwór.mp3" albo "Styl/Utwór.mp3", bez wychodzenia z katalogu i bez znaków zakazanych w nazwach.
func dobraSciezkaMuzyki(p string) bool {
	if p == "" || len(p) > 240 {
		return false
	}
	czesci := strings.Split(p, "/")
	if len(czesci) > 2 {
		return false
	}
	for _, c := range czesci {
		if c == "" || c[0] == '.' || c[0] == ' ' || strings.ContainsAny(c, "\\:*?\"<>|") || strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ") {
			return false
		}
		for _, r := range c {
			if r < 32 {
				return false
			}
		}
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mp3", ".m4a", ".wav":
		return true
	}
	return false
}

// adresPanelu: czy spod tego adresu wolno pobierać pliki (adresy panelu wbudowane w wydanie, podglądy wdrożeń
// tego samego projektu i adres testowy z flagi --panel).
func (s *Serwer) adresPanelu(o string) bool {
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return false
	}
	for _, p := range s.dodatkowe {
		if o == p {
			return true
		}
	}
	if u.Scheme != "https" {
		return false
	}
	for _, p := range pochodzeniaPanelu() {
		if o == p {
			return true
		}
		if pu, err := url.Parse(p); err == nil && strings.HasSuffix(pu.Hostname(), ".pages.dev") && strings.HasSuffix(u.Hostname(), "."+pu.Hostname()) {
			return true
		}
	}
	return false
}

// synchronizuj: polecenie ze strony panelu. Adres panelu bierzemy z nagłówka Origin (ustawia go przeglądarka,
// strona nie może go podrobić), żeton z treści.
func (s *Serwer) synchronizuj(w http.ResponseWriter, r *http.Request) {
	var we struct {
		Zeton string `json:"zeton"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&we); err != nil || we.Zeton == "" || len(we.Zeton) > 300 {
		odpowiedzBlad(w, 400, "brak żetonu")
		return
	}
	panel := r.Header.Get("Origin")
	if !s.adresPanelu(panel) {
		odpowiedzBlad(w, 403, "synchronizacja tylko ze strony panelu")
		return
	}
	s.mu.Lock()
	if s.synchro.Trwa {
		s.mu.Unlock()
		odpowiedzJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	s.synchro = stanSynchro{Trwa: true, Tekst: "Sprawdzam nowości w panelu", Nowa: s.synchro.Nowa, Kiedy: s.synchro.Kiedy}
	s.mu.Unlock()
	go s.synchronizujZPanelem(panel, we.Zeton)
	odpowiedzJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Serwer) synchronizujZPanelem(panel, zeton string) {
	ustaw := func(p float64, tekst string) {
		s.mu.Lock()
		s.synchro.Postep, s.synchro.Tekst = p, tekst
		s.mu.Unlock()
	}
	klient := &http.Client{
		Transport:     &http.Transport{ResponseHeaderTimeout: 40 * time.Second, Proxy: http.ProxyFromEnvironment},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// pobierz: plik z panelu do katalogu docelowego, ze sprawdzeniem rozmiaru i skrótu (do 4 prób)
	pobierz := func(adres, cel string, rozmiar int64, suma string) error {
		if !dobryAdresPliku(adres) {
			return fmt.Errorf("zły adres pliku w panelu")
		}
		var ostatni error
		for proba := 0; proba < 4; proba++ {
			if proba > 0 {
				time.Sleep(time.Duration(proba) * 2 * time.Second)
			}
			ostatni = func() error {
				ctx, anuluj := context.WithTimeout(context.Background(), 20*time.Minute)
				defer anuluj()
				rq, _ := http.NewRequestWithContext(ctx, "GET", panel+"/silnik/"+adres, nil)
				rq.Header.Set("Authorization", "Bearer "+zeton)
				rq.Header.Set("User-Agent", "zblizka-montaz/"+wersja)
				odp, err := klient.Do(rq)
				if err != nil {
					return err
				}
				defer odp.Body.Close()
				if odp.StatusCode != 200 {
					return fmt.Errorf("panel odpowiedział %s", odp.Status)
				}
				os.MkdirAll(filepath.Dir(cel), 0o755)
				czesc := cel + ".czesc"
				f, err := os.Create(czesc)
				if err != nil {
					return err
				}
				h := sha256.New()
				n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(odp.Body, rozmiar+1))
				f.Close()
				if err == nil && n != rozmiar {
					err = bladTresci("plik ma inny rozmiar niż w spisie")
				}
				if err == nil && hex.EncodeToString(h.Sum(nil)) != suma {
					err = bladTresci("plik ma inny skrót niż w spisie")
				}
				if err != nil {
					os.Remove(czesc)
					return err
				}
				os.Remove(cel)
				return os.Rename(czesc, cel)
			}()
			if ostatni == nil {
				return nil
			}
			if _, zla := ostatni.(bladTresci); zla {
				break // treść inna niż w spisie: ponawianie nic nie da
			}
		}
		return ostatni
	}
	koniec := func(err error) {
		s.mu.Lock()
		s.synchro.Trwa, s.synchro.Postep = false, 1
		if err != nil {
			s.synchro.Blad = err.Error()
		} else {
			s.synchro.Blad, s.synchro.Tekst, s.synchro.Kiedy = "", "", time.Now().Unix()
		}
		s.mu.Unlock()
	}

	// spis plików panelu
	var m manifestPanelu
	err := func() error {
		ctx, anuluj := context.WithTimeout(context.Background(), time.Minute)
		defer anuluj()
		rq, _ := http.NewRequestWithContext(ctx, "GET", panel+"/silnik/stan.json", nil)
		rq.Header.Set("Authorization", "Bearer "+zeton)
		rq.Header.Set("User-Agent", "zblizka-montaz/"+wersja)
		odp, err := klient.Do(rq)
		if err != nil {
			return err
		}
		defer odp.Body.Close()
		if odp.StatusCode != 200 {
			return fmt.Errorf("panel odpowiedział %s", odp.Status)
		}
		return json.NewDecoder(io.LimitReader(odp.Body, 8<<20)).Decode(&m)
	}()
	if err != nil {
		koniec(fmt.Errorf("nie udało się pobrać spisu z panelu: %v", err))
		return
	}
	// najpierw nowa wersja silnika (mały plik, może naprawiać błąd), potem muzyka; po przełączeniu wersji strona
	// zleca synchronizację jeszcze raz i muzykę dociąga już nowy silnik
	bladProgramu := s.pobierzNowyProgram(m, pobierz, ustaw)
	if bladProgramu == nil {
		s.przejdzNaNowaWersje()
	}
	if err := s.synchronizujMuzyke(m.Muzyka, pobierz, ustaw); err != nil {
		koniec(err)
		return
	}
	koniec(bladProgramu)
	// rytm nowych utworów liczymy od razu, żeby pierwszy montaż nie czekał (wyniki zostają w pamięci podręcznej)
	if n, _ := znajdzNarzedzia(s.dane); n != nil {
		for _, p := range bibliotekaMuzyki(n) {
			s.mu.Lock()
			zajety := s.zlecenie != nil && s.zlecenie.Stan == "praca"
			s.mu.Unlock()
			if zajety {
				break // montaż ma pierwszeństwo do procesora
			}
			analizujUtwor(n, p)
		}
	}
}

// synchronizujMuzyke: biblioteka w katalogu danych ma zawierać to, co jest w panelu. Utwory dodane ręcznie
// (których nie ma w spisie pobranych z panelu) zostają nietknięte.
func (s *Serwer) synchronizujMuzyke(lista []plikPanelu, pobierz func(adres, cel string, rozmiar int64, suma string) error, ustaw func(float64, string)) error {
	kat := filepath.Join(s.dane, "muzyka")
	indeks := filepath.Join(kat, ".z-panelu.json")
	var stare []string
	if b, err := os.ReadFile(indeks); err == nil {
		json.Unmarshal(b, &stare)
	}
	if len(lista) == 0 && len(stare) == 0 {
		return nil
	}
	wSpisie := map[string]bool{}
	var brak []plikPanelu
	for _, p := range lista {
		if !dobraSciezkaMuzyki(p.P) || p.R <= 0 || p.R > 80<<20 || len(p.S) != 64 {
			continue
		}
		wSpisie[p.P] = true
		if fi, err := os.Stat(filepath.Join(kat, filepath.FromSlash(p.P))); err == nil && fi.Size() == p.R {
			continue
		}
		brak = append(brak, p)
	}
	zapiszIndeks := func() {
		var nazwy []string
		for p := range wSpisie {
			if _, err := os.Stat(filepath.Join(kat, filepath.FromSlash(p))); err == nil {
				nazwy = append(nazwy, p)
			}
		}
		sort.Strings(nazwy)
		os.MkdirAll(kat, 0o755)
		b, _ := json.Marshal(nazwy)
		os.WriteFile(indeks, b, 0o644)
	}
	// jeden nieudany plik nie zatrzymuje reszty; błąd wraca na końcu, a brakujący utwór dociągnie się następnym razem
	var pierwszyBlad error
	nieudane := 0
	for i, p := range brak {
		ustaw(float64(i)/float64(len(brak)), fmt.Sprintf("Pobieram muzykę: %d z %d", i+1, len(brak)))
		if err := pobierz(p.U, filepath.Join(kat, filepath.FromSlash(p.P)), p.R, p.S); err != nil {
			nieudane++
			if pierwszyBlad == nil {
				pierwszyBlad = err
			}
			if nieudane >= 3 && nieudane == i+1 {
				break // panel nie odpowiada: nie ma sensu próbować kolejnych
			}
		}
	}
	// utwory wycofane z panelu znikają też tutaj (tylko te, które przyszły z panelu)
	for _, st := range stare {
		if !wSpisie[st] && dobraSciezkaMuzyki(st) {
			plik := filepath.Join(kat, filepath.FromSlash(st))
			os.Remove(plik)
			os.Remove(filepath.Dir(plik)) // pusty folder stylu (niepusty zostaje: Remove nie kasuje z zawartością)
		}
	}
	zapiszIndeks()
	if pierwszyBlad != nil {
		return fmt.Errorf("nie udało się pobrać %d z %d utworów: %v", nieudane, len(brak), pierwszyBlad)
	}
	return nil
}

// pobierzNowyProgram: nowsza wersja silnika trafia do katalogu danych (program/), po sprawdzeniu podpisu.
func (s *Serwer) pobierzNowyProgram(m manifestPanelu, pobierz func(adres, cel string, rozmiar int64, suma string) error, ustaw func(float64, string)) error {
	if kluczWydan == "" || !nowszaWersja(m.Wersja, wersja) {
		return nil
	}
	p, jest := m.Programy[runtime.GOOS+"-"+runtime.GOARCH]
	if !jest || p.R <= 0 || p.R > 200<<20 || len(p.S) != 64 {
		return nil
	}
	if !podpisProgramuOk(m.Wersja, p.S, p.Podpis) {
		return fmt.Errorf("nowa wersja silnika w panelu ma zły podpis: nie instaluję jej")
	}
	kat := filepath.Join(s.dane, "program")
	if z, plik := pobranyProgram(s.dane); z != nil && z.Wersja == m.Wersja && plik != "" {
		s.mu.Lock()
		s.synchro.Nowa = m.Wersja
		s.mu.Unlock()
		return nil
	}
	ustaw(0.02, "Pobieram nową wersję silnika "+m.Wersja)
	nazwa := exeNazwa("montaz-" + m.Wersja)
	cel := filepath.Join(kat, nazwa)
	if err := pobierz(p.U, cel, p.R, p.S); err != nil {
		return fmt.Errorf("nie udało się pobrać nowej wersji silnika: %v", err)
	}
	os.Chmod(cel, 0o755)
	b, _ := json.Marshal(zapisProgramu{Wersja: m.Wersja, Plik: nazwa, Sha256: p.S, Podpis: p.Podpis})
	if err := os.WriteFile(filepath.Join(kat, "wersja.json"), b, 0o644); err != nil {
		return err
	}
	// starsze pobrane wersje nie są już potrzebne (plik właśnie działającej wersji system może nie dać skasować)
	if wpisy, err := os.ReadDir(kat); err == nil {
		for _, w := range wpisy {
			if strings.HasPrefix(w.Name(), "montaz-") && w.Name() != nazwa {
				os.Remove(filepath.Join(kat, w.Name()))
			}
		}
	}
	s.mu.Lock()
	s.synchro.Nowa = m.Wersja
	s.mu.Unlock()
	return nil
}

// pobranyProgram: sprawdzona (skrót i podpis) nowsza wersja silnika czekająca w katalogu danych, jeśli jest.
func pobranyProgram(dane string) (*zapisProgramu, string) {
	if kluczWydan == "" {
		return nil, ""
	}
	kat := filepath.Join(dane, "program")
	b, err := os.ReadFile(filepath.Join(kat, "wersja.json"))
	if err != nil {
		return nil, ""
	}
	var z zapisProgramu
	if json.Unmarshal(b, &z) != nil || z.Plik == "" || z.Plik != filepath.Base(z.Plik) {
		return nil, ""
	}
	if !nowszaWersja(z.Wersja, wersja) {
		if ja, err := os.Executable(); err != nil || filepath.Dir(ja) != kat {
			os.RemoveAll(kat) // ten program jest już tą wersją albo nowszą: pobrany plik jest zbędny
		}
		return nil, ""
	}
	plik := filepath.Join(kat, z.Plik)
	suma, err := sumaPliku(plik)
	if err != nil || suma != z.Sha256 || !podpisProgramuOk(z.Wersja, z.Sha256, z.Podpis) {
		return nil, ""
	}
	return &z, plik
}

// przekazNowszej: gdy w katalogu danych czeka nowsza, podpisana wersja silnika, uruchamia ją zamiast tego programu
// (macOS: w miejscu bieżącego procesu, Windows: jako nowy proces). Zwraca true, gdy ten proces ma zakończyć pracę.
func przekazNowszej(dane string, arg []string) bool {
	if os.Getenv("ZM_PRZEKAZANO") != "" {
		return false
	}
	_, plik := pobranyProgram(dane)
	if plik == "" {
		return false
	}
	return uruchomZamiast(plik, arg)
}

func uruchomZamiast(plik string, arg []string) bool {
	srodowisko := append(os.Environ(), "ZM_PRZEKAZANO=1")
	if runtime.GOOS != "windows" {
		// po udanym wywołaniu ten proces przestaje istnieć (zastępuje go nowy program)
		return zastapProces(plik, append([]string{plik}, arg...), srodowisko) == nil
	}
	cmd := exec.Command(plik, arg...)
	cmd.Env = srodowisko
	ukryjOkno(cmd)
	return cmd.Start() == nil
}

// przejdzNaNowaWersje: od razu po pobraniu, ale tylko gdy nikt jeszcze nie zaczął pracy (bez dodanych nagrań
// i bez zleceń). W innym wypadku nowa wersja włączy się przy następnym uruchomieniu silnika.
func (s *Serwer) przejdzNaNowaWersje() {
	_, plik := pobranyProgram(s.dane)
	if plik == "" {
		return
	}
	// wolny: bez zlecenia, bez instalacji dodatków, bez dodanych i właśnie dodawanych nagrań
	wolny := func() bool { return s.zlecenie == nil && !s.przyg.Trwa && len(s.pliki) == 0 && s.dodawane == 0 }
	s.mu.Lock()
	teraz := wolny()
	if teraz {
		s.synchro.Tekst = "Włączam nową wersję silnika"
	}
	s.mu.Unlock()
	if !teraz {
		return
	}
	time.Sleep(1200 * time.Millisecond) // strona zdąży odczytać komunikat
	// przez tę chwilę ktoś mógł dodać nagranie: wtedy nowa wersja poczeka do następnego uruchomienia
	s.mu.Lock()
	teraz = wolny()
	if !teraz {
		s.synchro.Tekst = ""
	}
	s.mu.Unlock()
	if !teraz {
		return
	}
	// Windows: nowy proces czeka na zwolnienie portu, który ten proces oddaje, kończąc pracę.
	// macOS: nowy program zastępuje ten proces, a nasłuch zamyka się sam w chwili zastąpienia.
	if uruchomZamiast(plik, s.argRestartu) {
		os.Exit(0)
	}
	// nie udało się: dalej pracuje stara wersja
}

// utrwalNowaWersje (Windows): wersja uruchomiona z katalogu program/ zastępuje zainstalowaną kopię, do której
// prowadzi adres zblizka-montaz://, żeby kolejne uruchomienia startowały od razu z nowej.
func utrwalNowaWersje(dane string) {
	if runtime.GOOS != "windows" || os.Getenv("ZM_PRZEKAZANO") == "" {
		return
	}
	ja, err := os.Executable()
	if err != nil || filepath.Dir(ja) != filepath.Join(dane, "program") {
		return
	}
	cel := filepath.Join(dane, exeNazwa("ZblizkaMontaz"))
	go func() {
		for proba := 0; proba < 8; proba++ {
			time.Sleep(1500 * time.Millisecond)
			if kopiujDuzy(ja, cel+".nowy") == nil {
				os.Remove(cel)
				if os.Rename(cel+".nowy", cel) == nil {
					return
				}
			}
		}
		os.Remove(cel + ".nowy")
	}()
}
