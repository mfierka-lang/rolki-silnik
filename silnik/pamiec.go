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
)

func transkrybujKlip(n *Narzedzia, k *Klip, wav, katalog, podpowiedz string) ([]Slowo, error) {
	pamiec := ""
	if fi, err := os.Stat(k.Plik); err == nil {
		h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%s|%s|v1", k.Plik, fi.Size(), fi.ModTime().UnixNano(), filepath.Base(n.Model), podpowiedz)))
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
