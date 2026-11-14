// Krótki opis komputera (system, procesor, pamięć, wolne miejsce) do pomocy technicznej. Silnik podaje go
// stronie panelu w odpowiedzi /api/stan; panel zapisuje go przy koncie użytkownika, żeby administrator wiedział,
// na jakim sprzęcie pracuje montaż. Nic poza tymi kilkoma polami nie jest wysyłane.
package main

import (
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

type Sprzet struct {
	System   string `json:"system"`
	Procesor string `json:"procesor"`
	PamiecGB int    `json:"pamiecGB"`
	WolneGB  int    `json:"wolneGB"`
	Silnik   string `json:"silnik"`
}

func wynikPolecenia(program string, arg ...string) string {
	cmd := exec.Command(program, arg...)
	ukryjOkno(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var reWersjaWindows = regexp.MustCompile(`\d+\.\d+\.\d+`)

func odczytajSprzet(dane string) Sprzet {
	s := Sprzet{Silnik: wersja, System: runtime.GOOS + " " + runtime.GOARCH}
	switch runtime.GOOS {
	case "windows":
		if w := reWersjaWindows.FindString(wynikPolecenia("cmd", "/c", "ver")); w != "" {
			s.System = "Windows " + w
		}
		// ostatnia kolumna wiersza z rejestru to nazwa procesora
		out := wynikPolecenia("reg", "query", `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "/v", "ProcessorNameString")
		if i := strings.Index(out, "REG_SZ"); i >= 0 {
			s.Procesor = strings.TrimSpace(out[i+len("REG_SZ"):])
		}
	case "darwin":
		if w := wynikPolecenia("sw_vers", "-productVersion"); w != "" {
			s.System = "macOS " + w
		}
		s.Procesor = wynikPolecenia("sysctl", "-n", "machdep.cpu.brand_string")
		if runtime.GOARCH == "amd64" && wynikPolecenia("sysctl", "-n", "sysctl.proc_translated") == "1" {
			s.Procesor += " (program w trybie Rosetta)"
		}
	}
	if len(s.Procesor) > 80 {
		s.Procesor = s.Procesor[:80]
	}
	if ram, ok := pamiecRAM(); ok {
		s.PamiecGB = int((ram + (1 << 29)) >> 30)
	}
	if wolne, ok := wolneMiejsce(dane); ok {
		s.WolneGB = int(wolne >> 30)
	}
	return s
}
