// Przygotowanie komputera do montażu: silnik sam pobiera programy pomocnicze i modele (raz na komputer).
// Każdy plik ma przypiętą wersję i sumę kontrolną SHA-256 z oficjalnego wydania: plik o innej sumie jest odrzucany.
// Źródła: ffmpeg (kompilacje gyan.dev publikowane na GitHubie), whisper.cpp i llama.cpp (oficjalne wydania
// ggml-org na GitHubie), modele z Hugging Face (ggerganov/whisper.cpp, Qwen, ggml-org). Wszystko darmowe.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Skladnik struct {
	Nazwa    string // nazwa pobieranego pliku
	Opis     string // dla użytkownika
	URL      string
	SHA256   string
	MB       int      // przybliżona wielkość (do paska postępu i sprawdzenia miejsca)
	Cel      string   // katalog w danych silnika
	Wyjmij   []string // z archiwum tylko pliki o tych nazwach (puste = wszystko), bez katalogów
	Sprawdz  string   // plik, którego obecność oznacza, że składnik jest na miejscu
	Wymagany bool     // bez niego silnik nie zmontuje nawet rolki z samych ujęć
	Duzy     bool     // duży model: tylko gdy komputer ma dość pamięci i miejsca
}

const (
	hf = "https://huggingface.co/"
	gh = "https://github.com/"
)

