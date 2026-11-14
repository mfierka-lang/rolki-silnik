// Test synchronizacji silnika z panelem na prawdziwych programach (Windows, macOS, Linux):
// udawany panel podaje muzykę i „nową wersję” silnika, a stary silnik ma: pobrać muzykę, odrzucić program ze złym
// podpisem, pobrać program z dobrym podpisem, przełączyć się na niego i przy następnym starcie znów go uruchomić.
//   cd silnik && node test-synchro.mjs
// Nie potrzebuje ffmpeg ani modeli. Używa własnego klucza testowego i portu 47699 (nie koliduje z silnikiem).
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const tu = path.dirname(fileURLToPath(import.meta.url));
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'zm-synchro-'));
const exe = process.platform === 'win32' ? '.exe' : '';
const system = (process.platform === 'win32' ? 'windows' : process.platform) + '-' + (process.arch === 'x64' ? 'amd64' : process.arch);
const PORT = 47699, silnik = 'http://127.0.0.1:' + PORT;
let bledy = 0;
const sprawdz = (warunek, opis) => { console.log((warunek ? '  ok   ' : '  BŁĄD ') + opis); if (!warunek) bledy++; };
const czekaj = ms => new Promise(r => setTimeout(r, ms));

// --- klucz testowy i dwa programy: stary (wersja z kodu) i „nowy” (9.9.9)
const { privateKey, publicKey } = crypto.generateKeyPairSync('ed25519');
const pub = publicKey.export({ format: 'der', type: 'spki' }).subarray(-32).toString('base64');
const buduj = (plik, dodatkowe) => {
  const r = spawnSync('go', ['build', '-ldflags', `-X main.kluczWydan=${pub} ${dodatkowe}`, '-o', plik, '.'], { cwd: tu, stdio: 'inherit' });
  if (r.status !== 0) throw new Error('budowa nie powiodła się');
};
const stary = path.join(tmp, 'stary' + exe), nowy = path.join(tmp, 'nowy' + exe);
buduj(stary, '');
buduj(nowy, '-X main.wersja=9.9.9');
const wersjaStara = spawnSync(stary, ['wersja']).stdout.toString().trim().split(/\s+/).pop();
console.log('system', system, '· stary silnik', wersjaStara, '· nowy 9.9.9');

// --- udawany panel
const sha = b => crypto.createHash('sha256').update(b).digest('hex');
const podpis = (wersja, suma, klucz = privateKey) => crypto.sign(null, Buffer.from(`zblizka-montaz|${wersja}|${system}|${suma}`), klucz).toString('base64');
const utwory = { 'm/a1.mp3': crypto.randomBytes(300000), 'm/b2.mp3': crypto.randomBytes(200000), 'm/c3.mp3': crypto.randomBytes(100000) };
const program = fs.readFileSync(nowy);
const pliki = { ...utwory, 'p/nowy': program };
let spis = {};
const ustawSpis = (o) => { spis = o; };
const wpis = (p, u) => ({ p, u, r: pliki[u].length, s: sha(pliki[u]) });
let zadania = [];
const panel = http.createServer((req, res) => {
  zadania.push(req.url);
  if (req.headers.authorization !== 'Bearer dobry-zeton') { res.writeHead(401); res.end('brak dostępu'); return; }
  if (req.url === '/silnik/stan.json') { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(spis)); return; }
  const b = pliki[req.url.replace('/silnik/', '')];
  if (!b) { res.writeHead(404); res.end(); return; }
  res.writeHead(200, { 'Content-Length': b.length }); res.end(b);
});
await new Promise(r => panel.listen(0, '127.0.0.1', r));
const adresPanelu = 'http://127.0.0.1:' + panel.address().port;

