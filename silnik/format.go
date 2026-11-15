// Format filmu: pionowy 9:16 (rolka), post 4:5 (post na Instagramie), kwadrat 1:1 i poziomy 16:9.
// Każdy format ma swoją siatkę układu w HD; jakość 2K i 4K powiększa obraz (jakosc.go).
package main

import (
	"regexp"
)

// siatkaFormatu: wymiary siatki układu (obraz w jakości HD) dla formatu filmu.
func siatkaFormatu(format string) (int, int) {
	switch format {
	case "poziom":
		return 1920, 1080
	case "post":
		return 1080, 1350
	case "kwadrat":
		return 1080, 1080
	}
	return 1080, 1920
}

// formatZKlucza: znany klucz formatu; wszystko inne (także puste) to rolka pionowa.
func formatZKlucza(format string) string {
	switch format {
	case "poziom", "post", "kwadrat":
		return format
	}
	return "pion"
}

// zdanieFormatu: zdanie do raportu, gdy format wynika z opisu albo z uwagi.
func zdanieFormatu(format string) string {
	switch format {
	case "poziom":
		return "Format z opisu: poziomy 16:9."
	case "post":
		return "Format z opisu: post 4:5."
	case "kwadrat":
		return "Format z opisu: kwadrat 1:1."
	}
	return "Format z opisu: pionowy 9:16."
}

var reProporcje = regexp.MustCompile(`(^|[^\d])(16|9|4|1)\s?[:x]\s?(16|9|5|1)($|[^\d])`)

// formatZTekstu: format wpisany w opisie albo w uwadze. Proporcje podane liczbami mają pierwszeństwo przed słowami
// („post na Instagram 4:5” to nie rolka). ma: czy znormalizowany tekst zawiera któryś z fragmentów.
func formatZTekstu(male string, ma func(...string) bool) string {
	if m := reProporcje.FindStringSubmatch(male); m != nil {
		switch m[2] + ":" + m[3] {
		case "16:9":
			return "poziom"
		case "9:16":
			return "pion"
		case "4:5":
			return "post"
		case "1:1":
			return "kwadrat"
		}
	}
	switch {
	case ma(" kwadrat"):
		return "kwadrat"
	case ma(" poziom", " panoram", " na youtube", " na yt "):
		return "poziom"
	case ma(" pionow", " w pionie", " pion ", " na tiktok", " na instagram", " rolk"):
		return "pion"
	}
	return ""
}
