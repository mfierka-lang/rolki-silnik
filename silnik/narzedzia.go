// Narzędzia zewnętrzne silnika: ffmpeg, ffprobe, whisper.cpp i model mowy. Ten sam kod na Windows i macOS:
// różnice systemowe są tylko w plikach proc_windows.go / proc_inne.go.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Narzedzia struct {
	FFmpeg  string
	FFprobe string
	Whisper string
	Model   string
	Dane    string // katalog danych silnika (programy pomocnicze, modele, biblioteka)
}

func exeNazwa(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// katalogDanych: miejsce na pobrane programy i modele. Windows: %LOCALAPPDATA%\ZblizkaMontaz,
// macOS: ~/Library/Application Support/ZblizkaMontaz. Zmienna ZM_DANE albo flaga --dane ma pierwszeństwo.
func katalogDanych(flaga string) string {
	if flaga != "" {
		return flaga
	}
	if v := os.Getenv("ZM_DANE"); v != "" {
		return v
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "ZblizkaMontaz")
	}
	return "."
}

func znajdz(env, nazwa string, katalogi ...string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	for _, k := range katalogi {
		p := filepath.Join(k, exeNazwa(nazwa))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(nazwa); err == nil {
		return p
	}
	return ""
}

func znajdzNarzedzia(dane string) (*Narzedzia, error) {
	if abs, err := filepath.Abs(dane); err == nil {
		dane = abs // programy uruchamiamy z własnym katalogiem roboczym, więc ścieżki muszą być pełne
	}
	n := &Narzedzia{Dane: dane}
	bin := filepath.Join(dane, "bin")
	n.FFmpeg = znajdz("ZM_FFMPEG", "ffmpeg", bin, filepath.Join(dane, "ffmpeg", "bin"))
	n.FFprobe = znajdz("ZM_FFPROBE", "ffprobe", bin, filepath.Join(dane, "ffmpeg", "bin"))
	n.Whisper = znajdz("ZM_WHISPER", "whisper-cli", bin, filepath.Join(dane, "whisper", "Release"), filepath.Join(dane, "whisper"))
	n.Model = os.Getenv("ZM_MODEL")
	if n.Model == "" {
		kandydaci, _ := filepath.Glob(filepath.Join(dane, "modele", "ggml-*.bin"))
		if len(kandydaci) > 0 {
			n.Model = kandydaci[0]
		}
	}
	var brak []string
	if n.FFmpeg == "" {
		brak = append(brak, "ffmpeg")
	}
	if n.FFprobe == "" {
		brak = append(brak, "ffprobe")
	}
	if len(brak) > 0 {
		return n, fmt.Errorf("brak programów: %s (katalog danych: %s)", strings.Join(brak, ", "), dane)
	}
	return n, nil
}

var filtryFFmpeg string

// maFiltr: czy ten ffmpeg ma dany filtr (np. stabilizacja vidstab bywa pomijana w gotowych kompilacjach).
func maFiltr(n *Narzedzia, nazwa string) bool {
	if filtryFFmpeg == "" {
		ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		out, _, _ := uruchom(ctx, "", n.FFmpeg, "-hide_banner", "-filters")
		filtryFFmpeg = out + " "
	}
	return strings.Contains(filtryFFmpeg, " "+nazwa+" ")
}

// uruchom: polecenie bez okna konsoli, z limitem czasu; zwraca standardowe wyjście i błędy.
func uruchom(ctx context.Context, katalog string, program string, arg ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, program, arg...)
	cmd.Dir = katalog
	ukryjOkno(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func ogon(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// Enkoder: sprzętowe kodowanie H.264 tam, gdzie działa, inaczej programowe (libx264).
type Enkoder struct {
	Nazwa string
	Arg   []string
}

func kandydaciEnkoderow() []Enkoder {
	x264 := Enkoder{"libx264", []string{"-c:v", "libx264", "-preset", "medium", "-crf", "19", "-maxrate", "12M", "-bufsize", "24M"}}
	switch runtime.GOOS {
	case "darwin":
		return []Enkoder{
			{"h264_videotoolbox", []string{"-c:v", "h264_videotoolbox", "-b:v", "9M", "-maxrate", "12M", "-bufsize", "24M", "-profile:v", "high"}},
			x264,
		}
	case "windows":
		return []Enkoder{
			{"h264_nvenc", []string{"-c:v", "h264_nvenc", "-preset", "p5", "-rc", "vbr", "-cq", "21", "-b:v", "0", "-maxrate", "12M", "-bufsize", "24M"}},
			{"h264_qsv", []string{"-c:v", "h264_qsv", "-preset", "medium", "-global_quality", "21", "-maxrate", "12M", "-bufsize", "24M"}},
			{"h264_amf", []string{"-c:v", "h264_amf", "-quality", "quality", "-rc", "cqp", "-qp_i", "20", "-qp_p", "22"}},
			x264,
		}
	}
	return []Enkoder{x264}
}

// wybierzEnkoder: próbne zakodowanie kilku klatek każdym kandydatem; pierwszy działający wygrywa.
func wybierzEnkoder(n *Narzedzia) Enkoder {
	if w := os.Getenv("ZM_ENKODER"); w != "" {
		for _, e := range kandydaciEnkoderow() {
			if e.Nazwa == w {
				return e
			}
		}
	}
	kand := kandydaciEnkoderow()
	for _, e := range kand[:len(kand)-1] {
		ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		arg := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=gray:s=1080x1920:r=30", "-frames:v", "8", "-pix_fmt", "yuv420p"}
		arg = append(arg, e.Arg...)
		arg = append(arg, "-f", "null", "-")
		_, _, err := uruchom(ctx, "", n.FFmpeg, arg...)
		stop()
		if err == nil {
			return e
		}
	}
	return kand[len(kand)-1]
}
