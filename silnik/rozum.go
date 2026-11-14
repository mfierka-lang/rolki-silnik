// „Rozum” silnika: mały model językowy z obsługą obrazu, uruchamiany lokalnie przez llama.cpp (llama-server).
// Startuje tylko na czas jednego montażu, słucha wyłącznie na 127.0.0.1 z losowym kluczem i jest zamykany po pracy.
// Służy do rzeczy, których nie da się zrobić regułami: opis tego, co widać na ujęciu, temat wypowiedzi,
// nagłówek, opis posta. Każda odpowiedź jest wymuszona schematem JSON, więc model nie może odpowiedzieć „po swojemu”.
// Gdy modelu nie ma (albo nie wystartuje), silnik pracuje dalej na samych regułach.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Rozum struct {
	cmd      *exec.Cmd
	adres    string
	klucz    string
	MaObraz  bool
	Model    string
	rozmiar  int64
	dziennik *os.File
}

// znajdzModelLLM: największy pasujący model z katalogu modeli (większy = lepszy; instalator dobiera wielkość do sprzętu)
// i jego projektor obrazu (plik mmproj o tej samej nazwie bazowej).
//
// Dwie role (sprawdzone 14.11.2026 na tych samych klatkach i tekstach):
//   - "obraz": do oglądania ujęć najlepiej wypada mały model z rodziny VL (Qwen3-VL): czyta napisy, widzi szczegóły;
//   - "tekst": do pisania po polsku największy model, jaki jest na dysku (Gemma 4 pisze poprawnie, mały model nie).
//
// Gdy na komputerze jest tylko jeden model, pełni obie role.
func znajdzModelLLM(dane, rola string) (model, mmproj string) {
	if v := os.Getenv("ZM_LLM"); v != "" {
		return v, os.Getenv("ZM_LLM_MMPROJ")
	}
	pliki, _ := filepath.Glob(filepath.Join(dane, "modele", "*.gguf"))
	type kand struct {
		plik    string
		rozmiar int64
	}
	var modele []kand
	var projektory []string
	for _, p := range pliki {
		nazwa := strings.ToLower(filepath.Base(p))
		if strings.HasPrefix(nazwa, "mmproj") {
			projektory = append(projektory, p)
			continue
		}
		if fi, err := os.Stat(p); err == nil {
			modele = append(modele, kand{p, fi.Size()})
		}
	}
	if len(modele) == 0 {
		return "", ""
	}
	sort.Slice(modele, func(a, b int) bool { return modele[a].rozmiar > modele[b].rozmiar })
	// projektor: najdłuższy wspólny początek nazwy (mmproj-Qwen3VL-2B-... do Qwen3VL-2B-...)
	projektor := func(m string) string {
		baza := strings.ToLower(filepath.Base(m))
		naj, wynik := 0, ""
		for _, p := range projektory {
			nazwa := strings.TrimPrefix(strings.ToLower(filepath.Base(p)), "mmproj-")
			i := 0
			for i < len(nazwa) && i < len(baza) && nazwa[i] == baza[i] {
				i++
			}
			if i > naj && i >= 6 {
				naj, wynik = i, p
			}
		}
		return wynik
	}
	if rola == "obraz" {
		// najpierw model z rodziny VL, potem największy, który w ogóle ma projektor obrazu
		for _, m := range modele {
			if nazwa := strings.ToLower(filepath.Base(m.plik)); strings.Contains(nazwa, "vl") && projektor(m.plik) != "" {
				return m.plik, projektor(m.plik)
			}
		}
		for _, m := range modele {
			if projektor(m.plik) != "" {
				return m.plik, projektor(m.plik)
			}
		}
		return "", ""
	}
	return modele[0].plik, ""
}

// DobryWTekscie: czy model nadaje się do pisania tekstów, które zobaczy klient (opis posta). Małe modele mylą się
// w polskiej pisowni, więc poniżej ok. 3 GB teksty biorą się z szablonu marki.
func (r *Rozum) DobryWTekscie() bool { return r != nil && r.rozmiar >= 3<<30 }

func wolnyPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// uruchomRozum: zwraca nil bez błędu, gdy na komputerze nie ma programu albo modelu (praca na regułach).
//
// Program: najpierw wersja z kartą graficzną (Windows: Vulkan, na zintegrowanej grafice Intela 3 razy szybciej
// niż sam procesor; macOS: Metal jest w zwykłej wersji), a gdy nie wystartuje, wersja na sam procesor.
func uruchomRozum(n *Narzedzia, praca, rola string) (*Rozum, error) {
	model, mmproj := znajdzModelLLM(n.Dane, rola)
	if model == "" {
		return nil, nil
	}
	var serwery []string
	for _, kat := range []string{"llama-vulkan", "llama", "bin"} {
		if p := filepath.Join(n.Dane, kat, exeNazwa("llama-server")); os.Getenv("ZM_LLAMA") == "" {
			if _, err := os.Stat(p); err == nil {
				serwery = append(serwery, p)
			}
		}
	}
	if v := os.Getenv("ZM_LLAMA"); v != "" {
		serwery = []string{v}
	}
	if len(serwery) == 0 {
		return nil, nil
	}
	var ostatni error
	for _, serwer := range serwery {
		r, err := startRozumu(n, praca, rola, serwer, model, mmproj)
		if err == nil {
			return r, nil
		}
		ostatni = err
	}
	return nil, ostatni
}

