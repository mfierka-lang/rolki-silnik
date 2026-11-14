//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// ukryjOkno: programy pomocnicze (ffmpeg, whisper) nie otwierają okna konsoli.
func ukryjOkno(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

var setThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// nieUsypiaj: na czas montażu komputer nie przechodzi w uśpienie (ekran może zgasnąć). Wywołanie z false
// przywraca zwykłe zachowanie. Flaga dotyczy wątku, dlatego wołający trzyma się jednego wątku systemowego.
func nieUsypiaj(wlacz bool, pid int) {
	const esContinuous, esSystemRequired = 0x80000000, 0x00000001
	stan := uintptr(esContinuous)
	if wlacz {
		stan |= esSystemRequired
	}
	setThreadExecutionState.Call(stan)
}

// zabijDrzewo: kończy montaż razem z programami, które uruchomił (ffmpeg, whisper, model).
func zabijDrzewo(pid int) {
	cmd := exec.Command("taskkill", "/PID", fmt.Sprint(pid), "/T", "/F")
	ukryjOkno(cmd)
	cmd.Run()
}

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// wolneMiejsce: ile bajtów jest wolne na dysku z danym katalogiem.
func wolneMiejsce(katalog string) (int64, bool) {
	p, err := syscall.UTF16PtrFromString(katalog)
	if err != nil {
		return 0, false
	}
	var wolne, razem, wolneRazem uint64
	r, _, _ := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&wolne)), uintptr(unsafe.Pointer(&razem)), uintptr(unsafe.Pointer(&wolneRazem)))
	if r == 0 {
		return 0, false
	}
	return int64(wolne), true
}

// zarejestrujAdres: adresy zblizka-montaz://... otwierają ten program (wpis w rejestrze bieżącego użytkownika)
// i skrót w menu Start.
func zarejestrujAdres(program string) {
	klucz := `HKCU\Software\Classes\` + schematAdresu
	polecenia := [][]string{
		{"add", klucz, "/ve", "/d", "URL:Zblizka Montaz", "/f"},
		{"add", klucz, "/v", "URL Protocol", "/d", "", "/f"},
		{"add", klucz + `\shell\open\command`, "/ve", "/d", `"` + program + `" "%1"`, "/f"},
	}
	for _, p := range polecenia {
		cmd := exec.Command("reg", p...)
		ukryjOkno(cmd)
		cmd.Run()
	}
	skrot := `$s = (New-Object -ComObject WScript.Shell).CreateShortcut([Environment]::GetFolderPath('Programs') + '\Zblizka Montaz.lnk'); ` +
		`$s.TargetPath = '` + strings.ReplaceAll(program, "'", "''") + `'; $s.Description = 'Montaz rolek Zblizki'; $s.Save()`
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", skrot)
	ukryjOkno(cmd)
	cmd.Run()
}

// otworzAdres: strona w domyślnej przeglądarce.
func otworzAdres(adres string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", adres)
	ukryjOkno(cmd)
	cmd.Start()
}

// pokazWFolderze: Eksplorator z zaznaczonym plikiem.
func pokazWFolderze(plik string) {
	exec.Command("explorer", "/select,", plik).Start()
}

// oknoWyboruPlikow: systemowe okno wyboru nagrań (PowerShell + Windows Forms; okno wychodzi na wierzch).
func oknoWyboruPlikow() *exec.Cmd {
	skrypt := "Add-Type -AssemblyName System.Windows.Forms;" +
		"$f = New-Object System.Windows.Forms.Form; $f.TopMost = $true; $f.ShowInTaskbar = $false; $f.Opacity = 0; $f.Show(); $f.Activate();" +
		"$d = New-Object System.Windows.Forms.OpenFileDialog; $d.Multiselect = $true; $d.Title = 'Wybierz nagrania';" +
		"$d.Filter = 'Nagrania|*.mp4;*.mov;*.m4v;*.mkv;*.avi;*.webm;*.mts;*.m2ts;*.3gp|Wszystkie pliki|*.*';" +
		"$r = $d.ShowDialog($f); $f.Close();" +
		"if ($r -eq 'OK') { [Console]::OutputEncoding = [Text.Encoding]::UTF8; $d.FileNames | ForEach-Object { [Console]::WriteLine($_) } }"
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", skrypt)
	ukryjOkno(cmd)
	return cmd
}
