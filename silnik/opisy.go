// Co widać na ujęciu: model językowy z obsługą obrazu opisuje klatkę ze środka każdego kandydata na ujęcie.
// Z opisu wynika rola ujęcia w rolce (otwarcie, praca, detal, spokojny widok na koniec), słowa kluczowe do
// dopasowania haseł z szablonu i przebitek. Opisy są zapamiętywane obok skanów, więc każde nagranie jest
// opisywane tylko raz. Teksty widoczne w rolce (hasła, wezwanie, opis posta) pochodzą z szablonu marki,
// nie z modelu: mały model myli się w polskiej pisowni, a szablon nie.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type OpisUjecia struct {
	Ludzie int      `json:"ludzie"`
	Akcja  bool     `json:"akcja"`
	Plan   string   `json:"plan"` // szeroki | sredni | zblizenie
	Slowa  []string `json:"slowa"`
	Opis   string   `json:"opis"`
}

var schematUjecia = schematObiektu(map[string]any{
	"ludzie": map[string]any{"type": "integer", "minimum": 0, "maximum": 9},
	"akcja":  map[string]any{"type": "boolean"},
	"plan":   poleWybor("", "szeroki", "sredni", "zblizenie"),
	"slowa":  map[string]any{"type": "array", "minItems": 2, "maxItems": 5, "items": map[string]any{"type": "string", "maxLength": 24}},
	"opis":   map[string]any{"type": "string", "maxLength": 110},
}, "ludzie", "akcja", "plan", "slowa", "opis")

const pytanieUjecia = "Opisz klatke. ludzie: ile osob widac (0, gdy nikogo). akcja: true, gdy ktos cos robi lub cos sie rusza; false, gdy to nieruchomy widok. " +
	"plan: szeroki (caly budynek, miejsce, widok), sredni (cala postac lub kilka osob) albo zblizenie (dlonie, narzedzie, przedmiot z bliska). " +
	"slowa: 2-5 rzeczownikow po polsku nazywajacych to, co widac. opis: jedno krotkie zdanie po polsku, co widac i co sie dzieje."

// klatkaUjecia: klatka ze środka okna w takim kadrze, jaki zobaczy widz (pion), jako JPEG 432 x 768.
func klatkaUjecia(n *Narzedzia, k *Klip, o Okno, st *Styl) ([]byte, error) {
	kh := k.H - k.H%2
	kw := int(math.Round(float64(kh)*float64(st.Szer)/float64(st.Wys)/2)) * 2
	if kw > k.W {
		kw = k.W - k.W%2
		kh = int(math.Round(float64(kw)*float64(st.Wys)/float64(st.Szer)/2)) * 2
	}
	x := max(0, min(k.W-kw, int(math.Round(o.KadrX*float64(k.W)-float64(kw)/2))))
	arg := []string{"-v", "error", "-nostdin", "-ss", liczba((o.Od + o.Do) / 2)}
	arg = append(arg, dekodowanieSprzetowe()...)
	arg = append(arg, "-i", k.Plik, "-frames:v", "1", "-vf", fmt.Sprintf("crop=%d:%d:%d:%d,scale=%s", kw, kh, x, (k.H-kh)/2, skalaKlatki(st)), "-q:v", "4", "-f", "image2pipe", "-c:v", "mjpeg", "-")
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stop()
	out, errs, err := uruchomBajty(ctx, n.FFmpeg, arg...)
	if err != nil || len(out) < 500 {
		return nil, fmt.Errorf("nie udało się pobrać klatki: %s", ogon(errs, 200))
	}
	return out, nil
}

