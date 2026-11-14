// Pytanie do modelu ze zwykłą odpowiedzią tekstową (bez wymuszonego schematu JSON). Używane tam, gdzie model
// pisze dłuższy tekst: przy wymuszonym JSON-ie potrafił wypełniać odpowiedź pustymi znakami aż do limitu.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func (r *Rozum) pytajTekst(system, tekst string, maksTokenow int) (string, error) {
	var rozmowa []map[string]any
	if system != "" {
		rozmowa = append(rozmowa, map[string]any{"role": "system", "content": system})
	}
	rozmowa = append(rozmowa, map[string]any{"role": "user", "content": tekst})
	cialo, _ := json.Marshal(map[string]any{"messages": rozmowa, "temperature": 0.3, "max_tokens": maksTokenow, "cache_prompt": false})
	ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
	defer stop()
	rq, _ := http.NewRequestWithContext(ctx, "POST", r.adres+"/v1/chat/completions", bytes.NewReader(cialo))
	rq.Header.Set("Content-Type", "application/json")
	rq.Header.Set("Authorization", "Bearer "+r.klucz)
	odp, err := http.DefaultClient.Do(rq)
	if err != nil {
		return "", err
	}
	defer odp.Body.Close()
	b, _ := io.ReadAll(odp.Body)
	if odp.StatusCode != 200 {
		return "", fmt.Errorf("model językowy: %s", ogon(string(b), 300))
	}
	var o struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &o); err != nil || len(o.Choices) == 0 {
		return "", fmt.Errorf("model językowy: nieczytelna odpowiedź")
	}
	wynik := strings.TrimSpace(o.Choices[0].Message.Content)
	if slad := os.Getenv("ZM_SLAD"); slad != "" { // diagnostyka: zapis pytań i odpowiedzi modelu do pliku
		if f, err := os.OpenFile(slad, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			fmt.Fprintf(f, "--- PYTANIE\n%s\n--- ODPOWIEDŹ\n%s\n\n", tekst, wynik)
			f.Close()
		}
	}
	return wynik, nil
}

// rozbierzPost: z odpowiedzi modelu wyjmuje opis (zwykłe linie) i hashtagi (słowa zaczynające się od #).
// Model raz używa etykiet OPIS: i HASHTAGI:, raz pisze same linie, dlatego etykiety są po prostu zdejmowane.
func rozbierzPost(odp string) (string, []string) {
	var opis, tagi []string
	for _, l := range strings.Split(odp, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "*`\""))
		for _, e := range []string{"OPIS:", "HASHTAGI:", "Opis:", "Hashtagi:", "opis:", "hashtagi:"} {
			l = strings.TrimSpace(strings.TrimPrefix(l, e))
		}
		l = strings.TrimSpace(strings.Trim(l, "*`\""))
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "#") {
			tagi = append(tagi, strings.Fields(strings.NewReplacer(",", " ", ";", " ").Replace(l))...)
			continue
		}
		opis = append(opis, l)
	}
	return strings.Join(opis, " "), tagi
}