func startRozumu(n *Narzedzia, praca, rola, serwer, model, mmproj string) (*Rozum, error) {
	port, err := wolnyPort()
	if err != nil {
		return nil, err
	}
	kl := make([]byte, 16)
	rand.Read(kl)
	r := &Rozum{adres: fmt.Sprintf("http://127.0.0.1:%d", port), klucz: hex.EncodeToString(kl), MaObraz: mmproj != "", Model: filepath.Base(model)}
	if fi, err := os.Stat(model); err == nil {
		r.rozmiar = fi.Size()
	}
	watki := runtime.NumCPU() - 2
	if watki < 2 {
		watki = 2
	}
	if watki > 8 {
		watki = 8
	}
	arg := []string{"-m", model, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--api-key", r.klucz,
		"-c", "8192", "-t", fmt.Sprint(watki), "--no-webui", "-np", "1", "--reasoning", "off"}
	if mmproj != "" {
		arg = append(arg, "--mmproj", mmproj)
	}
	if runtime.GOOS == "darwin" || strings.Contains(filepath.Base(filepath.Dir(serwer)), "vulkan") {
		arg = append(arg, "-ngl", "99")
	}
	r.cmd = exec.Command(serwer, arg...)
	r.cmd.Dir = filepath.Dir(serwer)
	ukryjOkno(r.cmd)
	if praca != "" {
		if f, err := os.OpenFile(filepath.Join(praca, "rozum-"+rola+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			r.dziennik = f
			r.cmd.Stdout, r.cmd.Stderr = f, f
		}
	}
	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("model językowy nie wystartował: %v", err)
	}
	koniec := make(chan error, 1)
	go func() { koniec <- r.cmd.Wait() }()
	// wczytanie modelu: zwykle kilka sekund, na wolnym dysku do minuty
	for start := time.Now(); time.Since(start) < 3*time.Minute; {
		select {
		case err := <-koniec:
			r.cmd = nil
			return nil, fmt.Errorf("model językowy zakończył pracę przy starcie: %v", err)
		case <-time.After(400 * time.Millisecond):
		}
		rq, _ := http.NewRequest("GET", r.adres+"/health", nil)
		odp, err := http.DefaultClient.Do(rq)
		if err == nil {
			odp.Body.Close()
			if odp.StatusCode == 200 {
				return r, nil
			}
		}
	}
	r.Zamknij()
	return nil, fmt.Errorf("model językowy nie odpowiedział w ciągu 3 minut")
}

func (r *Rozum) Zamknij() {
	if r == nil {
		return
	}
	if r.cmd != nil && r.cmd.Process != nil {
		r.cmd.Process.Kill()
	}
	if r.dziennik != nil {
		r.dziennik.Close()
	}
}

// pytaj: jedno pytanie do modelu. obrazy = pliki JPEG; schemat = schemat JSON odpowiedzi; wynik = wskaźnik na strukturę.
func (r *Rozum) pytaj(system, tekst string, obrazy [][]byte, schemat map[string]any, maksTokenow int, wynik any) error {
	var tresc []map[string]any
	for _, o := range obrazy {
		tresc = append(tresc, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(o)}})
	}
	tresc = append(tresc, map[string]any{"type": "text", "text": tekst})
	var rozmowa []map[string]any
	if system != "" {
		rozmowa = append(rozmowa, map[string]any{"role": "system", "content": system})
	}
	rozmowa = append(rozmowa, map[string]any{"role": "user", "content": tresc})
	cialo, _ := json.Marshal(map[string]any{
		"messages": rozmowa, "temperature": 0.2, "max_tokens": maksTokenow, "cache_prompt": false,
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "odpowiedz", "strict": true, "schema": schemat}},
	})
	ctx, stop := context.WithTimeout(context.Background(), 6*time.Minute)
	defer stop()
	rq, _ := http.NewRequestWithContext(ctx, "POST", r.adres+"/v1/chat/completions", bytes.NewReader(cialo))
	rq.Header.Set("Content-Type", "application/json")
	rq.Header.Set("Authorization", "Bearer "+r.klucz)
	odp, err := http.DefaultClient.Do(rq)
	if err != nil {
		return err
	}
	defer odp.Body.Close()
	b, _ := io.ReadAll(odp.Body)
	if odp.StatusCode != 200 {
		return fmt.Errorf("model językowy: %s", ogon(string(b), 300))
	}
	var o struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &o); err != nil || len(o.Choices) == 0 {
		return fmt.Errorf("model językowy: nieczytelna odpowiedź")
	}
	if err := json.Unmarshal([]byte(o.Choices[0].Message.Content), wynik); err != nil {
		return fmt.Errorf("model językowy: odpowiedź nie pasuje do schematu (%v)", err)
	}
	return nil
}

func schematObiektu(pola map[string]any, wymagane ...string) map[string]any {
	return map[string]any{"type": "object", "properties": pola, "required": wymagane, "additionalProperties": false}
}

func poleTekst(opis string) map[string]any {
	return map[string]any{"type": "string", "description": opis}
}

func poleWybor(opis string, wartosci ...string) map[string]any {
	return map[string]any{"type": "string", "description": opis, "enum": wartosci}
}

func poleLista(element map[string]any, maks int) map[string]any {
	return map[string]any{"type": "array", "items": element, "maxItems": maks}
}