func skladniki() []Skladnik {
	modele := []Skladnik{
		{Nazwa: "ggml-large-v3-turbo-q5_0.bin", Opis: "model rozpoznawania mowy (574 MB)", MB: 574, Cel: "modele", Sprawdz: "ggml-large-v3-turbo-q5_0.bin", Wymagany: true,
			URL: hf + "ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo-q5_0.bin", SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2"},
		{Nazwa: "Qwen3VL-2B-Instruct-Q4_K_M.gguf", Opis: "model rozpoznający, co widać na ujęciach (1,1 GB)", MB: 1107, Cel: "modele", Sprawdz: "Qwen3VL-2B-Instruct-Q4_K_M.gguf",
			URL: hf + "Qwen/Qwen3-VL-2B-Instruct-GGUF/resolve/main/Qwen3VL-2B-Instruct-Q4_K_M.gguf", SHA256: "089d75c52f4b7ffc56ba998ffc50aae89fcafc755f9e7208aacca281dca6c2ae"},
		{Nazwa: "mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf", Opis: "część obrazowa tego modelu (445 MB)", MB: 445, Cel: "modele", Sprawdz: "mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf",
			URL: hf + "Qwen/Qwen3-VL-2B-Instruct-GGUF/resolve/main/mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf", SHA256: "f9a68fabba69c3b81e153367b2c7521030b0fa8bb0de400c9599c8e6725f9c82"},
		{Nazwa: "gemma-4-E4B-it-Q4_0.gguf", Opis: "model piszący po polsku: nagłówki i opisy postów (4,6 GB)", MB: 4590, Cel: "modele", Sprawdz: "gemma-4-E4B-it-Q4_0.gguf", Duzy: true,
			URL: hf + "ggml-org/gemma-4-E4B-it-GGUF/resolve/main/gemma-4-E4B-it-Q4_0.gguf", SHA256: "a555b900214b477d8880e7832e0b8925e139b0159640036b09fe472b6f2097f2"},
	}
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return append([]Skladnik{
			{Nazwa: "ffmpeg-2026-11-11-git-23c4540053-essentials_build.zip", Opis: "ffmpeg: cięcie, kadrowanie i składanie filmu (118 MB)", MB: 118, Cel: "bin",
				Wyjmij: []string{"ffmpeg.exe", "ffprobe.exe"}, Sprawdz: "ffmpeg.exe", Wymagany: true,
				URL: gh + "GyanD/codexffmpeg/releases/download/2026-11-11-git-23c4540053/ffmpeg-2026-11-11-git-23c4540053-essentials_build.zip", SHA256: "65dcc60aff3f159d6f39a2fc285b340d6e4b4d221901f1a8955fe1ce55d77083"},
			{Nazwa: "whisper-bin-x64.zip", Opis: "whisper.cpp: zamiana mowy na tekst (9 MB)", MB: 9, Cel: "whisper", Sprawdz: "whisper-cli.exe", Wymagany: true,
				URL: gh + "ggml-org/whisper.cpp/releases/download/b5454/whisper-bin-x64.zip", SHA256: "6ba69e3482d7826214f90a6a9c84ca07782aec1e1d0c6a7c30c994fd5d816ccb"},
			{Nazwa: "llama-b12377-bin-win-vulkan-x64.zip", Opis: "llama.cpp: uruchamianie modeli na karcie graficznej (38 MB)", MB: 38, Cel: "llama-vulkan", Sprawdz: "llama-server.exe",
				URL: gh + "ggml-org/llama.cpp/releases/download/b12377/llama-b12377-bin-win-vulkan-x64.zip", SHA256: "0162ff9acc36ef88575d673e56e16673f455fb685fea1857c96fb57dd4831ff7"},
			{Nazwa: "llama-b12377-bin-win-cpu-x64.zip", Opis: "llama.cpp: wersja zapasowa bez karty graficznej (25 MB)", MB: 25, Cel: "llama", Sprawdz: "llama-server.exe",
				URL: gh + "ggml-org/llama.cpp/releases/download/b12377/llama-b12377-bin-win-cpu-x64.zip", SHA256: "d3b60c5abbe3a131c77e568310547492438bc7c785721eb765d2d817fe4424c5"},
		}, modele...)
	case "darwin/arm64":
		// ffmpeg: statyczna kompilacja martin-riedl.de (ma libass; nie ma vidstab, więc na Macu rolki są bez
		// dodatkowej stabilizacji). whisper-cli: zbudowany z oficjalnego kodu whisper.cpp b5454 w GitHub Actions
		// (Metal, jeden plik), bo oficjalnego wydania dla macOS nie ma.
		return append([]Skladnik{
			{Nazwa: "mac-ffmpeg.zip", Opis: "ffmpeg: cięcie, kadrowanie i składanie filmu (28 MB)", MB: 28, Cel: "bin", Wyjmij: []string{"ffmpeg"}, Sprawdz: "ffmpeg", Wymagany: true,
				URL: "https://ffmpeg.martin-riedl.de/download/macos/arm64/1789931890_9.0.2/ffmpeg.zip", SHA256: "c8ed4c4e6978a03c485edbfe4e0a5dc2380f8a30bba5150531b31b094492d924"},
			{Nazwa: "mac-ffprobe.zip", Opis: "ffprobe: odczyt parametrów nagrań (28 MB)", MB: 28, Cel: "bin", Wyjmij: []string{"ffprobe"}, Sprawdz: "ffprobe", Wymagany: true,
				URL: "https://ffmpeg.martin-riedl.de/download/macos/arm64/1789931890_9.0.2/ffprobe.zip", SHA256: "fcbe839537485eaee7a7a8bc5cbc0f90d53617e80943e8a5b2e31cb851197ea6"},
			{Nazwa: "whisper-cli-macos-arm64.zip", Opis: "whisper.cpp: zamiana mowy na tekst (8 MB)", MB: 8, Cel: "whisper", Sprawdz: "whisper-cli", Wymagany: true,
				URL: gh + "mfierka-lang/rolki-silnik/releases/download/mac-narzedzia-1/whisper-cli-macos-arm64.zip", SHA256: "d06992144e9b5982f9dc7d919310dd69f05c3348577cf32fae8825128733c565"},
			{Nazwa: "llama-b12377-bin-macos-arm64.tar.gz", Opis: "llama.cpp: uruchamianie modeli (17 MB)", MB: 17, Cel: "llama", Sprawdz: "llama-server",
				URL: gh + "ggml-org/llama.cpp/releases/download/b12377/llama-b12377-bin-macos-arm64.tar.gz", SHA256: "abf1e91b761acdd707cc8e66abe930e47fe1a64ccb1e63662bdd67f7aebe3306"},
		}, modele...)
	case "darwin/amd64":
		return append([]Skladnik{
			{Nazwa: "llama-b12377-bin-macos-x64.tar.gz", Opis: "llama.cpp: uruchamianie modeli (13 MB)", MB: 13, Cel: "llama", Sprawdz: "llama-server",
				URL: gh + "ggml-org/llama.cpp/releases/download/b12377/llama-b12377-bin-macos-x64.tar.gz", SHA256: "7434dbe49257c3ee9074b92fcec12c50cca6b616eec4d2252e2b6fd490375891"},
		}, modele...)
	}
	return modele
}

