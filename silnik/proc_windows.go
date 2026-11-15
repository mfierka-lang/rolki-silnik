//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// zastapProces: w Windows nie ma zastępowania procesu; nową wersję uruchamia się jako osobny proces.
func zastapProces(program string, arg, srodowisko []string) error {
	return fmt.Errorf("niedostępne w Windows")
}

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

var globalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// pamiecRAM: ile pamięci ma komputer (bajty).
func pamiecRAM() (int64, bool) {
	var m struct {
		Dlugosc, Obciazenie                                                 uint32
		Razem, Wolne, PlikRazem, PlikWolny, WirtRazem, WirtWolny, WirtRozsz uint64
	}
	m.Dlugosc = uint32(unsafe.Sizeof(m))
	if r, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0, false
	}
	return int64(m.Razem), true
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
// skryptOknaPlikow: okno wyboru nagrań (PowerShell + Windows Forms). Silnik pracuje w tle, więc Windows nie daje jego
// oknom pierwszego planu: skrypt sam wyciąga okno na wierzch (połączenie z wątkiem okna pierwszoplanowego) i trzyma je
// nad innymi. Skrypt jest bez polskich znaków, bo Windows PowerShell czyta pliki bez BOM jako ANSI.
var skryptOknaPlikow = strings.Join([]string{
	"Add-Type -AssemblyName System.Windows.Forms",
	"Add-Type -AssemblyName System.Drawing",
	"Add-Type -Namespace Zm -Name Okno -MemberDefinition @'",
	"[DllImport(\"user32.dll\")] public static extern IntPtr GetForegroundWindow();",
	"[DllImport(\"user32.dll\")] public static extern uint GetWindowThreadProcessId(IntPtr h, IntPtr p);",
	"[DllImport(\"kernel32.dll\")] public static extern uint GetCurrentThreadId();",
	"[DllImport(\"user32.dll\")] public static extern bool AttachThreadInput(uint a, uint b, bool f);",
	"[DllImport(\"user32.dll\")] public static extern bool SetForegroundWindow(IntPtr h);",
	"[DllImport(\"user32.dll\")] public static extern bool BringWindowToTop(IntPtr h);",
	"[DllImport(\"user32.dll\")] public static extern bool SetWindowPos(IntPtr h, IntPtr po, int x, int y, int cx, int cy, uint flagi);",
	"[DllImport(\"user32.dll\", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindow(string klasa, string tytul);",
	"public static bool NaWierzch(IntPtr h) {",
	"  if (h == IntPtr.Zero) return false;",
	"  uint obcy = GetWindowThreadProcessId(GetForegroundWindow(), IntPtr.Zero), moj = GetCurrentThreadId();",
	"  AttachThreadInput(moj, obcy, true);",
	"  SetWindowPos(h, new IntPtr(-1), 0, 0, 0, 0, 0x0043);",
	"  BringWindowToTop(h); SetForegroundWindow(h);",
	"  AttachThreadInput(moj, obcy, false);",
	"  return GetForegroundWindow() == h;",
	"}",
	"'@",
	"$tytul = 'Wybierz nagrania'",
	"$f = New-Object System.Windows.Forms.Form",
	"$f.TopMost = $true; $f.ShowInTaskbar = $false; $f.Opacity = 0; $f.StartPosition = 'CenterScreen'; $f.Size = New-Object System.Drawing.Size(1, 1)",
	"$f.Show(); [void][Zm.Okno]::NaWierzch($f.Handle)",
	"$d = New-Object System.Windows.Forms.OpenFileDialog; $d.Multiselect = $true; $d.Title = $tytul",
	"$d.Filter = 'Nagrania|*.mp4;*.mov;*.m4v;*.mkv;*.avi;*.webm;*.mts;*.m2ts;*.3gp|Wszystkie pliki|*.*'",
	"# okno wyboru pojawia sie chwile po ShowDialog: zegar kilka razy wyciaga je na pierwszy plan",
	"$script:proby = 0",
	"$t = New-Object System.Windows.Forms.Timer; $t.Interval = 200",
	"$t.add_Tick({ $script:proby++; $ok = [Zm.Okno]::NaWierzch([Zm.Okno]::FindWindow('#32770', $tytul)); if ($env:ZM_SLAD_OKNA) { [Console]::Error.WriteLine('na wierzchu: ' + $ok) }; if ($ok -or $script:proby -ge 8) { $t.Stop() } })",
	"$t.Start()",
	"$r = $d.ShowDialog($f); $t.Stop(); $f.Close()",
	"if ($r -eq 'OK') { [Console]::OutputEncoding = [Text.Encoding]::UTF8; $d.FileNames | ForEach-Object { [Console]::WriteLine($_) } }",
}, "\r\n")

func oknoWyboruPlikow() *exec.Cmd {
	plik := filepath.Join(os.TempDir(), "zblizka-montaz-wybierz.ps1")
	os.WriteFile(plik, []byte(skryptOknaPlikow), 0o644)
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", plik)
	ukryjOkno(cmd)
	return cmd
}