func plikOpisu(n *Narzedzia, k *Klip, o Okno, st *Styl) string {
	znak := k.Plik
	if fi, err := os.Stat(k.Plik); err == nil {
		znak = fmt.Sprintf("%s|%d|%d", k.Plik, fi.Size(), fi.ModTime().UnixNano())
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%.1f|%.2f|%dx%d|v2", znak, (o.Od+o.Do)/2, o.KadrX, st.Szer, st.Wys)))
	return filepath.Join(n.Dane, "opisy", hex.EncodeToString(h[:])+".json")
}

// opiszUjecie: opis z pamięci albo od modelu. Błąd modelu nie przerywa montażu (ujęcie zostaje bez opisu).
func opiszUjecie(r *Rozum, n *Narzedzia, k *Klip, o Okno, st *Styl) (*OpisUjecia, bool) {
	plik := plikOpisu(n, k, o, st)
	if b, err := os.ReadFile(plik); err == nil {
		op := &OpisUjecia{}
		if json.Unmarshal(b, op) == nil && op.Plan != "" {
			return op, true
		}
	}
	if r == nil || !r.MaObraz {
		return nil, false
	}
	jpg, err := klatkaUjecia(n, k, o, st)
	if err != nil {
		return nil, false
	}
	op := &OpisUjecia{}
	if err := r.pytaj("Jestes montazysta wideo. Opisujesz jedna klatke nagrania. Odpowiadasz po polsku w formacie JSON.", pytanieUjecia, [][]byte{jpg}, schematUjecia, 130, op); err != nil {
		return nil, false
	}
	op.Opis = czyscTekst(op.Opis)
	os.MkdirAll(filepath.Dir(plik), 0o755)
	if b, err := json.Marshal(op); err == nil {
		os.WriteFile(plik, b, 0o644)
	}
	return op, false
}

var reMiejsce = regexp.MustCompile(`budyn|wejsci|ulic|sklep|lokal|restaurac|kawiarn|teatr|klub|dom|hal|biur|firm|szyld|witryn|elewac|fasad|plac|zaklad|warsztat|pracowni`)

// rolaUjecia: otwarcie (szeroki plan miejsca), detal (zbliżenie), praca (ludzie w ruchu), widok (nieruchomy kadr
// bez ludzi: dobry na spokojne zakończenie, słaby w środku rolki), inne.
func rolaUjecia(op *OpisUjecia) string {
	if op == nil {
		return ""
	}
	tekst := normalizujZdanie(op.Opis + " " + strings.Join(op.Slowa, " "))
	switch {
	case op.Plan == "zblizenie":
		return "detal"
	case op.Ludzie == 0 && !op.Akcja:
		return "widok"
	case op.Plan == "szeroki" && op.Ludzie <= 2 && reMiejsce.MatchString(tekst):
		return "otwarcie"
	case op.Ludzie >= 1 && op.Akcja:
		return "praca"
	}
	return "inne"
}

// pasujeRdzen: czy w tekście (słowa bez polskich znaków, oddzielone spacjami, ze spacją na początku i końcu)
// jest słowo zaczynające się od rdzenia; rdzeń z "=" na początku musi być całym słowem.
func pasujeRdzen(tekst, rdzen string) bool {
	if strings.HasPrefix(rdzen, "=") {
		return strings.Contains(tekst, " "+normalizujZdanie(rdzen[1:])+" ")
	}
	return strings.Contains(tekst, " "+normalizujZdanie(rdzen))
}

// dopasujHaslo: pierwsze nieużyte hasło z szablonu, którego rdzeń występuje w opisie ujęcia.
func dopasujHaslo(st *Styl, op *OpisUjecia, uzyte map[int]bool) string {
	if op == nil {
		return ""
	}
	tekst := " " + normalizujZdanie(op.Opis+" "+strings.Join(op.Slowa, " ")) + " "
	for i, h := range st.Hasla {
		if uzyte[i] {
			continue
		}
		for _, rdzen := range h.Gdy {
			if pasujeRdzen(tekst, rdzen) {
				uzyte[i] = true
				return h.Napis
			}
		}
	}
	return ""
}

var ogonki = strings.NewReplacer("ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n", "ó", "o", "ś", "s", "ź", "z", "ż", "z")

// bezOgonkow: polskie litery zamienione na łacińskie (tekst musi być już małymi literami).
func bezOgonkow(s string) string { return ogonki.Replace(s) }

// normalizujZdanie: małe litery bez polskich znaków i interpunkcji, słowa oddzielone spacją.
func normalizujZdanie(s string) string {
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if n := bezOgonkow(normalizuj(w)); n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}

var reWstepOpisu = regexp.MustCompile(`(?i)^\s*(zr[oó]b\s+|zmontuj\s+|poka[zż]\s+|prosz[eę]\s+)?(kr[oó]tk[aąi]\s+|dynamiczn[aąy]\s+)?(rolk[aęi]|film|filmik|montaż|montaz|materia[lł])?\s*(o|na temat|z|pokazuj[aą]c[aąy])?\s+`)

// słowa, które sterują montażem, a nie są treścią nagłówka
var reSlowaStylu = regexp.MustCompile(`(?i)[,;]?\s*\b(dynamiczn\w*|energiczn\w*|spokojn\w*|szybk\w*|szybciej|wolniej|powoli|kr[oó]tk\w*|kr[oó]cej|d[lł]u[zż]\w*|m[oó]wion\w*|same uj[eę]cia|tylko uj[eę]cia|bez mowy|bez g[lł]osu|bez napis[oó]w|\d{1,2}\s*(s|sek\w*))\b`)

var schematNaglowka = schematObiektu(map[string]any{
	"linia1": map[string]any{"type": "string", "maxLength": 30},
	"linia2": map[string]any{"type": "string", "maxLength": 30},
}, "linia1", "linia2")

func liniaNaglowkaOk(s string) bool {
	if n := len([]rune(s)); n < 2 || n > 24 { // schemat pozwala na 30: dłuższa linia jest ucięta albo za szeroka
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" ,.!?%-", r)) {
			return false
		}
		if unicode.IsLetter(r) && r > 0x17F { // tylko alfabet łaciński z polskimi znakami
			return false
		}
	}
	return true
}

