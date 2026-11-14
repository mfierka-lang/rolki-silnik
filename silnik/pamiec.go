// Pamięć wyników, które długo się liczą: transkrypcja nagrania jest zapisywana w katalogu danych (klucz: plik,
// jego rozmiar i data oraz model mowy). Dzięki temu poprawka rolki („Popraw”) nie słucha nagrań drugi raz.
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var (
	odciski   = map[string]string{}
	odciskiMu sync.Mutex
)

// odciskPliku: krótki skrót treści nagrania (rozmiar + trzy wycinki po 256 KB: początek, środek, koniec).
// Liczy się w ułamku sekundy także dla plików po kilka GB i nie zależy od nazwy ani położenia pliku.
func odciskPliku(plik string) string {
	fi, err := os.Stat(plik)
	if err != nil {
		return plik
	}
	klucz := fmt.Sprintf("%s|%d|%d", plik, fi.Size(), fi.ModTime().UnixNano())
	odciskiMu.Lock()
	defer odciskiMu.Unlock()
	if o, jest := odciski[klucz]; jest {
		return o
	}
	f, err := os.Open(plik)
	if err != nil {
		return klucz
	}
	defer f.Close()
	h := sha1.New()
	fmt.Fprintf(h, "%d|", fi.Size())
	bufor := make([]byte, 256<<10)
	for _, od := range []int64{0, fi.Size()/2 - int64(len(bufor))/2, fi.Size() - int64(len(bufor))} {
		if od < 0 {
			od = 0
		}
		n, _ := f.ReadAt(bufor, od)
		h.Write(bufor[:n])
	}
	o := hex.EncodeToString(h.Sum(nil))
	odciski[klucz] = o
	return o
}

func transkrybujKlip(n *Narzedzia, k *Klip, wav, katalog, podpowiedz string) ([]Slowo, error) {
	pamiec := ""
	if _, err := os.Stat(k.Plik); err == nil {
		h := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%s|v2", odciskPliku(k.Plik), filepath.Base(n.Model), podpowiedz)))
		pamiec = filepath.Join(n.Dane, "mowa", hex.EncodeToString(h[:])+".json")
		if b, err := os.ReadFile(pamiec); err == nil {
			var slowa []Slowo
			if json.Unmarshal(b, &slowa) == nil {
				return slowa, nil
			}
		}
	}
	slowa, err := transkrybuj(n, wav, katalog, podpowiedz)
	if err != nil {
		return nil, err
	}
	if pamiec != "" {
		os.MkdirAll(filepath.Dir(pamiec), 0o755)
		if b, err := json.Marshal(slowa); err == nil {
			os.WriteFile(pamiec, b, 0o644)
		}
	}
	return slowa, nil
}

// uruchomBajty: polecenie bez okna konsoli, wynik jako surowe bajty (np. klatka JPEG z ffmpeg).
func uruchomBajty(ctx context.Context, program string, arg ...string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, program, arg...)
	ukryjOkno(cmd)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	return out.Bytes(), errs.String(), err
}
