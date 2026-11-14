//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

func ukryjOkno(cmd *exec.Cmd) {}

var kofeina *exec.Cmd

// nieUsypiaj: macOS ma do tego program caffeinate; trzyma komputer przytomnym, dopóki żyje proces o podanym numerze.
func nieUsypiaj(wlacz bool, pid int) {
	if kofeina != nil && kofeina.Process != nil {
		kofeina.Process.Kill()
		kofeina = nil
	}
	if wlacz && runtime.GOOS == "darwin" {
		kofeina = exec.Command("caffeinate", "-i", "-w", fmt.Sprint(pid))
		kofeina.Start()
	}
}

func zabijDrzewo(pid int) {
	exec.Command("pkill", "-TERM", "-P", fmt.Sprint(pid)).Run()
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}

func wolneMiejsce(katalog string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(katalog, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}

// zarejestrujAdres: na macOS adres zblizka-montaz:// jest zapisany w pliku Info.plist aplikacji.
func zarejestrujAdres(program string) {}

func otworzAdres(adres string) {
	if runtime.GOOS == "darwin" {
		exec.Command("open", adres).Start()
		return
	}
	exec.Command("xdg-open", adres).Start()
}

func pokazWFolderze(plik string) {
	if runtime.GOOS == "darwin" {
		exec.Command("open", "-R", plik).Start()
	}
}

// oknoWyboruPlikow: systemowe okno wyboru nagrań (AppleScript), ścieżki w kolejnych wierszach.
func oknoWyboruPlikow() *exec.Cmd {
	return exec.Command("osascript",
		"-e", `set wybrane to choose file with prompt "Wybierz nagrania" of type {"public.movie"} with multiple selections allowed`,
		"-e", `set wynik to ""`,
		"-e", `repeat with p in wybrane`,
		"-e", `set wynik to wynik & POSIX path of p & linefeed`,
		"-e", `end repeat`,
		"-e", `return wynik`)
}