// --- silnik
const dane = path.join(tmp, 'dane');
fs.mkdirSync(dane);
const srodowisko = { ...process.env, ZM_PORT: String(PORT), ZM_PRZEKAZANO: '' };
delete srodowisko.ZM_PRZEKAZANO;
const start = (plik) => spawn(plik, ['serwer', '--okno=false', '--dane', dane, '--panel', adresPanelu], { env: srodowisko, stdio: 'ignore', detached: false });
const stan = async () => { try { const r = await fetch(silnik + '/api/stan', { headers: { Origin: adresPanelu } }); return await r.json(); } catch { return null; } };
const czekajNa = async (warunek, sekund, opis) => {
  for (let i = 0; i < sekund * 4; i++) { const s = await stan(); if (s && warunek(s)) return s; await czekaj(250); }
  console.log('  (nie doczekałem się: ' + opis + ')', JSON.stringify((await stan())?.synchro));
  return null;
};
const synchronizuj = (zeton, origin = adresPanelu) => fetch(silnik + '/api/synchronizuj', { method: 'POST', headers: { Origin: origin, 'Content-Type': 'application/json' }, body: JSON.stringify({ zeton }) });
const poSynchro = () => czekajNa(s => s.synchro && !s.synchro.trwa && (s.synchro.kiedy > 0 || s.synchro.blad), 30, 'koniec synchronizacji');
const zamknij = async () => { try { await fetch(silnik + '/api/zamknij', { method: 'POST', headers: { Origin: adresPanelu } }); } catch {} for (let i = 0; i < 40; i++) { if (!(await stan())) return; await czekaj(250); } };