// brakujaceSkladniki: czego nie ma w katalogu danych. Drugi wynik: czy brakuje czegoś, bez czego montaż nie ruszy.
// Programy znalezione w systemie (ffmpeg z PATH) też się liczą.
func brakujaceSkladniki(dane string) ([]Skladnik, bool) {
	n, _ := znajdzNarzedzia(dane)
	var brak []Skladnik
	// ffmpeg znaleziony w systemie wystarcza tylko wtedy, gdy umie wypalać napisy (libass); np. ffmpeg z Homebrew nie umie
	ffmpegOk := n != nil && n.FFmpeg != "" && n.FFprobe != "" && maFiltr(n, "ass")
	wymagane := !ffmpegOk
	for _, s := range skladniki() {
		if _, err := os.Stat(filepath.Join(dane, s.Cel, s.Sprawdz)); err == nil {
			continue
		}
		switch {
		case (s.Sprawdz == "ffmpeg.exe" || s.Sprawdz == "ffmpeg" || s.Sprawdz == "ffprobe") && ffmpegOk:
			continue
		case strings.HasPrefix(s.Sprawdz, "whisper-cli") && n != nil && n.Whisper != "":
			continue
		case s.Duzy && !stacNaDuzyModel(dane):
			continue
		}
		brak = append(brak, s)
		if s.Wymagany {
			wymagane = true
		}
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" && n != nil {
		if !ffmpegOk {
			brak = append(brak, Skladnik{Opis: "ffmpeg z obsługą napisów (Mac z procesorem Intel: na razie ręcznie, brew install ffmpeg-full)", Wymagany: true})
		}
		if n.Whisper == "" {
			brak = append(brak, Skladnik{Opis: "whisper-cli (Mac z procesorem Intel: na razie ręcznie, brew install whisper-cpp)"})
		}
	}
	return brak, wymagane
}

// stacNaDuzyModel: duży model piszący tylko tam, gdzie jest na niego miejsce (12 GB wolnego dysku).
func stacNaDuzyModel(dane string) bool {
	wolne, ok := wolneMiejsce(dane)
	return !ok || wolne >= 12<<30
}

func przygotujNarzedzia(dane string, postep func(float64, string)) error {
	brak, _ := brakujaceSkladniki(dane)
	var doPobrania []Skladnik
	razem := 0
	for _, s := range brak {
		if s.URL != "" {
			doPobrania = append(doPobrania, s)
			razem += s.MB
		}
	}
	if len(doPobrania) == 0 {
		return nil
	}
	if wolne, ok := wolneMiejsce(dane); ok && wolne < int64(razem)*3/2<<20 {
		return fmt.Errorf("za mało miejsca na dysku: potrzeba ok. %d MB, wolne jest %d MB", razem*3/2, wolne>>20)
	}
	zrobione := 0
	for i, s := range doPobrania {
		baza, udzial := float64(zrobione)/float64(razem), float64(s.MB)/float64(razem)
		opis := fmt.Sprintf("Pobieram %d z %d: %s", i+1, len(doPobrania), s.Opis)
		postep(baza, opis)
		if err := pobierzSkladnik(dane, s, func(p float64) { postep(baza+udzial*p, opis) }); err != nil {
			return fmt.Errorf("%s: %v", s.Opis, err)
		}
		zrobione += s.MB
	}
	postep(1, "Gotowe")
	return nil
}

