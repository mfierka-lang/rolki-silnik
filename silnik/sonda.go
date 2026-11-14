// Odczyt parametrów nagrania przez ffprobe: czas, wymiary po obrocie, klatki, dźwięk, HDR.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Klip struct {
	Plik     string
	Czas     float64
	W, H     int // wymiary obrazu po uwzględnieniu obrotu (tak, jak widzi go widz)
	FPS      float64
	MaDzwiek bool
	Kodek    string  // np. hevc, h264
	Obrot    float64 // obrót zapisany w pliku (stopnie)
	HDR      bool    // HLG albo PQ (np. iPhone z włączonym HDR): wymaga sprowadzenia do zwykłych kolorów
}

type sondaWynik struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType     string            `json:"codec_type"`
		CodecName     string            `json:"codec_name"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		AvgFrameRate  string            `json:"avg_frame_rate"`
		RFrameRate    string            `json:"r_frame_rate"`
		Duration      string            `json:"duration"`
		ColorTransfer string            `json:"color_transfer"`
		Tags          map[string]string `json:"tags"`
		SideData      []struct {
			Rotation json.Number `json:"rotation"`
		} `json:"side_data_list"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

func ulamek(s string) float64 {
	cz := strings.Split(s, "/")
	if len(cz) == 2 {
		a, _ := strconv.ParseFloat(cz[0], 64)
		b, _ := strconv.ParseFloat(cz[1], 64)
		if b > 0 {
			return a / b
		}
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func sonduj(n *Narzedzia, plik string) (*Klip, error) {
	// jedna postać ścieżki (pełna, z ukośnikami systemu): od niej zależą klucze pamięci skanów, opisów i mowy
	if abs, err := filepath.Abs(plik); err == nil {
		plik = abs
	}
	ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	out, errs, err := uruchom(ctx, "", n.FFprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", plik)
	if err != nil {
		return nil, fmt.Errorf("nie udało się odczytać pliku %s: %s", plik, ogon(errs, 200))
	}
	var w sondaWynik
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		return nil, err
	}
	k := &Klip{Plik: plik}
	k.Czas, _ = strconv.ParseFloat(w.Format.Duration, 64)
	maObraz := false
	for _, s := range w.Streams {
		switch s.CodecType {
		case "video":
			if maObraz || s.Disposition.AttachedPic == 1 {
				continue
			}
			maObraz = true
			k.W, k.H = s.Width, s.Height
			obrot := 0.0
			if r, ok := s.Tags["rotate"]; ok {
				obrot, _ = strconv.ParseFloat(r, 64)
			}
			for _, sd := range s.SideData {
				if sd.Rotation != "" {
					obrot, _ = sd.Rotation.Float64()
				}
			}
			if o := math.Mod(math.Abs(obrot), 180); o > 45 && o < 135 {
				k.W, k.H = k.H, k.W
			}
			k.Kodek, k.Obrot = s.CodecName, obrot
			k.FPS = ulamek(s.AvgFrameRate)
			if k.FPS < 1 || k.FPS > 240 {
				k.FPS = ulamek(s.RFrameRate)
			}
			k.HDR = s.ColorTransfer == "arib-std-b67" || s.ColorTransfer == "smpte2084"
			if d, _ := strconv.ParseFloat(s.Duration, 64); d > 0 && (k.Czas <= 0 || d < k.Czas) {
				k.Czas = d
			}
		case "audio":
			k.MaDzwiek = true
		}
	}
	if !maObraz {
		return nil, fmt.Errorf("plik %s nie zawiera obrazu", plik)
	}
	if k.Czas <= 0 {
		return nil, fmt.Errorf("nie udało się ustalić długości pliku %s", plik)
	}
	return k, nil
}