try {
  start(stary);
  let s = await czekajNa(x => x.ok, 15, 'start silnika');
  sprawdz(s && s.wersja === wersjaStara, 'stary silnik działa (' + (s && s.wersja) + ')');

  console.log('1. Dostęp');
  let r = await synchronizuj('dobry-zeton', 'https://obca-strona.example');
  sprawdz(r.status === 403, 'obca strona nie może zlecić synchronizacji (' + r.status + ')');
  ustawSpis({ wersja: wersjaStara, programy: {}, muzyka: [wpis('Test/Pierwszy.mp3', 'm/a1.mp3')] });
  await synchronizuj('zly-zeton');
  s = await poSynchro();
  sprawdz(s && /401/.test(s.synchro.blad) && !fs.existsSync(path.join(dane, 'muzyka', 'Test', 'Pierwszy.mp3')), 'zły żeton: błąd i nic nie pobrane');

  console.log('2. Muzyka');
  ustawSpis({ wersja: wersjaStara, programy: {}, muzyka: [wpis('Test/Pierwszy.mp3', 'm/a1.mp3'), wpis('Test/Drugi.mp3', 'm/b2.mp3'), wpis('Luzem.mp3', 'm/c3.mp3'),
    { p: '../ucieczka.mp3', u: 'm/a1.mp3', r: 300000, s: sha(utwory['m/a1.mp3']) }, { p: 'Test/zly-skrot.mp3', u: 'm/b2.mp3', r: 200000, s: '0'.repeat(64) }] });
  await synchronizuj('dobry-zeton');
  s = await poSynchro();
  const mz = f => path.join(dane, 'muzyka', ...f.split('/'));
  sprawdz(fs.existsSync(mz('Test/Pierwszy.mp3')) && fs.readFileSync(mz('Test/Pierwszy.mp3')).equals(utwory['m/a1.mp3']), 'utwór pobrany bez zmian w treści');
  sprawdz(fs.existsSync(mz('Test/Drugi.mp3')) && fs.existsSync(mz('Luzem.mp3')), 'pozostałe utwory pobrane');
  sprawdz(!fs.existsSync(path.join(dane, 'ucieczka.mp3')) && !fs.existsSync(path.join(tmp, 'ucieczka.mp3')), 'ścieżka wychodząca poza bibliotekę pominięta');
  sprawdz(!fs.existsSync(mz('Test/zly-skrot.mp3')) && s && /skrót/.test(s.synchro.blad || ''), 'plik ze złym skrótem odrzucony (' + (s && s.synchro.blad) + ')');
  fs.writeFileSync(mz('Test/Wlasny.mp3'), 'utwór dodany ręcznie');
  ustawSpis({ wersja: wersjaStara, programy: {}, muzyka: [wpis('Test/Pierwszy.mp3', 'm/a1.mp3'), wpis('Luzem.mp3', 'm/c3.mp3')] });
  zadania = [];
  await synchronizuj('dobry-zeton');
  s = await poSynchro();
  sprawdz(s && !s.synchro.blad && !fs.existsSync(mz('Test/Drugi.mp3')), 'utwór wycofany z panelu zniknął');
  sprawdz(fs.existsSync(mz('Test/Wlasny.mp3')) && fs.existsSync(mz('Test/Pierwszy.mp3')), 'utwór dodany ręcznie został');
  sprawdz(!zadania.some(z => z.includes('/m/')), 'nic nie jest pobierane drugi raz');

  console.log('3. Nowa wersja ze złym podpisem');
  const sumaP = sha(program);
  const obcy = crypto.generateKeyPairSync('ed25519').privateKey;
  ustawSpis({ wersja: '9.9.9', programy: { [system]: { u: 'p/nowy', r: program.length, s: sumaP, podpis: podpis('9.9.9', sumaP, obcy) } }, muzyka: spis.muzyka });
  await synchronizuj('dobry-zeton');
  s = await poSynchro();
  sprawdz(s && /podpis/.test(s.synchro.blad || '') && s.wersja === wersjaStara && !fs.existsSync(path.join(dane, 'program', 'wersja.json')), 'obcy podpis: nowa wersja odrzucona');
  ustawSpis({ wersja: '9.9.9', programy: { [system]: { u: 'p/nowy', r: program.length, s: sumaP, podpis: podpis('8.0.0', sumaP) } }, muzyka: spis.muzyka });
  await synchronizuj('dobry-zeton');
  s = await poSynchro();
  sprawdz(s && /podpis/.test(s.synchro.blad || '') && s.wersja === wersjaStara, 'podpis od innej wersji: odrzucony');

  console.log('4. Nowa wersja z dobrym podpisem');
  ustawSpis({ wersja: '9.9.9', programy: { [system]: { u: 'p/nowy', r: program.length, s: sumaP, podpis: podpis('9.9.9', sumaP) } }, muzyka: spis.muzyka });
  await synchronizuj('dobry-zeton');
  s = await czekajNa(x => x.wersja === '9.9.9', 40, 'przełączenie na nową wersję');
  sprawdz(!!s, 'silnik sam przełączył się na nową wersję (' + (s && s.wersja) + ')');
  sprawdz(fs.existsSync(mz('Test/Pierwszy.mp3')), 'biblioteka muzyki została po przełączeniu');

  console.log('5. Następne uruchomienie');
  await zamknij();
  sprawdz(!(await stan()), 'silnik zamknięty');
  start(stary);
  s = await czekajNa(x => x.ok, 20, 'start po aktualizacji');
  sprawdz(s && s.wersja === '9.9.9', 'stary program od razu oddaje pracę nowej wersji (' + (s && s.wersja) + ')');
  if (process.platform === 'win32') {
    const kopia = path.join(dane, 'ZblizkaMontaz.exe');
    for (let i = 0; i < 60 && !fs.existsSync(kopia); i++) await czekaj(250);
    sprawdz(fs.existsSync(kopia) && fs.statSync(kopia).size === program.length, 'Windows: nowa wersja zastąpiła zainstalowaną kopię');
  }
  // podmieniony plik programu nie może zostać uruchomiony
  await zamknij();
  const zapis = JSON.parse(fs.readFileSync(path.join(dane, 'program', 'wersja.json'), 'utf8'));
  fs.appendFileSync(path.join(dane, 'program', zapis.plik), 'dopisane przez kogoś');
  start(stary);
  s = await czekajNa(x => x.ok, 20, 'start z podmienionym plikiem');
  sprawdz(s && s.wersja === wersjaStara, 'podmieniony plik nowej wersji nie jest uruchamiany (działa ' + (s && s.wersja) + ')');
} finally {
  await zamknij();
  panel.close();
  await czekaj(500);
  try { fs.rmSync(tmp, { recursive: true, force: true }); } catch {}
}
console.log(bledy ? `NIEUDANE: ${bledy}` : 'WSZYSTKO OK');
process.exit(bledy ? 1 : 0);
