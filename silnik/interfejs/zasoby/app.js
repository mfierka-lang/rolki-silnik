// Interfejs montażu rolek Zbliżki. Ten sam plik działa w dwóch miejscach:
//  - w oknie otwartym przez silnik (adres 127.0.0.1): rozmawia z silnikiem pod tym samym adresem,
//  - na stronie panelu: rozmawia z silnikiem na tym komputerze (window.MONTAZ.silnik).
// Nagrania nie opuszczają komputera: przeciągnięty plik trafia tylko do silnika lokalnego.
(function () {
  "use strict";
  var KONF = window.MONTAZ || JSON.parse(document.body.getAttribute("data-montaz") || "{}");
  var API = (KONF.silnik || "").replace(/\/$/, "");
  var NA_STRONIE = !!KONF.strona;
  var $ = function (id) { return document.getElementById(id); };
  var ekrany = ["brak", "przygotowanie", "start", "praca", "wynik", "blad"];
  var pliki = [];            // {id, nazwa, rozmiar, el, gotowy}
  var zadanie = null;        // id bieżącego zlecenia
  var wynik = null;
  var zegar = null, startPracy = 0, ekranTeraz = "";
  var system = /Mac/i.test(navigator.platform || navigator.userAgent) ? "mac" : "windows";

  function pokaz(nazwa) {
    if (ekranTeraz === nazwa) return;
    ekranTeraz = nazwa;
    ekrany.forEach(function (e) { $("ekran-" + e).hidden = e !== nazwa; });
    window.scrollTo(0, 0);
  }
  function stanSilnika(stan, tekst) {
    var el = $("stan-silnika");
    el.dataset.stan = stan;
    el.querySelector("b").textContent = tekst;
  }
  function wolaj(sciezka, opcje) {
    opcje = opcje || {};
    opcje.headers = Object.assign({}, opcje.headers || {});
    if (opcje.json !== undefined) {
      opcje.method = opcje.method || "POST";
      opcje.headers["Content-Type"] = "application/json";
      opcje.body = JSON.stringify(opcje.json);
    }
    return fetch(API + sciezka, opcje).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) throw new Error(j.blad || ("Błąd " + r.status));
        return j;
      });
    });
  }
  function rozmiarTekst(b) {
    if (b >= 1e9) return (b / 1e9).toFixed(1).replace(".", ",") + " GB";
    if (b >= 1e6) return Math.round(b / 1e6) + " MB";
    return Math.max(1, Math.round(b / 1e3)) + " KB";
  }
  function czasTekst(s) {
    s = Math.max(0, Math.round(s));
    return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
  }

  // ---------- wykrywanie silnika ----------
  var proby = 0, pominiete = false;
  function sprawdz() {
    return wolaj("/api/stan").then(function (s) {
      proby = 0;
      $("wersja-silnika").textContent = "silnik " + s.wersja + (s.modele ? " · " + s.modele : "");
      if (s.zadanie && s.zadanie.stan === "praca") {
        zadanie = s.zadanie.id;
        stanSilnika("praca", "Montuję");
        if (ekranTeraz !== "praca") { startPracy = Date.now() - (s.zadanie.sekundy || 0) * 1000; pokaz("praca"); pilnuj(); }
        return s;
      }
      if (!s.gotowy || (!s.komplet && !pominiete)) {
        $("pomin-przyg").hidden = !s.gotowy;
        stanSilnika("brak", "Silnik: brakuje narzędzi");
        rysujBraki(s);
        pokaz("przygotowanie");
        if (s.przygotowanie && s.przygotowanie.trwa) pilnujPrzygotowania();
        return s;
      }
      stanSilnika("gotowy", "Silnik gotowy");
      if (ekranTeraz === "" || ekranTeraz === "brak" || ekranTeraz === "przygotowanie") {
        if (s.zadanie && s.zadanie.stan === "gotowe" && s.zadanie.wynik) { zadanie = s.zadanie.id; pokazWynik(s.zadanie.wynik); }
        else pokaz("start");
      }
      return s;
    }).catch(function () {
      proby++;
      stanSilnika("brak", NA_STRONIE ? "Silnik nie działa" : "Brak połączenia");
      if (ekranTeraz === "" || ekranTeraz === "start" || ekranTeraz === "brak") {
        ustawBrak();
        pokaz("brak");
      }
      return null;
    });
  }
  function ustawBrak() {
    var p = (KONF.pobierz || {})[system];
    var a = $("pobierz-silnik");
    if (p) { a.href = p; a.textContent = system === "mac" ? "Pobierz silnik dla macOS" : "Pobierz silnik dla Windows"; }
    else { a.hidden = true; }
    $("krok-system").innerHTML = system === "mac"
      ? "Przy pierwszym otwarciu kliknij plik prawym przyciskiem i wybierz <b>Otwórz</b>."
      : "Jeśli Windows pokaże niebieskie okno, kliknij <b>Więcej informacji</b>, potem <b>Uruchom mimo to</b>.";
    $("brak-podpowiedz").textContent = /^((?!chrome|android).)*safari/i.test(navigator.userAgent)
      ? "W Safari montaż otworzy się w osobnej karcie po uruchomieniu silnika."
      : "Jeśli przeglądarka zapyta o dostęp do urządzeń w sieci lokalnej, zezwól: chodzi o program na tym komputerze.";
  }
  setInterval(function () { if (ekranTeraz !== "praca") sprawdz(); }, 4000);

  // ---------- przygotowanie narzędzi ----------
  function rysujBraki(s) {
    var ul = $("lista-brakow");
    ul.innerHTML = "";
    (s.brak || []).forEach(function (b) { var li = document.createElement("li"); li.textContent = b; ul.appendChild(li); });
  }
  var zegarPrzyg = null;
  function pilnujPrzygotowania() {
    if (zegarPrzyg) return;
    $("start-przyg").disabled = true;
    zegarPrzyg = setInterval(function () {
      wolaj("/api/stan").then(function (s) {
        var p = s.przygotowanie || {};
        $("pasek-przyg").style.width = Math.round((p.postep || 0) * 100) + "%";
        $("etap-przyg").textContent = p.tekst || "Pobieram";
        if (p.blad) { $("etap-przyg").textContent = "Nie udało się: " + p.blad; }
        if (!p.trwa) {
          clearInterval(zegarPrzyg); zegarPrzyg = null; $("start-przyg").disabled = false;
          if (s.komplet || (s.gotowy && pominiete)) { ekranTeraz = "przygotowanie"; sprawdz(); }
        }
      }).catch(function () {});
    }, 1000);
  }
  $("start-przyg").addEventListener("click", function () {
    wolaj("/api/przygotuj", { method: "POST" }).then(pilnujPrzygotowania).catch(function (e) { $("etap-przyg").textContent = e.message; });
  });

  $("pomin-przyg").addEventListener("click", function () { pominiete = true; ekranTeraz = "przygotowanie"; sprawdz(); });

  // ---------- nagrania ----------
  function odswiezMontuj() {
    var gotowe = pliki.length > 0 && pliki.every(function (p) { return p.gotowy; });
    $("montuj").disabled = !gotowe;
    var suma = pliki.reduce(function (a, p) { return a + (p.rozmiar || 0); }, 0);
    $("start-uwaga").textContent = pliki.length
      ? pliki.length + (pliki.length === 1 ? " nagranie, " : pliki.length < 5 ? " nagrania, " : " nagrań, ") + rozmiarTekst(suma) + ". Zostają na tym komputerze."
      : "Nagrania zostają na tym komputerze.";
  }
  function dodajWiersz(nazwa, rozmiar) {
    var li = document.createElement("li");
    li.innerHTML = '<span class="nazwa"></span><span class="rozmiar"></span><button class="usun" type="button" aria-label="Usuń">×</button><i class="wgrywanie"></i>';
    li.querySelector(".nazwa").textContent = nazwa;
    li.querySelector(".rozmiar").textContent = rozmiarTekst(rozmiar);
    $("pliki").appendChild(li);
    var p = { id: null, nazwa: nazwa, rozmiar: rozmiar, el: li, gotowy: false };
    li.querySelector(".usun").addEventListener("click", function () {
      if (p.xhr) p.xhr.abort();
      pliki = pliki.filter(function (x) { return x !== p; });
      li.remove();
      odswiezMontuj();
    });
    pliki.push(p);
    return p;
  }
  var kolejka = Promise.resolve();
  function wrzuc(plik) {
    if (!/\.(mp4|mov|m4v|mkv|avi|webm|mts|m2ts|3gp)$/i.test(plik.name) && !/^video\//.test(plik.type)) return;
    var p = dodajWiersz(plik.name, plik.size);
    odswiezMontuj();
    // po kolei: dysk i tak zapisuje jeden plik naraz, a pasek postępu jest czytelny
    kolejka = kolejka.then(function () {
      return new Promise(function (ok) {
        if (pliki.indexOf(p) < 0) return ok();
        var x = new XMLHttpRequest();
        p.xhr = x;
        x.open("POST", API + "/api/wrzuc?nazwa=" + encodeURIComponent(plik.name));
        x.upload.onprogress = function (e) { if (e.lengthComputable) p.el.querySelector(".wgrywanie").style.width = (100 * e.loaded / e.total) + "%"; };
        x.onload = function () {
          try { var j = JSON.parse(x.responseText); if (x.status === 200 && j.id) { p.id = j.id; p.gotowy = true; p.el.querySelector(".wgrywanie").style.width = "0"; } else throw 0; }
          catch (e) { p.el.classList.add("blad"); p.el.querySelector(".rozmiar").textContent = "nie udało się dodać"; }
          odswiezMontuj(); ok();
        };
        x.onerror = x.onabort = function () { p.el.classList.add("blad"); odswiezMontuj(); ok(); };
        x.send(plik);
      });
    });
  }
  var strefa = $("strefa");
  ["dragenter", "dragover"].forEach(function (z) { strefa.addEventListener(z, function (e) { e.preventDefault(); strefa.classList.add("nad-strefa"); }); });
  ["dragleave", "drop"].forEach(function (z) { strefa.addEventListener(z, function (e) { e.preventDefault(); strefa.classList.remove("nad-strefa"); }); });
  strefa.addEventListener("drop", function (e) { Array.prototype.forEach.call(e.dataTransfer.files, wrzuc); });
  window.addEventListener("dragover", function (e) { e.preventDefault(); });
  window.addEventListener("drop", function (e) { e.preventDefault(); });
  function wybierzZDysku() {
    // okno wyboru plików otwiera silnik: wybrane nagrania nie są kopiowane, silnik czyta je z miejsca, gdzie leżą
    wolaj("/api/wybierz", { method: "POST" }).then(function (j) {
      (j.pliki || []).forEach(function (f) { var p = dodajWiersz(f.nazwa, f.rozmiar); p.id = f.id; p.gotowy = true; });
      odswiezMontuj();
    }).catch(function () { $("pole-plikow").click(); });
  }
  $("wybierz").addEventListener("click", function (e) { e.stopPropagation(); wybierzZDysku(); });
  strefa.addEventListener("click", wybierzZDysku);
  strefa.addEventListener("keydown", function (e) { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); wybierzZDysku(); } });
  $("pole-plikow").addEventListener("change", function (e) { Array.prototype.forEach.call(e.target.files, wrzuc); e.target.value = ""; });

  // ---------- montaż ----------
  function montuj(dane) {
    $("montuj").disabled = true;
    wolaj("/api/montuj", { json: dane }).then(function (j) {
      zadanie = j.id;
      startPracy = Date.now();
      $("pasek").style.width = "2%";
      $("etap").textContent = "Zaczynam";
      stanSilnika("praca", "Montuję");
      pokaz("praca");
      pilnuj();
    }).catch(function (e) { bladEkran(e.message); });
  }
  $("montuj").addEventListener("click", function () {
    montuj({ pliki: pliki.map(function (p) { return p.id; }), opis: $("opis").value.trim() });
  });
  function pilnuj() {
    clearInterval(zegar);
    zegar = setInterval(function () {
      $("czas-pracy").textContent = czasTekst((Date.now() - startPracy) / 1000);
      wolaj("/api/postep").then(function (p) {
        if (p.stan === "praca") {
          $("pasek").style.width = Math.max(2, Math.round((p.postep || 0) * 100)) + "%";
          if (p.tekst) $("etap").textContent = p.tekst;
        } else if (p.stan === "gotowe") {
          clearInterval(zegar);
          stanSilnika("gotowy", "Silnik gotowy");
          pokazWynik(p.wynik);
        } else if (p.stan === "blad") {
          clearInterval(zegar);
          stanSilnika("gotowy", "Silnik gotowy");
          bladEkran(p.blad || "Nieznany błąd");
        }
      }).catch(function () {});
    }, 800);
  }
  $("przerwij").addEventListener("click", function () {
    wolaj("/api/przerwij", { method: "POST" }).catch(function () {});
    clearInterval(zegar);
    stanSilnika("gotowy", "Silnik gotowy");
    pokaz("start");
    odswiezMontuj();
  });
  function bladEkran(tekst) {
    $("blad-tekst").textContent = tekst;
    pokaz("blad");
  }
  $("blad-wroc").addEventListener("click", function () { pokaz("start"); odswiezMontuj(); });

  // ---------- wynik ----------
  function pokazWynik(w) {
    wynik = w || {};
    var adres = API + "/api/rolka/" + encodeURIComponent(zadanie);
    $("film").src = adres + ".mp4?t=" + Date.now();
    $("pobierz").href = adres + "/pobierz";
    var czesci = [];
    if (wynik.czas) czesci.push("Rolka ma " + Math.round(wynik.czas) + " s");
    if (wynik.trwalo) czesci.push("montaż trwał " + czasTekst(wynik.trwalo) + " min");
    $("wynik-podsumowanie").textContent = (wynik.temat ? wynik.temat + " " : "") + (czesci.length ? czesci.join(", ") + "." : "");
    $("wynik-nad").textContent = wynik.rodzaj === "mowa" ? "Rolka mówiona" : "Rolka z ujęć";
    var opis = (wynik.opis || "") + ((wynik.hashtagi || []).length ? "\n\n" + wynik.hashtagi.join(" ") : "");
    $("blok-opis").hidden = !opis.trim();
    $("opis-posta").textContent = opis.trim();
    var ul = $("raport");
    ul.innerHTML = "";
    (wynik.raport || []).forEach(function (r) { var li = document.createElement("li"); li.textContent = r; ul.appendChild(li); });
    $("tekst-mowy").textContent = wynik.tekst ? "Tekst wypowiedzi: " + wynik.tekst : "";
    $("blok-co").hidden = !(wynik.raport || []).length && !wynik.tekst;
    $("popraw").value = "";
    $("popraw-btn").disabled = true;
    pokaz("wynik");
  }
  $("kopiuj-opis").addEventListener("click", function () {
    var t = $("opis-posta").textContent, b = $("kopiuj-opis");
    (navigator.clipboard ? navigator.clipboard.writeText(t) : Promise.reject()).then(function () { b.textContent = "Skopiowano"; }, function () {
      var r = document.createRange(); r.selectNodeContents($("opis-posta")); var s = getSelection(); s.removeAllRanges(); s.addRange(r);
      try { document.execCommand("copy"); b.textContent = "Skopiowano"; } catch (e) {}
    });
    setTimeout(function () { b.textContent = "Kopiuj"; }, 1800);
  });
  $("pokaz-folder").addEventListener("click", function () { wolaj("/api/pokaz", { json: { id: zadanie } }).catch(function () {}); });
  $("popraw").addEventListener("input", function () { $("popraw-btn").disabled = !$("popraw").value.trim(); });
  $("popraw-btn").addEventListener("click", function () { montuj({ poprzednie: zadanie, popraw: $("popraw").value.trim() }); });
  $("nowa").addEventListener("click", function () {
    pliki.forEach(function (p) { p.el.remove(); });
    pliki = [];
    $("opis").value = "";
    $("film").removeAttribute("src");
    wolaj("/api/nowa", { method: "POST" }).catch(function () {});
    pokaz("start");
    odswiezMontuj();
  });

  // podgląd pokazuje kadr z rolki zamiast czarnej pierwszej klatki
  $("film").addEventListener("loadedmetadata", function () { if (this.currentTime === 0 && this.duration > 1) this.currentTime = 0.6; });
  if (KONF.konto) $("gora-konto").innerHTML = KONF.konto;
  sprawdz();
})();
