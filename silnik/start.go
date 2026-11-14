// Uruchamianie bez terminala. Dwuklik w pobrany plik: silnik kopiuje się do katalogu danych, rejestruje własny
// rodzaj adresu (zblizka-montaz://), dzięki któremu strona może go potem obudzić jednym kliknięciem, i startuje
// serwer lokalny z interfejsem. Klik „uruchom” na stronie: system otwiera zainstalowaną kopię z adresem jako
// argumentem (Windows) albo po prostu uruchamia aplikację (macOS).
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const schematAdresu = "zblizka-montaz"

func uruchomZKlikniecia(adres string) {
	dane := katalogDanych("")
	os.MkdirAll(dane, 0o755)
	okno := true
	if adres != "" {
		if u, err := url.Parse(adres); err == nil && u.Query().Get("okno") == "0" {
			okno = false // strona, z której kliknięto, sama jest interfejsem
		}
	}
	if runtime.GOOS == "windows" {
		if cel, przeniesiony := zainstalujSie(dane); przeniesiony {
			// dalej pracuje kopia z katalogu danych, żeby pobrany plik można było skasować
			arg := []string{"serwer"}
			if !okno {
				arg = append(arg, "--okno=false")
			}
			cmd := exec.Command(cel, arg...)
			ukryjOkno(cmd)
			if cmd.Start() == nil {
				return
			}
		}
	}
	uruchomSerwer(dane, "", okno, "")
}

// zainstalujSie: kopia programu w katalogu danych + rejestracja adresu zblizka-montaz:// dla bieżącego użytkownika
// (bez uprawnień administratora). Zwraca ścieżkę kopii i informację, czy bieżący program jest inną kopią.
func zainstalujSie(dane string) (string, bool) {
	ja, err := os.Executable()
	if err != nil {
		return "", false
	}
	cel := filepath.Join(dane, exeNazwa("ZblizkaMontaz"))
	inny := !tenSamPlik(ja, cel)
	if inny {
		if err := kopiujDuzy(ja, cel); err != nil {
			return "", false // np. kopia jest właśnie uruchomiona: zostaje stara, adres i tak na nią wskazuje
		}
		os.Chmod(cel, 0o755)
	}
	zarejestrujAdres(cel)
	return cel, inny
}

func tenSamPlik(a, b string) bool {
	fa, e1 := os.Stat(a)
	fb, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(fa, fb)
}

// polecenieInstaluj: pobranie brakujących programów i modeli z linii poleceń (do testów instalatora).
func polecenieInstaluj(arg []string) {
	fs := flag.NewFlagSet("instaluj", flag.ExitOnError)
	dane := fs.String("dane", "", "katalog danych silnika")
	fs.Parse(arg)
	d := katalogDanych(*dane)
	if abs, err := filepath.Abs(d); err == nil {
		d = abs
	}
	os.MkdirAll(d, 0o755)
	brak, _ := brakujaceSkladniki(d)
	for _, b := range brak {
		fmt.Println("brakuje:", b.Opis)
	}
	ostatni := ""
	err := przygotujNarzedzia(d, func(p float64, tekst string) {
		if linia := fmt.Sprintf("%3.0f%% %s", p*100, tekst); linia != ostatni {
			ostatni = linia
			fmt.Println(linia)
		}
	})
	if err != nil {
		blad(err)
	}
	zapewnijSzablon(d)
	fmt.Println("Katalog danych gotowy:", d)
}

// paczkaMac: aplikacja macOS (katalog .app spakowany do zip) zbudowana na dowolnym systemie.
// Program w paczce musi zachować prawo uruchamiania, dlatego zip powstaje tutaj, a nie zwykłym archiwizatorem.
func paczkaMac(program, wyjscie string) error {
	const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Zblizka Montaz</string>
<key>CFBundleDisplayName</key><string>Zbliżka Montaż</string>
<key>CFBundleIdentifier</key><string>pl.zblizka.montaz</string>
<key>CFBundleExecutable</key><string>ZblizkaMontaz</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>` + wersja + `</string>
<key>CFBundleVersion</key><string>` + wersja + `</string>
<key>LSMinimumSystemVersion</key><string>11.0</string>
<key>LSUIElement</key><true/>
<key>CFBundleURLTypes</key><array><dict>
<key>CFBundleURLName</key><string>pl.zblizka.montaz</string>
<key>CFBundleURLSchemes</key><array><string>` + schematAdresu + `</string></array>
</dict></array>
</dict></plist>
`
	f, err := os.Create(wyjscie)
	if err != nil {
		return err
	}
	defer f.Close()
	z := zip.NewWriter(f)
	dodaj := func(nazwa string, tryb os.FileMode, tresc io.Reader) error {
		h := &zip.FileHeader{Name: nazwa, Method: zip.Deflate}
		h.SetMode(tryb)
		w, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, tresc)
		return err
	}
	baza := "Zblizka Montaz.app/Contents/"
	p, err := os.Open(program)
	if err != nil {
		return err
	}
	defer p.Close()
	if err := dodaj(baza+"Info.plist", 0o644, stringReader(plist)); err != nil {
		return err
	}
	if err := dodaj(baza+"MacOS/ZblizkaMontaz", 0o755, p); err != nil {
		return err
	}
	return z.Close()
}

type czytnikTekstu struct {
	s string
	i int
}

func (c *czytnikTekstu) Read(b []byte) (int, error) {
	if c.i >= len(c.s) {
		return 0, io.EOF
	}
	n := copy(b, c.s[c.i:])
	c.i += n
	return n, nil
}

func stringReader(s string) io.Reader { return &czytnikTekstu{s: s} }
