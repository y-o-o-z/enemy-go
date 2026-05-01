# enemy-go

Test-clones harness do **IRCnetu** napisany w Go. Rewrite klasycznej C-owej
`enemy` (fahren / Pojeby Team, 2003) z wykorzystaniem biblioteki
[`github.com/kofany/go-ircevo`](https://github.com/kofany/go-ircevo).

Narzędzie służy do uruchamiania kontrolowanej grupy klientów IRC
("klonów") wyłącznie na własnej infrastrukturze — typowy use-case to
testowanie własnych botów (mutual-op, anti-flood, takeover-detection),
testy stress-owe własnych kanałów, sprawdzanie reakcji własnych
serwisów na zachowania klonów. **Nie używaj na sieciach/hostach do
których nie masz autoryzacji.**

---

## Co to robi

* Pobiera aktualną listę otwartych serwerów IRCnet z
  `https://bot.ircnet.info/api/v2/serversByCountry` (filtruje
  `open: true`, równolegle resolwuje A/AAAA).
* Spawnuje N klonów w jednym procesie. Każdy klon to osobna gorutyna
  z własnym bind-IP i własną sesją IRC.
* Wybiera tryb adresacji: `ipv4`, `ipv6` albo `both` (50/50 alternacja).
* Wykrywa wszystkie przypisane lokalne IP (skip loopback / link-local /
  ULA) — albo bierze listę z flagi.
* Round-robinem rozdaje klonom serwery i lokalne IP — każdy klon
  ląduje na innym serwerze i innym (jeśli są) lokalnym adresie.
* Auto-reconnect z exponential backoffem (5s → 90s) i auto-rejoin
  po `KICK`.
* Interaktywny shell: `load`, `join`, `part`, `msg`, `say`, `raw`,
  `mode`, `kick`, `op`, `deop`, `del`, `stat`, `refresh`, `pool`,
  `disco`, `exit` (pełne `help` w shellu lub w [INFO.md](INFO.md)).
* Czyste zamknięcie: każdy klon wysyła własny `QUIT :<reason>` z
  konfigurowalnej puli, potem socket się zamyka.

---

## Wymagania

### Do uruchomienia (gotowy binarek)

* Linux x86_64 (testowane na Debian 13, Ubuntu 22+).
* Brak zależności runtime — pojedynczy statyczny-ish binarek
  (`go build`, ~9 MB).
* Internet wychodzący na port 6667 (TCP) i HTTPS (port 443) do
  `bot.ircnet.info` przy pierwszym strzale (lub przy `refresh`).

### Do zbudowania ze źródeł

* Go ≥ **1.23.2**.
* Internet do pobrania zależności:
  * `github.com/kofany/go-ircevo` (≥ v1.2.5)
  * `golang.org/x/net`, `golang.org/x/text`, `h12.io/socks`.
* `git` do klonowania repo.

---

## Instalacja

### Szybka — prebuilt linux/amd64

```bash
# z poziomu zbudowanego binarka:
sudo install -m 0755 ./enemy /usr/local/bin/enemy
enemy -version
```

### Build ze źródeł

```bash
git clone git@github.com:y-o-o-z/enemy-go.git
cd enemy-go
go build -o enemy ./...
sudo install -m 0755 ./enemy /usr/local/bin/enemy
```

Cross-compile (np. budujesz na Macu, deploy na linux x86_64):

```bash
GOOS=linux GOARCH=amd64 go build -o enemy ./...
scp enemy user@target:/tmp/
ssh user@target 'sudo install -m 0755 /tmp/enemy /usr/local/bin/enemy'
```

### Wymagane uprawnienia

**Nie potrzebuje roota.** Bind do lokalnych IP (zarówno v4 jak v6)
nie wymaga capabilities — IP musi tylko być przypisany do interfejsu
(`ip addr show`). Jeśli chcesz dodać kolejny adres do interfejsu:

```bash
sudo ip -6 addr add 2a01:6ee0:b:b811::42/64 dev eth0
```

i `enemy` automatycznie go wykryje.

---

## Konfiguracja sieciowa

Sprawdzenie lokalnej puli przed startem:

```bash
ip -4 addr show | grep 'inet '
ip -6 addr show | grep 'inet6 '
```

Adresy które `enemy` weźmie pod uwagę:

* IPv4 globalne (skip loopback, multicast, link-local).
* IPv6 globalne (skip `::1`, `fe80::/10`, ULA `fc00::/7`).

Jeśli auto-detect nie pasuje, wymuś pulę explicit:

```bash
enemy -mode ipv6 -bind-v6 '2a01:6ee0:b:b811::1,2a01:6ee0:b:b811::42'
```

Adresy spoza listy interfejsów dadzą błąd `bind: cannot assign
requested address` przy pierwszym connect — system na to nie
pozwoli.

---

## Pierwsze uruchomienie

```bash
enemy -mode both -n 5 -channels '#test'
```

Co się stanie:

```
[*] mode=both  local IPs: v4=1 v6=23
[*] fetching https://bot.ircnet.info/api/v2/serversByCountry ...
[*] 12 open IRCnet servers
[*] resolved: 12 total (v4=12, v6=10)
[*] spawning 5 clones (stagger=250ms)...
Type 'help' for available commands.
enemy>
```

Po wpisaniu `stat` zobaczysz każdego klona z bind-IP, nick-iem,
serwerem i statusem (`online` / `connecting` / `off`).

Pełna instrukcja komend, takeover-flow, kick-reasonów i
diagnostyki: zobacz **[INFO.md](INFO.md)**.

---

## Przykładowe scenariusze

```bash
# 10 klonów na czysto IPv4, jeden źródłowy adres
enemy -mode ipv4 -n 10 -bind-v4 79.139.59.33 -channels '#mychan'

# IPv6 only, kanały rozdzielone przecinkiem
enemy -mode ipv6 -n 5 -channels '#chan1,#chan2'

# Tylko podgląd otwartych serwerów IRCnet (bez spawnowania)
enemy -list-servers -mode both

# Ograniczenie do dwóch konkretnych serwerów (omija registry)
enemy -mode both -n 4 \
      -servers 'irc.spadhausen.com,openirc.snt.utwente.nl'

# Własny plik z kick-reasonami (jedna linia = jeden reason, # to komentarz)
enemy -mode ipv6 -n 3 -kick-reasons /home/y/my-reasons.txt

# Druga instancja na innych serwerach (np. w drugim tmuxie)
enemy -mode both -n 5 -servers 'ircnet.tngnet.nl,hostsailor.ircnet.nl'
```

---

## Zamykanie

W shellu `enemy>`:

| komenda            | efekt                                                  |
|--------------------|--------------------------------------------------------|
| `exit` / `quit`    | wszystkie klony QUIT-ują, proces wychodzi              |
| `disco`            | QUIT wszystkich, ale shell zostaje (możesz `load` znów)|
| `del <id>`         | killuje pojedynczego klona (`stat` daje listę ID)      |
| Ctrl-C / SIGTERM   | sygnał: 8s na czysty QUIT, potem `os.Exit(0)`          |

---

## Pliki w repo

```
main.go        — CLI, flagi, sygnały, bootstrap menedżera
shell.go       — interaktywny REPL operatora
manager.go     — pula klonów, scheduler IP/serwerów, broadcast
clone.go       — pojedynczy klon (wrapper na go-ircevo + reconnect)
servers.go     — fetch + DNS-resolve listy serwerów IRCnet
pool.go        — auto-detect lokalnych IP, IPMode (v4/v6/both)
random.go      — generator nicków/identów wzorowany na C enemy
go.mod / go.sum — moduł Go
INFO.md        — pełna instrukcja obsługi (PL)
README.md      — ten plik
```

---

## Bezpieczeństwo i uwagi

* **Tylko własna infrastruktura.** Skrypt to test-tool — uruchamiasz
  wyłącznie tam gdzie masz autoryzację (własny serwer / własne
  kanały / własna sieć).
* IRCnet ma per-host limity połączeń. Zbyt agresywny `-n` z jednego
  /64 → `Too many host connections (local)`. Rozłóż klony po kilku
  prefixach albo użyj `-stagger 2s+`.
* Niektóre serwery IRCnet banują całe /64 osobnych operatorów /
  hostingów. Sprawdź `enemy -list-servers` jeśli coś nie połączy.
* `bot.ircnet.info` to nieoficjalny registry (Angular-SPA z API JSON).
  Jeśli kiedyś padnie — przekaż swoją listę przez `-servers
  'host1,host2,...'`.

---

## Licencja / pochodzenie

Rewrite Go, własny kod. Inspiracja: oryginalna `enemy` autorstwa
**fahren@bochnia.pl** (Maciej Freudenheim, 2002–2003) — z tego
pochodzą m.in. alfabety nick-generatora i format kick-reasonów
(`[PT]` suffix). Treść kick-reasonów w tej wersji jest świeża.

To repo jest forkiem [`kofany/enemy`](https://github.com/kofany/enemy)
(C — rebrand X-men2). Historia C-owego upstreamu jest zachowana w
`git log`; bieżący tree to kompletny rewrite w Go.