// naglowekZOpisu: dwie krótkie linie nagłówka z opisu użytkownika. Model przerabia opis na hasło w mianowniku
// („rolka o magnesach NFC dla restauracji” -> „Magnesy NFC” / „dla restauracji”); wynik jest sprawdzany, a gdy
// modelu nie ma albo odpowiedź jest podejrzana, nagłówek powstaje regułą z pierwszych słów opisu.
func naglowekZOpisu(r *Rozum, opis string) []string {
	opis = czyscTekst(reSlowaStylu.ReplaceAllString(opis, " "))
	opis = strings.Trim(opis, " ,.;:-")
	if opis == "" {
		return nil
	}
	if r != nil {
		var o struct {
			Linia1 string `json:"linia1"`
			Linia2 string `json:"linia2"`
		}
		pyt := "Zamien opis rolki na naglowek z dwoch krotkich linii (razem do 6 slow). Uzyj tylko slow z opisu, w mianowniku, bez slow rolka, film, montaz. Nie dodawaj nic od siebie.\n" +
			"Opis: rolka o magnesach NFC dla restauracji, dynamicznie\nNaglowek: {\"linia1\":\"Magnesy NFC\",\"linia2\":\"dla restauracji\"}\n" +
			"Opis: pokaż jak drukujemy breloki z logo klienta\nNaglowek: {\"linia1\":\"Breloki z logo\",\"linia2\":\"prosto z drukarki\"}\n" +
			"Opis: montaż witryn w teatrze Atelier w Sopocie\nNaglowek: {\"linia1\":\"Montaż witryn\",\"linia2\":\"Teatr Atelier, Sopot\"}\n" +
			"Opis: nowe breloki\nNaglowek: {\"linia1\":\"Nowe breloki\",\"linia2\":\"\"}\n" +
			"Opis: " + opis + "\nNaglowek:"
		if err := r.pytaj("Piszesz po polsku, poprawnie, z polskimi znakami.", pyt, nil, schematNaglowka, 60, &o); err == nil {
			l1, l2 := czyscTekst(o.Linia1), czyscTekst(o.Linia2)
			if liniaNaglowkaOk(l1) && (l2 == "" || liniaNaglowkaOk(l2)) && slowaZOpisu(l1+" "+l2, opis) {
				if l2 == "" {
					return []string{l1}
				}
				return []string{l1, l2}
			}
		}
	}
	// reguła: opis bez wstępu „rolka o…”, do pierwszego przecinka, najwyżej 5 słów, podział w połowie
	t := reWstepOpisu.ReplaceAllString(opis+" ", "")
	if i := strings.IndexAny(t, ",.;:!?"); i > 0 {
		t = t[:i]
	}
	slowa := strings.Fields(t)
	if len(slowa) == 0 {
		return nil
	}
	if len(slowa) > 5 {
		slowa = slowa[:5]
	}
	r0 := []rune(slowa[0])
	r0[0] = unicode.ToUpper(r0[0])
	slowa[0] = string(r0)
	if len(slowa) <= 2 {
		return []string{strings.Join(slowa, " ")}
	}
	pol := (len(slowa) + 1) / 2
	return []string{strings.Join(slowa[:pol], " "), strings.Join(slowa[pol:], " ")}
}