func sumaPliku(plik string) (string, error) {
	f, err := os.Open(plik)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func pobierzSkladnik(dane string, s Skladnik, postep func(float64)) error {
	kat := filepath.Join(dane, "pobrane")
	os.MkdirAll(kat, 0o755)
	plik := filepath.Join(kat, s.Nazwa)
	gotowy := false
	// ten sam plik leżący już na dysku (np. w folderze instalatora) nie jest pobierany drugi raz
	for _, zr := range []string{os.Getenv("ZM_ZRODLO"), kat} {
		if zr == "" {
			continue
		}
		kandydat := filepath.Join(zr, s.Nazwa)
		if suma, err := sumaPliku(kandydat); err == nil && suma == s.SHA256 {
			if kandydat != plik {
				if err := kopiujDuzy(kandydat, plik); err != nil {
					return err
				}
			}
			gotowy = true
			break
		}
	}
	if !gotowy {
		if err := pobierzPlik(s.URL, plik, int64(s.MB)<<20, postep); err != nil {
			return err
		}
		suma, err := sumaPliku(plik)
		if err != nil {
			return err
		}
		if suma != s.SHA256 {
			os.Remove(plik)
			return fmt.Errorf("pobrany plik ma inną sumę kontrolną niż oficjalne wydanie, odrzucam go")
		}
	}
	cel := filepath.Join(dane, s.Cel)
	os.MkdirAll(cel, 0o755)
	var err error
	switch {
	case strings.HasSuffix(s.Nazwa, ".zip"):
		err = rozpakujZip(plik, cel, s.Wyjmij)
	case strings.HasSuffix(s.Nazwa, ".tar.gz"):
		err = rozpakujTarGz(plik, cel, s.Wyjmij)
	default:
		err = os.Rename(plik, filepath.Join(cel, s.Nazwa))
		if err != nil {
			err = kopiujDuzy(plik, filepath.Join(cel, s.Nazwa))
		}
	}
	if err != nil {
		return err
	}
	os.Remove(plik)
	if _, err := os.Stat(filepath.Join(cel, s.Sprawdz)); err != nil {
		return fmt.Errorf("po rozpakowaniu brakuje pliku %s", s.Sprawdz)
	}
	return nil
}

func kopiujDuzy(z, do string) error {
	we, err := os.Open(z)
	if err != nil {
		return err
	}
	defer we.Close()
	wy, err := os.Create(do)
	if err != nil {
		return err
	}
	if _, err := io.Copy(wy, we); err != nil {
		wy.Close()
		return err
	}
	return wy.Close()
}

// pobierzPlik: pobieranie ze wznawianiem po zerwaniu połączenia (do 30 prób).
func pobierzPlik(adres, cel string, spodziewane int64, postep func(float64)) error {
	czesc := cel + ".czesc"
	klient := &http.Client{Timeout: 0}
	var ostatni error
	for proba := 0; proba < 30; proba++ {
		maJuz := int64(0)
		if fi, err := os.Stat(czesc); err == nil {
			maJuz = fi.Size()
		}
		rq, _ := http.NewRequest("GET", adres, nil)
		rq.Header.Set("User-Agent", "zblizka-montaz/"+wersja)
		if maJuz > 0 {
			rq.Header.Set("Range", fmt.Sprintf("bytes=%d-", maJuz))
		}
		odp, err := klient.Do(rq)
		if err != nil {
			ostatni = err
			time.Sleep(3 * time.Second)
			continue
		}
		if odp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			odp.Body.Close()
			break // plik już jest cały
		}
		if odp.StatusCode != 200 && odp.StatusCode != 206 {
			odp.Body.Close()
			return fmt.Errorf("serwer odpowiedział %s", odp.Status)
		}
		tryb := os.O_CREATE | os.O_WRONLY | os.O_APPEND
		if odp.StatusCode == 200 {
			tryb, maJuz = os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0
		}
		f, err := os.OpenFile(czesc, tryb, 0o644)
		if err != nil {
			odp.Body.Close()
			return err
		}
		calosc := maJuz + odp.ContentLength
		if odp.ContentLength <= 0 {
			calosc = spodziewane
		}
		bufor := make([]byte, 1<<20)
		pobrane := maJuz
		var blad error
		for {
			n, err := odp.Body.Read(bufor)
			if n > 0 {
				if _, e := f.Write(bufor[:n]); e != nil {
					blad = e
					break
				}
				pobrane += int64(n)
				if postep != nil && calosc > 0 {
					postep(float64(pobrane) / float64(calosc))
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				blad = err
				break
			}
		}
		f.Close()
		odp.Body.Close()
		if blad == nil {
			return os.Rename(czesc, cel)
		}
		ostatni = blad
		time.Sleep(3 * time.Second)
	}
	if ostatni != nil {
		return fmt.Errorf("pobieranie nie powiodło się: %v", ostatni)
	}
	return os.Rename(czesc, cel)
}

func chcePlik(nazwa string, wyjmij []string) bool {
	if len(wyjmij) == 0 {
		return true
	}
	for _, w := range wyjmij {
		if nazwa == w {
			return true
		}
	}
	return false
}

// rozpakujZip: pliki trafiają płasko do katalogu docelowego (bez katalogów z archiwum), co przy okazji zamyka
// drogę do zapisu poza tym katalogiem przez spreparowane ścieżki.
func rozpakujZip(plik, cel string, wyjmij []string) error {
	z, err := zip.OpenReader(plik)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, f := range z.File {
		nazwa := filepath.Base(filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() || nazwa == "." || !chcePlik(nazwa, wyjmij) {
			continue
		}
		we, err := f.Open()
		if err != nil {
			return err
		}
		wy, err := os.OpenFile(filepath.Join(cel, nazwa), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			we.Close()
			return err
		}
		_, err = io.Copy(wy, we)
		we.Close()
		wy.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func rozpakujTarGz(plik, cel string, wyjmij []string) error {
	f, err := os.Open(plik)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		nazwa := filepath.Base(filepath.FromSlash(h.Name))
		if h.Typeflag == tar.TypeSymlink && nazwa != "." && chcePlik(nazwa, wyjmij) {
			// dowiązanie odtwarzamy płasko: wskazuje na plik o tej samej nazwie w katalogu docelowym
			os.Remove(filepath.Join(cel, nazwa))
			os.Symlink(filepath.Base(filepath.FromSlash(h.Linkname)), filepath.Join(cel, nazwa))
			continue
		}
		if h.Typeflag != tar.TypeReg || nazwa == "." || !chcePlik(nazwa, wyjmij) {
			continue
		}
		wy, err := os.OpenFile(filepath.Join(cel, nazwa), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(wy, tr); err != nil {
			wy.Close()
			return err
		}
		wy.Close()
	}
}