// slowaZOpisu: czy nagłówek składa się głównie ze słów użytkownika (porównanie po rdzeniach 4-literowych).
// Chroni przed zmyśleniami modelu.
func slowaZOpisu(naglowek, opis string) bool {
	zr := strings.Fields(normalizujZdanie(opis))
	traf, ile := 0, 0
	for _, w := range strings.Fields(normalizujZdanie(naglowek)) {
		if len(w) < 3 {
			continue
		}
		ile++
		rdzen := w
		if len(rdzen) > 4 {
			rdzen = rdzen[:4]
		}
		for _, z := range zr {
			if strings.HasPrefix(z, rdzen) {
				traf++
				break
			}
		}
	}
	return ile > 0 && float64(traf)/float64(ile) >= 0.6
}

// tematWypowiedzi: jedno zdanie o tym, o czym mówi osoba (do pokazania użytkownikowi; nie trafia do rolki).
func tematWypowiedzi(r *Rozum, tekst string) string {
	if r == nil || strings.TrimSpace(tekst) == "" {
		return ""
	}
	if s := []rune(tekst); len(s) > 2500 {
		tekst = string(s[:2500])
	}
	odp, err := r.pytajTekst("Odpowiadasz po polsku, jednym krótkim zdaniem, bez wstępów.", "Tekst wypowiedzi z nagrania:\n\n"+tekst+"\n\nNapisz jednym zdaniem, o czym mówi ta osoba. Zacznij od słów: Rolka o", 70)
	if err != nil {
		return ""
	}
	if i := strings.IndexAny(odp, "\r\n"); i > 0 {
		odp = odp[:i]
	}
	odp = czyscTekst(strings.Trim(odp, " *`\""))
	if n := len([]rune(odp)); n < 12 || n > 200 {
		return ""
	}
	return odp
}

// opisPosta: propozycja opisu i hashtagów. Zdanie otwierające to słowa użytkownika (początek wypowiedzi albo jego
// opis rolki), reszta pochodzi z szablonu marki: wezwanie i hashtagi dobrane po słowach kluczowych.
func opisPosta(st *Styl, poczatek string, zrodlaSlow ...string) (string, []string) {
	poczatek = czyscTekst(poczatek)
	if poczatek != "" {
		r := []rune(poczatek)
		r[0] = unicode.ToUpper(r[0])
		poczatek = string(r)
		if !strings.ContainsRune(".!?", r[len(r)-1]) {
			poczatek += "."
		}
	}
	czesci := []string{}
	if poczatek != "" {
		czesci = append(czesci, poczatek)
	}
	if st.Post.Wezwanie != "" {
		czesci = append(czesci, st.Post.Wezwanie)
	}
	tekst := " " + normalizujZdanie(strings.Join(zrodlaSlow, " ")) + " "
	tagi := append([]string(nil), st.Post.Hashtagi...)
	jest := map[string]bool{}
	for _, t := range tagi {
		jest[strings.ToLower(t)] = true
	}
	for _, grupa := range st.Post.HashtagiTematy {
		trafione := false
		for _, rdzen := range grupa.Gdy {
			if pasujeRdzen(tekst, rdzen) {
				trafione = true
				break
			}
		}
		if !trafione {
			continue
		}
		for _, t := range grupa.Tagi {
			if !jest[strings.ToLower(t)] && len(tagi) < 10 {
				tagi = append(tagi, t)
				jest[strings.ToLower(t)] = true
			}
		}
	}
	return strings.Join(czesci, "\n\n"), tagi
}
